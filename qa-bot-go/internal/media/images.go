// Package media keeps short-lived Telegram file references, never image binaries.
package media

import (
	"context"
	"regexp"
	"sort"
	"sync"
	"time"
)

const MaxImages = 4
const MaxImageBytes = 8 << 20
const MaxPixels = 12_000_000
const MaxSide = 8192
const Ack = "🖼️ 图片已暂存，可继续发图（最多 4 张），然后发送问题。5 分钟内可用「它 / 上图」追问；新图片替换上一批，/clear 清除。"
const VisionDisabled = "当前「助手问答」模型未开启图片输入。请在 WebUI 的 AI 配置中选择支持图片的助手模型，并开启该模型的图片能力。"
const RequestFailed = "图片问答失败：上游未接受图片或请求超时，请检查助手模型的图片能力与服务配置后重试。"

type InputError string

func (e InputError) Error() string { return string(e) }

type Scope struct {
	UserID, ChatID int64
	Thread         string
}
type Ref struct {
	FileID    string
	MessageID int
	Created   time.Time
}
type state struct {
	pending, active []Ref
	session         string
	busy            bool
	seen            map[int]bool
}
type Lease struct {
	scope    Scope
	state    *state
	Refs     []Ref
	consumed bool
}
type Store struct {
	mu      sync.Mutex
	states  map[Scope]*state
	ttl     time.Duration
	now     func() time.Time
	expired map[Scope]expiry
}
type expiry struct {
	at      time.Time
	pending bool
}

var ImageSlots = make(chan struct{}, 2)

func NewStore(ttl time.Duration) *Store {
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	if ttl < 30*time.Second {
		ttl = 30 * time.Second
	}
	if ttl > 10*time.Minute {
		ttl = 10 * time.Minute
	}
	return &Store{states: make(map[Scope]*state), expired: make(map[Scope]expiry), ttl: ttl, now: time.Now}
}
func (s *Store) cleanupLocked() {
	cutoff := s.now().Add(-s.ttl)
	for key, st := range s.states {
		hadPending, hadImages := len(st.pending) > 0, len(st.pending)+len(st.active) > 0
		keep := func(refs []Ref) []Ref {
			out := make([]Ref, 0, len(refs))
			for _, r := range refs {
				if r.Created.After(cutoff) {
					out = append(out, r)
				}
			}
			return out
		}
		st.pending, st.active = keep(st.pending), keep(st.active)
		st.seen = make(map[int]bool)
		for _, r := range append(append([]Ref{}, st.pending...), st.active...) {
			st.seen[r.MessageID] = true
		}
		if len(st.pending)+len(st.active) == 0 && !st.busy {
			if hadImages {
				s.expired[key] = expiry{s.now(), hadPending}
			}
			delete(s.states, key)
		}
	}
	for key, value := range s.expired {
		if s.now().Sub(value.at) > time.Minute {
			delete(s.expired, key)
		}
	}
	if len(s.expired) > 256 {
		for key := range s.expired {
			delete(s.expired, key)
			if len(s.expired) <= 256 {
				break
			}
		}
	}
}
func (s *Store) Cleanup() { s.mu.Lock(); defer s.mu.Unlock(); s.cleanupLocked() }
func (s *Store) Run(ctx context.Context) {
	timer := time.NewTicker(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.Cleanup()
		}
	}
}

// Add deduplicates Telegram message IDs and orders albums by message ID.
func (s *Store) Add(scope Scope, ref Ref) (added, first bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	st := s.states[scope]
	delete(s.expired, scope)
	if st == nil {
		if len(s.states) >= 256 {
			return false, false, InputError("图片暂存已满，请稍后再试。")
		}
		st = &state{seen: make(map[int]bool)}
		s.states[scope] = st
	}
	if st.seen[ref.MessageID] {
		return false, false, nil
	}
	if len(st.pending) >= MaxImages {
		return false, false, InputError("一次最多 4 张图片，请先提问或用 /clear 清除。")
	}
	if ref.Created.IsZero() {
		ref.Created = s.now()
	}
	first = len(st.pending) == 0
	st.pending = append(st.pending, ref)
	sort.Slice(st.pending, func(i, j int) bool { return st.pending[i].MessageID < st.pending[j].MessageID })
	st.seen[ref.MessageID] = true
	return true, first, nil
}

var followup = regexp.MustCompile(`(?i)它|这张|那张|上图|图中|图片|照片|\b(it|its|image|photo|picture)\b`)

func (s *Store) Begin(scope Scope, session, query string, reply bool) (*Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	st := s.states[scope]
	if st == nil {
		if expired, ok := s.expired[scope]; ok && (expired.pending || reply || followup.MatchString(query)) {
			delete(s.expired, scope)
			return nil, InputError("图片已过期，请重新发送图片后提问。")
		}
		return nil, nil
	}
	if st.busy {
		return nil, InputError("上一条图片问题仍在处理，请稍后再发送问题。")
	}
	if st.session != session {
		st.active = nil
	}
	consumed := len(st.pending) > 0
	if consumed {
		st.active, st.pending = st.pending, nil
	}
	st.session = session
	if !consumed && !reply && !followup.MatchString(query) {
		return nil, nil
	}
	if len(st.active) == 0 {
		return nil, nil
	}
	st.busy = true
	return &Lease{scope: scope, state: st, Refs: append([]Ref(nil), st.active...), consumed: consumed}, nil
}
func (s *Store) Finish(lease *Lease, success bool) {
	if lease == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[lease.scope]
	if st != lease.state {
		return
	}
	st.busy = false
	if !success && lease.consumed && len(st.pending) == 0 {
		st.pending = append([]Ref(nil), lease.Refs...)
	}
	s.cleanupLocked()
}
func (s *Store) ClearUser(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.states {
		if key.UserID == userID {
			delete(s.states, key)
		}
	}
	for key := range s.expired {
		if key.UserID == userID {
			delete(s.expired, key)
		}
	}
}

type Request struct {
	Store         *Store
	Scope         Scope
	Reply         bool
	RequireImages bool
	UsedImages    bool
	Download      func(context.Context, Ref) (string, error)
}
type requestKey struct{}

func WithRequest(ctx context.Context, r *Request) context.Context {
	return context.WithValue(ctx, requestKey{}, r)
}
func RequestFrom(ctx context.Context) *Request { r, _ := ctx.Value(requestKey{}).(*Request); return r }

func (s *Store) ClearScope(scope Scope) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, scope)
	delete(s.expired, scope)
}
