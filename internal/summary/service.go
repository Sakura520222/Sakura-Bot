// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package summary

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/db"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
	"github.com/Sakura520222/Sakura-Bot/internal/llm"
	"github.com/Sakura520222/Sakura-Bot/internal/media"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/Sakura520222/Sakura-Bot/internal/vectors"
)

var (
	weeklyRangeRE = regexp.MustCompile(`\*\*.*?周报\s*(\d{1,2})\.(\d{1,2})\s*[-—~]\s*(\d{1,2})\.(\d{1,2})\*\*`)
	dailyDateRE   = regexp.MustCompile(`\*\*.*?日报\s*(\d{1,2})\.(\d{1,2})\*\*`)
)

// ExtractDateRangeFromSummary 从报告标题中提取周报/日报起止日期（UTC，对齐 client_management.py:extract_date_range_from_summary）。
func ExtractDateRangeFromSummary(summaryText string, refNow time.Time) (*time.Time, *time.Time) {
	if refNow.IsZero() {
		refNow = time.Now().UTC()
	} else {
		refNow = refNow.UTC()
	}
	year := refNow.Year()

	if m := weeklyRangeRE.FindStringSubmatch(summaryText); len(m) == 5 {
		sm, _ := strconv.Atoi(m[1])
		sd, _ := strconv.Atoi(m[2])
		em, _ := strconv.Atoi(m[3])
		ed, _ := strconv.Atoi(m[4])
		if validMonthDay(sm, sd) && validMonthDay(em, ed) {
			start := time.Date(year, time.Month(sm), sd, 0, 0, 0, 0, time.UTC)
			end := time.Date(year, time.Month(em), ed, 23, 59, 59, 0, time.UTC)
			if end.Before(start) {
				end = time.Date(year+1, time.Month(em), ed, 23, 59, 59, 0, time.UTC)
			}
			return &start, &end
		}
	}

	if m := dailyDateRE.FindStringSubmatch(summaryText); len(m) == 3 {
		mo, _ := strconv.Atoi(m[1])
		da, _ := strconv.Atoi(m[2])
		if validMonthDay(mo, da) {
			start := time.Date(year, time.Month(mo), da, 0, 0, 0, 0, time.UTC)
			end := time.Date(year, time.Month(mo), da, 23, 59, 59, 0, time.UTC)
			return &start, &end
		}
	}
	return nil, nil
}

func validMonthDay(month, day int) bool {
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return false
	}
	t := time.Date(2024, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return int(t.Month()) == month && t.Day() == day
}

// SummaryRepo 抽象总结落库接口（由 *db.Manager 实现）。
type SummaryRepo interface {
	SaveSummary(ctx context.Context, in db.SummaryInsert) (int64, error)
}

// VectorAdder 抽象总结向量入库接口（由 *vectors.Store 适配器实现）。
type VectorAdder interface {
	AddSummary(ctx context.Context, summaryID int64, channelID, channelName, summaryText string, startTime, endTime, createdAt time.Time, messageCount int) error
}

// PollSender 抽象总结发出后附加互动投票的钩子（由 *poll.Service 实现）。
type PollSender interface {
	SendSummaryPoll(ctx context.Context, botClient mtproto.Messenger, channelRef string, summaryMessageID int, summaryText string) (int, error)
}

// ReportSendResult 描述报告发送后产生的消息 ID。
type ReportSendResult struct {
	SummaryMessageIDs []int `json:"summary_message_ids"`
	PollMessageID     int   `json:"poll_message_id,omitempty"`
	ButtonMessageID   int   `json:"button_message_id,omitempty"`
}

// GenerateResult 对齐 generate_channel_summary 返回值。
type GenerateResult struct {
	Success      bool             `json:"success"`
	SummaryID    int64            `json:"summary_id,omitempty"`
	SummaryText  string           `json:"summary_text,omitempty"`
	MessageCount int              `json:"message_count"`
	ChannelID    string           `json:"channel_id"`
	ChannelName  string           `json:"channel_name,omitempty"`
	MessageIDs   ReportSendResult `json:"message_ids"`
	Error        string           `json:"error,omitempty"`
}

// ServiceDeps 注入总结核心服务所需的所有依赖。
type ServiceDeps struct {
	Config        *config.Config
	BotClient     func() mtproto.Messenger
	HistoryClient func() mtproto.Messenger // 优先返回 UserBot，未启用或回退时返回可用 MTProto 客户端
	Completer     func() (Completer, string)
	DB            SummaryRepo
	VectorStore   VectorAdder
	ImagePipeline *media.ImagePipeline
	TimeManager   *TimeManager
	PushHandler   *PushHandler
	PollSender    PollSender
	SourceArchDir string
	AuditDir      string
	Logger        *slog.Logger
}

// Service 统一承载手动命令、定时调度与跨 Bot 请求三种路径的频道总结编排。
type Service struct {
	deps     ServiceDeps
	inFlight sync.Map
}

// NewService 创建总结业务服务。
func NewService(deps ServiceDeps) *Service {
	if deps.TimeManager == nil {
		deps.TimeManager = NewTimeManager("")
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &Service{deps: deps}
}

// TimeManager 返回内部时间状态管理器。
func (s *Service) TimeManager() *TimeManager { return s.deps.TimeManager }

// SetPollSender 允许在初始化阶段后绑定投票发送器（解耦 summary 与 poll 初始化顺序）。
func (s *Service) SetPollSender(ps PollSender) { s.deps.PollSender = ps }

// FetchChannelMessages 抓取单个或多个频道自 startTime 起的新消息（带指数退避、图片 OCR 富化与原文归档，对齐 fetch_last_week_messages）。
func (s *Service) FetchChannelMessages(
	ctx context.Context,
	channels []string,
	startTimes map[string]time.Time,
	excludeByChannel map[string]map[int]bool,
) (map[string][]string, map[string]string, error) {
	histClient := s.resolveHistoryClient()
	if histClient == nil {
		return nil, nil, fmt.Errorf("历史消息抓取客户端未就绪（请检查 UserBot 会话）")
	}

	if len(channels) == 0 && s.deps.Config != nil {
		channels = s.deps.Config.Channels()
	}
	outMsgs := make(map[string][]string, len(channels))
	titles := make(map[string]string, len(channels))
	if len(channels) == 0 {
		return outMsgs, titles, nil
	}

	archiveDir := s.deps.SourceArchDir
	if archiveDir == "" && s.deps.Config != nil {
		archiveDir = s.deps.Config.MessageSourceDir()
	}
	if archiveDir == "" {
		archiveDir = config.DefaultMessageSourceDir
	}

	defaultStart := time.Now().UTC().Add(-7 * 24 * time.Hour)
	for _, ch := range channels {
		start := defaultStart
		if st, ok := startTimes[ch]; ok && !st.IsZero() {
			start = st.UTC()
		}
		excluded := excludeByChannel[ch]

		var rawList []*mtproto.IncomingMessage
		var chInfo *mtproto.ChannelInfo
		var fetchErr error
		for attempt := 0; attempt < 3; attempt++ {
			rawList, chInfo, fetchErr = histClient.FetchChannelHistory(ctx, ch, start, excluded)
			if fetchErr == nil {
				break
			}
			if attempt < 2 {
				if serr := sleepCtx(ctx, time.Duration(2*(1<<attempt))*time.Second); serr != nil {
					return nil, nil, serr
				}
			}
		}
		if fetchErr != nil {
			return nil, nil, fmt.Errorf("抓取频道 %s 消息失败: %w", ch, fetchErr)
		}

		if chInfo != nil && chInfo.Title != "" {
			titles[ch] = chInfo.Title
		}

		formatted := make([]string, 0, len(rawList))
		slug := channelSlug(ch, chInfo)
		for _, msg := range rawList {
			if msg == nil {
				continue
			}
			if excluded[msg.ID] {
				continue
			}
			text := s.enrichIncomingMessage(ctx, ch, chInfo, msg)
			if strings.TrimSpace(text) == "" {
				continue
			}
			msgLink := msg.PostLink
			if msgLink == "" {
				msgLink = fmt.Sprintf("https://t.me/%s/%d", slug, msg.ID)
			}
			msgDateISO := msg.Date.UTC().Format(time.RFC3339)
			_ = vectors.ArchiveSource(archiveDir, vectors.SourceRecord{
				ChannelID:       ch,
				MessageID:       int64(msg.ID),
				Text:            text,
				CreatedAt:       msgDateISO,
				ChannelName:     titles[ch],
				ChannelUsername: chUsername(chInfo),
				PostURL:         msgLink,
				SenderID:        msg.SenderID,
			})
			formatted = append(formatted, fmt.Sprintf(
				"频道: %s\n消息ID: %d\n发布时间: %s\n内容: %s\n链接: %s",
				ch, msg.ID, msgDateISO, text, msgLink,
			))
		}
		outMsgs[ch] = formatted
	}
	return outMsgs, titles, nil
}

// SendReport 将生成的总结报告发送给管理员列表及可选源频道（含自动置顶与投票发送，对齐 messaging.py:send_report）。
func (s *Service) SendReport(
	ctx context.Context,
	reportText string,
	sourceChannel string,
	skipAdmins bool,
) ReportSendResult {
	var res ReportSendResult
	botClient := s.resolveBotClient()
	if botClient == nil {
		s.deps.Logger.Error("主 Bot 客户端未就绪，无法发送总结报告")
		return res
	}

	if !skipAdmins && s.deps.Config != nil {
		for _, adminID := range s.deps.Config.AdminIDs() {
			if _, err := botClient.SendLongMessage(ctx, adminID, reportText, mtproto.SendOptions{
				ParseMode: "markdown",
				NoWebpage: true,
			}); err != nil {
				s.deps.Logger.Error("向管理员发送总结报告失败", "admin_id", adminID, "error", err)
			}
		}
	}

	if strings.TrimSpace(sourceChannel) != "" {
		msgIDs, err := botClient.SendLongMessage(ctx, sourceChannel, reportText, mtproto.SendOptions{
			ParseMode: "markdown",
			NoWebpage: true,
		})
		if err != nil {
			s.deps.Logger.Error("向源频道发送总结报告失败", "channel", sourceChannel, "error", err)
			return res
		}
		res.SummaryMessageIDs = msgIDs
		if len(msgIDs) > 0 {
			firstID := msgIDs[0]
			if perr := botClient.PinMessage(ctx, sourceChannel, firstID, false); perr != nil {
				s.deps.Logger.Warn("自动置顶总结消息失败（可能需要频道管理员置顶权限）", "channel", sourceChannel, "message_id", firstID, "error", perr)
			}
			if s.deps.PollSender != nil {
				pollID, pollErr := s.deps.PollSender.SendSummaryPoll(ctx, botClient, sourceChannel, firstID, reportText)
				if pollErr != nil {
					s.deps.Logger.Warn("发送总结互动投票失败", "channel", sourceChannel, "error", pollErr)
				} else if pollID > 0 {
					res.PollMessageID = pollID
				}
			}
		}
	}
	return res
}

// GenerateChannelSummary 为指定频道执行完整总结闭环：读取上次时间 -> 抓历史 -> 分层摘要 -> 发送报告/置顶/投票 -> 单次精准落库与向量化 -> 通知订阅者 -> 更新时间文件。
func (s *Service) GenerateChannelSummary(
	ctx context.Context,
	channelRef string,
	skipAdmins bool,
	summaryType string,
) GenerateResult {
	normChannel := config.NormalizeChannelID(channelRef)
	key := strings.ToLower(normChannel)
	if _, active := s.inFlight.LoadOrStore(key, struct{}{}); active {
		return GenerateResult{ChannelID: normChannel, Error: "该频道已有总结任务正在运行"}
	}
	defer s.inFlight.Delete(key)
	if summaryType == "" {
		summaryType = "manual"
	}

	startTimes := map[string]time.Time{}
	excludeMap := map[string]map[int]bool{}
	var lastSummaryTime time.Time
	if prevState, ok, err := s.deps.TimeManager.LoadChannel(normChannel); err == nil && ok && prevState != nil {
		lastSummaryTime = prevState.Time
		startTimes[normChannel] = prevState.Time
		excludeMap[normChannel] = prevState.AllExcludedIDs()
	}

	msgsByCh, titlesByCh, err := s.FetchChannelMessages(ctx, []string{normChannel}, startTimes, excludeMap)
	if err != nil {
		return GenerateResult{Success: false, ChannelID: normChannel, Error: err.Error()}
	}
	messages := msgsByCh[normChannel]
	if len(messages) == 0 {
		return GenerateResult{
			Success:   false,
			ChannelID: normChannel,
			Error:     fmt.Sprintf("频道 %s 没有新消息需要总结", normChannel),
		}
	}

	channelName := titlesByCh[normChannel]
	if channelName == "" {
		if botCli := s.resolveBotClient(); botCli != nil {
			if info, rerr := botCli.ResolveChannel(ctx, normChannel); rerr == nil && info != nil && info.Title != "" {
				channelName = info.Title
			}
		}
	}
	if channelName == "" {
		parts := strings.Split(strings.TrimRight(normChannel, "/"), "/")
		channelName = parts[len(parts)-1]
	}

	prompt := ""
	contextTokens := config.DefaultSummaryContextTokens
	outputTokens := config.DefaultSummaryOutputTokens
	auditDir := s.deps.AuditDir
	sendToSource := false
	frequency := "weekly"
	if s.deps.Config != nil {
		prompt, _ = s.deps.Config.GetPrompt(config.PromptSummary)
		contextTokens = s.deps.Config.SummaryContextTokens()
		outputTokens = s.deps.Config.SummaryOutputTokens()
		if auditDir == "" {
			auditDir = s.deps.Config.SummaryAuditDir()
		}
		sendToSource = s.deps.Config.SendReportToSource()
		sched := s.deps.Config.ChannelSchedule(normChannel)
		if sched.Frequency != "" {
			frequency = sched.Frequency
		}
	}

	completer, modelName := s.resolveCompleter()
	summaryBody, err := SummarizeComplete(ctx, messages, prompt, completer, PipelineOptions{
		AuditDir:      auditDir,
		ContextTokens: contextTokens,
		OutputTokens:  outputTokens,
		Model:         modelName,
		Logger:        s.deps.Logger,
	})
	if err != nil {
		return GenerateResult{Success: false, ChannelID: normChannel, ChannelName: channelName, Error: err.Error()}
	}

	endDate := time.Now().UTC()
	startDate := lastSummaryTime
	if startDate.IsZero() {
		startDate = endDate.Add(-7 * 24 * time.Hour)
	}
	startStr := fmt.Sprintf("%d.%d", int(startDate.Month()), startDate.Day())
	endStr := fmt.Sprintf("%d.%d", int(endDate.Month()), endDate.Day())

	var reportTitle string
	if frequency == "daily" {
		reportTitle = i18n.T("summary.daily_title", map[string]string{
			"channel": channelName,
			"date":    endStr,
		})
	} else {
		reportTitle = i18n.T("summary.weekly_title", map[string]string{
			"channel":    channelName,
			"start_date": startStr,
			"end_date":   endStr,
		})
	}
	reportText := fmt.Sprintf("**%s**\n\n%s", reportTitle, summaryBody)

	targetSource := ""
	if sendToSource {
		targetSource = normChannel
	}
	sentIDs := s.SendReport(ctx, reportText, targetSource, skipAdmins)

	// 单次精准落库（修复 Python 版 send_report 与调用方重复插入两条 summaries 的历史缺陷）。
	var savedSummaryID int64
	if s.deps.DB != nil {
		extStart, extEnd := ExtractDateRangeFromSummary(reportText, endDate)
		if extStart == nil {
			st := startDate
			extStart = &st
		}
		if extEnd == nil {
			et := endDate
			extEnd = &et
		}
		msgIDs64 := make([]int64, len(sentIDs.SummaryMessageIDs))
		for i, id := range sentIDs.SummaryMessageIDs {
			msgIDs64[i] = int64(id)
		}
		var pollPtr, btnPtr *int64
		if sentIDs.PollMessageID > 0 {
			pid := int64(sentIDs.PollMessageID)
			pollPtr = &pid
		}
		if sentIDs.ButtonMessageID > 0 {
			bid := int64(sentIDs.ButtonMessageID)
			btnPtr = &bid
		}
		sid, dbErr := s.deps.DB.SaveSummary(ctx, db.SummaryInsert{
			ChannelID:         normChannel,
			ChannelName:       channelName,
			SummaryText:       reportText,
			MessageCount:      len(messages),
			StartTime:         extStart,
			EndTime:           extEnd,
			SummaryMessageIDs: msgIDs64,
			PollMessageID:     pollPtr,
			ButtonMessageID:   btnPtr,
			AIModel:           modelName,
			SummaryType:       summaryType,
		})
		if dbErr != nil {
			s.deps.Logger.Error("保存总结到 MySQL 失败", "channel", normChannel, "error", dbErr)
		} else {
			savedSummaryID = sid
			if s.deps.VectorStore != nil && sid > 0 {
				if verr := s.deps.VectorStore.AddSummary(
					ctx, sid, normChannel, channelName, reportText,
					*extStart, *extEnd, endDate, len(messages),
				); verr != nil {
					s.deps.Logger.Warn("保存总结向量失败（MySQL 记录已保留）", "summary_id", sid, "error", verr)
				}
			}
		}
	}

	if s.deps.PushHandler != nil {
		s.deps.PushHandler.NotifySummarySubscribers(ctx, normChannel, channelName, reportText)
	}

	var pollSlice, btnSlice []int
	if sentIDs.PollMessageID > 0 {
		pollSlice = []int{sentIDs.PollMessageID}
	}
	if sentIDs.ButtonMessageID > 0 {
		btnSlice = []int{sentIDs.ButtonMessageID}
	}
	if terr := s.deps.TimeManager.SaveChannel(normChannel, ChannelSummaryState{
		Time:              endDate,
		SummaryMessageIDs: sentIDs.SummaryMessageIDs,
		PollMessageIDs:    pollSlice,
		ButtonMessageIDs:  btnSlice,
	}); terr != nil {
		s.deps.Logger.Error("保存上次总结时间失败", "channel", normChannel, "error", terr)
	}

	return GenerateResult{
		Success:      true,
		SummaryID:    savedSummaryID,
		SummaryText:  reportText,
		MessageCount: len(messages),
		ChannelID:    normChannel,
		ChannelName:  channelName,
		MessageIDs:   sentIDs,
	}
}

func (s *Service) enrichIncomingMessage(ctx context.Context, chRef string, info *mtproto.ChannelInfo, msg *mtproto.IncomingMessage) string {
	if s.deps.ImagePipeline == nil || msg.Media == nil || !msg.Media.IsStaticImage() {
		return msg.Text
	}
	chID := chRef
	chTitle := ""
	chUser := ""
	if info != nil {
		if info.ID > 0 {
			chID = strconv.FormatInt(info.ID, 10)
		}
		chTitle = info.Title
		chUser = info.Username
	}
	grouped := ""
	if msg.GroupedID != 0 {
		grouped = strconv.FormatInt(msg.GroupedID, 10)
	}
	return s.deps.ImagePipeline.EnrichMessage(ctx, media.RemoteImageMessage{
		ChannelID:       chID,
		ChannelTitle:    chTitle,
		ChannelUsername: chUser,
		MessageID:       int64(msg.ID),
		GroupedID:       grouped,
		MediaID:         msg.Media.MediaID,
		FileSize:        msg.Media.FileSize,
		Caption:         msg.Text,
		Date:            msg.Date,
		RawMedia:        msg.Media.Raw,
	})
}

func (s *Service) resolveBotClient() mtproto.Messenger {
	if s.deps.BotClient != nil {
		return s.deps.BotClient()
	}
	return nil
}

func (s *Service) resolveHistoryClient() mtproto.Messenger {
	if s.deps.HistoryClient != nil {
		if cli := s.deps.HistoryClient(); cli != nil {
			return cli
		}
	}
	if s.deps.Config != nil && s.deps.Config.UserBotFallbackToBot() {
		return s.resolveBotClient()
	}
	return nil
}

func (s *Service) resolveCompleter() (Completer, string) {
	if s.deps.Completer != nil {
		return s.deps.Completer()
	}
	if s.deps.Config != nil {
		cfg := s.deps.Config.EffectiveAIConfig("summary")
		return llm.NewPurposeClient(s.deps.Config, "summary"), cfg.Model
	}
	return nil, ""
}

func channelSlug(ch string, info *mtproto.ChannelInfo) string {
	if info != nil && info.Username != "" {
		return info.Username
	}
	parts := strings.Split(strings.TrimRight(ch, "/"), "/")
	last := strings.TrimPrefix(parts[len(parts)-1], "@")
	if strings.HasPrefix(last, "-100") {
		return "c/" + strings.TrimPrefix(last, "-100")
	}
	return last
}

func chUsername(info *mtproto.ChannelInfo) string {
	if info == nil {
		return ""
	}
	return info.Username
}
