// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// appeals.go：AppealStore 仓储方法，逐条移植 core/moderation/appeal_repository.py
//（行号见各方法注释）。行值统一用 Row（DictCursor 等价物）；payload 已在
// All/One 中解析为 map/list。时间参数全部走 MysqlDatetime6（DATETIME(6)）。

package guard

import (
	"context"
	"fmt"
	"time"

	db "github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
)

// Get 按 ID 取申诉（appeal_repository.py:38-39）。nil = 不存在。
func (s *Store) Get(ctx context.Context, appealID string) (Row, error) {
	return s.One(ctx, "SELECT * FROM moderation_appeals WHERE id=?", appealID)
}

// Active 取 (chat_id, user_id) 的进行中申诉（appeal_repository.py:41-44）。
func (s *Store) Active(ctx context.Context, chatID, userID int64) (Row, error) {
	return s.One(ctx, "SELECT * FROM moderation_appeals WHERE active_key=?", ActiveKey(chatID, userID))
}

// Input 取 actor 的输入草稿（appeal_repository.py:46-49）。nil = 无草稿。
func (s *Store) Input(ctx context.Context, actorID int64) (Row, error) {
	return s.One(ctx, "SELECT * FROM moderation_appeal_inputs WHERE actor_id=?", actorID)
}

// SaveInput upsert 输入草稿（appeal_repository.py:51-60）：token 传空串时
// 生成 uuid4().hex；写后返回最新草稿行。
func (s *Store) SaveInput(ctx context.Context, actorID int64, appealID, mode string, payload any, token string) (Row, error) {
	if token == "" {
		token = uuid4Hex()
	}
	encoded := db.EncodeJSONMust(payload)
	if _, err := s.Exec(ctx, `INSERT INTO moderation_appeal_inputs
            (actor_id,token,appeal_id,mode,payload,updated_at) VALUES (?,?,?,?,?,?)
            ON DUPLICATE KEY UPDATE token=VALUES(token),appeal_id=VALUES(appeal_id),
            mode=VALUES(mode),payload=VALUES(payload),updated_at=VALUES(updated_at)`,
		actorID, token, appealID, mode, encoded, db.MysqlDatetime6(db.NowUTC())); err != nil {
		return nil, err
	}
	return s.Input(ctx, actorID)
}

// ClearInput 删除输入草稿（appeal_repository.py:62-65）。
func (s *Store) ClearInput(ctx context.Context, actorID int64, token string) error {
	_, err := s.Exec(ctx,
		"DELETE FROM moderation_appeal_inputs WHERE actor_id=? AND token=?", actorID, token)
	return err
}

// Event 插入审计事件（appeal_repository.py:67-81）：to_status 取申诉行当前
// status；oldStatus 传 nil 写 NULL（Python from_status=None）；payload 为 nil
// 时写 {}（对齐 encode(payload or {})）。actor 已是字符串形态。
func (s *Store) Event(ctx context.Context, appeal Row, actor, action string, oldStatus any, payload any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	_, err := s.Exec(ctx, `INSERT INTO moderation_appeal_events
            (appeal_id,actor,action,from_status,to_status,payload,created_at)
            VALUES (?,?,?,?,?,?,?)`,
		rowString(appeal, "id"), actor, action, oldStatus, rowString(appeal, "status"),
		db.EncodeJSONMust(payload), db.MysqlDatetime6(db.NowUTC()))
	return err
}

// Create 创建 draft 申诉（appeal_repository.py:83-111）：三语句事务
// （插入 + created 审计 + 初始化草稿输入），返回完整申诉行。
func (s *Store) Create(ctx context.Context, caseRow Row, snapshot any) (Row, error) {
	appealID := uuid4Hex()
	chatID := rowInt(caseRow, "chat_id")
	userID := rowInt(caseRow, "user_id")
	appeal := Row{"id": appealID, "status": "draft"}
	now := db.MysqlDatetime6(db.NowUTC())
	err := s.Atomic(ctx, func(s *Store) error {
		if _, err := s.Exec(ctx, `INSERT INTO moderation_appeals
            (id,case_id,chat_id,user_id,active_key,status,published_revision,payload,retry_at,created_at,updated_at)
            VALUES (?,?,?,?,?,'draft',1,?,?,?,?)`,
			appealID, rowString(caseRow, "id"), chatID, userID,
			ActiveKey(chatID, userID), db.EncodeJSONMust(snapshot),
			now, now, now); err != nil {
			return err
		}
		if err := s.Event(ctx, appeal,
			fmt.Sprintf("telegram:%d", userID), "created", nil, nil); err != nil {
			return err
		}
		_, err := s.SaveInput(ctx, userID, appealID, "draft",
			map[string]any{"text": "", "files": []any{}}, "")
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, appealID)
}

// TransitionOptions 对齐 _transition 的关键字参数（appeal_repository.py:113-115）：
// Payload 为 nil 时沿用申诉当前 payload；Error 为 nil 写 NULL；Detail 为审计
// 事件 payload。
type TransitionOptions struct {
	Payload any
	Error   *string
	Detail  any
}

// Transition 原子执行状态迁移（对齐 transition 的 atomic 包裹，
// appeal_repository.py:134-136）。
func (s *Store) Transition(ctx context.Context, appeal Row, status, actor, action string, opts TransitionOptions) (Row, error) {
	var out Row
	err := s.Atomic(ctx, func(s *Store) error {
		r, e := s.transitionTx(ctx, appeal, status, actor, action, opts)
		out = r
		return e
	})
	return out, err
}

// TransitionTx 在调用方事务内执行状态迁移（对齐 _transition，
// appeal_repository.py:113-132）：revision+1；终态清 active_key；随后
// 重读申诉行并写审计事件。
func (s *Store) TransitionTx(ctx context.Context, appeal Row, status, actor, action string, opts TransitionOptions) (Row, error) {
	return s.transitionTx(ctx, appeal, status, actor, action, opts)
}

func (s *Store) transitionTx(ctx context.Context, appeal Row, status, actor, action string, opts TransitionOptions) (Row, error) {
	oldStatus := rowString(appeal, "status")
	appealID := rowString(appeal, "id")

	// 终态清 active_key（appeal_repository.py:118-122）。
	var activeKey any
	if !IsTerminal(status) {
		activeKey = ActiveKey(rowInt(appeal, "chat_id"), rowInt(appeal, "user_id"))
	}
	payloadAny := opts.Payload
	if payloadAny == nil {
		payloadAny = appeal["payload"]
	}
	var errorArg any
	if opts.Error != nil {
		errorArg = *opts.Error
	}
	now := db.MysqlDatetime6(db.NowUTC())
	if _, err := s.Exec(ctx, `UPDATE moderation_appeals SET status=?,revision=revision+1,active_key=?,
            payload=?,error=?,retry_at=?,updated_at=? WHERE id=?`,
		status, activeKey, db.EncodeJSONMust(payloadAny), errorArg, now, now, appealID); err != nil {
		return nil, err
	}
	result, err := s.Get(ctx, appealID)
	if err != nil {
		return nil, err
	}
	if err := s.Event(ctx, result, actor, action, oldStatus, opts.Detail); err != nil {
		return nil, err
	}
	return result, nil
}

// RejectedRecently 同 case 24h 内是否被拒（appeal_repository.py:138-143）；
// 配额冷却判定之一（appeal_inbox.py:36-37）。
func (s *Store) RejectedRecently(ctx context.Context, caseRow Row) (bool, error) {
	row, err := s.One(ctx,
		"SELECT id FROM moderation_appeals WHERE case_id=? AND status='rejected'"+
			" AND updated_at>? LIMIT 1",
		rowString(caseRow, "id"), db.MysqlDatetime6(db.NowUTC().Add(-24*time.Hour)))
	if err != nil || row == nil {
		return false, err
	}
	return true, nil
}

// SubmissionCount 用户 24h 内提交次数（appeal_repository.py:145-152；
// appeal_inbox.py:36-37 与 :144 的 3 次/24h 配额）。
func (s *Store) SubmissionCount(ctx context.Context, userID int64) (int64, error) {
	row, err := s.One(ctx,
		"SELECT COUNT(*) AS total FROM moderation_appeal_events e"+
			" JOIN moderation_appeals a ON a.id=e.appeal_id WHERE a.user_id=?"+
			" AND e.action='submitted' AND e.created_at>?",
		userID, db.MysqlDatetime6(db.NowUTC().Add(-24*time.Hour)))
	if err != nil || row == nil {
		return 0, err
	}
	return rowInt(row, "total"), nil
}

// Notices 取申诉通知行（appeal_repository.py:154-157）。
func (s *Store) Notices(ctx context.Context, appealID string) ([]Row, error) {
	return s.All(ctx,
		"SELECT * FROM moderation_appeal_notifications WHERE appeal_id=?", appealID)
}

// SaveNotice upsert 申诉通知（appeal_repository.py:159-167）：
// 成功同步时清 error、attempts 归零。
func (s *Store) SaveNotice(ctx context.Context, appeal Row, targetID int64, messageID int64, kind string) error {
	_, err := s.Exec(ctx, "INSERT INTO moderation_appeal_notifications"+
		" (appeal_id,target_id,kind,message_id,synced_revision,retry_at)"+
		" VALUES (?,?,?,?,?,?) ON DUPLICATE KEY UPDATE"+
		" message_id=VALUES(message_id),synced_revision=VALUES(synced_revision),"+
		" error=NULL,attempts=0,retry_at=VALUES(retry_at)",
		rowString(appeal, "id"), targetID, kind, messageID,
		rowInt(appeal, "revision"), db.MysqlDatetime6(db.NowUTC()))
	return err
}

// BindUserCard 绑定用户卡片消息（appeal_repository.py:169-177）：
// 先把复用同一消息的旧申诉通知翻页让位（kind='user' 且 message_id 相同、
// 属于其他申诉的行置 NULL），再保存本次绑定。
func (s *Store) BindUserCard(ctx context.Context, appeal Row, messageID int64) error {
	if _, err := s.Exec(ctx, "UPDATE moderation_appeal_notifications n"+
		" JOIN moderation_appeals a ON a.id=n.appeal_id"+
		" SET n.message_id=NULL,n.synced_revision=a.revision"+
		" WHERE n.target_id=? AND n.kind='user' AND n.message_id=? AND n.appeal_id<>?",
		rowInt(appeal, "user_id"), messageID, rowString(appeal, "id")); err != nil {
		return err
	}
	return s.SaveNotice(ctx, appeal, rowInt(appeal, "user_id"), messageID, "user")
}

// InputMessage 把卡片消息 ID 写入输入草稿 payload.ui_message_id
// （appeal_repository.py:179-184，JSON_SET）。
func (s *Store) InputMessage(ctx context.Context, actorID int64, token string, messageID int64) error {
	_, err := s.Exec(ctx,
		"UPDATE moderation_appeal_inputs SET payload=JSON_SET(payload,'$.ui_message_id',?)"+
			" WHERE actor_id=? AND token=?",
		messageID, actorID, token)
	return err
}

// Publish 发布申诉通知（appeal_repository.py:186-210）：
// 目标 = 用户 + 审核人（去重保序）；不在目标集的旧通知置 kind='retired'；
// 其余 upsert；最后 published_revision=revision。整体在事务内。
func (s *Store) Publish(ctx context.Context, appeal Row, reviewers []int64) error {
	appealID := rowString(appeal, "id")
	userID := rowInt(appeal, "user_id")
	targets := []int64{userID}
	seen := map[int64]bool{userID: true}
	for _, r := range reviewers {
		if !seen[r] {
			seen[r] = true
			targets = append(targets, r)
		}
	}
	return s.Atomic(ctx, func(s *Store) error {
		notices, err := s.Notices(ctx, appealID)
		if err != nil {
			return err
		}
		for _, notice := range notices {
			if tid := rowInt(notice, "target_id"); !seen[tid] {
				if _, err := s.Exec(ctx,
					"UPDATE moderation_appeal_notifications SET kind='retired'"+
						" WHERE appeal_id=? AND target_id=?",
					appealID, tid); err != nil {
					return err
				}
			}
		}
		for _, target := range targets {
			kind := "reviewer"
			if target == userID {
				kind = "user"
			}
			if _, err := s.Exec(ctx, "INSERT INTO moderation_appeal_notifications"+
				" (appeal_id,target_id,kind,retry_at) VALUES (?,?,?,?)"+
				" ON DUPLICATE KEY UPDATE kind=VALUES(kind)",
				appealID, target, kind, db.MysqlDatetime6(db.NowUTC())); err != nil {
				return err
			}
		}
		_, err = s.Exec(ctx,
			"UPDATE moderation_appeals SET published_revision=revision WHERE id=?", appealID)
		return err
	})
}

// QueueRequest 入队审核请求（appeal_repository.py:212-230）：INSERT IGNORE，
// id 为调用方派生的幂等键（见 QuickReviewRequestID）。
func (s *Store) QueueRequest(ctx context.Context, appealID string, actorID int64, action string, revision int64, originChatID, originMessageID int64, reason, requestID string) error {
	_, err := s.Exec(ctx, "INSERT IGNORE INTO moderation_appeal_requests"+
		" (id,appeal_id,actor_id,action,revision,origin_chat_id,origin_message_id,payload,retry_at,created_at)"+
		" VALUES (?,?,?,?,?,?,?,?,?,?)",
		requestID, appealID, actorID, action, revision, originChatID, originMessageID,
		db.EncodeJSONMust(map[string]any{"reason": reason}),
		db.MysqlDatetime6(db.NowUTC()), db.MysqlDatetime6(db.NowUTC()))
	return err
}

// FinishRequest 结束审核请求（appeal_repository.py:232-236）：
// error 非 nil → status='failed'，否则 'done'。
func (s *Store) FinishRequest(ctx context.Context, request Row, errStr *string, payload any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	status := "done"
	if errStr != nil {
		status = "failed"
	}
	_, err := s.Exec(ctx,
		"UPDATE moderation_appeal_requests SET status=?,error=?,payload=? WHERE id=?",
		status, errStr, db.EncodeJSONMust(payload), rowString(request, "id"))
	return err
}

// Events 取申诉审计（appeal_repository.py:238-242）：最新 30 条。
func (s *Store) Events(ctx context.Context, appealID string) ([]Row, error) {
	return s.All(ctx,
		"SELECT * FROM moderation_appeal_events WHERE appeal_id=? ORDER BY id DESC LIMIT 30", appealID)
}

// ExecutionError 记录执行错误（appeal_repository.py:244-257）：
// retry_at = now+30s；错误文案变化时 revision+1 并补 execution_failed 审计
// （同一错误不重复填满审计）。
func (s *Store) ExecutionError(ctx context.Context, appeal Row, errStr string) error {
	return s.Atomic(ctx, func(s *Store) error {
		if _, err := s.Exec(ctx,
			"UPDATE moderation_appeals SET error=?,retry_at=? WHERE id=?",
			errStr, db.MysqlDatetime6(db.NowUTC().Add(30*time.Second)),
			rowString(appeal, "id")); err != nil {
			return err
		}
		if prev, _ := appeal["error"].(string); prev != errStr {
			if _, err := s.Exec(ctx,
				"UPDATE moderation_appeals SET revision=revision+1 WHERE id=?",
				rowString(appeal, "id")); err != nil {
				return err
			}
			return s.Event(ctx, appeal, "system", "execution_failed",
				rowString(appeal, "status"), map[string]any{"error": errStr})
		}
		return nil
	})
}

// FinishApproval 三表事务（appeal_repository.py:259-299）：按 decision
// 处理 blacklist/whitelist、更新 moderation_cases 终态，并把申诉迁移到
// approved。返回迁移后的申诉行。
func (s *Store) FinishApproval(ctx context.Context, appeal Row, caseRow Row) (Row, error) {
	payloadMap, _ := appeal["payload"].(map[string]any)
	decision, _ := payloadMap["decision"].(map[string]any)
	actorID := rowInt(decision, "actor_id")
	action := rowString(decision, "action")
	actor := fmt.Sprintf("telegram:%d", actorID)
	appealID := rowString(appeal, "id")

	// payload = dict(case["payload"])；review_history 追加本次决定
	//（appeal_repository.py:262-272）。
	casePayload, _ := caseRow["payload"].(map[string]any)
	newPayload := make(map[string]any, len(casePayload)+1)
	for k, v := range casePayload {
		newPayload[k] = v
	}
	history, _ := newPayload["review_history"].([]any)
	history = append(history, map[string]any{
		"action":    action,
		"actor":     actor,
		"appeal_id": appealID,
		"reason":    rowString(decision, "reason"),
		"at":        db.PythonISOFormat(db.NowUTC()),
	})
	newPayload["review_history"] = history

	var out Row
	err := s.Atomic(ctx, func(s *Store) error {
		if action == "global" {
			// decision.get("blacklist_case_id") or ""（appeal_repository.py:275-278）。
			blacklistCaseID := ""
			if v, ok := decision["blacklist_case_id"]; ok && v != nil {
				blacklistCaseID = fmt.Sprintf("%v", v)
			}
			if _, err := s.Exec(ctx,
				"DELETE FROM moderation_blacklist WHERE user_id=? AND case_id=?",
				rowInt(appeal, "user_id"), blacklistCaseID); err != nil {
				return err
			}
		}
		if action == "allow" {
			if err := s.SetWhitelist(ctx, rowInt(appeal, "chat_id"),
				rowInt(appeal, "user_id"), actorID, actor); err != nil {
				return err
			}
		}
		caseStatus := "unbanned"
		if action == "allow" {
			caseStatus = "allowed"
		}
		now := db.MysqlDatetime6(db.NowUTC())
		if _, err := s.Exec(ctx, "UPDATE moderation_cases SET status=?,payload=?,reviewer_id=?,reviewer_actor=?,"+
			" reviewed_at=?,error=NULL,updated_at=? WHERE id=?",
			caseStatus, db.EncodeJSONMust(newPayload), actorID, actor, now, now,
			rowString(caseRow, "id")); err != nil {
			return err
		}
		// updated = {**appeal["payload"], "blacklisted": False}。
		updated := make(map[string]any, len(payloadMap)+1)
		for k, v := range payloadMap {
			updated[k] = v
		}
		updated["blacklisted"] = false
		r, err := s.transitionTx(ctx, appeal, "approved", actor, "approved",
			TransitionOptions{Payload: updated, Detail: decision})
		out = r
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
