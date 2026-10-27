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
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// defaultQAPersona 与 core/config.py:168-184 的 DEFAULT_QA_PERSONA 逐字一致
// （含换行；结尾无换行符）。改动前必须同步 Python 侧。
const defaultQAPersona = `你是一个友好、专业的通用智能助手。

【角色特点】
- [Helpful] 乐于助人，可以回答各类问题：日常聊天、知识问答、编程、写作、分析等
- [Concise] 回答清晰有条理，直击要点
- [Trustworthy] 基于事实回答，不确定时如实说明

【你的能力】
- 你接入了一个 Telegram 频道管理系统的数据（频道历史总结、消息记录、统计数据等）。
  当用户的问题涉及这些频道内容时，你可以按需调用提供的检索工具查询后再回答。
- 普通聊天、通用知识、编程与创作类问题无需检索，直接回答即可。
- 是否需要检索由你根据问题性质自主判断，不要把每条消息都当成频道查询。

【回答风格】
- 使用清晰的结构和要点，语言自然、专业但不失亲和力
- 引用频道检索结果时保留相关帖子链接作为证据来源
- 检索不到相关信息时如实说明，不要编造`

// DefaultQAPersona 暴露内置默认人格（与 Python DEFAULT_QA_PERSONA 一致）。
func DefaultQAPersona() string {
	return defaultQAPersona
}

// personaPath 返回 data/qa_persona.txt 的实际路径（对齐 config.py:48）。
func (c *Config) personaPath() string {
	return filepath.Join(c.dataDir, "qa_persona.txt")
}

// Persona 返回问答 Bot 人格描述，四级优先级逐分支对齐
// core/config.py:1740-1794 的 get_qa_bot_persona：
//
//  1. config.json 的 qa_bot_persona；
//  2. env QA_BOT_PERSONA——若“像路径”（.txt 结尾或含路径分隔符）则读文件，
//     失败告警并把 env 原文当人格；
//  3. data/qa_persona.txt（不存在则写入默认人格——保留 Python 的自动建文件
//     副作用，规格 R16）；
//  4. 内置 DEFAULT_QA_PERSONA。
//
// 文件读取结果按 Python .strip() 去除首尾空白。
func (c *Config) Persona() string {
	// 1. config.json
	if p := stringField(c.readConfig(), "qa_bot_persona"); p != "" {
		slog.Debug("使用配置文件中的问答Bot人格")
		return p
	}

	// 2. 环境变量
	if p, ok := c.Env.Lookup(KeyQABotPersona); ok && p != "" {
		if strings.HasSuffix(p, ".txt") || strings.ContainsAny(p, `\/`) {
			content, err := os.ReadFile(p)
			if err == nil {
				slog.Debug("从环境变量指定的文件加载问答Bot人格", "path", p)
				return strings.TrimSpace(string(content))
			}
			slog.Warn("从环境变量指定的文件加载人格失败，使用环境变量值作为人格文本",
				"path", p, "error", err)
			return p
		}
		slog.Debug("使用环境变量中的问答Bot人格")
		return p
	}

	// 3. 默认文件；不存在 → 创建默认文件（副作用必须保留）
	path := c.personaPath()
	content, err := os.ReadFile(path)
	if err == nil {
		slog.Debug("从默认文件加载问答Bot人格", "path", path)
		return strings.TrimSpace(string(content))
	}
	if os.IsNotExist(err) {
		slog.Info("人格文件不存在，创建默认文件", "path", path)
		if writeErr := os.WriteFile(path, []byte(defaultQAPersona), 0o644); writeErr != nil {
			slog.Warn("创建默认人格文件失败", "error", writeErr)
		} else {
			slog.Info("已创建默认人格文件", "path", path)
		}
		return defaultQAPersona
	}
	slog.Error("读取人格文件失败，使用默认人格", "error", err)
	return defaultQAPersona
}
