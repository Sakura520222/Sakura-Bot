// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package poll

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/media"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

// SummaryPollUpdater 用于在重新生成投票后同步更新 .last_summary_time.json 中的 poll_message_ids。
type SummaryPollUpdater interface {
	UpdatePollMessageIDs(channel string, pollIDs []int) error
}

type SummaryPollRepository interface {
	UpdatePollMessageIDs(ctx context.Context, channel string, summaryMsgID, pollMsgID, buttonMsgID int64) error
}

type forwardWaitKey struct {
	discussionChatID int64
	channelPostID    int
}

// ServiceDeps 配置总结互动投票与重生成回调处理器。
type ServiceDeps struct {
	Config            *config.Config
	Completer         Completer
	Pipeline          *media.ImagePipeline
	RegenStore        *RegenStore
	SummaryUpdater    SummaryPollUpdater
	SummaryRepo       SummaryPollRepository
	ForwardWaitWindow time.Duration
	Logger            *slog.Logger
}

// Service 负责总结投票发送（频道模式 & 讨论组 15s 自动转发监听模式）与按钮重新生成闭环。
type Service struct {
	deps ServiceDeps

	waitMu  sync.Mutex
	waiters map[forwardWaitKey]chan int
}

// NewService 创建总结投票服务。
func NewService(deps ServiceDeps) *Service {
	if deps.RegenStore == nil {
		deps.RegenStore = NewRegenStore("")
	}
	if deps.ForwardWaitWindow <= 0 {
		deps.ForwardWaitWindow = 15 * time.Second
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &Service{
		deps:    deps,
		waiters: make(map[forwardWaitKey]chan int),
	}
}

// RegenStore 返回内部重生成存储。
func (s *Service) RegenStore() *RegenStore { return s.deps.RegenStore }

// NotifyDiscussionMessage 在主 Bot 收到讨论群消息时唤醒等待对应频道自动转发帖的投票协程。
func (s *Service) NotifyDiscussionMessage(msg *mtproto.IncomingMessage) {
	if msg == nil || msg.FwdFrom == nil || msg.FwdFrom.ChannelPost <= 0 {
		return
	}
	key := forwardWaitKey{
		discussionChatID: msg.ChatID,
		channelPostID:    msg.FwdFrom.ChannelPost,
	}
	s.waitMu.Lock()
	ch, ok := s.waiters[key]
	if ok {
		delete(s.waiters, key)
	}
	s.waitMu.Unlock()
	if ok {
		select {
		case ch <- msg.ID:
		default:
		}
	}
}

// SendSummaryPoll 实现 summary.PollSender，按频道投票配置发送到源频道或绑定讨论群（对齐 poll_handlers.py:send_poll）。
func (s *Service) SendSummaryPoll(
	ctx context.Context,
	botClient mtproto.Messenger,
	channelRef string,
	summaryMessageID int,
	summaryText string,
) (int, error) {
	if botClient == nil {
		return 0, fmt.Errorf("bot client unavailable")
	}
	normChannel := config.NormalizeChannelID(channelRef)

	globalEnabled := true
	sendToChannel := false
	if s.deps.Config != nil {
		globalEnabled = s.deps.Config.EnablePoll()
		chCfg := s.deps.Config.ChannelPollConfig(normChannel)
		if chCfg.Enabled != nil {
			globalEnabled = *chCfg.Enabled
		}
		if chCfg.SendToChannel != nil {
			sendToChannel = *chCfg.SendToChannel
		}
	}
	if !globalEnabled {
		return 0, nil
	}

	if sendToChannel {
		return s.sendToChannel(ctx, botClient, normChannel, summaryMessageID, summaryText)
	}
	return s.sendToDiscussionGroup(ctx, botClient, normChannel, summaryMessageID, summaryText)
}

func (s *Service) sendToChannel(
	ctx context.Context,
	botClient mtproto.Messenger,
	channelRef string,
	summaryMessageID int,
	summaryText string,
) (int, error) {
	chInfo, err := botClient.ResolveChannel(ctx, channelRef)
	channelName := channelRef
	if err == nil && chInfo != nil && chInfo.Title != "" {
		channelName = chInfo.Title
	}

	spec, _ := GeneratePollFromSummary(ctx, summaryText, nil, GenerateOptions{
		Config:    s.deps.Config,
		Completer: s.deps.Completer,
		Pipeline:  s.deps.Pipeline,
		Logger:    s.deps.Logger,
	})
	if spec == nil {
		spec = DefaultPollSpec()
	}

	buttons := s.buildRegenButtons(summaryMessageID, 0)
	// Telegram 广播频道强制匿名投票（public_voters=false）。
	pollID, err := botClient.SendPoll(ctx, channelRef, mtproto.PollSendOptions{
		Question:     clampRunes(spec.Question, 250, 247),
		Options:      spec.Options,
		PublicVoters: false,
		ReplyToMsgID: summaryMessageID,
		Buttons:      buttons,
	})
	if err != nil {
		return 0, err
	}

	_ = s.deps.RegenStore.Add(channelRef, summaryMessageID, RegenRecord{
		PollMessageID: pollID,
		SummaryText:   summaryText,
		ChannelName:   channelName,
		SendToChannel: true,
	})
	return pollID, nil
}

func (s *Service) sendToDiscussionGroup(
	ctx context.Context,
	botClient mtproto.Messenger,
	channelRef string,
	summaryMessageID int,
	summaryText string,
) (int, error) {
	chInfo, err := botClient.ResolveChannel(ctx, channelRef)
	channelName := channelRef
	if err == nil && chInfo != nil && chInfo.Title != "" {
		channelName = chInfo.Title
	}

	discussionID, err := botClient.GetLinkedDiscussionChatID(ctx, channelRef)
	if err != nil || discussionID == 0 {
		s.deps.Logger.Warn("频道未绑定讨论组，跳过评论区投票发送", "channel", channelRef, "error", err)
		return 0, nil
	}

	// 在调用 LLM 生成投票前注册转发消息等待器，避免生成期间错过 Telegram 自动同步帖。
	key := forwardWaitKey{discussionChatID: discussionID, channelPostID: summaryMessageID}
	waitCh := make(chan int, 1)
	s.waitMu.Lock()
	s.waiters[key] = waitCh
	s.waitMu.Unlock()
	defer func() {
		s.waitMu.Lock()
		delete(s.waiters, key)
		s.waitMu.Unlock()
	}()

	spec, _ := GeneratePollFromSummary(ctx, summaryText, nil, GenerateOptions{
		Config:    s.deps.Config,
		Completer: s.deps.Completer,
		Pipeline:  s.deps.Pipeline,
		Logger:    s.deps.Logger,
	})
	if spec == nil {
		spec = DefaultPollSpec()
	}

	var forwardMsgID int
	select {
	case forwardMsgID = <-waitCh:
	default:
		timer := time.NewTimer(s.deps.ForwardWaitWindow)
		select {
		case forwardMsgID = <-waitCh:
			timer.Stop()
		case <-timer.C:
			s.deps.Logger.Warn("等待讨论组自动转发消息超时，改为发送独立投票", "channel", channelRef, "discussion_id", discussionID)
		case <-ctx.Done():
			timer.Stop()
			return 0, ctx.Err()
		}
	}

	publicVoters := s.effectivePublicVoters(channelRef)
	buttons := s.buildRegenButtons(summaryMessageID, 0)
	pollID, err := botClient.SendPoll(ctx, discussionID, mtproto.PollSendOptions{
		Question:     clampRunes(spec.Question, 250, 247),
		Options:      spec.Options,
		PublicVoters: publicVoters,
		ReplyToMsgID: forwardMsgID,
		Buttons:      buttons,
	})
	if err != nil {
		return 0, err
	}

	var fwdPtr *int
	if forwardMsgID > 0 {
		fid := forwardMsgID
		fwdPtr = &fid
	}
	_ = s.deps.RegenStore.Add(channelRef, summaryMessageID, RegenRecord{
		PollMessageID:          pollID,
		SummaryText:            summaryText,
		ChannelName:            channelName,
		SendToChannel:          false,
		DiscussionForwardMsgID: fwdPtr,
	})
	return pollID, nil
}

// HandleVoteRegenRequestCallback 处理用户点击 request_regen_{summary_message_id}（对齐 poll_regeneration_handlers.py:handle_vote_regen_request_callback）。
func (s *Service) HandleVoteRegenRequestCallback(ctx context.Context, botClient mtproto.Messenger, ev mtproto.CallbackEvent) error {
	if botClient == nil {
		return nil
	}
	voteEnabled := true
	threshold := config.DefaultPollRegenThreshold
	if s.deps.Config != nil {
		voteEnabled = s.deps.Config.EnableVoteRegenRequest()
		threshold = s.deps.Config.PollRegenThreshold()
	}
	if !voteEnabled {
		return botClient.AnswerCallback(ctx, ev.QueryID, i18n.T("poll_regen.feature_disabled", nil), true)
	}

	summaryMsgID, ok := parseTrailingInt(ev.DataString(), "request_regen_")
	if !ok {
		return botClient.AnswerCallback(ctx, ev.QueryID, i18n.T("poll_regen.invalid_format", nil), true)
	}

	targetChannel, rec, found := s.deps.RegenStore.FindBySummaryMessageID(summaryMsgID)
	if !found || rec == nil {
		return botClient.AnswerCallback(ctx, ev.QueryID, i18n.T("poll_regen.data_not_found", nil), true)
	}

	added, count, alreadyVoted := s.deps.RegenStore.IncrementVote(targetChannel, summaryMsgID, ev.SenderID)
	params := map[string]string{
		"count":     strconv.Itoa(count),
		"threshold": strconv.Itoa(threshold),
	}
	if alreadyVoted {
		return botClient.AnswerCallback(ctx, ev.QueryID, i18n.T("poll_regen.already_voted", params), true)
	}
	if !added {
		return botClient.AnswerCallback(ctx, ev.QueryID, i18n.T("poll_regen.data_not_found", nil), true)
	}

	buttons := s.buildRegenButtons(summaryMsgID, count)
	_ = botClient.EditMessage(ctx, ev.ChatID, rec.PollMessageID, "", mtproto.SendOptions{Buttons: buttons})
	_ = botClient.AnswerCallback(ctx, ev.QueryID, i18n.T("poll_regen.vote_success", params), false)

	if count >= threshold {
		if latest, ok := s.deps.RegenStore.Get(targetChannel, summaryMsgID); ok {
			if s.RegeneratePoll(ctx, botClient, targetChannel, summaryMsgID, *latest) {
				s.deps.RegenStore.ResetVotes(targetChannel, summaryMsgID)
			}
		}
	}
	return nil
}

// HandleAdminRegenCallback 处理管理员点击 regen_poll_{summary_message_id}（对齐 handle_poll_regeneration_callback）。
func (s *Service) HandleAdminRegenCallback(ctx context.Context, botClient mtproto.Messenger, ev mtproto.CallbackEvent) error {
	if botClient == nil {
		return nil
	}
	if s.deps.Config == nil || !s.deps.Config.IsAdmin(ev.SenderID) {
		return botClient.AnswerCallback(ctx, ev.QueryID, i18n.T("poll_regen.admin_only", nil), true)
	}

	summaryMsgID, ok := parseTrailingInt(ev.DataString(), "regen_poll_")
	if !ok {
		return botClient.AnswerCallback(ctx, ev.QueryID, i18n.T("poll_regen.invalid_format", nil), true)
	}

	targetChannel, rec, found := s.deps.RegenStore.FindBySummaryMessageID(summaryMsgID)
	if !found || rec == nil {
		return botClient.AnswerCallback(ctx, ev.QueryID, i18n.T("poll_regen.data_not_found", nil), true)
	}

	_ = botClient.AnswerCallback(ctx, ev.QueryID, i18n.T("poll_regen.regen_in_progress", nil), false)
	if s.RegeneratePoll(ctx, botClient, targetChannel, summaryMsgID, *rec) {
		s.deps.RegenStore.ResetVotes(targetChannel, summaryMsgID)
	}
	return nil
}

// RegeneratePoll 删除旧投票并在原位置重新生成新投票（对齐 regenerate_poll）。
func (s *Service) RegeneratePoll(
	ctx context.Context,
	botClient mtproto.Messenger,
	channelRef string,
	summaryMsgID int,
	rec RegenRecord,
) bool {
	if botClient == nil {
		return false
	}

	toDelete := []int{rec.PollMessageID}
	if rec.ButtonMessageID != nil && *rec.ButtonMessageID > 0 {
		toDelete = append(toDelete, *rec.ButtonMessageID)
	}

	var discussionID int64
	if rec.SendToChannel {
		_ = botClient.DeleteMessages(ctx, channelRef, toDelete)
	} else {
		discussionID, _ = botClient.GetLinkedDiscussionChatID(ctx, channelRef)
		if discussionID != 0 {
			_ = botClient.DeleteMessages(ctx, discussionID, toDelete)
		} else {
			_ = botClient.DeleteMessages(ctx, channelRef, toDelete)
		}
	}

	spec, _ := GeneratePollFromSummary(ctx, rec.SummaryText, nil, GenerateOptions{
		Config:    s.deps.Config,
		Completer: s.deps.Completer,
		Pipeline:  s.deps.Pipeline,
		Logger:    s.deps.Logger,
	})
	if spec == nil {
		spec = DefaultPollSpec()
	}

	buttons := s.buildRegenButtons(summaryMsgID, 0)
	var newPollID int
	var err error
	if rec.SendToChannel {
		newPollID, err = botClient.SendPoll(ctx, channelRef, mtproto.PollSendOptions{
			Question:     clampRunes(spec.Question, 250, 247),
			Options:      spec.Options,
			PublicVoters: false,
			ReplyToMsgID: summaryMsgID,
			Buttons:      buttons,
		})
	} else {
		if discussionID == 0 {
			discussionID, err = botClient.GetLinkedDiscussionChatID(ctx, channelRef)
			if err != nil || discussionID == 0 {
				return false
			}
		}
		replyID := 0
		if rec.DiscussionForwardMsgID != nil && *rec.DiscussionForwardMsgID > 0 {
			replyID = *rec.DiscussionForwardMsgID
		}
		newPollID, err = botClient.SendPoll(ctx, discussionID, mtproto.PollSendOptions{
			Question:     clampRunes(spec.Question, 250, 247),
			Options:      spec.Options,
			PublicVoters: s.effectivePublicVoters(channelRef),
			ReplyToMsgID: replyID,
			Buttons:      buttons,
		})
	}
	if err != nil || newPollID <= 0 {
		s.deps.Logger.Error("重新发送新投票失败", "channel", channelRef, "error", err)
		return false
	}

	_ = s.deps.RegenStore.UpdatePollMessage(channelRef, summaryMsgID, newPollID, nil)
	if s.deps.SummaryUpdater != nil {
		_ = s.deps.SummaryUpdater.UpdatePollMessageIDs(channelRef, []int{newPollID})
	}
	if s.deps.SummaryRepo != nil {
		if err := s.deps.SummaryRepo.UpdatePollMessageIDs(ctx, channelRef, int64(summaryMsgID), int64(newPollID), 0); err != nil {
			s.deps.Logger.Warn("更新总结投票记录失败", "channel", channelRef, "error", err)
		}
	}
	return true
}

func (s *Service) buildRegenButtons(summaryMsgID, count int) [][]mtproto.InlineButton {
	voteEnabled := true
	threshold := config.DefaultPollRegenThreshold
	if s.deps.Config != nil {
		voteEnabled = s.deps.Config.EnableVoteRegenRequest()
		threshold = s.deps.Config.PollRegenThreshold()
	}
	var rows [][]mtproto.InlineButton
	if voteEnabled {
		rows = append(rows, []mtproto.InlineButton{{
			Text: i18n.T("poll_regen.request_button", map[string]string{
				"count":     strconv.Itoa(count),
				"threshold": strconv.Itoa(threshold),
			}),
			Data: fmt.Sprintf("request_regen_%d", summaryMsgID),
		}})
	}
	rows = append(rows, []mtproto.InlineButton{{
		Text: i18n.T("poll_regen.admin_button", nil),
		Data: fmt.Sprintf("regen_poll_%d", summaryMsgID),
	}})
	return rows
}

func (s *Service) effectivePublicVoters(channelRef string) bool {
	if s.deps.Config == nil {
		return true
	}
	pub := s.deps.Config.PollPublicVoters()
	chCfg := s.deps.Config.ChannelPollConfig(channelRef)
	if chCfg.PublicVoters != nil {
		pub = *chCfg.PublicVoters
	}
	return pub
}

func parseTrailingInt(data, prefix string) (int, bool) {
	if !strings.HasPrefix(data, prefix) {
		return 0, false
	}
	id, err := strconv.Atoi(strings.TrimPrefix(data, prefix))
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
