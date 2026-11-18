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
	"strings"
	"sync"
)

// FSMMode 表示管理员交互式设置状态类型。
type FSMMode string

const (
	FSMNone       FSMMode = ""
	FSMPrompt     FSMMode = "prompt"
	FSMPollPrompt FSMMode = "poll_prompt"
	FSMAIConfig   FSMMode = "ai_config"
)

type aiConfigWizard struct {
	step    int // 0=api_key, 1=base_url, 2=model
	values  map[string]string
	skipped map[string]bool
}

// UserStateStore 线程安全维护主 Bot 管理员对话式配置状态机（对齐 core/infrastructure/utils/states.py）。
type UserStateStore struct {
	mu          sync.Mutex
	promptUsers map[int64]bool
	pollUsers   map[int64]bool
	aiWizards   map[int64]*aiConfigWizard
}

// NewUserStateStore 创建用户状态机存储。
func NewUserStateStore() *UserStateStore {
	return &UserStateStore{
		promptUsers: make(map[int64]bool),
		pollUsers:   make(map[int64]bool),
		aiWizards:   make(map[int64]*aiConfigWizard),
	}
}

// StartPrompt 进入 /setprompt 等待输入状态（互斥清除其他状态）。
func (s *UserStateStore) StartPrompt(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pollUsers, userID)
	delete(s.aiWizards, userID)
	s.promptUsers[userID] = true
}

// StartPollPrompt 进入 /setpollprompt 等待输入状态（互斥清除其他状态）。
func (s *UserStateStore) StartPollPrompt(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.promptUsers, userID)
	delete(s.aiWizards, userID)
	s.pollUsers[userID] = true
}

// StartAIConfig 进入 /setaicfg 三步向导（api_key -> base_url -> model）。
func (s *UserStateStore) StartAIConfig(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.promptUsers, userID)
	delete(s.pollUsers, userID)
	s.aiWizards[userID] = &aiConfigWizard{
		step:    0,
		values:  make(map[string]string),
		skipped: make(map[string]bool),
	}
}

// Mode 返回指定用户当前所处的状态机模式。
func (s *UserStateStore) Mode(userID int64) FSMMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.promptUsers[userID]:
		return FSMPrompt
	case s.pollUsers[userID]:
		return FSMPollPrompt
	case s.aiWizards[userID] != nil:
		return FSMAIConfig
	default:
		return FSMNone
	}
}

// Cancel 清除用户所有挂起状态。
func (s *UserStateStore) Cancel(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.promptUsers, userID)
	delete(s.pollUsers, userID)
	delete(s.aiWizards, userID)
}

// ConsumePrompt 在非斜杠输入到达时消费 /setprompt 状态。
func (s *UserStateStore) ConsumePrompt(userID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.promptUsers[userID] {
		return false
	}
	delete(s.promptUsers, userID)
	return true
}

// ConsumePollPrompt 在非斜杠输入到达时消费 /setpollprompt 状态。
func (s *UserStateStore) ConsumePollPrompt(userID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pollUsers[userID] {
		return false
	}
	delete(s.pollUsers, userID)
	return true
}

// AIConfigStepResult 描述 /setaicfg 单步推进结果。
type AIConfigStepResult struct {
	Handled      bool
	Cancelled    bool
	OtherCommand bool
	Done         bool
	NextStep     string // "base_url" | "model"
	Updates      map[string]any
}

var aiWizardKeys = []string{"api_key", "base_url", "model"}

// StepAIConfig 推进 /setaicfg 向导（支持 /skip 跳过当前字段并保留并发 WebUI 修改，/cancel 终止向导）。
func (s *UserStateStore) StepAIConfig(userID int64, rawText string) AIConfigStepResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	wiz := s.aiWizards[userID]
	if wiz == nil {
		return AIConfigStepResult{}
	}
	trimmed := strings.TrimSpace(rawText)
	lower := strings.ToLower(trimmed)
	if lower == "/cancel" || strings.HasPrefix(lower, "/cancel@") {
		delete(s.aiWizards, userID)
		return AIConfigStepResult{Handled: true, Cancelled: true}
	}
	isSkip := lower == "/skip" || strings.HasPrefix(lower, "/skip@")
	if !isSkip && strings.HasPrefix(trimmed, "/") {
		return AIConfigStepResult{Handled: true, OtherCommand: true}
	}

	key := aiWizardKeys[wiz.step]
	if isSkip {
		wiz.skipped[key] = true
	} else {
		wiz.values[key] = trimmed
	}

	wiz.step++
	if wiz.step < len(aiWizardKeys) {
		return AIConfigStepResult{
			Handled:  true,
			NextStep: aiWizardKeys[wiz.step],
		}
	}

	updates := make(map[string]any)
	for _, k := range aiWizardKeys {
		if !wiz.skipped[k] {
			updates[k] = wiz.values[k]
		}
	}
	delete(s.aiWizards, userID)
	return AIConfigStepResult{
		Handled: true,
		Done:    true,
		Updates: updates,
	}
}
