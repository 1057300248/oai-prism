package facade

import (
	"encoding/json"
	"sync"
	"time"
)

// JournalEntry 是处于进行中或挂起状态的请求条目。
// 满足 Issue #256 要求：start 请求超时或结果不明时记录 pending 状态，禁止自动重放。
type JournalEntry struct {
	RequestID      string          `json:"request_id"`
	ConversationID string          `json:"conversation_id"`
	TurnState      json.RawMessage `json:"turn_state"`
	AccountID      string          `json:"account_id"`
	ProjectID      string          `json:"project_id"`
	StartedAt      time.Time       `json:"started_at"`
	LastPollAt     time.Time       `json:"last_poll_at"`
	Status         string          `json:"status"` // "started" | "pending" | "completed" | "failed"
	FinalText      string          `json:"final_text,omitempty"`
	Error          string          `json:"error,omitempty"`
}

// PendingJournal 提供挂起日志和不可重放保护。
type PendingJournal struct {
	mu      sync.RWMutex
	entries map[string]*JournalEntry
}

// NewPendingJournal 初始化挂起日志表。
func NewPendingJournal() *PendingJournal {
	return &PendingJournal{
		entries: make(map[string]*JournalEntry),
	}
}

// RecordStart 记录一次成功的 start 请求。
func (j *PendingJournal) RecordStart(reqID, convID, acctID, projectID string, turnState json.RawMessage) {
	j.mu.Lock()
	defer j.mu.Unlock()

	e := &JournalEntry{
		RequestID:      reqID,
		ConversationID: convID,
		TurnState:      turnState,
		AccountID:      acctID,
		ProjectID:      projectID,
		StartedAt:      time.Now(),
		LastPollAt:     time.Now(),
		Status:         "started",
	}

	if reqID != "" {
		j.entries[reqID] = e
	}
	if convID != "" {
		j.entries["conv:"+convID] = e
	}
}

// UpdateState 更新轮询到的最新 turn_state 和状态。
func (j *PendingJournal) UpdateState(reqID string, turnState json.RawMessage, status string) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if e, ok := j.entries[reqID]; ok {
		if len(turnState) > 0 {
			e.TurnState = turnState
		}
		e.LastPollAt = time.Now()
		e.Status = status
	}
}

// MarkTerminal 标记请求到达终态。
func (j *PendingJournal) MarkTerminal(reqID string, status string, text string, err error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if e, ok := j.entries[reqID]; ok {
		e.Status = status
		e.FinalText = text
		if err != nil {
			e.Error = err.Error()
		}
		e.LastPollAt = time.Now()
	}
}

// Get 查询挂起条目。
func (j *PendingJournal) Get(reqID string) (*JournalEntry, bool) {
	j.mu.RLock()
	defer j.mu.RUnlock()

	e, ok := j.entries[reqID]
	if !ok {
		return nil, false
	}
	cp := *e
	return &cp, true
}

// Cleanup 清理超时的条目。
func (j *PendingJournal) Cleanup(ttl time.Duration) {
	j.mu.Lock()
	defer j.mu.Unlock()

	now := time.Now()
	for k, v := range j.entries {
		if now.Sub(v.LastPollAt) > ttl {
			delete(j.entries, k)
		}
	}
}
