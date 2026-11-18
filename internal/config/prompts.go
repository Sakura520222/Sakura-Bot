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
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PromptType 标识受管提示词类型（"summary" | "poll" | "qa"）。
type PromptType string

const (
	PromptSummary PromptType = "summary"
	PromptPoll    PromptType = "poll"
	PromptQA      PromptType = "qa"
)

// ErrInvalidPromptType 表示未知的提示词类型。
var ErrInvalidPromptType = errors.New("invalid prompt type")

// defaultSummaryPrompt 对齐 core/config.py:DEFAULT_PROMPT 与 core/infrastructure/utils/constants.py:78。
const defaultSummaryPrompt = "请总结以下 Telegram 消息，提取核心要点并列出重要消息的链接：\n\n"

// defaultPollPrompt 对齐 core/config.py:DEFAULT_POLL_PROMPT 与 core/infrastructure/utils/constants.py:80-97。
const defaultPollPrompt = `根据以下内容生成一个有趣的单选投票。
1. **趣味性**：题目和选项要幽默、有梗，具有互动性，避免平铺直叙。
2. **双语要求**：整体内容中文在上，英文在下。在 JSON 字段内部，中文与英文之间使用 " / " 分隔。
3. **输出格式**：仅输出标准的 JSON 格式，严禁包含任何前言、解释或 Markdown 代码块标识符。
4. **JSON 结构**：
{{
  "question": "中文题目 / English Question",
  "options": [
    "中文选项1 / English Option 1",
    "中文选项2 / English Option 2",
    "中文选项3 / English Option 3",
    "中文选项4 / English Option 4"
  ]
}}

# Input Content
{summary_text}
`

// DefaultSummaryPrompt 返回内置默认总结提示词。
func DefaultSummaryPrompt() string { return defaultSummaryPrompt }

// DefaultPollPrompt 返回内置默认投票生成提示词。
func DefaultPollPrompt() string { return defaultPollPrompt }

// DefaultPrompt 返回指定类型的内置默认提示词。
func DefaultPrompt(kind PromptType) (string, error) {
	switch normalizePromptType(kind) {
	case PromptSummary:
		return defaultSummaryPrompt, nil
	case PromptPoll:
		return defaultPollPrompt, nil
	case PromptQA:
		return DefaultQAPersona(), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidPromptType, kind)
	}
}

func normalizePromptType(kind PromptType) PromptType {
	if kind == "qa_persona" {
		return PromptQA
	}
	return kind
}

// PromptFilePath 返回指定类型提示词文件在当前 Config 数据目录下的路径。
func (c *Config) PromptFilePath(kind PromptType) (string, error) {
	dir := c.DataDirectory()
	switch normalizePromptType(kind) {
	case PromptSummary:
		return filepath.Join(dir, "prompt.txt"), nil
	case PromptPoll:
		return filepath.Join(dir, "poll_prompt.txt"), nil
	case PromptQA:
		return filepath.Join(dir, "qa_persona.txt"), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidPromptType, kind)
	}
}

// GetPrompt 读取指定类型的提示词内容（文件不存在时自动写入默认提示词并返回默认值，
// 对齐 prompt_manager.py、poll_prompt_manager.py 与 persona.go）。
func (c *Config) GetPrompt(kind PromptType) (string, error) {
	canonicalKind := normalizePromptType(kind)
	def, err := DefaultPrompt(canonicalKind)
	if err != nil {
		return "", err
	}
	path, err := c.PromptFilePath(canonicalKind)
	if err != nil {
		return "", err
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			_ = os.MkdirAll(filepath.Dir(path), 0o755)
			_ = atomicWriteFile(path, []byte(def), 0o644)
			return def, nil
		}
		return def, nil
	}

	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return def, nil
	}
	if canonicalKind == PromptQA {
		trimmed = migratePersona(trimmed)
	}
	if trimmed == strings.TrimSpace(def) {
		return def, nil
	}
	return trimmed, nil
}

// SavePrompt 原子写入指定类型的提示词内容并发布 PromptChangedEvent。
func (c *Config) SavePrompt(kind PromptType, content string) error {
	canonicalKind := normalizePromptType(kind)
	path, err := c.PromptFilePath(canonicalKind)
	if err != nil {
		return err
	}
	if err := atomicWriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	effective, _ := c.GetPrompt(canonicalKind)
	c.setLastPrompt(canonicalKind, effective)
	c.EventBus().PublishPrompt(context.Background(), PromptChangedEvent{
		PromptType: canonicalKind,
		FilePath:   path,
		Content:    effective,
		Timestamp:  time.Now(),
	})
	return nil
}

func (c *Config) setLastPrompt(kind PromptType, content string) {
	c.promptMu.Lock()
	defer c.promptMu.Unlock()
	if c.lastPrompts == nil {
		c.lastPrompts = make(map[PromptType]string, 3)
	}
	c.lastPrompts[kind] = content
}

func (c *Config) getLastPrompt(kind PromptType) (string, bool) {
	c.promptMu.Lock()
	defer c.promptMu.Unlock()
	if c.lastPrompts == nil {
		return "", false
	}
	v, ok := c.lastPrompts[kind]
	return v, ok
}

func (c *Config) peekPromptIfExists(kind PromptType) (string, bool) {
	canonicalKind := normalizePromptType(kind)
	def, err := DefaultPrompt(canonicalKind)
	if err != nil {
		return "", false
	}
	path, err := c.PromptFilePath(canonicalKind)
	if err != nil {
		return "", false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return def, true
	}
	if canonicalKind == PromptQA {
		trimmed = migratePersona(trimmed)
	}
	if trimmed == strings.TrimSpace(def) {
		return def, true
	}
	return trimmed, true
}

// ResetPrompt 将指定类型的提示词恢复为内置默认值。
func (c *Config) ResetPrompt(kind PromptType) error {
	canonicalKind := normalizePromptType(kind)
	def, err := DefaultPrompt(canonicalKind)
	if err != nil {
		return err
	}
	return c.SavePrompt(canonicalKind, def)
}

// IsDefaultPrompt 判断当前生效提示词是否与内置默认值一致。
func (c *Config) IsDefaultPrompt(kind PromptType) (bool, error) {
	canonicalKind := normalizePromptType(kind)
	def, err := DefaultPrompt(canonicalKind)
	if err != nil {
		return false, err
	}
	cur, err := c.GetPrompt(canonicalKind)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(cur) == strings.TrimSpace(def), nil
}
