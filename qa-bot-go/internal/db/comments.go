package db

import (
	"context"

	"encoding/json"
)

type CommentRecord struct {
	ChatID    int64  `json:"chat_id"`
	MessageID int    `json:"message_id"`
	RootID    int    `json:"root_id"`
	Channel   string `json:"channel"`
	UserID    int64  `json:"user_id"`
	Text      string `json:"text"`
	Date      int    `json:"date"`
	ReplyID   int    `json:"reply_id"`
	Edited    bool   `json:"edited"`
	Media     string `json:"media"`
}

func (m *Manager) InitComments(ctx context.Context) error {
	_, e := m.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS qa_comments(chat_id BIGINT NOT NULL,message_id BIGINT NOT NULL,root_id BIGINT NOT NULL,channel VARCHAR(500) NOT NULL,payload LONGTEXT NOT NULL,message_date BIGINT NOT NULL,PRIMARY KEY(chat_id,message_id),INDEX(chat_id,root_id,message_date)) ENGINE=InnoDB`)
	if e != nil {
		return e
	}
	_, e = m.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS qa_posts(channel_id BIGINT NOT NULL,post_id BIGINT NOT NULL,chat_id BIGINT NOT NULL,root_id BIGINT NOT NULL,PRIMARY KEY(channel_id,post_id)) ENGINE=InnoDB`)
	return e
}
func (m *Manager) CommentRoot(ctx context.Context, chat int64, id int) int {
	var root int
	_ = m.DB.QueryRowContext(ctx, `SELECT root_id FROM qa_comments WHERE chat_id=? AND message_id=?`, chat, id).Scan(&root)
	return root
}
func (m *Manager) SaveComment(ctx context.Context, r CommentRecord) error {
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	_, e = m.DB.ExecContext(ctx, `INSERT INTO qa_comments(chat_id,message_id,root_id,channel,payload,message_date) VALUES(?,?,?,?,?,?) ON DUPLICATE KEY UPDATE root_id=VALUES(root_id),payload=VALUES(payload),message_date=VALUES(message_date)`, r.ChatID, r.MessageID, r.RootID, r.Channel, string(b), r.Date)
	return e
}
func (m *Manager) CommentContext(ctx context.Context, chat int64, root int) (string, error) {
	rows, e := m.DB.QueryContext(ctx, `SELECT payload FROM qa_comments WHERE chat_id=? AND root_id=? ORDER BY message_date,message_id`, chat, root)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	data := []json.RawMessage{}
	for rows.Next() {
		var s string
		if e = rows.Scan(&s); e != nil {
			return "", e
		}
		data = append(data, json.RawMessage(s))
	}
	if e = rows.Err(); e != nil {
		return "", e
	}
	b, e := json.Marshal(data)
	return "以下为接入后收集的帖子与评论快照；删除与不可见消息无法由 Bot API 完整同步。媒体仅记录可见说明。\n" + string(b), e
}

func (m *Manager) MapPost(ctx context.Context, channel int64, post int, chat int64, root int) error {
	_, e := m.DB.ExecContext(ctx, `INSERT INTO qa_posts(channel_id,post_id,chat_id,root_id) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE chat_id=VALUES(chat_id),root_id=VALUES(root_id)`, channel, post, chat, root)
	return e
}
func (m *Manager) EditPost(ctx context.Context, channel int64, post int, text string) error {
	var chat int64
	var root int
	var raw string
	e := m.DB.QueryRowContext(ctx, `SELECT p.chat_id,p.root_id,c.payload FROM qa_posts p JOIN qa_comments c ON c.chat_id=p.chat_id AND c.message_id=p.root_id WHERE p.channel_id=? AND p.post_id=?`, channel, post).Scan(&chat, &root, &raw)
	if e != nil {
		return e
	}
	var r CommentRecord
	if e = json.Unmarshal([]byte(raw), &r); e != nil {
		return e
	}
	r.Text = text
	r.Edited = true
	return m.SaveComment(ctx, r)
}
func (m *Manager) ReconcileComments(ctx context.Context, chat int64) error {
	for i := 0; i < 64; i++ {
		res, e := m.DB.ExecContext(ctx, `UPDATE qa_comments child JOIN qa_comments parent ON child.chat_id=parent.chat_id AND CAST(JSON_UNQUOTE(JSON_EXTRACT(child.payload,'$.reply_id')) AS SIGNED)=parent.message_id SET child.root_id=parent.root_id,child.payload=JSON_SET(child.payload,'$.root_id',parent.root_id) WHERE child.chat_id=? AND child.root_id=0 AND parent.root_id>0`, chat)
		if e != nil {
			return e
		}
		n, e := res.RowsAffected()
		if e != nil {
			return e
		}
		if n == 0 {
			return nil
		}
	}
	return nil
}

func (m *Manager) CommentChannel(ctx context.Context, chat int64, root int) string {
	var channel string
	_ = m.DB.QueryRowContext(ctx, `SELECT channel FROM qa_comments WHERE chat_id=? AND message_id=?`, chat, root).Scan(&channel)
	return channel
}
