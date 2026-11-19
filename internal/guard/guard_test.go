// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package guard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

type fakeTelegramOps struct {
	mu           sync.Mutex
	botID        int64
	channels     map[int64]*mtproto.ChannelInfo
	resolveErrs  map[int64]error
	permissions  map[int64]*mtproto.ParticipantPermissions
	permErrs     map[int64]error
	leftChats    []int64
	deletedMsgs  map[int64][]int
	sentMessages []string
}

func newFakeOps(botID int64) *fakeTelegramOps {
	return &fakeTelegramOps{
		botID:       botID,
		channels:    make(map[int64]*mtproto.ChannelInfo),
		resolveErrs: make(map[int64]error),
		permissions: make(map[int64]*mtproto.ParticipantPermissions),
		permErrs:    make(map[int64]error),
		deletedMsgs: make(map[int64][]int),
	}
}

func (f *fakeTelegramOps) BotID() int64 { return f.botID }
func (f *fakeTelegramOps) KnownGroupPeers() []mtproto.CachedPeer {
	return []mtproto.CachedPeer{
		{MarkedID: mtproto.ChannelMarkedID(111), Title: "Admin Supergroup"},
		{MarkedID: mtproto.ChannelMarkedID(222), Title: "Member Supergroup"},
		{MarkedID: mtproto.ChannelMarkedID(333), Title: "Channel DM"},
		{MarkedID: mtproto.ChannelMarkedID(444), Title: "Private Lost Group"},
	}
}
func (f *fakeTelegramOps) ResolveChannel(_ context.Context, ref string) (*mtproto.ChannelInfo, error) {
	for cid, info := range f.channels {
		if cid != 0 && strings.TrimSpace(ref) == formatInt64(cid) {
			return info, nil
		}
	}
	for cid, err := range f.resolveErrs {
		if strings.TrimSpace(ref) == formatInt64(cid) {
			return nil, err
		}
	}
	return nil, errors.New("ChannelInvalidError")
}
func (f *fakeTelegramOps) Permissions(_ context.Context, chatID, _ int64) (*mtproto.ParticipantPermissions, error) {
	if err, ok := f.permErrs[chatID]; ok {
		return nil, err
	}
	if p, ok := f.permissions[chatID]; ok {
		return p, nil
	}
	return &mtproto.ParticipantPermissions{IsMember: true}, nil
}
func (f *fakeTelegramOps) Rights(context.Context, int64, int64) (mtproto.BannedRights, error) {
	return mtproto.BannedRights{}, nil
}
func (f *fakeTelegramOps) SetRights(context.Context, int64, int64, mtproto.BannedRights) error {
	return nil
}
func (f *fakeTelegramOps) Ban(context.Context, int64, int64) error            { return nil }
func (f *fakeTelegramOps) Unban(context.Context, int64, int64) error          { return nil }
func (f *fakeTelegramOps) KickBasicGroup(context.Context, int64, int64) error { return nil }
func (f *fakeTelegramOps) DeleteMessages(_ context.Context, chatID int64, ids []int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletedMsgs[chatID] = append(f.deletedMsgs[chatID], ids...)
	return nil
}
func (f *fakeTelegramOps) GetMessages(context.Context, int64, []int) ([]*mtproto.IncomingMessage, error) {
	return nil, nil
}
func (f *fakeTelegramOps) DownloadMediaBytes(context.Context, *mtproto.MediaAttachment, int64) ([]byte, error) {
	return nil, nil
}
func (f *fakeTelegramOps) SendMessage(_ context.Context, _ any, text string, _ mtproto.SendOptions) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sentMessages = append(f.sentMessages, text)
	return len(f.sentMessages), nil
}
func (f *fakeTelegramOps) SendOnce(_ context.Context, _ int64, text string, _ mtproto.SendOptions, _ int64) (int, error) {
	return f.SendMessage(context.Background(), 0, text, mtproto.SendOptions{})
}
func (f *fakeTelegramOps) EditMessage(context.Context, any, int, string, mtproto.SendOptions) error {
	return nil
}
func (f *fakeTelegramOps) ForwardOnce(context.Context, int64, int64, int, int64) error { return nil }
func (f *fakeTelegramOps) AnswerCallback(context.Context, int64, string, bool) error   { return nil }
func (f *fakeTelegramOps) JoinRequest(context.Context, int64, int64, bool) error       { return nil }
func (f *fakeTelegramOps) AnswerJoinRequestQuery(context.Context, int64, string, float64) error {
	return nil
}
func (f *fakeTelegramOps) LeaveChat(_ context.Context, chatID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leftChats = append(f.leftChats, chatID)
	return nil
}
func (f *fakeTelegramOps) GetFullUserBio(context.Context, int64) (*mtproto.ProfileSummary, error) {
	return nil, nil
}
func (f *fakeTelegramOps) ScanDeletedParticipants(context.Context, int64, int, int) (*mtproto.DeletedParticipantsPage, error) {
	return &mtproto.DeletedParticipantsPage{Done: true}, nil
}
func (f *fakeTelegramOps) ScanHistoryDeletedSenders(context.Context, int64, int, int) (*mtproto.DeletedHistoryPage, error) {
	return &mtproto.DeletedHistoryPage{Done: true}, nil
}
func (f *fakeTelegramOps) RevokeMessagesViaBotAPI(context.Context, string, int64, int64) error {
	return nil
}

type fakeMessenger struct {
	mu      sync.Mutex
	replies []string
}

func (m *fakeMessenger) SelfID() int64        { return 777000 }
func (m *fakeMessenger) SelfUsername() string { return "sakura_main_bot" }
func (m *fakeMessenger) IsConnected() bool    { return true }
func (m *fakeMessenger) ResolveChannel(context.Context, string) (*mtproto.ChannelInfo, error) {
	return nil, nil
}
func (m *fakeMessenger) GetLinkedDiscussionChatID(context.Context, string) (int64, error) {
	return 0, nil
}
func (m *fakeMessenger) ClearLinkedChatCache(string) int   { return 0 }
func (m *fakeMessenger) LinkedChatCacheChannels() []string { return nil }
func (m *fakeMessenger) SendMessage(_ context.Context, _ any, text string, _ mtproto.SendOptions) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.replies = append(m.replies, text)
	return len(m.replies), nil
}
func (m *fakeMessenger) SendLongMessage(context.Context, any, string, mtproto.SendOptions) ([]int, error) {
	return nil, nil
}
func (m *fakeMessenger) EditMessage(context.Context, any, int, string, mtproto.SendOptions) error {
	return nil
}
func (m *fakeMessenger) DeleteMessages(context.Context, any, []int) error { return nil }
func (m *fakeMessenger) ForwardMessages(context.Context, any, any, []int) ([]int, error) {
	return nil, nil
}
func (m *fakeMessenger) PinMessage(context.Context, any, int, bool) error { return nil }
func (m *fakeMessenger) SendPoll(context.Context, any, mtproto.PollSendOptions) (int, error) {
	return 1, nil
}
func (m *fakeMessenger) SendMedia(context.Context, any, mtproto.MediaUpload, mtproto.SendOptions) (int, error) {
	return 1, nil
}
func (m *fakeMessenger) SendAlbum(context.Context, any, []mtproto.MediaUpload, mtproto.SendOptions) ([]int, error) {
	return nil, nil
}
func (m *fakeMessenger) GetMessages(context.Context, any, []int) ([]*mtproto.IncomingMessage, error) {
	return nil, nil
}
func (m *fakeMessenger) FetchChannelHistory(context.Context, string, time.Time, map[int]bool) ([]*mtproto.IncomingMessage, *mtproto.ChannelInfo, error) {
	return nil, nil, nil
}
func (m *fakeMessenger) DownloadMediaBytes(context.Context, *mtproto.MediaAttachment, int64) ([]byte, error) {
	return nil, nil
}
func (m *fakeMessenger) DownloadMediaToFile(context.Context, *mtproto.MediaAttachment, string, int64) (int64, error) {
	return 0, nil
}
func (m *fakeMessenger) DownloadProfilePhoto(context.Context, any, int64) ([]byte, int64, error) {
	return nil, 0, nil
}
func (m *fakeMessenger) AnswerCallback(context.Context, int64, string, bool) error { return nil }
func (m *fakeMessenger) SetBotCommands(context.Context, string, []mtproto.BotCommandDef) error {
	return nil
}

func formatInt64(v int64) string {
	return configIntString(v)
}

func configIntString(v int64) string {
	var b [24]byte
	return string(appendInt(b[:0], v))
}

func appendInt(dst []byte, v int64) []byte {
	if v == 0 {
		return append(dst, '0')
	}
	if v < 0 {
		dst = append(dst, '-')
		v = -v
	}
	var tmp [20]byte
	i := len(tmp)
	for v > 0 {
		i--
		tmp[i] = byte('0' + v%10)
		v /= 10
	}
	return append(dst, tmp[i:]...)
}

func TestConfigMigrationAndStrictValidation(t *testing.T) {
	doc := map[string]any{
		"sakura_guard": map[string]any{
			"nickname_keywords": []any{"主页"},
			"groups": map[string]any{
				"-100123456": map[string]any{
					"enabled":           true,
					"nickname_keywords": []any{"兼职", "主页"},
				},
			},
		},
	}
	if !MigrateNicknameKeywords(doc) {
		t.Fatalf("expected legacy per-group nickname_keywords to be migrated")
	}
	parsed, err := ParseGuardConfig(doc)
	if err != nil {
		t.Fatalf("ParseGuardConfig failed: %v", err)
	}
	if len(parsed.NicknameKeywords) != 2 || parsed.NicknameKeywords[1] != "兼职" {
		t.Fatalf("unexpected migrated keywords: %+v", parsed.NicknameKeywords)
	}
	if !parsed.Groups["-100123456"].Enabled {
		t.Fatalf("expected group -100123456 enabled")
	}

	// 非法正整数群 ID 应被严格拒绝
	badDoc := map[string]any{
		"sakura_guard": map[string]any{
			"groups": map[string]any{
				"12345": map[string]any{"enabled": true},
			},
		},
	}
	if _, err := ParseGuardConfig(badDoc); err == nil {
		t.Fatalf("expected positive chat_id key to fail validation")
	}
}

func TestNormalizeAndMatcher(t *testing.T) {
	// 零宽字符 + 繁体 + 全角混合防绕过归一化
	norm := NormalizeText("點\u200b擊\u200d頭\u0301像  ＶＩＰ")
	if !strings.Contains(norm, "点击头像vip") {
		t.Fatalf("NormalizeText did not strip invisible/t2s/NFKC properly: %q", norm)
	}

	// UTF-16 实体链接提取（包含 emoji 占 2 个 UTF-16 单元）
	text := "🔥 点击这里领福利"
	// "🔥 " = 2 + 1 = 3 UTF-16 code units; "点击这里" = 4 UTF-16 code units
	links := ExtractLinks(text, []mtproto.EntitySpan{
		{Kind: "text_url", Offset: 3, Length: 4, URL: "https://scam.example.org/promo?a=1"},
	})
	if len(links) != 1 || links[0] != "https://scam.example.org/promo?a=1" {
		t.Fatalf("ExtractLinks failed: %+v", links)
	}

	globalCfg := DefaultGuardConfig()
	grp := DefaultGuardGroup()
	grp.Enabled = true
	grp.BanAnyTraditional = true
	grp.MessageFilter = true
	grp.Rules = []GuardRule{
		{ID: "r-link", Target: "link", Kind: "regex", Value: `scam\.example\.org`, Action: "ban"},
	}

	m := NewMatcher()
	hit := m.MatchNickname(mtproto.ProfileSummary{ID: 10, FirstName: "專屬客服"}, globalCfg, grp)
	if hit == nil || hit.RuleID != "traditional-chinese-nickname" {
		t.Fatalf("expected traditional nickname hit, got %+v", hit)
	}

	msgHit := m.MatchMessageAndLinks(&mtproto.IncomingMessage{
		Text: text,
		Entities: []mtproto.EntitySpan{
			{Kind: "text_url", Offset: 3, Length: 4, URL: "https://scam.example.org/promo"},
		},
	}, grp)
	if msgHit == nil || msgHit.RuleID != "r-link" || msgHit.Action != "ban" {
		t.Fatalf("expected link regex rule hit, got %+v", msgHit)
	}
}

func TestAIRuleSafetyAndVerdictGuards(t *testing.T) {
	if SafeRegex(`foo.*bar`) {
		t.Fatalf("expected .* to be rejected")
	}
	if SafeRegex(`(capturing)`) {
		t.Fatalf("expected capturing group to be rejected")
	}
	if !SafeRegex(`(?:usdt|trx)[0-9]{2,6}`) {
		t.Fatalf("expected non-capturing bounded regex to pass")
	}

	if _, ok := SanitizeDomain("https://t.me/some_group"); ok {
		t.Fatalf("expected t.me to be blocked by domain denylist")
	}
	if dom, ok := SanitizeDomain("https://www.Evil-Scam.COM/landing"); !ok || dom != "evil-scam.com" {
		t.Fatalf("unexpected sanitized domain: %q, %v", dom, ok)
	}

	sources := map[string]string{
		"message": "需要兼职日结 500U 的私聊飞机 @badguy",
	}
	validVerdict := ReportVerdict{
		Verdict:    "violation",
		Category:   "scam",
		Confidence: 0.98,
		Evidence:   []QuoteEvidence{{Source: "message", Quote: "兼职日结 500U"}},
	}
	if !IsActionableVerdict(validVerdict, 0.95, []string{"scam", "spam"}, false, sources) {
		t.Fatalf("expected exact quote verdict to be actionable")
	}

	hallucinated := validVerdict
	hallucinated.Evidence = []QuoteEvidence{{Source: "message", Quote: "不存在的捏造引用"}}
	if IsActionableVerdict(hallucinated, 0.95, []string{"scam", "spam"}, false, sources) {
		t.Fatalf("expected hallucinated quote to be rejected")
	}
}

func TestVerificationRiskAndObservationHeuristics(t *testing.T) {
	score, flags, _ := ComputeRiskScore(
		mtproto.ProfileSummary{ID: 42, FirstName: "user9876543"},
		2,
		true,
		nil,
	)
	if score < 8 || len(flags) < 4 {
		t.Fatalf("expected high risk score and flags, got score=%d flags=%v", score, flags)
	}

	prompt, opts, ans := BuildLegacyChallenge("word")
	if prompt == "" || len(opts) != 4 || ans < 0 || ans >= 4 {
		t.Fatalf("invalid word challenge: %q %v %d", prompt, opts, ans)
	}

	reason, _ := EvaluateHeuristics(&mtproto.IncomingMessage{
		Text: "诚聘线上兼职，日结 800 元，有意者私聊咨询",
	}, false, nil, nil, time.Now().Unix())
	if reason != "observation_recruitment_spam" {
		t.Fatalf("expected observation_recruitment_spam, got %q", reason)
	}
}

func TestDirectoryCleanupAndGuardCommands(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	cfgPath := filepath.Join(tmpDir, "config.json")
	_ = os.WriteFile(envPath, []byte("TELEGRAM_API_ID=12345\nTELEGRAM_API_HASH=abcdef\nTELEGRAM_BOT_TOKEN=777000:testtoken\nREPORT_ADMIN_IDS=999\n"), 0o600)
	_ = os.WriteFile(cfgPath, []byte(`{"channels":[]}`), 0o600)

	appCfg := config.New(envPath, cfgPath)
	if err := appCfg.Init(); err != nil {
		t.Fatalf("appCfg.Init: %v", err)
	}

	cAdmin := mtproto.ChannelMarkedID(111)
	cMember := mtproto.ChannelMarkedID(222)
	cDM := mtproto.ChannelMarkedID(333)
	cLost := mtproto.ChannelMarkedID(444)

	ops := newFakeOps(777000)
	ops.channels[cAdmin] = &mtproto.ChannelInfo{MarkedID: cAdmin, Title: "Admin Supergroup", Megagroup: true}
	ops.permissions[cAdmin] = &mtproto.ParticipantPermissions{
		IsMember:          true,
		IsAdmin:           true,
		CanBanUsers:       true,
		CanDeleteMessages: true,
	}
	ops.channels[cMember] = &mtproto.ChannelInfo{MarkedID: cMember, Title: "Member Supergroup", Megagroup: true}
	ops.permissions[cMember] = &mtproto.ParticipantPermissions{
		IsMember: true,
		IsAdmin:  false,
	}
	ops.channels[cDM] = &mtproto.ChannelInfo{MarkedID: cDM, Title: "Channel DM", Monoforum: true, LinkedChatID: cAdmin}
	ops.resolveErrs[cLost] = errors.New("ChannelPrivateError")

	svc := NewService(nil, appCfg, ops, nil, nil)
	ctx := context.Background()

	svc.Directory().ScanSync(ctx)
	snap := svc.Directory().Snapshot()
	if snap["related_chat_count"] != 1 {
		t.Fatalf("expected 1 monoforum related chat, got %v", snap["related_chat_count"])
	}
	if snap["history_cleanable_count"] != 1 {
		t.Fatalf("expected 1 ChannelPrivateError cleanable chat, got %v", snap["history_cleanable_count"])
	}

	preview, err := svc.ChatCleanup().Preview("admin")
	if err != nil {
		t.Fatalf("ChatCleanup.Preview: %v", err)
	}
	if preview["total"] != 1 {
		t.Fatalf("expected exactly 1 non-admin group (%d) in leave preview, got %v", cMember, preview["total"])
	}
	tok := preview["token"].(string)
	if _, err := svc.ChatCleanup().Start(tok, "admin", nil); err != nil {
		t.Fatalf("ChatCleanup.Start: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st := svc.ChatCleanup().Status()
		if st != nil && st["status"] == "completed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(ops.leftChats) != 1 || ops.leftChats[0] != cMember {
		t.Fatalf("expected LeaveChat(%d), got %v", cMember, ops.leftChats)
	}

	// 测试 /guard_on 与 /guard_addword 命令闭环
	bot := &fakeMessenger{}
	if err := svc.HandleGuardCommand(ctx, bot, &mtproto.IncomingMessage{
		ID:     1,
		ChatID: cAdmin,
		Sender: mtproto.ProfileSummary{ID: 999},
	}, "guard_on", ""); err != nil {
		t.Fatalf("HandleGuardCommand /guard_on: %v", err)
	}
	if !svc.Group(cAdmin).Enabled {
		t.Fatalf("expected %d to be enabled and persisted, got bot replies %v", cAdmin, bot.replies)
	}

	if err := svc.HandleGuardCommand(ctx, bot, &mtproto.IncomingMessage{
		ID:     2,
		ChatID: 999,
		Sender: mtproto.ProfileSummary{ID: 999},
	}, "guard_addword", "高仿测试词"); err != nil {
		t.Fatalf("HandleGuardCommand /guard_addword: %v", err)
	}
	if !strings.Contains(strings.Join(svc.Config().NicknameKeywords, ","), "高仿测试词") {
		t.Fatalf("expected new global keyword persisted, got %v", svc.Config().NicknameKeywords)
	}
}
