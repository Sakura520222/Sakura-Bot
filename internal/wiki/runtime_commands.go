// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package wiki

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

var (
	wikiCommand      = regexp.MustCompile(`^/wiki_(status|build|pause)(?:@[a-zA-Z0-9_]+)?(?:\s+(\S+))?\s*$`)
	proposalCallback = regexp.MustCompile(`^wpr:([ar]):([a-f0-9]{32})$`)
)

// HandleCommand consumes the Wiki Telegram commands and replies in the originating chat.
func (r *Runtime) HandleCommand(ctx context.Context, msg *mtproto.IncomingMessage) bool {
	if msg == nil {
		return false
	}
	args := wikiCommand.FindStringSubmatch(msg.Text)
	if args == nil {
		return false
	}
	bot := r.bot()
	if bot == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	reply := func(text string) {
		if _, err := bot.SendMessage(ctx, msg.ChatID, text, mtproto.SendOptions{ParseMode: "plain", ReplyToMsgID: msg.ID}); err != nil {
			r.logger.Info("Wiki 命令回复延后", "command", args[1])
		}
	}
	action, key := args[1], args[2]
	switch {
	case key == "":
		reply("用法：/wiki_status 频道；管理员可用 /wiki_build 频道、/wiki_pause 频道")
	case action != "status" && (r.cfg == nil || !r.cfg.IsAdmin(msg.SenderID)):
		reply("此命令仅限管理员。")
	case !r.Ready():
		reply("Wiki 服务尚未启动")
	case action == "status" && (r.cfg == nil || !r.cfg.IsAdmin(msg.SenderID)):
		channel, err := r.Reader.Channel(ctx, key, false)
		if err != nil {
			reply("Wiki 暂时无法查询，请稍后重试。")
		} else if channel == nil {
			reply("该频道暂无公开 Wiki。")
		} else {
			reply(fmt.Sprintf("Sakura Wiki：%s，已发布 %d 个条目", mapString(channel, "title"), mapInt(channel, "page_count")))
		}
	default:
		reply(r.runAdminCommand(ctx, action, key, strconv.FormatInt(msg.SenderID, 10)))
	}
	return true
}

// HandleWikiCommand runs a Wiki operation selected from the main Bot menu.
func (r *Runtime) HandleWikiCommand(ctx context.Context, bot mtproto.Messenger, msg *mtproto.IncomingMessage, command, args string) error {
	request := *msg
	request.Text = "/" + command + " " + args
	if !r.HandleCommand(ctx, &request) {
		_, err := bot.SendMessage(ctx, msg.ChatID, "请输入一个频道用户名或链接。", mtproto.SendOptions{})
		return err
	}
	return nil
}

func (r *Runtime) runAdminCommand(ctx context.Context, action, key, admin string) string {
	channel, err := r.Store.GetChannel(ctx, key)
	if err != nil {
		r.logger.Warn("Wiki 命令读取频道失败", "command", action, "error", err)
		return "Wiki 暂时无法查询，请稍后重试。"
	}
	if channel == nil {
		return "频道未完成身份核实，请检查 WebUI / UserBot。"
	}
	switch action {
	case "build":
		var job string
		job, err = r.Store.EnqueueJob(ctx, nil, channel.ID, "backfill", admin, "", false)
		if err == nil {
			r.Wake()
			return fmt.Sprintf("Wiki 后台任务已排队：%s。可在 WebUI 查看进度。", job)
		}
	case "pause":
		cfg := channel.Config
		cfg.Paused = true
		err = r.Store.ConfigureChannel(ctx, channel.ID, cfg, admin)
		if err == nil && channel.ActiveJob != "" {
			err = r.Store.ControlJob(ctx, channel.ID, channel.ActiveJob, "pause", admin)
		}
		if err == nil {
			return "Wiki 更新已暂停，可在 WebUI 恢复。"
		}
	default:
		job := channel.ActiveJob
		if job == "" {
			job = "无"
		}
		return fmt.Sprintf("Wiki：启用=%t，公开=%t，覆盖=%s，任务=%s", channel.Enabled, channel.Public, mapString(channel.Coverage, "status"), job)
	}
	r.logger.Warn("Wiki 管理命令失败", "command", action, "channel", channel.ID, "error", err)
	return "Wiki 操作未能完成，请在 WebUI 查看当前状态。"
}

func (r *Runtime) proposalDetailButtons(id string) [][]mtproto.InlineButton {
	if r.cfg != nil {
		origin := strings.TrimRight(r.cfg.WikiLoginOrigin(), "/")
		if parsed, err := url.Parse(origin); err == nil && parsed.Scheme == "https" && parsed.Host != "" {
			return [][]mtproto.InlineButton{{{
				Text: "查看详情",
				URL:  origin + "/wiki/admin?proposal=" + id,
			}}}
		}
	}
	return [][]mtproto.InlineButton{}
}

// NotifyProposal sends the pending proposal to the configured Telegram admins.
func (r *Runtime) NotifyProposal(ctx context.Context, proposalID string) {
	bot := r.bot()
	if bot == nil || r.cfg == nil || !reHex32.MatchString(proposalID) {
		return
	}
	proposals, err := r.Community.ListProposals(ctx, ProposalQueryOptions{ProposalID: proposalID, Review: true})
	if err != nil {
		r.logger.Info("Wiki 投稿通知读取失败", "proposal", proposalID)
		return
	}
	if len(proposals) == 0 {
		return
	}
	buttons := []mtproto.InlineButton{
		{Text: "批准", Data: "wpr:a:" + proposalID},
		{Text: "拒绝", Data: "wpr:r:" + proposalID},
	}
	if detail := r.proposalDetailButtons(proposalID); len(detail) != 0 {
		buttons = append(buttons, detail[0]...)
	}
	text := ProposalSummary(proposals[0])
	for _, admin := range r.cfg.AdminIDs() {
		if admin <= 0 {
			continue
		}
		sendCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		_, err := bot.SendMessage(sendCtx, admin, text, mtproto.SendOptions{
			ParseMode: "plain",
			Buttons:   [][]mtproto.InlineButton{buttons},
			NoWebpage: true,
		})
		cancel()
		if err != nil && ctx.Err() == nil {
			r.logger.Info("Wiki 投稿通知发送失败", "proposal", proposalID, "admin", admin)
		}
	}
}

// HandleCallback consumes and authorizes Telegram proposal review callbacks.
func (r *Runtime) HandleCallback(ctx context.Context, cb mtproto.CallbackEvent) bool {
	parts := proposalCallback.FindStringSubmatch(cb.DataString())
	if parts == nil {
		return false
	}
	bot := r.bot()
	if bot == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	feedback := func(text string, alert bool) {
		if err := bot.AnswerCallback(ctx, cb.QueryID, text, alert); err != nil && cb.ChatID != 0 {
			_, _ = bot.SendMessage(ctx, cb.ChatID, text, mtproto.SendOptions{ParseMode: "plain"})
		}
	}
	if r.cfg == nil || !r.cfg.IsAdmin(cb.SenderID) {
		feedback("仅管理员可审核", true)
		return true
	}
	if !r.Ready() {
		feedback("Wiki 服务尚未启动", true)
		return true
	}
	action := "approved"
	if parts[1] == "r" {
		action = "rejected"
	}
	id := parts[2]
	result, err := r.Community.Decide(ctx, id, action, strconv.FormatInt(cb.SenderID, 10), "", true)
	if err != nil {
		r.logger.Warn("Wiki Telegram 审批失败", "proposal", id, "error", err)
		feedback("审批暂时失败，请重试或在 WebUI 查看状态", true)
		return true
	}
	r.Wake()
	message := "已拒绝投稿"
	if mapString(result, "status") == "approved" {
		message = "已批准，已排入词条处理队列"
	}
	feedback(message, false)
	if cb.ChatID != 0 && cb.MessageID > 0 {
		original, err := bot.GetMessages(ctx, cb.ChatID, []int{cb.MessageID})
		if err == nil && len(original) > 0 && original[0] != nil {
			message = strings.TrimSpace(original[0].Text + "\n\n" + message)
		}
		if err := bot.EditMessage(ctx, cb.ChatID, cb.MessageID, message, mtproto.SendOptions{
			ParseMode: "plain", Buttons: r.proposalDetailButtons(id), NoWebpage: true,
		}); err != nil {
			r.logger.Info("Wiki 投稿通知更新失败", "proposal", id)
		}
	}
	return true
}
