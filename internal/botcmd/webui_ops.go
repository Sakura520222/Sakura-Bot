// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package botcmd

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
	"github.com/Sakura520222/Sakura-Bot/internal/db"
	"github.com/Sakura520222/Sakura-Bot/internal/logging"
)

// DangerConfirmText 是 WebUI 危险命令二次确认固定口令（对齐 commands.py:DANGER_CONFIRM_TEXT）。
const DangerConfirmText = "CONFIRM"

// CommandParameter 对齐 WebUI 命令中心参数 Schema。
type CommandParameter struct {
	Name        string           `json:"name"`
	Label       string           `json:"label"`
	Type        string           `json:"type"`
	Required    bool             `json:"required"`
	Description string           `json:"description"`
	Placeholder string           `json:"placeholder"`
	Default     any              `json:"default"`
	Options     []map[string]any `json:"options"`
}

// CommandItem 对齐 WebUI 命令中心单条命令项。
type CommandItem struct {
	Command       string             `json:"command"`
	Description   string             `json:"description"`
	OperationID   string             `json:"operation_id"`
	Category      string             `json:"category"`
	Risk          string             `json:"risk"`
	Executable    bool               `json:"executable"`
	CoveredByPage *string            `json:"covered_by_page"`
	Parameters    []CommandParameter `json:"parameters"`
	Aliases       []string           `json:"aliases"`
}

// CommandCategoryDTO 对齐 WebUI 命令中心分类视图。
type CommandCategoryDTO struct {
	Category string        `json:"category"`
	I18nKey  string        `json:"i18n_key"`
	Commands []CommandItem `json:"commands"`
}

// HTTPError 表示命令执行器需要返回特定 HTTP 状态码（如 400 / 503）的显式错误。
type HTTPError struct {
	StatusCode int
	Detail     string
}

func (e *HTTPError) Error() string { return e.Detail }

type commandMetaSpec struct {
	risk          string
	coveredByPage string
	params        []CommandParameter
}

func logLevelSelectOptions() []map[string]any {
	levels := []string{"DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"}
	out := make([]map[string]any, len(levels))
	for i, l := range levels {
		out[i] = map[string]any{"label": l, "value": l}
	}
	return out
}

var webuiCommandMetadata = map[string]commandMetaSpec{
	"summary": {
		risk:          "normal",
		coveredByPage: "/stats",
		params: []CommandParameter{{
			Name: "channel", Label: "频道", Type: "string", Required: true,
			Placeholder: "https://t.me/example 或 @example", Options: []map[string]any{},
		}},
	},
	"showchannels": {risk: "safe", coveredByPage: "/channels"},
	"addchannel": {
		risk:          "normal",
		coveredByPage: "/channels",
		params: []CommandParameter{{
			Name: "channel", Label: "频道", Type: "string", Required: true,
			Placeholder: "https://t.me/example", Options: []map[string]any{},
		}},
	},
	"deletechannel": {
		risk:          "danger",
		coveredByPage: "/channels",
		params: []CommandParameter{{
			Name: "channel", Label: "频道", Type: "string", Required: true,
			Placeholder: "https://t.me/example", Options: []map[string]any{},
		}},
	},
	"setloglevel": {
		risk:          "normal",
		coveredByPage: "/system",
		params: []CommandParameter{{
			Name: "level", Label: "日志级别", Type: "select", Required: true,
			Default: "INFO", Options: logLevelSelectOptions(),
		}},
	},
	"pause":    {risk: "normal", coveredByPage: "/system"},
	"resume":   {risk: "normal", coveredByPage: "/system"},
	"restart":  {risk: "danger", coveredByPage: "/system"},
	"shutdown": {risk: "danger", coveredByPage: "/system"},
	"clearcache": {
		risk:          "danger",
		coveredByPage: "/system",
		params: []CommandParameter{{
			Name: "channel", Label: "频道", Type: "string", Required: false,
			Placeholder: "留空清理全部", Options: []map[string]any{},
		}},
	},
	"db_clear":   {risk: "danger", coveredByPage: "/system"},
	"update":     {risk: "danger", coveredByPage: "/system"},
	"qa_status":  {risk: "safe", coveredByPage: "/system"},
	"qa_start":   {risk: "normal", coveredByPage: "/system"},
	"qa_stop":    {risk: "danger", coveredByPage: "/system"},
	"qa_restart": {risk: "danger", coveredByPage: "/system"},
	"qa_stats":   {risk: "safe", coveredByPage: "/system"},
	"qa_runtime": {
		risk:          "danger",
		coveredByPage: "/system",
		params: []CommandParameter{{
			Name: "runtime", Label: "目标运行时", Type: "string", Required: false,
			Placeholder: "go 或 python，留空仅查看当前运行时", Options: []map[string]any{},
		}},
	},
	"forwarding":         {risk: "safe", coveredByPage: "/forwarding"},
	"forwarding_enable":  {risk: "normal", coveredByPage: "/forwarding"},
	"forwarding_disable": {risk: "danger", coveredByPage: "/forwarding"},
	"forwarding_add_rule": {
		risk:          "normal",
		coveredByPage: "/forwarding",
		params: []CommandParameter{
			{Name: "source_channel", Label: "源频道", Type: "string", Required: true, Placeholder: "https://t.me/source", Options: []map[string]any{}},
			{Name: "target_channel", Label: "目标频道", Type: "string", Required: true, Placeholder: "https://t.me/target", Options: []map[string]any{}},
			{Name: "keywords", Label: "关键词白名单", Type: "tags", Options: []map[string]any{}},
			{Name: "blacklist", Label: "关键词黑名单", Type: "tags", Options: []map[string]any{}},
			{Name: "copy_mode", Label: "复制模式", Type: "boolean", Default: false, Options: []map[string]any{}},
			{Name: "forward_original_only", Label: "只转发原创", Type: "boolean", Default: false, Options: []map[string]any{}},
			{Name: "custom_footer", Label: "自定义底栏", Type: "textarea", Options: []map[string]any{}},
		},
	},
	"forwarding_remove_rule": {
		risk:          "danger",
		coveredByPage: "/forwarding",
		params: []CommandParameter{
			{Name: "source_channel", Label: "源频道", Type: "string", Placeholder: "https://t.me/source", Description: "优先按源频道和目标频道删除，与 Telegram 命令保持一致", Options: []map[string]any{}},
			{Name: "target_channel", Label: "目标频道", Type: "string", Placeholder: "https://t.me/target", Description: "与源频道一起填写时按频道对删除规则", Options: []map[string]any{}},
			{Name: "rule_index", Label: "规则序号", Type: "number", Description: "兼容旧界面：从 1 开始；规则列表会展示序号与频道对应关系", Options: []map[string]any{}},
		},
	},
	"forwarding_stats": {risk: "safe", coveredByPage: "/forwarding"},
	"userbot_status":   {risk: "safe", coveredByPage: "/userbot"},
	"userbot_join": {
		risk:          "normal",
		coveredByPage: "/userbot",
		params: []CommandParameter{{
			Name: "channel", Label: "频道", Type: "string", Required: true,
			Placeholder: "https://t.me/example", Options: []map[string]any{},
		}},
	},
	"userbot_leave": {
		risk:          "danger",
		coveredByPage: "/userbot",
		params: []CommandParameter{{
			Name: "channel", Label: "频道", Type: "string", Required: true,
			Placeholder: "https://t.me/example", Options: []map[string]any{},
		}},
	},
	"userbot_list": {risk: "safe", coveredByPage: "/userbot"},
}

var executableWebUIOps = map[string]bool{
	"summary":                true,
	"showchannels":           true,
	"addchannel":             true,
	"deletechannel":          true,
	"pause":                  true,
	"resume":                 true,
	"restart":                true,
	"shutdown":               true,
	"setloglevel":            true,
	"clearcache":             true,
	"db_clear":               true,
	"update":                 true,
	"qa_status":              true,
	"qa_start":               true,
	"qa_stop":                true,
	"qa_restart":             true,
	"qa_stats":               true,
	"qa_runtime":             true,
	"forwarding":             true,
	"forwarding_enable":      true,
	"forwarding_disable":     true,
	"forwarding_stats":       true,
	"forwarding_add_rule":    true,
	"forwarding_remove_rule": true,
	"userbot_status":         true,
	"userbot_join":           true,
	"userbot_leave":          true,
	"userbot_list":           true,
}

// ListWebUICommands 构建 /api/commands 返回的完整命令中心目录数据（79 条命令，28 条结构化可执行操作）。
func ListWebUICommands() map[string]any {
	categories := make([]CommandCategoryDTO, 0, len(BotCommandGroups))
	total := 0
	executableCount := 0
	dangerCount := 0

	for _, g := range BotCommandGroups {
		items := make([]CommandItem, 0, len(g.Commands))
		for _, c := range g.Commands {
			total++
			meta, hasMeta := webuiCommandMetadata[c.Command]
			risk := "normal"
			var covered *string
			params := []CommandParameter{}
			if hasMeta {
				risk = meta.risk
				if meta.coveredByPage != "" {
					cp := meta.coveredByPage
					covered = &cp
				}
				if len(meta.params) > 0 {
					params = meta.params
				}
			} else if inferred := inferCoveredPage(c.Command); inferred != "" {
				covered = &inferred
			}
			execFlag := executableWebUIOps[c.Command]
			if execFlag {
				executableCount++
			}
			if risk == "danger" {
				dangerCount++
			}
			items = append(items, CommandItem{
				Command:       c.Command,
				Description:   c.Description,
				OperationID:   c.Command,
				Category:      g.Category,
				Risk:          risk,
				Executable:    execFlag,
				CoveredByPage: covered,
				Parameters:    params,
				Aliases:       []string{},
			})
		}
		categories = append(categories, CommandCategoryDTO{
			Category: g.Category,
			I18nKey:  g.I18nKey,
			Commands: items,
		})
	}

	return map[string]any{
		"categories":          categories,
		"total":               total,
		"executable_count":    executableCount,
		"danger_count":        dangerCount,
		"danger_confirm_text": DangerConfirmText,
	}
}

// ExecuteWebUICommand 执行 /api/commands/{operation_id}/execute 的 28 个结构化运维操作并记录审计日志。
func (r *Router) ExecuteWebUICommand(
	ctx context.Context,
	operationID string,
	params map[string]any,
	confirm bool,
	confirmText string,
	actor string,
) (map[string]any, error) {
	if params == nil {
		params = map[string]any{}
	}
	meta, hasMeta := webuiCommandMetadata[operationID]
	risk := "normal"
	if hasMeta && meta.risk != "" {
		risk = meta.risk
	}
	if risk == "danger" && (!confirm || confirmText != DangerConfirmText) {
		return map[string]any{
			"success": false,
			"message": fmt.Sprintf("危险操作需要二次确认，请输入 %s", DangerConfirmText),
			"data": map[string]any{
				"confirm_required": true,
				"confirm_text":     DangerConfirmText,
			},
		}, nil
	}
	if !executableWebUIOps[operationID] {
		return map[string]any{
			"success": false,
			"message": fmt.Sprintf("命令 %s 暂未接入结构化执行", operationID),
			"data":    map[string]any{},
		}, nil
	}

	started := time.Now()
	res, err := r.runWebUIOp(ctx, operationID, params)
	durMs := int(time.Since(started).Milliseconds())
	if durMs < 0 {
		durMs = 0
	}

	target := stringParam(params, "channel")
	if target == "" {
		target = stringParam(params, "source_channel")
	}
	if r.deps.DB != nil {
		ok := err == nil && res != nil && res["success"] == true
		msg := ""
		if err != nil {
			msg = err.Error()
		} else if res != nil {
			msg, _ = res["message"].(string)
		}
		_ = r.deps.DB.RecordAuditLog(ctx, db.AuditEntry{
			Action:        "command." + operationID,
			Actor:         actor,
			Target:        target,
			ParamsSummary: summarizeParamsForAudit(params),
			Success:       ok,
			Message:       msg,
			DurationMs:    durMs,
		})
	}
	return res, err
}

func (r *Router) runWebUIOp(ctx context.Context, op string, params map[string]any) (map[string]any, error) {
	switch op {
	case "summary":
		ch, err := requireStringParam(params, "channel")
		if err != nil {
			return nil, err
		}
		bot := r.resolveBot()
		if bot == nil || !bot.IsConnected() || r.deps.SummaryService == nil {
			return nil, &HTTPError{StatusCode: http.StatusServiceUnavailable, Detail: "Telegram 客户端未连接，无法生成总结"}
		}
		genRes := r.deps.SummaryService.GenerateChannelSummary(ctx, ch, true, "manual")
		if genRes.Success {
			return map[string]any{
				"success": true,
				"message": fmt.Sprintf("总结生成成功（%d 条消息）", genRes.MessageCount),
				"data": map[string]any{
					"channel_name":    genRes.ChannelName,
					"message_count":   genRes.MessageCount,
					"summary_preview": truncateRunesStr(genRes.SummaryText, 200),
				},
			}, nil
		}
		return map[string]any{"success": false, "message": genRes.Error, "data": map[string]any{}}, nil

	case "showchannels":
		chs := r.deps.Config.Channels()
		return map[string]any{
			"success": true,
			"message": fmt.Sprintf("共 %d 个频道", len(chs)),
			"data":    map[string]any{"channels": chs},
		}, nil

	case "addchannel":
		rawCh, err := requireStringParam(params, "channel")
		if err != nil {
			return nil, err
		}
		ch := config.NormalizeChannelID(rawCh)
		exists := false
		if uerr := r.deps.Config.UpdateConfig(func(doc map[string]any) error {
			cur := extractStringSlice(doc["channels"])
			if containsChannel(cur, ch) {
				exists = true
				return nil
			}
			doc["channels"] = append(cur, ch)
			return nil
		}); uerr != nil {
			return nil, uerr
		}
		if exists {
			return map[string]any{"success": false, "message": "频道已存在: " + ch, "data": map[string]any{}}, nil
		}
		return map[string]any{"success": true, "message": "频道添加成功: " + ch, "data": map[string]any{"channel": ch}}, nil

	case "deletechannel":
		rawCh, err := requireStringParam(params, "channel")
		if err != nil {
			return nil, err
		}
		ch := config.NormalizeChannelID(rawCh)
		removed := false
		if uerr := r.deps.Config.UpdateConfig(func(doc map[string]any) error {
			cur := extractStringSlice(doc["channels"])
			next, ok := removeChannelEquivalent(cur, ch)
			if !ok {
				return nil
			}
			removed = true
			doc["channels"] = next
			for _, key := range []string{"summary_schedules", "channel_poll_settings", "channel_auto_poll_settings"} {
				if sm, ok := doc[key].(map[string]any); ok {
					delete(sm, ch)
				}
			}
			return nil
		}); uerr != nil {
			return nil, uerr
		}
		if !removed {
			return map[string]any{"success": false, "message": "频道不存在: " + ch, "data": map[string]any{}}, nil
		}
		return map[string]any{"success": true, "message": "频道删除成功: " + ch, "data": map[string]any{"channel": ch}}, nil

	case "pause":
		if r.deps.Scheduler != nil && r.deps.Scheduler.IsPaused() {
			return map[string]any{"success": false, "message": "Bot 已处于暂停状态", "data": map[string]any{}}, nil
		}
		if r.deps.Scheduler != nil {
			r.deps.Scheduler.Pause()
		}
		return map[string]any{"success": true, "message": "Bot 已暂停", "data": map[string]any{}}, nil

	case "resume":
		if r.deps.Scheduler != nil && !r.deps.Scheduler.IsPaused() {
			return map[string]any{"success": false, "message": "Bot 已处于运行状态", "data": map[string]any{}}, nil
		}
		if r.deps.Scheduler != nil {
			r.deps.Scheduler.Resume()
		}
		return map[string]any{"success": true, "message": "Bot 已恢复", "data": map[string]any{}}, nil

	case "restart":
		if r.deps.TriggerRestart != nil {
			r.deps.TriggerRestart("webui_command_restart")
		}
		return map[string]any{"success": true, "message": "正在重启 Bot...", "data": map[string]any{}}, nil

	case "shutdown":
		if r.deps.TriggerShutdown != nil {
			r.deps.TriggerShutdown()
		}
		return map[string]any{"success": true, "message": "正在关闭 Bot...", "data": map[string]any{}}, nil

	case "setloglevel":
		rawLvl, err := requireStringParam(params, "level")
		if err != nil {
			return nil, err
		}
		lvl := strings.ToUpper(strings.TrimSpace(rawLvl))
		if !ensureLogLevelValid(lvl) {
			return nil, &HTTPError{StatusCode: http.StatusBadRequest, Detail: "无效的日志级别: " + lvl}
		}
		_ = logging.SetDefaultLevel(lvl)
		if uerr := r.deps.Config.UpdateConfig(func(doc map[string]any) error {
			doc["log_level"] = lvl
			return nil
		}); uerr != nil {
			return nil, uerr
		}
		return map[string]any{"success": true, "message": "日志级别已修改为: " + lvl, "data": map[string]any{"level": lvl}}, nil

	case "clearcache":
		ch := stringParam(params, "channel")
		cleared := 0
		if bot := r.resolveBot(); bot != nil {
			cleared = bot.ClearLinkedChatCache(ch)
		}
		msg := fmt.Sprintf("已清理全部讨论组缓存: %d 条", cleared)
		if ch != "" {
			if cleared > 0 {
				msg = "已清理频道缓存: " + ch
			} else {
				msg = "频道缓存不存在: " + ch
			}
		}
		return map[string]any{"success": true, "message": msg, "data": map[string]any{"cleared": cleared}}, nil

	case "db_clear":
		if r.deps.DB == nil {
			return nil, &HTTPError{StatusCode: http.StatusServiceUnavailable, Detail: "当前数据库管理器不支持清空操作"}
		}
		results, totalDeleted, _ := r.deps.DB.ClearAllData(ctx)
		failed := []string{}
		for tbl, cnt := range results {
			if cnt < 0 {
				failed = append(failed, tbl)
			}
		}
		return map[string]any{
			"success": len(failed) == 0,
			"message": fmt.Sprintf("数据库清空完成，总删除 %d 行", totalDeleted),
			"data": map[string]any{
				"results":       results,
				"total_deleted": totalDeleted,
				"failed_tables": failed,
			},
		}, nil

	case "update":
		var result applicationUpdate
		r.updateApplication(ctx, false, "webui_command_update", 0, func(prepared applicationUpdate) {
			result = prepared
		})
		data := map[string]any{}
		if result.git != "" {
			data["git"] = result.git
		}
		return map[string]any{
			"success": result.success,
			"message": result.message,
			"data":    data,
		}, nil

	case "qa_status", "qa_stats":
		if r.deps.QAManager == nil {
			return map[string]any{"success": true, "message": "QA Bot 状态已获取", "data": map[string]any{"running": false, "status": map[string]any{}}}, nil
		}
		st := r.deps.QAManager.Status()
		return map[string]any{
			"success": true,
			"message": "QA Bot 状态已获取",
			"data":    map[string]any{"running": st.Running, "status": st},
		}, nil

	case "qa_start":
		if r.deps.QAManager == nil {
			return map[string]any{"success": false, "message": "QA 管理器未初始化", "data": map[string]any{}}, nil
		}
		res := r.deps.QAManager.Start()
		return map[string]any{"success": res.Success, "message": res.Message, "data": map[string]any{"pid": res.PID}}, nil

	case "qa_stop":
		if r.deps.QAManager == nil {
			return map[string]any{"success": false, "message": "QA 管理器未初始化", "data": map[string]any{}}, nil
		}
		res := r.deps.QAManager.Stop()
		return map[string]any{"success": res.Success, "message": res.Message, "data": map[string]any{}}, nil

	case "qa_restart":
		if r.deps.QAManager == nil {
			return map[string]any{"success": false, "message": "QA 管理器未初始化", "data": map[string]any{}}, nil
		}
		res := r.deps.QAManager.Restart()
		return map[string]any{"success": res.Success, "message": res.Message, "data": map[string]any{"pid": res.PID}}, nil

	case "qa_runtime":
		if r.deps.QAManager == nil {
			return map[string]any{"success": false, "message": "QA 管理器未初始化", "data": map[string]any{}}, nil
		}
		target := stringParam(params, "runtime")
		if target == "" {
			st := r.deps.QAManager.Status()
			return map[string]any{
				"success": true,
				"message": fmt.Sprintf("当前运行时: %s（实际运行: %s）", st.ConfiguredRuntime, st.Runtime),
				"data":    map[string]any{"runtime": st.ConfiguredRuntime, "actual": st.Runtime},
			}, nil
		}
		res := r.deps.QAManager.SetRuntime(target)
		return map[string]any{
			"success": res["success"] == true,
			"message": fmt.Sprintf("%v", res["message"]),
			"data":    map[string]any{"runtime": res["runtime"], "restart": res["restart"]},
		}, nil

	case "forwarding", "forwarding_stats":
		fwd := r.deps.Config.ForwardingConfig()
		state := "已禁用"
		if fwd.Enabled {
			state = "已启用"
		}
		return map[string]any{
			"success": true,
			"message": fmt.Sprintf("转发功能%s，共 %d 条规则", state, len(fwd.Rules)),
			"data": map[string]any{
				"enabled":    fwd.Enabled,
				"rules":      fwd.Rules,
				"rule_count": len(fwd.Rules),
			},
		}, nil

	case "forwarding_enable", "forwarding_disable":
		en := op == "forwarding_enable"
		if uerr := r.deps.Config.UpdateConfig(func(doc map[string]any) error {
			fm := ensureForwardingMap(doc)
			fm["enabled"] = en
			return nil
		}); uerr != nil {
			return nil, uerr
		}
		stText := "禁用"
		if en {
			stText = "启用"
		}
		return map[string]any{
			"success": true,
			"message": "转发功能已" + stText,
			"data":    map[string]any{"enabled": en},
		}, nil

	case "forwarding_add_rule":
		rawSrc, err := requireStringParam(params, "source_channel")
		if err != nil {
			return nil, err
		}
		rawDst, err := requireStringParam(params, "target_channel")
		if err != nil {
			return nil, err
		}
		src := config.NormalizeChannelID(rawSrc)
		dst := config.NormalizeChannelID(rawDst)
		rule := map[string]any{
			"source_channel":        src,
			"target_channel":        dst,
			"keywords":              extractStringSlice(params["keywords"]),
			"blacklist":             extractStringSlice(params["blacklist"]),
			"patterns":              extractStringSlice(params["patterns"]),
			"blacklist_patterns":    extractStringSlice(params["blacklist_patterns"]),
			"copy_mode":             params["copy_mode"] == true,
			"forward_original_only": params["forward_original_only"] == true,
		}
		if cf := stringParam(params, "custom_footer"); cf != "" {
			rule["custom_footer"] = cf
		}
		dup := false
		if uerr := r.deps.Config.UpdateConfig(func(doc map[string]any) error {
			fm := ensureForwardingMap(doc)
			rules, _ := fm["rules"].([]any)
			for _, item := range rules {
				if rm, ok := item.(map[string]any); ok && rm["source_channel"] == src && rm["target_channel"] == dst {
					dup = true
					return nil
				}
			}
			fm["rules"] = append(rules, rule)
			return nil
		}); uerr != nil {
			return nil, uerr
		}
		if dup {
			return map[string]any{"success": false, "message": "该转发规则已存在", "data": map[string]any{}}, nil
		}
		return map[string]any{
			"success": true,
			"message": fmt.Sprintf("转发规则已添加: %s -> %s", src, dst),
			"data":    map[string]any{"rule": rule},
		}, nil

	case "forwarding_remove_rule":
		src := stringParam(params, "source_channel")
		dst := stringParam(params, "target_channel")
		if src != "" && dst != "" {
			normSrc := config.NormalizeChannelID(src)
			normDst := config.NormalizeChannelID(dst)
			var removed any
			removedIdx := -1
			if uerr := r.deps.Config.UpdateConfig(func(doc map[string]any) error {
				fm := ensureForwardingMap(doc)
				rules, _ := fm["rules"].([]any)
				for i, item := range rules {
					rm, _ := item.(map[string]any)
					if rm != nil && rm["source_channel"] == normSrc && rm["target_channel"] == normDst {
						removed = rm
						removedIdx = i + 1
						fm["rules"] = append(rules[:i], rules[i+1:]...)
						return nil
					}
				}
				return nil
			}); uerr != nil {
				return nil, uerr
			}
			if removed == nil {
				return map[string]any{
					"success": false,
					"message": fmt.Sprintf("转发规则不存在: %s -> %s", normSrc, normDst),
					"data":    map[string]any{},
				}, nil
			}
			return map[string]any{
				"success": true,
				"message": fmt.Sprintf("转发规则已删除: %s -> %s", normSrc, normDst),
				"data":    map[string]any{"removed": removed, "rule_index": removedIdx},
			}, nil
		}
		if src != "" || dst != "" {
			return nil, &HTTPError{StatusCode: http.StatusBadRequest, Detail: "按频道删除规则时需同时提供源频道和目标频道"}
		}
		rawIdx, exists := params["rule_index"]
		if !exists || rawIdx == nil || fmt.Sprintf("%v", rawIdx) == "" {
			return nil, &HTTPError{StatusCode: http.StatusBadRequest, Detail: "缺少必填参数: rule_index"}
		}
		idx, ierr := strconv.Atoi(strings.TrimSpace(fmt.Sprintf("%v", rawIdx)))
		if ierr != nil {
			return nil, &HTTPError{StatusCode: http.StatusBadRequest, Detail: "规则序号必须是整数"}
		}
		var removed any
		invalidIdx := false
		if uerr := r.deps.Config.UpdateConfig(func(doc map[string]any) error {
			fm := ensureForwardingMap(doc)
			rules, _ := fm["rules"].([]any)
			if idx < 1 || idx > len(rules) {
				invalidIdx = true
				return nil
			}
			removed = rules[idx-1]
			fm["rules"] = append(rules[:idx-1], rules[idx:]...)
			return nil
		}); uerr != nil {
			return nil, uerr
		}
		if invalidIdx {
			return map[string]any{"success": false, "message": fmt.Sprintf("无效的规则序号: %d", idx), "data": map[string]any{}}, nil
		}
		return map[string]any{"success": true, "message": "转发规则已删除", "data": map[string]any{"removed": removed}}, nil

	case "userbot_status":
		enabled := r.deps.Config != nil && r.deps.Config.UserBotEnabled()
		connected := false
		if ub := r.resolveUserBot(); ub != nil {
			connected = ub.Status().Connected
		} else if bot := r.resolveBot(); bot != nil {
			connected = bot.IsConnected()
		}
		return map[string]any{
			"success": true,
			"message": "UserBot 状态已获取",
			"data":    map[string]any{"enabled": enabled, "connected": connected},
		}, nil

	case "userbot_join":
		rawCh, err := requireStringParam(params, "channel")
		if err != nil {
			return nil, err
		}
		ub := r.resolveUserBot()
		if ub == nil {
			return map[string]any{"success": false, "message": "UserBot 客户端未初始化", "data": map[string]any{}}, nil
		}
		res := ub.JoinChannel(ctx, config.NormalizeChannelID(rawCh))
		return map[string]any{"success": res.Success, "message": res.Message, "data": res}, nil

	case "userbot_leave":
		rawCh, err := requireStringParam(params, "channel")
		if err != nil {
			return nil, err
		}
		ub := r.resolveUserBot()
		if ub == nil {
			return map[string]any{"success": false, "message": "UserBot 客户端未初始化", "data": map[string]any{}}, nil
		}
		res := ub.LeaveChannel(ctx, config.NormalizeChannelID(rawCh))
		return map[string]any{"success": res.Success, "message": res.Message, "data": res}, nil

	case "userbot_list":
		ub := r.resolveUserBot()
		if ub == nil {
			return map[string]any{"success": false, "message": "UserBot 客户端未初始化", "data": map[string]any{}}, nil
		}
		list, err := ub.ListJoinedChannels(ctx)
		if err != nil {
			return map[string]any{"success": false, "message": "列出频道失败: " + err.Error(), "data": map[string]any{}}, nil
		}
		return map[string]any{
			"success": true,
			"message": fmt.Sprintf("共 %d 个频道", len(list)),
			"data":    map[string]any{"channels": list, "count": len(list)},
		}, nil

	default:
		return map[string]any{"success": false, "message": fmt.Sprintf("命令 %s 暂未接入结构化执行", op), "data": map[string]any{}}, nil
	}
}

func inferCoveredPage(cmd string) string {
	switch {
	case strings.Contains(cmd, "prompt") || strings.Contains(cmd, "aicfg"):
		return "/ai-config"
	case strings.Contains(cmd, "schedule") || cmd == "clearsummarytime" || cmd == "setsendtosource":
		return "/schedules"
	case strings.Contains(cmd, "poll") || strings.Contains(cmd, "commentwelcome") || strings.Contains(cmd, "autopoll"):
		return "/interaction"
	case cmd == "history" || cmd == "export" || cmd == "stats":
		return "/stats"
	default:
		return ""
	}
}

func stringParam(params map[string]any, key string) string {
	v, ok := params[key]
	if !ok || v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

func requireStringParam(params map[string]any, key string) (string, error) {
	s := stringParam(params, key)
	if s == "" {
		return "", &HTTPError{StatusCode: http.StatusBadRequest, Detail: "缺少必填参数: " + key}
	}
	return s, nil
}

func summarizeParamsForAudit(params map[string]any) string {
	safe := make(map[string]any, len(params))
	for k, v := range params {
		lk := strings.ToLower(k)
		if lk == "api_key" || lk == "secret_key" || lk == "token" || lk == "secret" || lk == "password" ||
			strings.HasSuffix(lk, "_token") || strings.HasSuffix(lk, "_secret") || strings.HasSuffix(lk, "_password") {
			safe[k] = "***"
			continue
		}
		if s, ok := v.(string); ok && len(s) > 120 {
			safe[k] = s[:120] + "..."
		} else {
			safe[k] = v
		}
	}
	return fmt.Sprintf("%v", safe)
}
