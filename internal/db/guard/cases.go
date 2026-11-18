// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package guard

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	db "github.com/Sakura520222/Sakura-Bot/internal/db"
)

var (
	// ErrGlobalBlacklistConflict 尝试为全局黑名单成员加群白名单时拒绝（repository.py:213）。
	ErrGlobalBlacklistConflict = errors.New("global_blacklist_conflict")
	// ErrInvalidReviewAction 未知审核动作（repository.py:245）。
	ErrInvalidReviewAction = errors.New("invalid review action")
	// ErrGuestSourceDecisionChanged 访客联动父案件的全局拉黑意图已变更或撤销（repository.py:202）。
	ErrGuestSourceDecisionChanged = errors.New("guest_source_decision_changed")
	// ErrGuestSourceBindingChanged 访客联动父子案件的来源身份绑定不一致（repository.py:209）。
	ErrGuestSourceBindingChanged = errors.New("guest_source_binding_changed")
	// ErrInvalidGuardRecordTable 非法 Guard 记录表名。
	ErrInvalidGuardRecordTable = errors.New("guard: invalid record table")
)

// ActiveVerifications 进行中的入群验证会话状态集（对齐 repository.py:18-27 ACTIVE_VERIFICATIONS）。
var ActiveVerifications = []string{
	"checking",
	"preparing",
	"waiting",
	"passing",
	"kicking",
	"cancelling",
	"admin_pending",
	"held",
}

// IsActiveVerificationStatus 判断入群验证状态是否仍处于活跃处理期。
func IsActiveVerificationStatus(status string) bool {
	for _, s := range ActiveVerifications {
		if s == status {
			return true
		}
	}
	return false
}

// CaseInsert 创建 moderation_cases 的强类型输入（对齐 repository.py:91-110 create_case）。
type CaseInsert struct {
	ID            string
	EventKey      string
	ChatID        int64
	UserID        int64
	Action        string
	Status        string
	Payload       map[string]any
	ReviewerID    *int64
	ReviewerActor *string
	ReviewedAt    *time.Time
}

// GuestParentRef 访客消息联动全局拉黑时的父案件校验引用（repository.py:194-210）。
type GuestParentRef struct {
	ParentCaseID string
	OperationID  string
}

// VerificationSessionSave 入群验证会话 upsert 参数（repository.py:332-351 save_verification）。
type VerificationSessionSave struct {
	ID        string
	ChatID    int64
	UserID    int64
	Status    string
	Payload   map[string]any
	MessageID *int64
	Attempts  int
	ExpiresAt time.Time
	Error     *string
}

// RecordFilter WebUI /api/guard/records/{table} 与 /guard_logs 过滤分页参数（repository.py:487-544）。
type RecordFilter struct {
	ChatID   *int64
	UserID   *int64
	Status   string
	Limit    int
	Offset   int
	Sanitize bool // 为 true 且查询 verifications 时隐藏验证码答案与原始权限快照（对齐 guard.py:537-553）
}

// RecordPage Guard 记录分页返回。
type RecordPage struct {
	Rows  []Row `json:"rows"`
	Total int64 `json:"total"`
}

// BlacklistTask 显式人工拉黑决定时写入访客消息联动拉黑意图（移植 guest_messages.py:46-75）。
func BlacklistTask(payload map[string]any, reviewerID *int64, actor string, retry bool) map[string]any {
	if payload == nil {
		return map[string]any{}
	}
	if _, hasGuest := payload["guest_actor"]; !hasGuest {
		return payload
	}
	validActor := actor == "webui:admin" ||
		(reviewerID != nil && *reviewerID > 0 && actor == fmt.Sprintf("telegram:%d", *reviewerID))
	if !validActor {
		return payload
	}

	copied := shallowCopyMap(payload)
	prev, _ := copied["guest_ban"].(map[string]any)
	if prev != nil && fmt.Sprint(prev["action"]) == "blacklist" {
		if !retry || fmt.Sprint(prev["status"]) != "skipped" {
			return payload
		}
		hist, _ := copied["guest_ban_history"].([]any)
		newHist := make([]any, 0, len(hist)+1)
		newHist = append(newHist, hist...)
		newHist = append(newHist, prev)
		copied["guest_ban_history"] = newHist
	}

	var revVal any
	if reviewerID != nil {
		revVal = *reviewerID
	}
	copied["guest_ban"] = map[string]any{
		"action":       "blacklist",
		"operation_id": uuid4Hex(),
		"status":       "pending",
		"attempts":     0,
		"actor":        actor,
		"reviewer_id":  revVal,
		"requested_at": db.PythonISOFormat(db.NowUTC()),
	}
	copied["guest_ban_pending"] = true
	copied["report_notice_dirty"] = true
	return copied
}

// CurrentBlacklistTask 核验父案件当前是否仍持有指定 operation_id 的联动拉黑意图（guest_messages.py:77-85）。
func CurrentBlacklistTask(parent Row, operationID string) bool {
	if parent == nil || rowString(parent, "status") != "blacklisted" {
		return false
	}
	payload, _ := parent["payload"].(map[string]any)
	if payload == nil {
		return false
	}
	pending, _ := payload["guest_ban_pending"].(bool)
	if !pending {
		return false
	}
	guestBan, _ := payload["guest_ban"].(map[string]any)
	if guestBan == nil {
		return false
	}
	return fmt.Sprint(guestBan["action"]) == "blacklist" &&
		fmt.Sprint(guestBan["operation_id"]) == operationID
}

// CaseForEvent 按幂等事件键查询 moderation_cases（repository.py:63-64）。
func (s *Store) CaseForEvent(ctx context.Context, eventKey string) (Row, error) {
	return s.One(ctx, "SELECT * FROM moderation_cases WHERE event_key=?", eventKey)
}

// LaterMute 检查同群同用户在当前禁言案件之后是否已有更新的有效禁言记录（repository.py:66-80）。
func (s *Store) LaterMute(ctx context.Context, caseRow Row) (Row, error) {
	rows, err := s.All(ctx, `
		SELECT * FROM moderation_cases
		WHERE chat_id=? AND user_id=? AND id<>? AND action='mute'
		  AND status IN ('executing','done','reviewing_allow','reviewing_unban','reviewing_ban','reviewing_blacklist')
		ORDER BY created_at DESC LIMIT 50
	`, rowInt(caseRow, "chat_id"), rowInt(caseRow, "user_id"), rowString(caseRow, "id"))
	if err != nil {
		return nil, err
	}

	payload, _ := caseRow["payload"].(map[string]any)
	start, _ := payload["restriction_started_at"].(string)
	hasOwnStart := start != ""
	if !hasOwnStart {
		start = rowTimeISO(caseRow, "created_at")
	}

	for _, r := range rows {
		otherPayload, _ := r["payload"].(map[string]any)
		other, _ := otherPayload["restriction_started_at"].(string)
		if other == "" {
			other = rowTimeISO(r, "created_at")
		}
		if other > start || (!hasOwnStart && other == start) {
			return r, nil
		}
	}
	return nil, nil
}

// ActiveBan 查询同群同用户是否存在其他进行中的封禁案件（repository.py:82-90）。
func (s *Store) ActiveBan(ctx context.Context, chatID, userID int64, excludeCaseID string) (Row, error) {
	return s.One(ctx, `
		SELECT * FROM moderation_cases
		WHERE chat_id=? AND user_id=? AND id<>?
		  AND (action='ban'
		    OR JSON_UNQUOTE(JSON_EXTRACT(payload,'$.review_ban_confirmed'))='true'
		    OR status IN ('reviewing_ban','reviewing_blacklist'))
		  AND status IN ('executing','pending','reviewing_allow','reviewing_blacklist','reviewing_unban','reviewing_ban')
		ORDER BY created_at DESC LIMIT 1
	`, chatID, userID, excludeCaseID)
}

// CreateCase 插入新群管案件（repository.py:91-110）。
func (s *Store) CreateCase(ctx context.Context, in CaseInsert) error {
	id := in.ID
	if id == "" {
		id = uuid4Hex()
	}
	payload := in.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	var revIDArg, revActorArg, revAtArg any
	if in.ReviewerID != nil {
		revIDArg = *in.ReviewerID
	}
	if in.ReviewerActor != nil && *in.ReviewerActor != "" {
		revActorArg = *in.ReviewerActor
	}
	if in.ReviewedAt != nil {
		revAtArg = db.MysqlDatetime(in.ReviewedAt.UTC())
	}
	now := db.MysqlDatetime(db.NowUTC())
	_, err := s.Exec(ctx, `
		INSERT INTO moderation_cases
		(id,event_key,chat_id,user_id,action,status,payload,reviewer_id,reviewer_actor,reviewed_at,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
	`, id, in.EventKey, in.ChatID, in.UserID, in.Action, in.Status,
		db.EncodeJSONMust(payload), revIDArg, revActorArg, revAtArg, now, now)
	return err
}

// CreateCaseRow 以字典 Row 形态插入案件（供 FinishReview guest_parent 分支复用）。
func (s *Store) CreateCaseRow(ctx context.Context, caseRow Row) error {
	var revID *int64
	if v := rowNullableInt(caseRow, "reviewer_id"); v != nil {
		revID = v
	}
	var revActor *string
	if actor := rowString(caseRow, "reviewer_actor"); actor != "" {
		revActor = &actor
	}
	var revAt *time.Time
	if t, ok := rowTime(caseRow, "reviewed_at"); ok {
		revAt = &t
	}
	payload, _ := caseRow["payload"].(map[string]any)
	return s.CreateCase(ctx, CaseInsert{
		ID:            rowString(caseRow, "id"),
		EventKey:      rowString(caseRow, "event_key"),
		ChatID:        rowInt(caseRow, "chat_id"),
		UserID:        rowInt(caseRow, "user_id"),
		Action:        rowString(caseRow, "action"),
		Status:        rowString(caseRow, "status"),
		Payload:       payload,
		ReviewerID:    revID,
		ReviewerActor: revActor,
		ReviewedAt:    revAt,
	})
}

// UpdateCase 更新案件状态、错误与可选 payload（repository.py:112-123）。
// payload 传 nil 时保留数据库原 payload 列不变。
func (s *Store) UpdateCase(ctx context.Context, caseID, status string, errStr *string, payload any) error {
	var errArg any
	if errStr != nil {
		errArg = *errStr
	}
	now := db.MysqlDatetime(db.NowUTC())
	if payload == nil {
		_, err := s.Exec(ctx,
			"UPDATE moderation_cases SET status=?,error=?,updated_at=? WHERE id=?",
			status, errArg, now, caseID)
		return err
	}
	_, err := s.Exec(ctx,
		"UPDATE moderation_cases SET status=?,error=?,payload=?,updated_at=? WHERE id=?",
		status, errArg, db.EncodeJSONMust(payload), now, caseID)
	return err
}

// TransitionCase 带前置状态断言的案件状态流转。
func (s *Store) TransitionCase(ctx context.Context, caseID, fromStatus, toStatus string, errStr *string, payload any) (bool, error) {
	var errArg any
	if errStr != nil {
		errArg = *errStr
	}
	now := db.MysqlDatetime(db.NowUTC())
	if payload == nil {
		n, err := s.Exec(ctx,
			"UPDATE moderation_cases SET status=?,error=?,updated_at=? WHERE id=? AND status=?",
			toStatus, errArg, now, caseID, fromStatus)
		return n > 0, err
	}
	n, err := s.Exec(ctx,
		"UPDATE moderation_cases SET status=?,error=?,payload=?,updated_at=? WHERE id=? AND status=?",
		toStatus, errArg, db.EncodeJSONMust(payload), now, caseID, fromStatus)
	return n > 0, err
}

// ConvertReportCase 将未被人工处理的 pending report 案件转换为 AI 自动执行动作（ai_report_repository.py:76-84）。
func (s *Store) ConvertReportCase(ctx context.Context, caseID, action string, payload any) (bool, error) {
	now := db.MysqlDatetime(db.NowUTC())
	n, err := s.Exec(ctx, `
		UPDATE moderation_cases
		SET action=?,status='executing',payload=?,
		    reviewer_id=NULL,reviewer_actor='system:ai_report',reviewed_at=?,error=NULL,updated_at=?
		WHERE id=? AND action='report' AND status='pending'
		  AND reviewer_actor IS NULL AND reviewer_id IS NULL
	`, action, db.EncodeJSONMust(payload), now, now, caseID)
	return n > 0, err
}

// RestoreReportCase 将 AI 转换后无法继续自动执行的案件回退为人工待审举报（ai_report_repository.py:86-92）。
func (s *Store) RestoreReportCase(ctx context.Context, caseID string, payload any) (bool, error) {
	now := db.MysqlDatetime(db.NowUTC())
	n, err := s.Exec(ctx, `
		UPDATE moderation_cases
		SET action='report',status='pending',payload=?,
		    reviewer_id=NULL,reviewer_actor=NULL,reviewed_at=NULL,error=NULL,updated_at=?
		WHERE id=? AND status='executing' AND reviewer_actor='system:ai_report'
	`, db.EncodeJSONMust(payload), now, caseID)
	return n > 0, err
}

// ClaimReview 抢占人工审核权并将案件置为 reviewing_<action>（repository.py:161-176）。
func (s *Store) ClaimReview(ctx context.Context, caseID, action string, reviewerID *int64, reviewerActor string) (bool, error) {
	actor := reviewerActor
	if actor == "" && reviewerID != nil {
		actor = fmt.Sprintf("telegram:%d", *reviewerID)
	}
	var revIDArg any
	if reviewerID != nil {
		revIDArg = *reviewerID
	}
	now := db.MysqlDatetime(db.NowUTC())
	n, err := s.Exec(ctx, `
		UPDATE moderation_cases
		SET status=?,reviewer_id=?,reviewer_actor=?,reviewed_at=?,error=NULL,updated_at=?
		WHERE id=?
		  AND ((status='pending' AND action IN ('ban','mute','warn','report'))
		    OR (status='done' AND action IN ('mute','warn'))
		    OR (?='kick' AND action='report' AND status IN ('allowed','blacklisted','unbanned','unmuted','ignored')))
	`, "reviewing_"+action, revIDArg, actor, now, now, caseID, action)
	return n > 0, err
}

// FinishReview 在 Telegram 侧执行成功后于同一数据库事务内提交黑白名单与案件终态（repository.py:178-272）。
func (s *Store) FinishReview(ctx context.Context, caseRow Row, action string, guestParent *GuestParentRef) error {
	origPayload, _ := caseRow["payload"].(map[string]any)
	payload := shallowCopyMap(origPayload)

	reviewerID := rowNullableInt(caseRow, "reviewer_id")
	reviewerActor := rowString(caseRow, "reviewer_actor")
	if reviewerActor == "" && reviewerID != nil {
		reviewerActor = fmt.Sprintf("telegram:%d", *reviewerID)
	}

	hist, _ := payload["review_history"].([]any)
	newHist := make([]any, 0, len(hist)+1)
	newHist = append(newHist, hist...)
	var actorVal any
	if rawActor := rowString(caseRow, "reviewer_actor"); rawActor != "" {
		actorVal = rawActor
	} else if reviewerActor != "" {
		actorVal = reviewerActor
	}
	newHist = append(newHist, map[string]any{
		"action": action,
		"actor":  actorVal,
		"at":     db.PythonISOFormat(db.NowUTC()),
	})
	payload["review_history"] = newHist

	if action == "blacklist" {
		payload = BlacklistTask(payload, reviewerID, reviewerActor, false)
		payload["learning_enforcement_pending"] = true
	}

	chatID := rowInt(caseRow, "chat_id")
	userID := rowInt(caseRow, "user_id")
	caseID := rowString(caseRow, "id")
	caseAction := rowString(caseRow, "action")

	return s.Atomic(ctx, func(s *Store) error {
		if guestParent != nil {
			parent, err := s.One(ctx,
				"SELECT * FROM moderation_cases WHERE id=? FOR UPDATE", guestParent.ParentCaseID)
			if err != nil {
				return err
			}
			parentBlack, err := s.Blacklist(ctx, rowInt(parent, "user_id"))
			if err != nil {
				return err
			}
			if !CurrentBlacklistTask(parent, guestParent.OperationID) || parentBlack == nil {
				return ErrGuestSourceDecisionChanged
			}
			parentPayload, _ := parent["payload"].(map[string]any)
			source, _ := parentPayload["guest_actor"].(map[string]any)
			guestSrc, _ := payload["guest_source"].(map[string]any)
			if !reflect.DeepEqual(source, guestSrc) ||
				rowInt(Row(source), "user_id") != userID ||
				rowInt(parent, "chat_id") != chatID {
				return ErrGuestSourceBindingChanged
			}
			if err := s.CreateCaseRow(ctx, caseRow); err != nil {
				return err
			}
		}

		switch action {
		case "allow":
			black, err := s.Blacklist(ctx, userID)
			if err != nil {
				return err
			}
			if black != nil {
				return ErrGlobalBlacklistConflict
			}
			if err := s.AddWhitelist(ctx, chatID, userID, reviewerID, reviewerActor); err != nil {
				return err
			}
		case "blacklist":
			existingBlack, err := s.Blacklist(ctx, userID)
			if err != nil {
				return err
			}
			if guestParent != nil && existingBlack != nil {
				if err := s.TouchBlacklist(ctx, userID); err != nil {
					return err
				}
			} else {
				firstSeen, ok := rowTime(caseRow, "created_at")
				if !ok {
					firstSeen = db.NowUTC()
				}
				if err := s.UpsertBlacklist(ctx, BlacklistUpsert{
					UserID:        userID,
					SourceChatID:  chatID,
					CaseID:        caseID,
					Payload:       payload,
					ReviewerID:    reviewerID,
					ReviewerActor: reviewerActor,
					FirstSeenAt:   firstSeen,
				}); err != nil {
					return err
				}
				if err := s.RemoveWhitelist(ctx, chatID, userID); err != nil {
					return err
				}
			}
		case "unban", "ban", "kick":
		default:
			return ErrInvalidReviewAction
		}

		banConfirmed, _ := payload["review_ban_confirmed"].(bool)
		unbanStatus := "ignored"
		if caseAction == "ban" || banConfirmed {
			unbanStatus = "unbanned"
		} else if caseAction == "mute" {
			unbanStatus = "unmuted"
		}

		if action == "ban" {
			payload["review_ban_confirmed"] = true
			payload["review_generation"] = rowInt(Row(payload), "review_generation") + 1
		}

		nextStatus := "done"
		if guestParent == nil {
			switch action {
			case "allow":
				nextStatus = "allowed"
			case "blacklist":
				nextStatus = "blacklisted"
			case "unban":
				nextStatus = unbanStatus
			case "ban":
				nextStatus = "pending"
			case "kick":
				nextStatus = "kicked"
			}
		}
		return s.UpdateCase(ctx, caseID, nextStatus, nil, payload)
	})
}

// VerificationSession 按 (chat_id, user_id) 读取主 Bot 入群验证会话（repository.py:274-278）。
func (s *Store) VerificationSession(ctx context.Context, chatID, userID int64) (Row, error) {
	return s.One(ctx,
		"SELECT * FROM moderation_verification_sessions WHERE chat_id=? AND user_id=?",
		chatID, userID)
}

// VerificationSessionByID 按会话 ID 读取入群验证会话（repository.py:280-283）。
func (s *Store) VerificationSessionByID(ctx context.Context, sessionID string) (Row, error) {
	return s.One(ctx,
		"SELECT * FROM moderation_verification_sessions WHERE id=?", sessionID)
}

// SaveVerificationSession 插入或更新入群验证会话（repository.py:332-351）。
func (s *Store) SaveVerificationSession(ctx context.Context, in VerificationSessionSave) error {
	var msgArg, errArg any
	if in.MessageID != nil {
		msgArg = *in.MessageID
	}
	if in.Error != nil {
		errArg = *in.Error
	}
	payload := in.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	_, err := s.Exec(ctx, `
		INSERT INTO moderation_verification_sessions
		(id,chat_id,user_id,status,payload,message_id,attempts,expires_at,error,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON DUPLICATE KEY UPDATE
			id=VALUES(id),status=VALUES(status),payload=VALUES(payload),
			message_id=VALUES(message_id),attempts=VALUES(attempts),
			expires_at=VALUES(expires_at),error=VALUES(error),updated_at=VALUES(updated_at)
	`, in.ID, in.ChatID, in.UserID, in.Status, db.EncodeJSONMust(payload),
		msgArg, in.Attempts, db.MysqlDatetime(in.ExpiresAt.UTC()), errArg, db.MysqlDatetime(db.NowUTC()))
	return err
}

// PreviousVerificationEnforcement 检查用户近 30 天是否曾被封禁或拉黑（repository.py:295-304）。
func (s *Store) PreviousVerificationEnforcement(ctx context.Context, userID int64) (bool, error) {
	row, err := s.One(ctx, `
		SELECT 1 FROM moderation_cases
		WHERE user_id=?
		  AND created_at >= UTC_TIMESTAMP() - INTERVAL 30 DAY
		  AND (status='blacklisted' OR (action='ban' AND status IN ('pending','done')))
		LIMIT 1
	`, userID)
	return row != nil, err
}

// SimilarVerificationNames 统计 24h 内同群相同昵称聚类的其他待验证人数（上限 3，repository.py:306-313）。
func (s *Store) SimilarVerificationNames(ctx context.Context, chatID, userID int64, cluster string) (int, error) {
	rows, err := s.All(ctx, `
		SELECT user_id FROM moderation_verification_sessions
		WHERE chat_id=? AND user_id<>?
		  AND updated_at >= UTC_TIMESTAMP() - INTERVAL 1 DAY
		  AND JSON_UNQUOTE(JSON_EXTRACT(payload,'$.profile.name_cluster'))=?
		LIMIT 3
	`, chatID, userID, cluster)
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

// GetCase 免锁读取单条群管案件（repository.py:389-391）。
func (r *Repository) GetCase(ctx context.Context, caseID string) (Row, error) {
	var out Row
	err := r.Reader(ctx, func(s *Store) error {
		row, e := s.Case(ctx, caseID)
		out = row
		return e
	})
	return out, err
}

// GetVerificationSession 免锁按会话 ID 读取入群验证会话（repository.py:393-395）。
func (r *Repository) GetVerificationSession(ctx context.Context, sessionID string) (Row, error) {
	var out Row
	err := r.Reader(ctx, func(s *Store) error {
		row, e := s.VerificationSessionByID(ctx, sessionID)
		out = row
		return e
	})
	return out, err
}

// VerificationDiagnostics 汇总指定群近 7 天的入群验证漏斗与验证助手在线状态（repository.py:397-417）。
func (r *Repository) VerificationDiagnostics(ctx context.Context, chatID, botID int64) (map[string]any, error) {
	var (
		assistant Row
		counts    Row
	)
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		assistant, e = s.ActiveAssistant(ctx, botID)
		if e != nil {
			return e
		}
		counts, e = s.One(ctx, `
			SELECT COUNT(*) AS joined,
			COUNT(CASE WHEN JSON_EXTRACT(payload,'$.assistant.started_at') IS NOT NULL THEN 1 END) AS started,
			COUNT(CASE WHEN JSON_LENGTH(JSON_EXTRACT(payload,'$.assistant.receipts')) >= 1 THEN 1 END) AS step_one,
			COUNT(CASE WHEN JSON_LENGTH(JSON_EXTRACT(payload,'$.assistant.receipts')) >= 2 THEN 1 END) AS step_two,
			COUNT(CASE WHEN status='passed' THEN 1 END) AS released,
			COUNT(CASE WHEN status IN ('failed_banned','failed_kicked','held') THEN 1 END) AS held_or_failed,
			COUNT(CASE WHEN JSON_UNQUOTE(JSON_EXTRACT(payload,'$.failure_intent.reason')) = 'timeout' THEN 1 END) AS expired,
			COUNT(CASE WHEN JSON_EXTRACT(payload,'$.observation.hits') > 0 THEN 1 END) AS observation_hits
			FROM moderation_verification_sessions
			WHERE chat_id=? AND updated_at >= UTC_TIMESTAMP() - INTERVAL 7 DAY
		`, chatID)
		return e
	})
	if err != nil {
		return nil, err
	}

	assistantState := "unconfigured"
	var assistantUsername any
	if assistant != nil {
		assistantState = "ready"
		assistantUsername = assistant["username"]
	} else if botID != 0 {
		assistantState = "offline"
	}
	return map[string]any{
		"assistant":          assistantState,
		"assistant_username": assistantUsername,
		"counts":             counts,
	}, nil
}

// EnforcementHistory 游标分页读取某群的历史封禁/全局拉黑案件（供 AI 样本回填，repository.py:419-428）。
func (r *Repository) EnforcementHistory(ctx context.Context, chatID int64, cursor string, since time.Time) ([]Row, error) {
	var out []Row
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		out, e = s.All(ctx, `
			SELECT c.id, c.user_id FROM moderation_cases c
			LEFT JOIN moderation_blacklist b ON b.case_id=c.id AND b.user_id=c.user_id
			WHERE c.chat_id=? AND c.id>? AND c.updated_at>=?
			  AND ((c.action='ban' AND c.status='pending') OR (c.status='blacklisted' AND b.user_id IS NOT NULL))
			ORDER BY c.id LIMIT 51
		`, chatID, cursor, db.MysqlDatetime(since.UTC()))
		return e
	})
	return out, err
}

// GuardWork 轮询主 Bot Sakura Guard 待恢复案件与待处理入群验证会话（逐字移植 repository.py:546-579）。
func (r *Repository) GuardWork(ctx context.Context, controlPolicy map[string]bool) ([]Row, []Row, error) {
	now := db.NowUTC()
	nowDT := db.MysqlDatetime(now)
	nowSecISO := now.Format("2006-01-02T15:04:05")
	nowISO := db.PythonISOFormat(now)
	policyJSON := "{}"
	if len(controlPolicy) > 0 {
		policyJSON = db.EncodeJSONMust(controlPolicy)
	}

	var cases, sessions []Row
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		cases, e = s.All(ctx, `
			SELECT DISTINCT c.* FROM moderation_cases c
			LEFT JOIN moderation_notifications n ON n.case_id=c.id
			WHERE NOT (c.action='verify_start' AND JSON_EXTRACT(c.payload,'$.notification_replaced_by') IS NOT NULL)
			AND (c.status IN ('executing','reviewing_allow','reviewing_blacklist','reviewing_unban','reviewing_ban','reviewing_kick')
			OR n.case_id IS NULL OR ((n.synced_status IS NULL OR n.synced_status NOT IN ('deleting','removed')) AND (n.synced_status IS NULL OR n.synced_status<>c.status OR n.error IS NOT NULL) AND n.retry_at<=?)
			OR (JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.notification_cleanup_pending'))='true' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.notification_cleanup_retry_at')),'1970-01-01T00:00:00')<=?)
			OR (c.action='report' AND c.status='pending' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.report_controls_version')),'0')<>'2')
			OR (((c.action IN ('ban','mute','warn','report') AND (c.status='pending' OR (c.status='done' AND c.action IN ('mute','warn')) OR (c.action='report' AND c.status IN ('allowed','blacklisted','unbanned','unmuted','ignored')))) OR (c.action='verify_skip' AND c.status='done') OR (JSON_EXTRACT(c.payload,'$.verification_failure') IS NOT NULL AND JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.verification_result'))='held') OR (JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.verification_state')) IN ('waiting','passing','cancelling','held','passed','admin_skipped'))) AND (COALESCE(JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.controls_version')),'0')<>'2' OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.kick_button_enabled')),'true')<>IF(JSON_CONTAINS(?, JSON_OBJECT(CAST(c.chat_id AS CHAR), JSON_EXTRACT('false','$'))), 'false', 'true')))
			OR JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.report_notice_dirty'))='true'
			OR JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.learning_enforcement_pending'))='true'
			OR (JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.guest_ban_pending'))='true' AND COALESCE(NULLIF(JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.guest_ban.retry_at')),'null'),'1970-01-01T00:00:00')<=?)
			OR JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.ban_message_cleanup_dirty'))='true'
			OR (JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.ban_message_cleanup.status'))='pending' AND COALESCE(NULLIF(JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.ban_message_cleanup.retry_at')),'null'),'1970-01-01T00:00:00')<=?)
			OR (JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.report_message_deletion.status'))='pending' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(c.payload,'$.report_message_deletion.retry_at')),'1970-01-01T00:00:00')<=?))
			ORDER BY c.updated_at ASC LIMIT 50
		`, nowDT, nowSecISO, policyJSON, nowISO, nowISO, nowISO)
		if e != nil {
			return e
		}

		sessions, e = s.All(ctx, `
			SELECT * FROM moderation_verification_sessions
			WHERE status IN ('checking','preparing','waiting','passing','kicking','cancelling','admin_pending','held')
			OR (message_id IS NOT NULL AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload,'$.cleanup_pending')),'true')='true')
			OR (status IN ('passed','cancelled','left') AND JSON_UNQUOTE(JSON_EXTRACT(payload,'$.lifecycle'))='true' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload,'$.lifecycle_recorded')),'false')='false')
			ORDER BY CASE WHEN status IN ('checking','preparing','waiting','passing','kicking','cancelling','admin_pending','held') THEN 0 ELSE 1 END, updated_at ASC, expires_at ASC LIMIT 100
		`)
		return e
	})
	if err != nil {
		return nil, nil, err
	}
	return cases, sessions, nil
}

// QueryRecords 通用分页查询 Guard 五大记录视图（cases/whitelist/blacklist/verifications/notifications，
// 对齐 repository.py:487-544 与 WebUI /api/guard/records/{table}）。
func (r *Repository) QueryRecords(ctx context.Context, table string, filter RecordFilter) (*RecordPage, error) {
	querySpec, err := buildGuardRecordQuery(table, filter)
	if err != nil {
		return nil, err
	}

	var (
		totalRow Row
		rows     []Row
	)
	err = r.Reader(ctx, func(s *Store) error {
		var e error
		totalRow, e = s.One(ctx, querySpec.countSQL, querySpec.whereArgs...)
		if e != nil {
			return e
		}
		rows, e = s.All(ctx, querySpec.dataSQL, querySpec.dataArgs...)
		return e
	})
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []Row{}
	}
	if table == "verifications" && filter.Sanitize {
		for _, row := range rows {
			sanitizeVerificationRecordRow(row)
		}
	}
	return &RecordPage{
		Rows:  rows,
		Total: rowInt(totalRow, "total"),
	}, nil
}

// ListCases 分页筛选群管案件（供 /guard_logs 与 WebUI 案件列表直接调用）。
func (r *Repository) ListCases(ctx context.Context, filter RecordFilter) (*RecordPage, error) {
	return r.QueryRecords(ctx, "cases", filter)
}

type guardRecordQuerySpec struct {
	countSQL  string
	dataSQL   string
	whereArgs []any
	dataArgs  []any
}

func buildGuardRecordQuery(table string, filter RecordFilter) (guardRecordQuerySpec, error) {
	tables := map[string]string{
		"cases":         "moderation_cases",
		"whitelist":     "moderation_whitelist",
		"blacklist":     "moderation_blacklist",
		"verifications": "moderation_verification_sessions",
		"notifications": "moderation_notifications",
	}
	tableName, ok := tables[table]
	if !ok {
		return guardRecordQuerySpec{}, ErrInvalidGuardRecordTable
	}

	isNotif := table == "notifications"
	source := tableName
	if isNotif {
		source = tableName + " n JOIN moderation_cases c ON c.id=n.case_id"
	}

	var (
		clauses []string
		values  []any
	)
	if filter.ChatID != nil {
		col := "chat_id"
		if isNotif {
			col = "c.chat_id"
		} else if table == "blacklist" {
			col = "source_chat_id"
		}
		clauses = append(clauses, col+"=?")
		values = append(values, *filter.ChatID)
	}
	if filter.UserID != nil {
		col := "user_id"
		if isNotif {
			col = "c.user_id"
		}
		clauses = append(clauses, col+"=?")
		values = append(values, *filter.UserID)
	}
	if filter.Status != "" && table != "whitelist" {
		col := "status"
		if isNotif {
			if filter.Status == "deleting" || filter.Status == "removed" {
				col = "n.synced_status"
			} else {
				col = "c.status"
			}
		}
		clauses = append(clauses, col+"=?")
		values = append(values, filter.Status)
	}

	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}

	orderCol := "updated_at"
	switch table {
	case "notifications":
		orderCol = "n.retry_at"
	case "blacklist":
		orderCol = "last_seen_at"
	case "cases", "whitelist":
		orderCol = "created_at"
	}

	selectCols := "*"
	if isNotif {
		selectCols = "n.*, c.chat_id, c.user_id"
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	dataArgs := make([]any, 0, len(values)+2)
	dataArgs = append(dataArgs, values...)
	dataArgs = append(dataArgs, limit, offset)

	return guardRecordQuerySpec{
		countSQL:  fmt.Sprintf("SELECT COUNT(*) AS total FROM %s%s", source, where),
		dataSQL:   fmt.Sprintf("SELECT %s FROM %s%s ORDER BY %s DESC LIMIT ? OFFSET ?", selectCols, source, where, orderCol),
		whereArgs: values,
		dataArgs:  dataArgs,
	}, nil
}

func sanitizeVerificationRecordRow(row Row) {
	payload, _ := row["payload"].(map[string]any)
	delete(row, "payload")

	cleanupPending := true
	if payload != nil {
		if v, ok := payload["cleanup_pending"].(bool); ok {
			cleanupPending = v
		}
	}
	status := rowString(row, "status")
	hasMsg := row["message_id"] != nil && rowInt(row, "message_id") != 0
	row["message_cleanup_pending"] = hasMsg && (!IsActiveVerificationStatus(status) || status == "kicking") && cleanupPending

	if payload != nil {
		if assistant, ok := payload["assistant"].(map[string]any); ok && assistant != nil {
			receipts, _ := assistant["receipts"].([]any)
			var obsHits int64
			if obs, ok := payload["observation"].(map[string]any); ok && obs != nil {
				obsHits = rowInt(Row(obs), "hits")
			}
			row["verification"] = map[string]any{
				"state":            assistant["state"],
				"steps":            assistant["steps"],
				"completed_steps":  len(receipts),
				"profile":          payload["profile"],
				"observation_hits": obsHits,
			}
		}
	}
}

func shallowCopyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+4)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func rowNullableInt(row Row, key string) *int64 {
	if row == nil || row[key] == nil {
		return nil
	}
	v := rowInt(row, key)
	return &v
}

func rowTime(row Row, key string) (time.Time, bool) {
	if row == nil || row[key] == nil {
		return time.Time{}, false
	}
	switch v := row[key].(type) {
	case time.Time:
		return v.UTC(), true
	case string:
		for _, layout := range []string{
			"2006-01-02 15:04:05.999999",
			"2006-01-02 15:04:05",
			time.RFC3339Nano,
			"2006-01-02T15:04:05.999999",
			"2006-01-02T15:04:05",
		} {
			if dt, err := time.Parse(layout, v); err == nil {
				return dt.UTC(), true
			}
		}
	}
	return time.Time{}, false
}

func rowTimeISO(row Row, key string) string {
	if t, ok := rowTime(row, key); ok {
		return db.PythonISOFormat(t)
	}
	return rowString(row, key)
}
