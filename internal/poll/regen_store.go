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
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultPollRegenerationsFile 是投票重新生成状态文件路径（对齐 config.POLL_REGENERATIONS_FILE）。
const DefaultPollRegenerationsFile = "data/.poll_regenerations.json"

// RegenRecord 对应 data/.poll_regenerations.json 中的单条记录。
type RegenRecord struct {
	PollMessageID          int     `json:"poll_message_id"`
	ButtonMessageID        *int    `json:"button_message_id"`
	SummaryText            string  `json:"summary_text"`
	ChannelName            string  `json:"channel_name"`
	Timestamp              string  `json:"timestamp"`
	SendToChannel          bool    `json:"send_to_channel"`
	VoteCount              int     `json:"vote_count"`
	Voters                 []int64 `json:"voters"`
	DiscussionForwardMsgID *int    `json:"discussion_forward_msg_id,omitempty"`
}

// RegenStore 线程安全管理 data/.poll_regenerations.json（对齐 core/config.py:1129-1371）。
type RegenStore struct {
	mu   sync.Mutex
	path string
}

// NewRegenStore 创建投票重生成状态管理器。
func NewRegenStore(path string) *RegenStore {
	p := strings.TrimSpace(path)
	if p == "" {
		p = DefaultPollRegenerationsFile
	}
	return &RegenStore{path: p}
}

// RemoveFile clears the regeneration state without racing active votes.
func (s *RegenStore) RemoveFile() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// Add 写入一条新的投票重生成映射。
func (s *RegenStore) Add(channel string, summaryMsgID int, rec RegenRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	all := s.loadLocked()
	if all[channel] == nil {
		all[channel] = map[string]RegenRecord{}
	}
	if rec.Timestamp == "" {
		rec.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	if rec.Voters == nil {
		rec.Voters = []int64{}
	}
	if rec.SendToChannel {
		rec.DiscussionForwardMsgID = nil
	}
	all[channel][strconv.Itoa(summaryMsgID)] = cloneRegenRecord(rec)
	return s.saveLocked(all)
}

// Get 获取指定频道与总结消息 ID 对应的记录。
func (s *RegenStore) Get(channel string, summaryMsgID int) (*RegenRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	all := s.loadLocked()
	chMap, ok := all[channel]
	if !ok {
		return nil, false
	}
	rec, ok := chMap[strconv.Itoa(summaryMsgID)]
	if !ok {
		return nil, false
	}
	cp := cloneRegenRecord(rec)
	return &cp, true
}

// FindBySummaryMessageID 遍历全部频道定位 summaryMsgID（对齐回调处理器遍历逻辑）。
func (s *RegenStore) FindBySummaryMessageID(summaryMsgID int) (string, *RegenRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := strconv.Itoa(summaryMsgID)
	all := s.loadLocked()
	for ch, records := range all {
		if rec, ok := records[key]; ok {
			cp := cloneRegenRecord(rec)
			return ch, &cp, true
		}
	}
	return "", nil, false
}

// UpdatePollMessage 更新重新生成后的新投票消息 ID。
func (s *RegenStore) UpdatePollMessage(channel string, summaryMsgID, newPollMsgID int, buttonMsgID *int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	all := s.loadLocked()
	key := strconv.Itoa(summaryMsgID)
	chMap, ok := all[channel]
	if !ok {
		return nil
	}
	rec, ok := chMap[key]
	if !ok {
		return nil
	}
	rec.PollMessageID = newPollMsgID
	rec.ButtonMessageID = buttonMsgID
	chMap[key] = rec
	return s.saveLocked(all)
}

// IncrementVote 为指定用户增加一次请求重生成投票（返回：是否成功写入、当前票数、是否重复投票）。
func (s *RegenStore) IncrementVote(channel string, summaryMsgID int, userID int64) (bool, int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	all := s.loadLocked()
	key := strconv.Itoa(summaryMsgID)
	chMap, ok := all[channel]
	if !ok {
		return false, 0, false
	}
	rec, ok := chMap[key]
	if !ok {
		return false, 0, false
	}
	for _, v := range rec.Voters {
		if v == userID {
			return false, rec.VoteCount, true
		}
	}
	rec.VoteCount++
	rec.Voters = append(rec.Voters, userID)
	chMap[key] = rec
	if err := s.saveLocked(all); err != nil {
		return false, 0, false
	}
	return true, rec.VoteCount, false
}

// ResetVotes 重置指定记录的票数与已投票名单。
func (s *RegenStore) ResetVotes(channel string, summaryMsgID int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	all := s.loadLocked()
	key := strconv.Itoa(summaryMsgID)
	chMap, ok := all[channel]
	if !ok {
		return false
	}
	rec, ok := chMap[key]
	if !ok {
		return false
	}
	rec.VoteCount = 0
	rec.Voters = []int64{}
	chMap[key] = rec
	return s.saveLocked(all) == nil
}

// CleanupOld 清理超过 days 天的陈旧记录（对齐 cleanup_old_regenerations，默认 30 天）。
func (s *RegenStore) CleanupOld(days int) int {
	if days <= 0 {
		days = 30
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	all := s.loadLocked()
	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	removed := 0
	for ch, records := range all {
		for sid, rec := range records {
			ts, err := parseFlexTimestamp(rec.Timestamp)
			if err == nil && ts.Before(cutoff) {
				delete(records, sid)
				removed++
			}
		}
		if len(records) == 0 {
			delete(all, ch)
		}
	}
	if removed > 0 {
		_ = s.saveLocked(all)
	}
	return removed
}

func (s *RegenStore) loadLocked() map[string]map[string]RegenRecord {
	raw, err := os.ReadFile(s.path)
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
		return map[string]map[string]RegenRecord{}
	}
	var out map[string]map[string]RegenRecord
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return map[string]map[string]RegenRecord{}
	}
	return out
}

func (s *RegenStore) saveLocked(all map[string]map[string]RegenRecord) error {
	parent := filepath.Dir(s.path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(parent, ".poll-regen-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	_ = tmp.Sync()
	_ = tmp.Close()
	if err := os.Rename(tmpName, s.path); err != nil {
		_ = os.Remove(s.path)
		if err2 := os.Rename(tmpName, s.path); err2 != nil {
			_ = os.Remove(tmpName)
			return err2
		}
	}
	return nil
}

func cloneRegenRecord(in RegenRecord) RegenRecord {
	cp := in
	if in.ButtonMessageID != nil {
		b := *in.ButtonMessageID
		cp.ButtonMessageID = &b
	}
	if in.DiscussionForwardMsgID != nil {
		d := *in.DiscussionForwardMsgID
		cp.DiscussionForwardMsgID = &d
	}
	cp.Voters = make([]int64, len(in.Voters))
	copy(cp.Voters, in.Voters)
	return cp
}

func parseFlexTimestamp(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
	} {
		if strings.Contains(layout, "Z07:00") {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC(), nil
			}
		} else {
			if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
				return t.UTC(), nil
			}
		}
	}
	return time.Time{}, os.ErrInvalid
}
