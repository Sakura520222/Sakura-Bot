package guard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type VerificationQuestion struct {
	Prompt  string   `json:"prompt"`
	Options []string `json:"options"`
	Nonce   string   `json:"nonce"`
	Answer  int      `json:"answer"`
}

type Verification struct {
	Version       int                   `json:"version"`
	State         string                `json:"state"`
	Secret        string                `json:"secret,omitempty"`
	Username      string                `json:"username"`
	BotID         int64                 `json:"bot_id"`
	Steps         int                   `json:"steps"`
	QuestionTypes []string              `json:"question_types,omitempty"`
	MaxAttempts   int                   `json:"max_attempts"`
	Question      *VerificationQuestion `json:"question,omitempty"`
	Receipts      []string              `json:"receipts,omitempty"`
	ActorID       int64                 `json:"actor_id,omitempty"`
	SessionID     string                `json:"session_id,omitempty"`
	ChatID        int64                 `json:"chat_id,omitempty"`
	StartedAt     int64                 `json:"started_at,omitempty"`
	CompletedAt   int64                 `json:"completed_at,omitempty"`
	Attempts      int                   `json:"-"`
	Now           int64                 `json:"-"`
}

func (r *Repository) VerificationHeartbeat(ctx context.Context, username string, botID int64) error {
	_, err := r.DB.ExecContext(ctx, `INSERT INTO moderation_verification_assistants (username,bot_id,seen_at)
		VALUES (?,?,UTC_TIMESTAMP()) ON DUPLICATE KEY UPDATE bot_id=VALUES(bot_id), seen_at=VALUES(seen_at)`, username, botID)
	return err
}

// Verification is read and updated only while holding the shared Guard user lock.
func (s *Store) Verification(ctx context.Context, id string, actor int64) (*Verification, error) {
	var state Verification
	var raw []byte
	err := s.runner().QueryRowContext(ctx, `SELECT JSON_EXTRACT(payload, '$.assistant'), chat_id, attempts,
		UNIX_TIMESTAMP() FROM moderation_verification_sessions WHERE id=? AND user_id=?
		AND status='waiting' AND message_id IS NOT NULL AND expires_at > UTC_TIMESTAMP()
		AND JSON_UNQUOTE(JSON_EXTRACT(payload, '$.restriction_confirmed'))='true'
		AND JSON_EXTRACT(payload, '$.assistant') IS NOT NULL`, id, actor).Scan(&raw, &state.ChatID, &state.Attempts, &state.Now)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	chatID := state.ChatID
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	if state.State != "pending" && (state.ChatID != chatID || state.SessionID != id || state.ActorID != actor) {
		return nil, nil
	}
	state.ChatID, state.SessionID, state.ActorID = chatID, id, actor
	return &state, nil
}

func (s *Store) SaveVerification(ctx context.Context, state *Verification) (bool, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return false, err
	}
	rows, err := s.Exec(ctx, `UPDATE moderation_verification_sessions
		SET payload=JSON_SET(payload, '$.assistant', JSON_EXTRACT(?, '$')), attempts=?, updated_at=UTC_TIMESTAMP()
		WHERE id=? AND user_id=? AND chat_id=? AND status='waiting' AND expires_at > UTC_TIMESTAMP()`,
		string(raw), state.Attempts, state.SessionID, state.ActorID, state.ChatID)
	return rows == 1, err
}
