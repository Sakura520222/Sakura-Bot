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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
)

// DefaultLastSummaryFile 是默认上次总结时间状态文件路径（对齐 config.LAST_SUMMARY_FILE）。
const DefaultLastSummaryFile = "data/.last_summary_time.json"

// ChannelSummaryState 表示单个频道的上次总结时间与关联消息 ID 集合。
type ChannelSummaryState struct {
	Time              time.Time `json:"time"`
	SummaryMessageIDs []int     `json:"summary_message_ids"`
	PollMessageIDs    []int     `json:"poll_message_ids"`
	ButtonMessageIDs  []int     `json:"button_message_ids"`
}

// AllExcludedIDs 合并该频道上次发出的总结、投票与按钮消息 ID，供历史抓取排除。
func (s ChannelSummaryState) AllExcludedIDs() map[int]bool {
	out := make(map[int]bool, len(s.SummaryMessageIDs)+len(s.PollMessageIDs)+len(s.ButtonMessageIDs))
	for _, id := range s.SummaryMessageIDs {
		if id > 0 {
			out[id] = true
		}
	}
	for _, id := range s.PollMessageIDs {
		if id > 0 {
			out[id] = true
		}
	}
	for _, id := range s.ButtonMessageIDs {
		if id > 0 {
			out[id] = true
		}
	}
	return out
}

type rawSummaryState struct {
	Time              string          `json:"time"`
	SummaryMessageIDs json.RawMessage `json:"summary_message_ids,omitempty"`
	ReportMessageIDs  json.RawMessage `json:"report_message_ids,omitempty"`
	PollMessageIDs    []int           `json:"poll_message_ids,omitempty"`
	ButtonMessageIDs  []int           `json:"button_message_ids,omitempty"`
}

type persistedSummaryState struct {
	Time              string `json:"time"`
	SummaryMessageIDs []int  `json:"summary_message_ids"`
	PollMessageIDs    []int  `json:"poll_message_ids"`
	ButtonMessageIDs  []int  `json:"button_message_ids"`
}

// TimeManager 线程安全读写 data/.last_summary_time.json（对齐 summary_time_manager.py）。
type TimeManager struct {
	mu   sync.Mutex
	path string
}

// NewTimeManager 创建总结时间状态管理器。
func NewTimeManager(path string) *TimeManager {
	p := strings.TrimSpace(path)
	if p == "" {
		p = DefaultLastSummaryFile
	}
	return &TimeManager{path: p}
}

// Path 返回底层 JSON 文件路径。
func (m *TimeManager) Path() string { return m.path }

// RemoveFile discards all state under the same lock as summary completion.
func (m *TimeManager) RemoveFile() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	err := os.Remove(m.path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// LoadChannel 读取单个频道的上次总结状态（支持标准化与等价频道键匹配）。
func (m *TimeManager) LoadChannel(channel string) (*ChannelSummaryState, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	all, err := m.readAllLocked()
	if err != nil {
		return nil, false, err
	}
	norm := config.NormalizeChannelID(channel)
	if st, ok := all[norm]; ok {
		cp := cloneState(st)
		return &cp, true, nil
	}
	if key, ok := config.FindEquivalentChannelKey(all, norm); ok {
		cp := cloneState(all[key])
		return &cp, true, nil
	}
	return nil, false, nil
}

// LoadAll 返回所有频道的上次总结状态映射。
func (m *TimeManager) LoadAll() (map[string]ChannelSummaryState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	all, err := m.readAllLocked()
	if err != nil {
		return nil, err
	}
	out := make(map[string]ChannelSummaryState, len(all))
	for k, v := range all {
		out[k] = cloneState(v)
	}
	return out, nil
}

// SaveChannel 保存单个频道的总结时间与三类消息 ID。
func (m *TimeManager) SaveChannel(channel string, state ChannelSummaryState) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	all, err := m.readAllLocked()
	if err != nil {
		all = map[string]ChannelSummaryState{}
	}
	norm := config.NormalizeChannelID(channel)
	if key, ok := config.FindEquivalentChannelKey(all, norm); ok && key != norm {
		delete(all, key)
	}
	if state.Time.IsZero() {
		state.Time = time.Now().UTC()
	} else {
		state.Time = state.Time.UTC()
	}
	all[norm] = cloneState(state)
	return m.writeAllLocked(all)
}

// UpdatePollMessageIDs 仅更新指定频道的 poll_message_ids，保留原总结时间与 summary/button 消息 ID（供重新生成投票调用）。
func (m *TimeManager) UpdatePollMessageIDs(channel string, pollIDs []int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	all, err := m.readAllLocked()
	if err != nil {
		return err
	}
	norm := config.NormalizeChannelID(channel)
	key := norm
	st, ok := all[norm]
	if !ok {
		if eqKey, found := config.FindEquivalentChannelKey(all, norm); found {
			key = eqKey
			st = all[eqKey]
			ok = true
		}
	}
	if !ok {
		return nil
	}
	st.PollMessageIDs = nonNilInts(pollIDs)
	all[key] = st
	return m.writeAllLocked(all)
}

// DeleteChannel 删除单个频道的总结时间记录，返回是否实际命中删除。
func (m *TimeManager) DeleteChannel(channel string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	all, err := m.readAllLocked()
	if err != nil {
		return false, err
	}
	norm := config.NormalizeChannelID(channel)
	if _, ok := all[norm]; ok {
		delete(all, norm)
		return true, m.writeAllLocked(all)
	}
	if key, ok := config.FindEquivalentChannelKey(all, norm); ok {
		delete(all, key)
		return true, m.writeAllLocked(all)
	}
	return false, nil
}

// ClearAll 清空全部频道的上次总结时间记录，返回清除的频道数。
func (m *TimeManager) ClearAll() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	all, err := m.readAllLocked()
	if err != nil {
		all = map[string]ChannelSummaryState{}
	}
	n := len(all)
	if err := m.writeAllLocked(map[string]ChannelSummaryState{}); err != nil {
		return 0, err
	}
	return n, nil
}

func (m *TimeManager) readAllLocked() (map[string]ChannelSummaryState, error) {
	rawBytes, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]ChannelSummaryState{}, nil
		}
		return nil, err
	}
	trimmed := strings.TrimSpace(string(rawBytes))
	if trimmed == "" {
		return map[string]ChannelSummaryState{}, nil
	}
	var rawMap map[string]rawSummaryState
	if err := json.Unmarshal([]byte(trimmed), &rawMap); err != nil {
		return nil, err
	}
	out := make(map[string]ChannelSummaryState, len(rawMap))
	for ch, raw := range rawMap {
		ts, err := parseSummaryTimestamp(raw.Time)
		if err != nil {
			continue
		}
		sumIDs := parseFlexibleIntSlice(raw.SummaryMessageIDs)
		if len(sumIDs) == 0 && len(raw.ReportMessageIDs) > 0 {
			sumIDs = parseFlexibleIntSlice(raw.ReportMessageIDs)
		}
		out[ch] = ChannelSummaryState{
			Time:              ts,
			SummaryMessageIDs: nonNilInts(sumIDs),
			PollMessageIDs:    nonNilInts(raw.PollMessageIDs),
			ButtonMessageIDs:  nonNilInts(raw.ButtonMessageIDs),
		}
	}
	return out, nil
}

func (m *TimeManager) writeAllLocked(all map[string]ChannelSummaryState) error {
	parent := filepath.Dir(m.path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	payload := make(map[string]persistedSummaryState, len(all))
	for ch, st := range all {
		payload[ch] = persistedSummaryState{
			Time:              st.Time.UTC().Format("2006-01-02T15:04:05.999999+00:00"),
			SummaryMessageIDs: nonNilInts(st.SummaryMessageIDs),
			PollMessageIDs:    nonNilInts(st.PollMessageIDs),
			ButtonMessageIDs:  nonNilInts(st.ButtonMessageIDs),
		}
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		tmp, terr := os.CreateTemp(parent, ".last-summary-*.tmp")
		if terr != nil {
			lastErr = terr
			time.Sleep(time.Duration(300*(1<<attempt)) * time.Millisecond)
			continue
		}
		tmpName := tmp.Name()
		_, werr := tmp.Write(encoded)
		serr := tmp.Sync()
		cerr := tmp.Close()
		if werr != nil || serr != nil || cerr != nil {
			_ = os.Remove(tmpName)
			lastErr = firstNonNil(werr, serr, cerr)
			continue
		}
		if rerr := os.Rename(tmpName, m.path); rerr != nil {
			_ = os.Remove(m.path)
			if rerr2 := os.Rename(tmpName, m.path); rerr2 != nil {
				_ = os.Remove(tmpName)
				lastErr = rerr2
				time.Sleep(time.Duration(300*(1<<attempt)) * time.Millisecond)
				continue
			}
		}
		return nil
	}
	return fmt.Errorf("保存上次总结时间文件 %s 失败: %w", m.path, lastErr)
}

func parseSummaryTimestamp(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999-07:00",
		"2006-01-02T15:04:05-07:00",
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if strings.Contains(layout, "-07:00") || layout == time.RFC3339 || layout == time.RFC3339Nano {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC(), nil
			}
		} else {
			if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
				return t.UTC(), nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp format: %q", s)
}

func parseFlexibleIntSlice(raw json.RawMessage) []int {
	if len(raw) == 0 {
		return []int{}
	}
	var direct []int
	if err := json.Unmarshal(raw, &direct); err == nil {
		return nonNilInts(direct)
	}
	var wrapped struct {
		SummaryMessageIDs []int `json:"summary_message_ids"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil {
		return nonNilInts(wrapped.SummaryMessageIDs)
	}
	return []int{}
}

func nonNilInts(in []int) []int {
	if len(in) == 0 {
		return []int{}
	}
	cp := make([]int, len(in))
	copy(cp, in)
	return cp
}

func cloneState(in ChannelSummaryState) ChannelSummaryState {
	return ChannelSummaryState{
		Time:              in.Time,
		SummaryMessageIDs: nonNilInts(in.SummaryMessageIDs),
		PollMessageIDs:    nonNilInts(in.PollMessageIDs),
		ButtonMessageIDs:  nonNilInts(in.ButtonMessageIDs),
	}
}

func firstNonNil(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
