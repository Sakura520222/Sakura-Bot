// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package mtproto

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gotd/td/tg"
)

type handlerEntry[T any] struct {
	id uint64
	fn T
}

// Dispatcher 封装 tg.UpdateDispatcher，负责：
//   - 在每次事件转换前先将 tg.Entities 写入 PeerCache；
//   - 将消息、编辑、删除、按钮回调、入群申请、改名、群成员变动及服务消息统一映射为标准化事件；
//   - 为每个订阅者提供独立 unsubscribe 闭包与 panic 隔离保护。
type Dispatcher struct {
	raw       tg.UpdateDispatcher
	peers     *PeerCache
	log       *slog.Logger
	selfIDPtr *atomic.Int64

	mu                sync.RWMutex
	nextID            uint64
	newMsgHandlers    []handlerEntry[func(ctx context.Context, msg *IncomingMessage)]
	editMsgHandlers   []handlerEntry[func(ctx context.Context, msg *IncomingMessage)]
	deleteMsgHandlers []handlerEntry[func(ctx context.Context, ev DeletedMessagesEvent)]
	callbackHandlers  []handlerEntry[func(ctx context.Context, ev CallbackEvent)]
	joinReqHandlers   []handlerEntry[func(ctx context.Context, ev JoinRequestEvent)]
	userNameHandlers  []handlerEntry[func(ctx context.Context, ev UserNameEvent)]
	partHandlers      []handlerEntry[func(ctx context.Context, ev ParticipantEvent)]
}

// NewDispatcher 构造实时事件分发器并完成底层 tg.UpdateDispatcher 钩子绑定。
func NewDispatcher(peers *PeerCache, logger *slog.Logger) *Dispatcher {
	if peers == nil {
		peers = NewMemoryPeerCache()
	}
	if logger == nil {
		logger = slog.Default()
	}
	d := &Dispatcher{
		raw:       tg.NewUpdateDispatcher(),
		peers:     peers,
		log:       logger,
		selfIDPtr: &atomic.Int64{},
	}
	d.wireRawHandlers()
	return d
}

// SetSelfID 注入当前登录账号 ID（用于补齐私聊发出消息 SenderID 等场景）。
func (d *Dispatcher) SetSelfID(id int64) {
	if d != nil && d.selfIDPtr != nil {
		d.selfIDPtr.Store(id)
	}
}

// Raw 返回底层 tg.UpdateDispatcher。
func (d *Dispatcher) Raw() tg.UpdateDispatcher { return d.raw }

// Handle 实现 telegram.UpdateHandler 接口（既可挂在 updates.Manager 下，也可直接接入 telegram.Options.UpdateHandler）。
func (d *Dispatcher) Handle(ctx context.Context, u tg.UpdatesClass) error {
	switch v := u.(type) {
	case *tg.UpdateShortMessage:
		msg := shortMessageToTG(v, d.selfIDPtr.Load())
		var e tg.Entities
		d.dispatchNewMessageClass(ctx, e, msg)
		return nil
	case *tg.UpdateShortChatMessage:
		msg := shortChatMessageToTG(v)
		var e tg.Entities
		d.dispatchNewMessageClass(ctx, e, msg)
		return nil
	default:
		return d.raw.Handle(ctx, u)
	}
}

func (d *Dispatcher) wireRawHandlers() {
	d.raw.OnBefore(func(_ context.Context, e tg.Entities, _ tg.UpdateClass) error {
		d.peers.ApplyEntities(e)
		return nil
	})

	d.raw.OnAnyNewMessage(func(ctx context.Context, e tg.Entities, msg tg.MessageClass) error {
		d.dispatchNewMessageClass(ctx, e, msg)
		return nil
	})

	d.raw.OnAnyEditMessage(func(ctx context.Context, e tg.Entities, msg tg.MessageClass) error {
		if m, ok := msg.(*tg.Message); ok && m != nil {
			inc := convertTGMessage(m, e, d.peers, d.selfIDPtr.Load())
			d.emitEditMessage(ctx, inc)
		}
		return nil
	})

	d.raw.OnAnyDeleteMessages(func(ctx context.Context, _ tg.Entities, channelID int64, ids []int) error {
		if len(ids) == 0 {
			return nil
		}
		var chatID int64
		if channelID > 0 {
			chatID = ChannelMarkedID(channelID)
		}
		ev := DeletedMessagesEvent{
			ChatID:     chatID,
			ChannelID:  channelID,
			MessageIDs: slices.Clone(ids),
		}
		d.emitDeletedMessages(ctx, ev)
		return nil
	})

	d.raw.OnBotCallbackQuery(func(ctx context.Context, e tg.Entities, u *tg.UpdateBotCallbackQuery) error {
		data, _ := u.GetData()
		ev := CallbackEvent{
			QueryID:   u.QueryID,
			SenderID:  u.UserID,
			Sender:    resolveProfileSummary(u.UserID, e, d.peers),
			ChatID:    PeerMarkedID(u.Peer),
			MessageID: u.MsgID,
			Data:      slices.Clone(data),
		}
		d.emitCallback(ctx, ev)
		return nil
	})

	d.raw.OnBotChatInviteRequester(func(ctx context.Context, e tg.Entities, u *tg.UpdateBotChatInviteRequester) error {
		prof := resolveProfileSummary(u.UserID, e, d.peers)
		if u.About != "" {
			prof.Bio = u.About
		}
		qid, hasQID := u.GetQueryID()
		ev := JoinRequestEvent{
			ChatID:     PeerMarkedID(u.Peer),
			UserID:     u.UserID,
			User:       prof,
			Bio:        u.About,
			Date:       time.Unix(int64(u.Date), 0).UTC(),
			QueryID:    qid,
			HasQueryID: hasQID,
			QTS:        u.Qts,
		}
		d.emitJoinRequest(ctx, ev)
		return nil
	})

	d.raw.OnUserName(func(ctx context.Context, _ tg.Entities, u *tg.UpdateUserName) error {
		active := extractActiveUsernames(u.Usernames)
		primary := ""
		if len(active) > 0 {
			primary = active[0]
		}
		ev := UserNameEvent{
			UserID:          u.UserID,
			FirstName:       u.FirstName,
			LastName:        u.LastName,
			Username:        primary,
			ActiveUsernames: active,
		}
		d.peers.UpdateUserName(ev)
		d.emitUserName(ctx, ev)
		return nil
	})

	d.raw.OnChannelParticipant(func(ctx context.Context, e tg.Entities, u *tg.UpdateChannelParticipant) error {
		ev := convertChannelParticipantUpdate(u, e, d.peers)
		d.emitParticipant(ctx, ev)
		return nil
	})

	d.raw.OnChatParticipant(func(ctx context.Context, e tg.Entities, u *tg.UpdateChatParticipant) error {
		ev := convertChatParticipantUpdate(u, e, d.peers)
		d.emitParticipant(ctx, ev)
		return nil
	})

	d.raw.OnChatParticipantAdd(func(ctx context.Context, e tg.Entities, u *tg.UpdateChatParticipantAdd) error {
		kind := "add"
		if u.InviterID == 0 || u.InviterID == u.UserID {
			kind = "join"
		}
		ev := ParticipantEvent{
			ChatID:     ChatMarkedID(u.ChatID),
			ActorID:    u.InviterID,
			UserID:     u.UserID,
			User:       resolveProfileSummary(u.UserID, e, d.peers),
			Kind:       kind,
			WasPresent: false,
			IsPresent:  true,
			Date:       time.Unix(int64(u.Date), 0).UTC(),
		}
		d.emitParticipant(ctx, ev)
		return nil
	})

	d.raw.OnChatParticipantDelete(func(ctx context.Context, e tg.Entities, u *tg.UpdateChatParticipantDelete) error {
		ev := ParticipantEvent{
			ChatID:     ChatMarkedID(u.ChatID),
			UserID:     u.UserID,
			User:       resolveProfileSummary(u.UserID, e, d.peers),
			Kind:       "leave",
			WasPresent: true,
			IsPresent:  false,
			Date:       time.Now().UTC(),
		}
		d.emitParticipant(ctx, ev)
		return nil
	})

	d.raw.OnChatParticipantAdmin(func(ctx context.Context, e tg.Entities, u *tg.UpdateChatParticipantAdmin) error {
		ev := ParticipantEvent{
			ChatID:     ChatMarkedID(u.ChatID),
			UserID:     u.UserID,
			User:       resolveProfileSummary(u.UserID, e, d.peers),
			Kind:       "admin",
			WasPresent: true,
			IsPresent:  true,
			IsAdmin:    u.IsAdmin,
			Date:       time.Now().UTC(),
		}
		d.emitParticipant(ctx, ev)
		return nil
	})
}

func (d *Dispatcher) dispatchNewMessageClass(ctx context.Context, e tg.Entities, msg tg.MessageClass) {
	switch m := msg.(type) {
	case *tg.Message:
		if m == nil {
			return
		}
		inc := convertTGMessage(m, e, d.peers, d.selfIDPtr.Load())
		d.emitNewMessage(ctx, inc)
	case *tg.MessageService:
		if m == nil {
			return
		}
		for _, ev := range convertServiceMessageToParticipantEvents(m, e, d.peers) {
			d.emitParticipant(ctx, ev)
		}
	}
}

// OnNewMessage 注册新消息回调，返回取消订阅函数。
func (d *Dispatcher) OnNewMessage(fn func(ctx context.Context, msg *IncomingMessage)) func() {
	return registerHandler(&d.mu, &d.nextID, &d.newMsgHandlers, fn)
}

// OnMessageEdited 注册消息编辑回调，返回取消订阅函数。
func (d *Dispatcher) OnMessageEdited(fn func(ctx context.Context, msg *IncomingMessage)) func() {
	return registerHandler(&d.mu, &d.nextID, &d.editMsgHandlers, fn)
}

// OnMessageDeleted 注册消息删除回调，返回取消订阅函数。
func (d *Dispatcher) OnMessageDeleted(fn func(ctx context.Context, ev DeletedMessagesEvent)) func() {
	return registerHandler(&d.mu, &d.nextID, &d.deleteMsgHandlers, fn)
}

// OnCallbackQuery 注册内联按钮回调查询事件监听器，返回取消订阅函数。
func (d *Dispatcher) OnCallbackQuery(fn func(ctx context.Context, ev CallbackEvent)) func() {
	return registerHandler(&d.mu, &d.nextID, &d.callbackHandlers, fn)
}

// OnJoinRequest 注册入群申请事件监听器，返回取消订阅函数。
func (d *Dispatcher) OnJoinRequest(fn func(ctx context.Context, ev JoinRequestEvent)) func() {
	return registerHandler(&d.mu, &d.nextID, &d.joinReqHandlers, fn)
}

// OnUserName 注册用户昵称/用户名变更事件监听器，返回取消订阅函数。
func (d *Dispatcher) OnUserName(fn func(ctx context.Context, ev UserNameEvent)) func() {
	return registerHandler(&d.mu, &d.nextID, &d.userNameHandlers, fn)
}

// OnParticipant 注册群成员进退群及权限变更监听器，返回取消订阅函数。
func (d *Dispatcher) OnParticipant(fn func(ctx context.Context, ev ParticipantEvent)) func() {
	return registerHandler(&d.mu, &d.nextID, &d.partHandlers, fn)
}

func registerHandler[T any](mu *sync.RWMutex, seq *uint64, slice *[]handlerEntry[T], fn T) func() {
	mu.Lock()
	*seq++
	id := *seq
	*slice = append(*slice, handlerEntry[T]{id: id, fn: fn})
	mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			mu.Lock()
			defer mu.Unlock()
			items := *slice
			for i, item := range items {
				if item.id == id {
					*slice = append(items[:i], items[i+1:]...)
					return
				}
			}
		})
	}
}

func snapshotHandlers[T any](mu *sync.RWMutex, registered *[]handlerEntry[T]) []T {
	mu.RLock()
	defer mu.RUnlock()
	slice := *registered
	if len(slice) == 0 {
		return nil
	}
	out := make([]T, len(slice))
	for i, item := range slice {
		out[i] = item.fn
	}
	return out
}

func (d *Dispatcher) safeCall(eventName string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			d.log.Error(
				"mtproto update handler panicked",
				"event", eventName,
				"panic", fmt.Sprintf("%v", r),
				"stack", string(debug.Stack()),
			)
		}
	}()
	fn()
}

func (d *Dispatcher) emitNewMessage(ctx context.Context, msg *IncomingMessage) {
	for _, fn := range snapshotHandlers(&d.mu, &d.newMsgHandlers) {
		fn := fn
		d.safeCall("OnNewMessage", func() { fn(ctx, msg) })
	}
}

func (d *Dispatcher) emitEditMessage(ctx context.Context, msg *IncomingMessage) {
	for _, fn := range snapshotHandlers(&d.mu, &d.editMsgHandlers) {
		fn := fn
		d.safeCall("OnMessageEdited", func() { fn(ctx, msg) })
	}
}

func (d *Dispatcher) emitDeletedMessages(ctx context.Context, ev DeletedMessagesEvent) {
	for _, fn := range snapshotHandlers(&d.mu, &d.deleteMsgHandlers) {
		fn := fn
		d.safeCall("OnMessageDeleted", func() { fn(ctx, ev) })
	}
}

func (d *Dispatcher) emitCallback(ctx context.Context, ev CallbackEvent) {
	for _, fn := range snapshotHandlers(&d.mu, &d.callbackHandlers) {
		fn := fn
		d.safeCall("OnCallbackQuery", func() { fn(ctx, ev) })
	}
}

func (d *Dispatcher) emitJoinRequest(ctx context.Context, ev JoinRequestEvent) {
	for _, fn := range snapshotHandlers(&d.mu, &d.joinReqHandlers) {
		fn := fn
		d.safeCall("OnJoinRequest", func() { fn(ctx, ev) })
	}
}

func (d *Dispatcher) emitUserName(ctx context.Context, ev UserNameEvent) {
	for _, fn := range snapshotHandlers(&d.mu, &d.userNameHandlers) {
		fn := fn
		d.safeCall("OnUserName", func() { fn(ctx, ev) })
	}
}

func (d *Dispatcher) emitParticipant(ctx context.Context, ev ParticipantEvent) {
	for _, fn := range snapshotHandlers(&d.mu, &d.partHandlers) {
		fn := fn
		d.safeCall("OnParticipant", func() { fn(ctx, ev) })
	}
}

func convertServiceMessageToParticipantEvents(svc *tg.MessageService, e tg.Entities, peers *PeerCache) []ParticipantEvent {
	chatID := PeerMarkedID(svc.PeerID)
	var actorID int64
	if fp, ok := svc.GetFromID(); ok {
		actorID = PeerMarkedID(fp)
	}
	msgDate := time.Unix(int64(svc.Date), 0).UTC()

	switch act := svc.Action.(type) {
	case *tg.MessageActionChatAddUser:
		events := make([]ParticipantEvent, 0, len(act.Users))
		for _, uid := range act.Users {
			kind := "add"
			if actorID == uid || (actorID == 0 && len(act.Users) == 1) {
				kind = "join"
			}
			events = append(events, ParticipantEvent{
				ChatID:          chatID,
				ActorID:         actorID,
				UserID:          uid,
				User:            resolveProfileSummary(uid, e, peers),
				Kind:            kind,
				WasPresent:      false,
				IsPresent:       true,
				Date:            msgDate,
				ActionMessageID: svc.ID,
			})
		}
		return events

	case *tg.MessageActionChatJoinedByLink:
		if actorID <= 0 {
			return nil
		}
		return []ParticipantEvent{{
			ChatID:          chatID,
			ActorID:         actorID,
			UserID:          actorID,
			User:            resolveProfileSummary(actorID, e, peers),
			Kind:            "join",
			WasPresent:      false,
			IsPresent:       true,
			Date:            msgDate,
			ActionMessageID: svc.ID,
		}}

	case *tg.MessageActionChatJoinedByRequest:
		if actorID <= 0 {
			return nil
		}
		return []ParticipantEvent{{
			ChatID:          chatID,
			ActorID:         actorID,
			UserID:          actorID,
			User:            resolveProfileSummary(actorID, e, peers),
			Kind:            "join",
			WasPresent:      false,
			IsPresent:       true,
			Date:            msgDate,
			ActionMessageID: svc.ID,
		}}

	case *tg.MessageActionChatDeleteUser:
		uid := act.UserID
		kind := "leave"
		var kickedBy int64
		if actorID > 0 && actorID != uid {
			kind = "kick"
			kickedBy = actorID
		}
		return []ParticipantEvent{{
			ChatID:          chatID,
			ActorID:         actorID,
			UserID:          uid,
			User:            resolveProfileSummary(uid, e, peers),
			Kind:            kind,
			WasPresent:      true,
			IsPresent:       false,
			KickedBy:        kickedBy,
			Date:            msgDate,
			ActionMessageID: svc.ID,
		}}
	}
	return nil
}

// channelParticipantActiveUserID 对齐 core/moderation/deleted_cleanup.py:participant_user_id：
// 普通成员使用 user_id，受限成员使用 peer；已离群、被封禁（view_messages=true）及非用户身份返回 0。
func channelParticipantActiveUserID(p tg.ChannelParticipantClass) int64 {
	switch v := p.(type) {
	case nil, *tg.ChannelParticipantLeft:
		return 0
	case *tg.ChannelParticipant:
		if v.UserID > 0 {
			return v.UserID
		}
	case *tg.ChannelParticipantSelf:
		if v.UserID > 0 {
			return v.UserID
		}
	case *tg.ChannelParticipantCreator:
		if v.UserID > 0 {
			return v.UserID
		}
	case *tg.ChannelParticipantAdmin:
		if v.UserID > 0 {
			return v.UserID
		}
	case *tg.ChannelParticipantBanned:
		if v.Left || v.BannedRights.ViewMessages {
			return 0
		}
		if pu, ok := v.Peer.(*tg.PeerUser); ok && pu.UserID > 0 {
			return pu.UserID
		}
	}
	return 0
}

func inspectChannelParticipant(p tg.ChannelParticipantClass) (banned BannedRights, isBanned, isAdmin bool, kickedBy int64) {
	switch v := p.(type) {
	case *tg.ChannelParticipantCreator, *tg.ChannelParticipantAdmin:
		return BannedRights{}, false, true, 0
	case *tg.ChannelParticipantBanned:
		br := BannedRightsFromTG(v.BannedRights)
		return br, v.BannedRights.ViewMessages, false, v.KickedBy
	default:
		return BannedRights{}, false, false, 0
	}
}

func convertChannelParticipantUpdate(u *tg.UpdateChannelParticipant, e tg.Entities, peers *PeerCache) ParticipantEvent {
	chatID := ChannelMarkedID(u.ChannelID)
	prevComp, _ := u.GetPrevParticipant()
	newComp, _ := u.GetNewParticipant()

	wasPresent := channelParticipantActiveUserID(prevComp) == u.UserID
	isPresent := channelParticipantActiveUserID(newComp) == u.UserID
	prevRights, _, prevAdmin, _ := inspectChannelParticipant(prevComp)
	newRights, isBanned, isAdmin, kickedBy := inspectChannelParticipant(newComp)

	var kind string
	switch {
	case !wasPresent && isPresent:
		if u.ActorID > 0 && u.ActorID != u.UserID {
			kind = "add"
		} else {
			kind = "join"
		}
	case wasPresent && !isPresent:
		if isBanned || (u.ActorID > 0 && u.ActorID != u.UserID) {
			kind = "kick"
		} else {
			kind = "leave"
		}
	case !wasPresent && !isPresent && isBanned:
		kind = "kick"
	case prevAdmin != isAdmin:
		kind = "admin"
	default:
		kind = "permissions"
	}

	return ParticipantEvent{
		ChatID:           chatID,
		ActorID:          u.ActorID,
		UserID:           u.UserID,
		User:             resolveProfileSummary(u.UserID, e, peers),
		Kind:             kind,
		WasPresent:       wasPresent,
		IsPresent:        isPresent,
		IsBanned:         isBanned,
		IsAdmin:          isAdmin,
		KickedBy:         kickedBy,
		BannedRights:     newRights,
		PrevBannedRights: prevRights,
		Date:             time.Unix(int64(u.Date), 0).UTC(),
		QTS:              u.Qts,
	}
}

func chatParticipantActiveUserID(p tg.ChatParticipantClass) (uid int64, isAdmin bool) {
	switch v := p.(type) {
	case *tg.ChatParticipant:
		return v.UserID, false
	case *tg.ChatParticipantAdmin:
		return v.UserID, true
	case *tg.ChatParticipantCreator:
		return v.UserID, true
	default:
		return 0, false
	}
}

func convertChatParticipantUpdate(u *tg.UpdateChatParticipant, e tg.Entities, peers *PeerCache) ParticipantEvent {
	chatID := ChatMarkedID(u.ChatID)
	prevComp, _ := u.GetPrevParticipant()
	newComp, _ := u.GetNewParticipant()

	prevUID, prevAdmin := chatParticipantActiveUserID(prevComp)
	newUID, isAdmin := chatParticipantActiveUserID(newComp)
	wasPresent := prevUID == u.UserID
	isPresent := newUID == u.UserID

	var kind string
	var kickedBy int64
	switch {
	case !wasPresent && isPresent:
		if u.ActorID > 0 && u.ActorID != u.UserID {
			kind = "add"
		} else {
			kind = "join"
		}
	case wasPresent && !isPresent:
		if u.ActorID > 0 && u.ActorID != u.UserID {
			kind = "kick"
			kickedBy = u.ActorID
		} else {
			kind = "leave"
		}
	case prevAdmin != isAdmin:
		kind = "admin"
	default:
		kind = "permissions"
	}

	return ParticipantEvent{
		ChatID:     chatID,
		ActorID:    u.ActorID,
		UserID:     u.UserID,
		User:       resolveProfileSummary(u.UserID, e, peers),
		Kind:       kind,
		WasPresent: wasPresent,
		IsPresent:  isPresent,
		IsAdmin:    isAdmin,
		KickedBy:   kickedBy,
		Date:       time.Unix(int64(u.Date), 0).UTC(),
		QTS:        u.Qts,
	}
}

func resolveProfileSummary(userID int64, e tg.Entities, peers *PeerCache) ProfileSummary {
	if userID <= 0 {
		return ProfileSummary{}
	}
	if e.Users != nil {
		if u, ok := e.Users[userID]; ok && u != nil {
			return tgUserToProfileSummary(u)
		}
	}
	if peers != nil {
		if p, ok := peers.GetByMarkedID(userID); ok {
			return p.ToProfileSummary()
		}
	}
	return ProfileSummary{ID: userID}
}

func tgUserToProfileSummary(u *tg.User) ProfileSummary {
	if u == nil {
		return ProfileSummary{}
	}
	active := extractActiveUsernames(u.Usernames)
	uname := normalizeUsername(u.Username)
	if uname == "" && len(active) > 0 {
		uname = active[0]
	}
	accessHash, _ := u.GetAccessHash()
	hasPhoto := false
	if photo, ok := u.GetPhoto(); ok {
		_, hasPhoto = photo.(*tg.UserProfilePhoto)
	}
	return ProfileSummary{
		ID:              u.ID,
		AccessHash:      accessHash,
		IsBot:           u.Bot,
		IsDeleted:       u.Deleted,
		FirstName:       u.FirstName,
		LastName:        u.LastName,
		Username:        uname,
		ActiveUsernames: active,
		Phone:           u.Phone,
		HasPhoto:        hasPhoto,
	}
}

func shortMessageToTG(u *tg.UpdateShortMessage, selfID int64) *tg.Message {
	m := &tg.Message{
		Out:         u.Out,
		Mentioned:   u.Mentioned,
		MediaUnread: u.MediaUnread,
		Silent:      u.Silent,
		ID:          u.ID,
		PeerID:      &tg.PeerUser{UserID: u.UserID},
		Date:        u.Date,
		Message:     u.Message,
	}
	if u.Out && selfID > 0 {
		m.SetFromID(&tg.PeerUser{UserID: selfID})
	} else if !u.Out {
		m.SetFromID(&tg.PeerUser{UserID: u.UserID})
	}
	if fwd, ok := u.GetFwdFrom(); ok {
		m.SetFwdFrom(fwd)
	}
	if via, ok := u.GetViaBotID(); ok {
		m.SetViaBotID(via)
	}
	if rep, ok := u.GetReplyTo(); ok {
		m.SetReplyTo(rep)
	}
	if ents, ok := u.GetEntities(); ok {
		m.SetEntities(ents)
	}
	return m
}

func shortChatMessageToTG(u *tg.UpdateShortChatMessage) *tg.Message {
	m := &tg.Message{
		Out:         u.Out,
		Mentioned:   u.Mentioned,
		MediaUnread: u.MediaUnread,
		Silent:      u.Silent,
		ID:          u.ID,
		PeerID:      &tg.PeerChat{ChatID: u.ChatID},
		Date:        u.Date,
		Message:     u.Message,
	}
	if u.FromID > 0 {
		m.SetFromID(&tg.PeerUser{UserID: u.FromID})
	}
	if fwd, ok := u.GetFwdFrom(); ok {
		m.SetFwdFrom(fwd)
	}
	if via, ok := u.GetViaBotID(); ok {
		m.SetViaBotID(via)
	}
	if rep, ok := u.GetReplyTo(); ok {
		m.SetReplyTo(rep)
	}
	if ents, ok := u.GetEntities(); ok {
		m.SetEntities(ents)
	}
	return m
}

func convertTGMessage(m *tg.Message, e tg.Entities, peers *PeerCache, selfID int64) *IncomingMessage {
	if m == nil {
		return nil
	}
	chatID := PeerMarkedID(m.PeerID)
	inc := &IncomingMessage{
		ID:     m.ID,
		ChatID: chatID,
		IsOut:  m.Out,
		Text:   m.Message,
		Date:   time.Unix(int64(m.Date), 0).UTC(),
		Raw:    m,
		Media:  extractMediaAttachment(m.Media),
	}

	switch p := m.PeerID.(type) {
	case *tg.PeerUser:
		inc.IsPrivate = true
		prof := resolveProfileSummary(p.UserID, e, peers)
		inc.ChatTitle = joinNonEmptySpace(prof.FirstName, prof.LastName)
		inc.ChatUsername = prof.Username
	case *tg.PeerChat:
		inc.IsBasicGroup = true
		if e.Chats != nil {
			if ch, ok := e.Chats[p.ChatID]; ok && ch != nil {
				inc.ChatTitle = ch.Title
			}
		}
		if inc.ChatTitle == "" && peers != nil {
			if cp, ok := peers.GetByMarkedID(chatID); ok {
				inc.ChatTitle = cp.Title
			}
		}
	case *tg.PeerChannel:
		var found bool
		if e.Channels != nil {
			if ch, ok := e.Channels[p.ChannelID]; ok && ch != nil {
				found = true
				inc.ChatTitle = ch.Title
				inc.ChatUsername = normalizeUsername(ch.Username)
				if inc.ChatUsername == "" {
					if active := extractActiveUsernames(ch.Usernames); len(active) > 0 {
						inc.ChatUsername = active[0]
					}
				}
				inc.IsSupergroup = ch.Megagroup || ch.Gigagroup
				inc.IsBroadcast = ch.Broadcast && !inc.IsSupergroup
			}
		}
		if !found && peers != nil {
			if cp, ok := peers.GetByMarkedID(chatID); ok {
				inc.ChatTitle = cp.Title
				inc.ChatUsername = cp.Username
				inc.IsSupergroup = cp.Megagroup
				inc.IsBroadcast = cp.Broadcast && !cp.Megagroup
			}
		}
	}

	if fromPeer, ok := m.GetFromID(); ok {
		switch fp := fromPeer.(type) {
		case *tg.PeerUser:
			inc.SenderID = fp.UserID
			inc.Sender = resolveProfileSummary(fp.UserID, e, peers)
		case *tg.PeerChannel:
			inc.SenderChatID = ChannelMarkedID(fp.ChannelID)
			if peers != nil {
				if cp, ok := peers.GetByMarkedID(inc.SenderChatID); ok {
					inc.Sender = cp.ToProfileSummary()
				}
			}
		case *tg.PeerChat:
			inc.SenderChatID = ChatMarkedID(fp.ChatID)
			if peers != nil {
				if cp, ok := peers.GetByMarkedID(inc.SenderChatID); ok {
					inc.Sender = cp.ToProfileSummary()
				}
			}
		}
	} else if pu, ok := m.PeerID.(*tg.PeerUser); ok {
		if m.Out && selfID > 0 {
			inc.SenderID = selfID
			inc.Sender = resolveProfileSummary(selfID, e, peers)
		} else if !m.Out {
			inc.SenderID = pu.UserID
			inc.Sender = resolveProfileSummary(pu.UserID, e, peers)
		}
	} else if inc.IsBroadcast {
		inc.SenderChatID = chatID
	}

	if via, ok := m.GetViaBotID(); ok {
		inc.ViaBotID = via
	}
	if caller, ok := m.GetGuestchatViaFrom(); ok && inc.SenderID > 0 {
		if user, ok := caller.(*tg.PeerUser); ok && user.UserID > 0 && user.UserID != inc.SenderID {
			inc.GuestCallerID = user.UserID
		}
	}
	if ed, ok := m.GetEditDate(); ok && ed > 0 {
		t := time.Unix(int64(ed), 0).UTC()
		inc.EditDate = &t
	}
	if gid, ok := m.GetGroupedID(); ok {
		inc.GroupedID = gid
	}
	if rh, ok := m.GetReplyTo(); ok {
		if rhm, ok := rh.(*tg.MessageReplyHeader); ok {
			if rmid, ok := rhm.GetReplyToMsgID(); ok {
				inc.ReplyToMsgID = rmid
			}
			if rtop, ok := rhm.GetReplyToTopID(); ok {
				inc.ReplyToTopID = rtop
			}
		}
	}
	if fh, ok := m.GetFwdFrom(); ok {
		fwd := &ForwardHeader{
			FromName:       fh.FromName,
			ChannelPost:    fh.ChannelPost,
			SavedFromMsgID: fh.SavedFromMsgID,
			PostAuthor:     fh.PostAuthor,
			Date:           time.Unix(int64(fh.Date), 0).UTC(),
		}
		if fp, ok := fh.GetFromID(); ok {
			fwd.FromID = PeerMarkedID(fp)
		}
		if sp, ok := fh.GetSavedFromPeer(); ok {
			fwd.SavedFromPeerID = PeerMarkedID(sp)
		}
		inc.FwdFrom = fwd
	}
	if ents, ok := m.GetEntities(); ok {
		inc.Entities = EntitiesFromTG(ents)
	}
	if rm, ok := m.GetReplyMarkup(); ok {
		if inline, ok := rm.(*tg.ReplyInlineMarkup); ok {
			for _, row := range inline.Rows {
				if len(row.Buttons) > 0 {
					inc.HasButtons = true
					break
				}
			}
		}
	}

	if m.ID > 0 {
		if inc.ChatUsername != "" && (inc.IsBroadcast || inc.IsSupergroup) {
			inc.PostLink = fmt.Sprintf("https://t.me/%s/%d", inc.ChatUsername, m.ID)
		} else if kind, rawID := ParseMarkedPeerID(chatID); kind == PeerKindChannel && rawID > 0 {
			inc.PostLink = fmt.Sprintf("https://t.me/c/%d/%d", rawID, m.ID)
		}
	}

	return inc
}

func joinNonEmptySpace(a, b string) string {
	switch {
	case a != "" && b != "":
		return a + " " + b
	case a != "":
		return a
	default:
		return b
	}
}
