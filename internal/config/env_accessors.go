// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultUserBotSessionPath   = "data/sessions/user_session"
	DefaultSummaryContextTokens = 1000000
	DefaultSummaryOutputTokens  = 64000
	DefaultSchedulerTimezone    = "Asia/Shanghai"
	DefaultWebUIHost            = "0.0.0.0"
	DefaultWebUIPort            = 8080
	DefaultWikiAssistantPort    = 8091
	DefaultVectorDBPath         = "data/vectors"
	DefaultEmbeddingDimension   = 1024
	DefaultMessageSourceDir     = "data/message_sources"
	DefaultSummaryAuditDir      = "data/summary_runs"
)

// ────────────────────────────── Telegram & UserBot ──────────────────────────────

// TelegramAPIID 解析 TELEGRAM_API_ID（必须为正整数，对齐 settings.py:TelegramSettings.validate_api_id）。
func (e *Env) TelegramAPIID() (int, error) {
	if e == nil {
		return 0, fmt.Errorf("TELEGRAM_API_ID 未配置")
	}
	raw := strings.TrimSpace(e.Get(KeyTelegramAPIID))
	if raw == "" {
		return 0, fmt.Errorf("TELEGRAM_API_ID 未配置")
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("TELEGRAM_API_ID 必须为正整数")
	}
	return id, nil
}

// TelegramAPIID 转发 Env.TelegramAPIID。
func (c *Config) TelegramAPIID() (int, error) { return c.Env.TelegramAPIID() }

// TelegramAPIHash 返回 TELEGRAM_API_HASH。
func (e *Env) TelegramAPIHash() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.Get(KeyTelegramAPIHash))
}

// TelegramAPIHash 转发 Env.TelegramAPIHash。
func (c *Config) TelegramAPIHash() string { return c.Env.TelegramAPIHash() }

// TelegramBotToken 返回主 Bot 令牌 TELEGRAM_BOT_TOKEN。
func (e *Env) TelegramBotToken() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.Get(KeyTelegramBotToken))
}

// TelegramBotToken 转发 Env.TelegramBotToken。
func (c *Config) TelegramBotToken() string { return c.Env.TelegramBotToken() }

// UserBotEnabled 返回 USERBOT_ENABLED（默认 false）。
func (e *Env) UserBotEnabled() bool { return e.GetBoolDef(KeyUserBotEnabled, false) }

// UserBotEnabled 转发 Env.UserBotEnabled。
func (c *Config) UserBotEnabled() bool { return c.Env.UserBotEnabled() }

// UserBotPhoneNumber 返回 USERBOT_PHONE_NUMBER。
func (e *Env) UserBotPhoneNumber() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.Get(KeyUserBotPhoneNumber))
}

// UserBotPhoneNumber 转发 Env.UserBotPhoneNumber。
func (c *Config) UserBotPhoneNumber() string { return c.Env.UserBotPhoneNumber() }

// UserBotSessionPath 返回 USERBOT_SESSION_PATH（默认 "data/sessions/user_session"）。
func (e *Env) UserBotSessionPath() string {
	if e != nil {
		if v := strings.TrimSpace(e.Get(KeyUserBotSessionPath)); v != "" {
			return v
		}
	}
	return DefaultUserBotSessionPath
}

// UserBotSessionPath 转发 Env.UserBotSessionPath。
func (c *Config) UserBotSessionPath() string { return c.Env.UserBotSessionPath() }

// UserBotFallbackToBot 返回 USERBOT_FALLBACK_TO_BOT（默认 false）。
func (e *Env) UserBotFallbackToBot() bool { return e.GetBoolDef(KeyUserBotFallbackToBot, false) }

// UserBotFallbackToBot 转发 Env.UserBotFallbackToBot。
func (c *Config) UserBotFallbackToBot() bool { return c.Env.UserBotFallbackToBot() }

// QABotUsername 返回去除前导 @ 的 QA_BOT_USERNAME（对齐 core/config.py:199）。
func (e *Env) QABotUsername() string {
	if e == nil {
		return ""
	}
	return strings.TrimLeft(strings.TrimSpace(e.Get(KeyQABotUsername)), "@")
}

// QABotUsername 转发 Env.QABotUsername。
func (c *Config) QABotUsername() string { return c.Env.QABotUsername() }

// ────────────────────────────── Summary & Scheduler ──────────────────────────────

// SummaryContextTokens 返回 SUMMARY_CONTEXT_TOKENS（默认 1000000，对齐 summary_pipeline.py:66）。
func (e *Env) SummaryContextTokens() int {
	n := int(e.GetIntDef(KeySummaryContextTokens, DefaultSummaryContextTokens))
	if n <= 0 {
		return DefaultSummaryContextTokens
	}
	return n
}

// SummaryContextTokens 转发 Env.SummaryContextTokens。
func (c *Config) SummaryContextTokens() int { return c.Env.SummaryContextTokens() }

// SummaryOutputTokens 返回 SUMMARY_OUTPUT_TOKENS（默认 64000，对齐 summary_pipeline.py:67）。
func (e *Env) SummaryOutputTokens() int {
	n := int(e.GetIntDef(KeySummaryOutputTokens, DefaultSummaryOutputTokens))
	if n <= 0 {
		return DefaultSummaryOutputTokens
	}
	return n
}

// SummaryOutputTokens 转发 Env.SummaryOutputTokens。
func (c *Config) SummaryOutputTokens() int { return c.Env.SummaryOutputTokens() }

// EnablePollEnv 返回环境变量 ENABLE_POLL（默认 true，对齐 settings.py:PollSettings.enable_poll）。
func (e *Env) EnablePollEnv() bool { return e.GetBoolDef(KeyEnablePoll, true) }

// EnablePollEnv 转发 Env.EnablePollEnv。
func (c *Config) EnablePollEnv() bool { return c.Env.EnablePollEnv() }

// PollPublicVotersEnv 返回环境变量 POLL_PUBLIC_VOTERS（默认 true，对齐 settings.py:181）。
func (e *Env) PollPublicVotersEnv() bool { return e.GetBoolDef(KeyPollPublicVoters, true) }

// PollPublicVotersEnv 转发 Env.PollPublicVotersEnv。
func (c *Config) PollPublicVotersEnv() bool { return c.Env.PollPublicVotersEnv() }

// SchedulerTimezone 返回调度器时区（优先 SCHEDULER_TIMEZONE，其次 TZ，回退 "Asia/Shanghai"，
// 对齐 Dockerfile / docker-compose.yml 的默认部署时区）。非法 IANA 时区回退默认值。
func (e *Env) SchedulerTimezone() string {
	if e != nil {
		for _, key := range []string{KeySchedulerTimezone, KeyTZ} {
			if tz := strings.TrimSpace(e.Get(key)); tz != "" {
				if _, err := time.LoadLocation(tz); err == nil {
					return tz
				}
			}
		}
	}
	return DefaultSchedulerTimezone
}

// SchedulerTimezone 转发 Env.SchedulerTimezone。
func (c *Config) SchedulerTimezone() string { return c.Env.SchedulerTimezone() }

// ────────────────────────────── WebUI ──────────────────────────────

// WebUIEnabled 返回 WEBUI_ENABLED（默认 false）。
func (e *Env) WebUIEnabled() bool { return e.GetBoolDef(KeyWebUIEnabled, false) }

// WebUIEnabled 转发 Env.WebUIEnabled。
func (c *Config) WebUIEnabled() bool { return c.Env.WebUIEnabled() }

// WebUIHost 返回 WEBUI_HOST（默认 "0.0.0.0"）。
func (e *Env) WebUIHost() string {
	if e != nil {
		if h := strings.TrimSpace(e.Get(KeyWebUIHost)); h != "" {
			return h
		}
	}
	return DefaultWebUIHost
}

// WebUIHost 转发 Env.WebUIHost。
func (c *Config) WebUIHost() string { return c.Env.WebUIHost() }

// WebUIPort 返回 WEBUI_PORT（默认 8080，合法范围 1..65535）。
func (e *Env) WebUIPort() int {
	p := int(e.GetIntDef(KeyWebUIPort, DefaultWebUIPort))
	if p < 1 || p > 65535 {
		return DefaultWebUIPort
	}
	return p
}

// WebUIPort 转发 Env.WebUIPort。
func (c *Config) WebUIPort() int { return c.Env.WebUIPort() }

// WebUIDevMode 返回 WEBUI_DEV_MODE（默认 false）。
func (e *Env) WebUIDevMode() bool { return e.GetBoolDef(KeyWebUIDevMode, false) }

// WebUIDevMode 转发 Env.WebUIDevMode。
func (c *Config) WebUIDevMode() bool { return c.Env.WebUIDevMode() }

// ────────────────────────────── Wiki Login & Assistant ──────────────────────────────

// WikiLoginEnabled 返回环境变量 WIKI_LOGIN_ENABLED（默认 false）。
func (e *Env) WikiLoginEnabled() bool { return e.GetBoolDef(KeyWikiLoginEnabled, false) }

// WikiLoginEnabled 优先读取 config.json["wiki_login"]["enabled"]，未配置时回退环境变量（对齐 telegram_login.py:69-80）。
func (c *Config) WikiLoginEnabled() bool {
	if m, ok := c.readConfig()["wiki_login"].(map[string]any); ok {
		if raw, exists := m["enabled"]; exists && raw != nil {
			if b, ok := parseLooseBool(raw); ok {
				return b
			}
		}
	}
	return c.Env.WikiLoginEnabled()
}

// WikiLoginOrigin 返回环境变量 WIKI_LOGIN_ORIGIN（默认 ""）。
func (e *Env) WikiLoginOrigin() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.Get(KeyWikiLoginOrigin))
}

// WikiLoginOrigin 优先读取 config.json["wiki_login"]["origin"]，未配置时回退环境变量。
func (c *Config) WikiLoginOrigin() string {
	if m, ok := c.readConfig()["wiki_login"].(map[string]any); ok {
		if s, ok := m["origin"].(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return c.Env.WikiLoginOrigin()
}

// WikiTelegramClientSecret 返回环境变量 WIKI_TELEGRAM_CLIENT_SECRET（默认 ""）。
func (e *Env) WikiTelegramClientSecret() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.Get(KeyWikiTelegramClientSecret))
}

// WikiTelegramClientSecret 优先读取 config.json["wiki_login"]["client_secret"]，未配置时回退环境变量。
func (c *Config) WikiTelegramClientSecret() string {
	if m, ok := c.readConfig()["wiki_login"].(map[string]any); ok {
		if s, ok := m["client_secret"].(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return c.Env.WikiTelegramClientSecret()
}

// WikiAssistantEnabled 返回环境变量 WIKI_ASSISTANT_ENABLED（默认 false）。
func (e *Env) WikiAssistantEnabled() bool { return e.GetBoolDef(KeyWikiAssistantEnabled, false) }

// WikiAssistantEnabled 优先读取 config.json["wiki_login"]["assistant_enabled"]，未配置时回退环境变量。
func (c *Config) WikiAssistantEnabled() bool {
	if m, ok := c.readConfig()["wiki_login"].(map[string]any); ok {
		if raw, exists := m["assistant_enabled"]; exists && raw != nil {
			if b, ok := parseLooseBool(raw); ok {
				return b
			}
		}
	}
	return c.Env.WikiAssistantEnabled()
}

// WikiAssistantCheckinEnabled 返回环境变量 WIKI_ASSISTANT_CHECKIN_ENABLED（默认 true，对齐 settings.py:258）。
func (e *Env) WikiAssistantCheckinEnabled() bool {
	return e.GetBoolDef(KeyWikiAssistantCheckinEnabled, true)
}

// WikiAssistantCheckinEnabled 优先读取 config.json["wiki_login"]["assistant_checkin"]，未配置时回退环境变量。
func (c *Config) WikiAssistantCheckinEnabled() bool {
	if m, ok := c.readConfig()["wiki_login"].(map[string]any); ok {
		if raw, exists := m["assistant_checkin"]; exists && raw != nil {
			if b, ok := parseLooseBool(raw); ok {
				return b
			}
		}
	}
	return c.Env.WikiAssistantCheckinEnabled()
}

// WikiAssistantPort 返回环境变量 WIKI_ASSISTANT_PORT（默认 8091，范围 1..65535）。
func (e *Env) WikiAssistantPort() int {
	p := int(e.GetIntDef(KeyWikiAssistantPort, DefaultWikiAssistantPort))
	if p < 1 || p > 65535 {
		return DefaultWikiAssistantPort
	}
	return p
}

// WikiAssistantPort 转发 Env.WikiAssistantPort。
func (c *Config) WikiAssistantPort() int { return c.Env.WikiAssistantPort() }

// ────────────────────────────── Vector & Media ──────────────────────────────

// VectorDBPath 返回 VECTOR_DB_PATH（默认 "data/vectors"）。
func (e *Env) VectorDBPath() string {
	if e != nil {
		if p := strings.TrimSpace(e.Get(KeyVectorDBPath)); p != "" {
			return p
		}
	}
	return DefaultVectorDBPath
}

// VectorDBPath 转发 Env.VectorDBPath。
func (c *Config) VectorDBPath() string { return c.Env.VectorDBPath() }

// EmbeddingDimension 返回 EMBEDDING_DIMENSION（默认 1024，对齐 vector_store.py:98）。
func (e *Env) EmbeddingDimension() int {
	d := int(e.GetIntDef(KeyEmbeddingDimension, DefaultEmbeddingDimension))
	if d <= 0 {
		return DefaultEmbeddingDimension
	}
	return d
}

// EmbeddingDimension 转发 Env.EmbeddingDimension。
func (c *Config) EmbeddingDimension() int { return c.Env.EmbeddingDimension() }

// MessageSourceDir 返回 MESSAGE_SOURCE_DIR（默认 "data/message_sources"，对齐 source_archive.py:16）。
func (e *Env) MessageSourceDir() string {
	if e != nil {
		if p := strings.TrimSpace(e.Get(KeyMessageSourceDir)); p != "" {
			return p
		}
	}
	return DefaultMessageSourceDir
}

// MessageSourceDir 转发 Env.MessageSourceDir。
func (c *Config) MessageSourceDir() string { return c.Env.MessageSourceDir() }

// SummaryAuditDir 返回 SUMMARY_AUDIT_DIR（默认 "data/summary_runs"，对齐 summary_pipeline.py:50）。
func (e *Env) SummaryAuditDir() string {
	if e != nil {
		if p := strings.TrimSpace(e.Get(KeySummaryAuditDir)); p != "" {
			return p
		}
	}
	return DefaultSummaryAuditDir
}

// SummaryAuditDir 转发 Env.SummaryAuditDir。
func (c *Config) SummaryAuditDir() string { return c.Env.SummaryAuditDir() }
