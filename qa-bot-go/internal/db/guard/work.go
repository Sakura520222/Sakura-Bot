// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// work.go：AppealRepository 层的免锁只读轮询与派生函数，移植
// core/moderation/appeal_repository.py:302-381 与 appeal_inbox.py:265-267。

package guard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	db "github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
)

// Get 按 ID 取申诉（免锁读，appeal_repository.py:316-318）。nil = 不存在。
func (r *Repository) Get(ctx context.Context, appealID string) (Row, error) {
	var out Row
	err := r.Reader(ctx, func(s *Store) error {
		row, e := s.Get(ctx, appealID)
		out = row
		return e
	})
	return out, err
}

// Input 取 actor 输入草稿（免锁读，appeal_repository.py:320-322）。
func (r *Repository) Input(ctx context.Context, actorID int64) (Row, error) {
	var out Row
	err := r.Reader(ctx, func(s *Store) error {
		row, e := s.Input(ctx, actorID)
		out = row
		return e
	})
	return out, err
}

// UserRows 用户申诉历史/来源 case 分页（appeal_repository.py:324-338）：
// cases=false 取申诉行，true 取 ban 相关 case 行；每页 6 条。
func (r *Repository) UserRows(ctx context.Context, userID int64, cases bool, offset int64) ([]Row, error) {
	if offset < 0 {
		offset = 0
	}
	var out []Row
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		if cases {
			out, e = s.All(ctx,
				`SELECT c.* FROM moderation_cases c WHERE c.user_id=?
                AND (c.action='ban' OR c.status='blacklisted'
                OR JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.review_ban_confirmed'))='true')
                AND c.status IN ('pending','blacklisted','done')
                ORDER BY c.created_at DESC,c.id DESC LIMIT 6 OFFSET ?`,
				userID, offset)
			return e
		}
		out, e = s.All(ctx,
			"SELECT * FROM moderation_appeals WHERE user_id=? ORDER BY created_at DESC,id DESC LIMIT 6 OFFSET ?",
			userID, offset)
		return e
	})
	return out, err
}

// Work 申诉工作轮询（appeal_repository.py:340-359，deliver 5s job 的数据源）：
//   - appeals：需要同步通知（published_revision<revision）、过期草稿、
//     或源 case 状态/动作/审核代际变化（JSON_EXTRACT 比较）的申诉；
//   - requests：待处理审核请求。
//
// 返回 (申诉行, 请求行)。
func (r *Repository) Work(ctx context.Context) ([]Row, []Row, error) {
	var appeals, requests []Row
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		appeals, e = s.All(ctx,
			`SELECT a.* FROM moderation_appeals a JOIN moderation_cases c ON c.id=a.case_id WHERE
            (a.status='executing' AND a.retry_at<=?) OR a.published_revision<a.revision
            OR (a.status='draft' AND a.updated_at<?)
            OR (a.status IN ('pending','need_info') AND
            (c.status<>JSON_UNQUOTE(JSON_EXTRACT(a.payload,'$.source.status'))
            OR c.action<>JSON_UNQUOTE(JSON_EXTRACT(a.payload,'$.source.action'))
            OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.review_generation')),'0')<>
            COALESCE(JSON_UNQUOTE(JSON_EXTRACT(a.payload,'$.source.payload.review_generation')),'0')))
            ORDER BY a.updated_at ASC LIMIT 50`,
			db.MysqlDatetime6(db.NowUTC()),
			db.MysqlDatetime6(db.NowUTC().Add(-24*time.Hour)))
		if e != nil {
			return e
		}
		requests, e = s.All(ctx,
			`SELECT r.*,a.user_id FROM moderation_appeal_requests r
            JOIN moderation_appeals a ON a.id=r.appeal_id
            WHERE r.status='pending' ORDER BY r.created_at ASC LIMIT 50`)
		return e
	})
	if err != nil {
		return nil, nil, err
	}
	return appeals, requests, nil
}

// InvalidateRouting 路由失效（appeal_repository.py:361-365）：进程重启后
// 让进行中的申诉重新发布卡片。
func (r *Repository) InvalidateRouting(ctx context.Context) error {
	return r.Reader(ctx, func(s *Store) error {
		_, e := s.Exec(ctx,
			"UPDATE moderation_appeals SET published_revision=0 WHERE status IN ('submitted','pending','need_info','executing')")
		return e
	})
}

// Deliveries 通知投递轮询（appeal_repository.py:367-381）：
//   - notices：kind<>'retired' 且 synced_revision<revision 且到期的通知；
//   - results：已结束（非 pending）且未 notified 且到期的请求回执。
func (r *Repository) Deliveries(ctx context.Context) ([]Row, []Row, error) {
	var notices, results []Row
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		notices, e = s.All(ctx,
			`SELECT n.*,a.user_id FROM moderation_appeal_notifications n
            JOIN moderation_appeals a ON a.id=n.appeal_id
            WHERE n.kind<>'retired' AND n.synced_revision<a.revision AND n.retry_at<=?
            ORDER BY n.retry_at ASC LIMIT 50`,
			db.MysqlDatetime6(db.NowUTC()))
		if e != nil {
			return e
		}
		results, e = s.All(ctx,
			`SELECT * FROM moderation_appeal_requests WHERE status<>'pending'
            AND notified=FALSE AND retry_at<=? ORDER BY created_at ASC LIMIT 50`,
			db.MysqlDatetime6(db.NowUTC()))
		return e
	})
	if err != nil {
		return nil, nil, err
	}
	return notices, results, nil
}

// QuickReviewRequestID 派生 quick_review 的幂等请求 ID
// （appeal_inbox.py:265-267）：
//
//	sha256(f"review:{appeal_id}:{actor_id}:{revision}:{origin}:{action}:{latest_id}")
//
// origin 复刻 Python 元组的 f-string 形态 "(chat, msg)"（圆括号、逗号后带
// 空格；调用点 appeal_handler.py:221 传入的是 tuple 而非 list）；
// latestID 为空串对应 Python 的 latest['id'] if latest else ”。
func QuickReviewRequestID(appealID string, actorID, revision int64, originChatID, originMessageID int64, action, latestID string) string {
	raw := fmt.Sprintf("review:%s:%d:%d:(%d, %d):%s:%s",
		appealID, actorID, revision, originChatID, originMessageID, action, latestID)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// rowInt 提取整型列/JSON 数值：int64（驱动整型列）与 float64
// （JSON 反序列化数字）统一转 int64。
func rowInt(row Row, key string) int64 {
	if row == nil {
		return 0
	}
	switch v := row[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
}

// rowString 提取字符串列（nil → 空串）。
func rowString(row Row, key string) string {
	if row == nil {
		return ""
	}
	switch v := row[key].(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return ""
	}
}
