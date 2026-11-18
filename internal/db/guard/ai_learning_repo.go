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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	db "github.com/Sakura520222/Sakura-Bot/internal/db"
)

const (
	// AILearningLeaseDuration AI 学习任务默认认领租约时长（6 分钟，覆盖最长 180s 模型推理，ai_learning.py:1008）。
	AILearningLeaseDuration = 6 * time.Minute
	// AILearningMaxAttempts AI 学习任务最大尝试次数（5 次失败后转人工 manual，ai_learning.py:42）。
	AILearningMaxAttempts = 5
)

// AISampleInsert 创建 AI 风控样本的输入参数（ai_learning_repository.py:49-73）。
type AISampleInsert struct {
	ID             string
	IdempotencyKey string
	ChatID         int64
	UserID         int64
	MessageID      *int64
	Source         string
	CaseID         *string
	Label          string
	Status         string
	Payload        map[string]any
	Error          *string
}

// AISampleUpdate 更新 AI 风控样本的可选字段（ai_learning_repository.py:101-116）。
type AISampleUpdate struct {
	Label    *string
	Status   *string
	Error    *string
	ClearErr bool
	Payload  map[string]any
	Source   *string
}

// AISampleFilter WebUI 样本列表筛选参数（ai_learning_repository.py:145-172）。
type AISampleFilter struct {
	ChatID *int64
	Label  string
	Status string
	Source string
	Limit  int
	Offset int
}

// AILearningJobPage WebUI 学习任务列表与全状态统计（ai_learning_repository.py:270-296）。
type AILearningJobPage struct {
	Rows   []Row            `json:"rows"`
	Total  int64            `json:"total"`
	Counts map[string]int64 `json:"counts"`
}

// AIRuleUpsert AI 动态规则创建或支持样本合并参数（ai_learning_repository.py:305-349）。
type AIRuleUpsert struct {
	ID              string
	RuleKey         string
	ChatID          int64
	RuleType        string
	MatchKind       string
	Value           string
	NormalizedValue string
	Action          string
	Status          string
	Confidence      float64
	Model           *string
	Actor           *string
	Explanation     string
	SampleIDs       []string
}

// AIRuleStatusOptions AI 动态规则状态流转选项（ai_learning_repository.py:381-406）。
type AIRuleStatusOptions struct {
	Actor       string
	Error       *string
	Activate    bool
	BumpVersion bool
	ExpiresDays int
}

// ActiveAIRule AIRuleCache 加载的已激活规则快照条目（ai_learning_repository.py:465-481）。
type ActiveAIRule struct {
	ID              string   `json:"id"`
	RuleKey         string   `json:"rule_key"`
	ChatID          int64    `json:"chat_id"`
	RuleType        string   `json:"rule_type"`
	MatchKind       string   `json:"match_kind"`
	Value           string   `json:"value"`
	NormalizedValue string   `json:"normalized_value"`
	Action          string   `json:"action"`
	Confidence      float64  `json:"confidence"`
	Version         int      `json:"version"`
	SharedChats     []int64  `json:"shared_chats"`
	SampleIDs       []string `json:"sample_ids"`
}

// AIRuleFeedbackInsert AI 规则反馈审计写入参数（ai_learning_repository.py:583-613）。
type AIRuleFeedbackInsert struct {
	RuleID   *string
	SampleID *string
	CaseID   *string
	ChatID   int64
	UserID   int64
	Kind     string
	Polarity string
	Actor    string
	Payload  map[string]any
}

// SampleKey 计算原消息级样本幂等键（对齐 ai_learning.py:47-48）。
func SampleKey(chatID, messageID int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("ai-sample:%d:%d", chatID, messageID)))
	return hex.EncodeToString(sum[:])
}

// RuleKey 计算群级动态规则幂等键（对齐 ai_learning.py:51-54）。
func RuleKey(chatID int64, ruleType, matchKind, normalizedValue string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s:%s", chatID, ruleType, matchKind, normalizedValue)))
	return hex.EncodeToString(sum[:])
}

// ────────────────────────────── AI Samples ──────────────────────────────

// CreateAISample 幂等写入学习样本（同一 idempotency_key 不覆盖已有行，ai_learning_repository.py:49-73）。
func (s *Store) CreateAISample(ctx context.Context, in AISampleInsert) (Row, bool, error) {
	id := in.ID
	if id == "" {
		id = uuid4Hex()
	}
	label := in.Label
	if label == "" {
		label = "pending"
	}
	status := in.Status
	if status == "" {
		status = "new"
	}
	payload := in.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	var msgArg, caseArg, errArg any
	if in.MessageID != nil {
		msgArg = *in.MessageID
	}
	if in.CaseID != nil && *in.CaseID != "" {
		caseArg = *in.CaseID
	}
	if in.Error != nil && *in.Error != "" {
		errArg = truncateUTF8(*in.Error, 128)
	}
	now := db.MysqlDatetime6(db.NowUTC())

	changed, err := s.Exec(ctx, `
		INSERT INTO moderation_ai_samples
		(id,idempotency_key,chat_id,user_id,message_id,source,case_id,label,status,payload,error,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON DUPLICATE KEY UPDATE id=id
	`, id, in.IdempotencyKey, in.ChatID, in.UserID, msgArg, in.Source, caseArg,
		label, status, db.EncodeJSONMust(payload), errArg, now, now)
	if err != nil {
		return nil, false, err
	}
	row, err := s.AISampleByKey(ctx, in.IdempotencyKey)
	return row, changed > 0, err
}

// AISampleByKey 按幂等键查询样本（ai_learning_repository.py:75-79）。
func (s *Store) AISampleByKey(ctx context.Context, idempotencyKey string) (Row, error) {
	return s.One(ctx, "SELECT * FROM moderation_ai_samples WHERE idempotency_key=?", idempotencyKey)
}

// GetAISample 按样本 ID 查询样本（ai_learning_repository.py:81-84）。
func (s *Store) GetAISample(ctx context.Context, sampleID string) (Row, error) {
	return s.One(ctx, "SELECT * FROM moderation_ai_samples WHERE id=?", sampleID)
}

// AISampleByCase 按关联 case_id 查询样本（ai_learning_repository.py:86-89）。
func (s *Store) AISampleByCase(ctx context.Context, caseID string) (Row, error) {
	return s.One(ctx, "SELECT * FROM moderation_ai_samples WHERE case_id=?", caseID)
}

// EnforcementAISamples 查询指定群用户在处置学习范围内的正向样本（ai_learning_repository.py:91-99）。
func (s *Store) EnforcementAISamples(ctx context.Context, chatID, userID int64) ([]Row, error) {
	return s.All(ctx, `
		SELECT * FROM moderation_ai_samples
		WHERE chat_id=? AND user_id=?
		  AND (label='positive' OR JSON_EXTRACT(payload,'$.enforcement') IS NOT NULL)
	`, chatID, userID)
}

// SaveAISample 更新样本标签、状态、错误、来源与 payload（ai_learning_repository.py:101-116）。
func (s *Store) SaveAISample(ctx context.Context, sample Row, upd AISampleUpdate) error {
	label := rowString(sample, "label")
	if upd.Label != nil {
		label = *upd.Label
	}
	status := rowString(sample, "status")
	if upd.Status != nil {
		status = *upd.Status
	}
	var errArg any
	if !upd.ClearErr {
		if upd.Error != nil {
			if *upd.Error != "" {
				errArg = truncateUTF8(*upd.Error, 128)
			}
		} else if existingErr := rowString(sample, "error"); existingErr != "" {
			errArg = existingErr
		}
	}
	var payloadAny any = sample["payload"]
	if upd.Payload != nil {
		payloadAny = upd.Payload
	}
	if payloadAny == nil {
		payloadAny = map[string]any{}
	}
	source := rowString(sample, "source")
	if upd.Source != nil && *upd.Source != "" {
		source = *upd.Source
	}

	_, err := s.Exec(ctx, `
		UPDATE moderation_ai_samples
		SET label=?,status=?,error=?,payload=?,updated_at=?,source=?
		WHERE id=?
	`, label, status, errArg, db.EncodeJSONMust(payloadAny),
		db.MysqlDatetime6(db.NowUTC()), source, rowString(sample, "id"))
	return err
}

// DeleteAISample 删除指定样本（关联学习任务随外键级联删除）。
func (s *Store) DeleteAISample(ctx context.Context, sampleID string) error {
	_, err := s.Exec(ctx, "DELETE FROM moderation_ai_samples WHERE id=?", sampleID)
	return err
}

// AISamplesByIDs 批量按 ID 查询样本集合（ai_learning_repository.py:118-126）。
func (s *Store) AISamplesByIDs(ctx context.Context, sampleIDs []string) ([]Row, error) {
	if len(sampleIDs) == 0 {
		return []Row{}, nil
	}
	placeholders := strings.Repeat("?,", len(sampleIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(sampleIDs))
	for i, id := range sampleIDs {
		args[i] = id
	}
	return s.All(ctx, "SELECT * FROM moderation_ai_samples WHERE id IN ("+placeholders+")", args...)
}

// SimilarAISamples 读取同群分析窗口内的上下文样本（排除 negative，ai_learning_repository.py:128-135）。
func (s *Store) SimilarAISamples(ctx context.Context, chatID int64, before time.Time, windowDays, limit int) ([]Row, error) {
	if windowDays <= 0 {
		windowDays = 7
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	beforeUTC := before.UTC()
	sinceUTC := beforeUTC.Add(-time.Duration(windowDays) * 24 * time.Hour)
	return s.All(ctx, `
		SELECT * FROM moderation_ai_samples
		WHERE chat_id=? AND created_at>=? AND created_at<=? AND label<>'negative'
		ORDER BY created_at DESC LIMIT ?
	`, chatID, db.MysqlDatetime6(sinceUTC), db.MysqlDatetime6(beforeUTC), limit)
}

// AISamplesForUserChat 查询同群同用户的最近样本（ai_learning_repository.py:137-143）。
func (s *Store) AISamplesForUserChat(ctx context.Context, chatID, userID int64, limit int) ([]Row, error) {
	if limit <= 0 {
		limit = 20
	}
	return s.All(ctx, `
		SELECT * FROM moderation_ai_samples
		WHERE chat_id=? AND user_id=?
		ORDER BY created_at DESC LIMIT ?
	`, chatID, userID, limit)
}

// ListAISamples 免锁分页查询 AI 风控样本（ai_learning_repository.py:145-172）。
func (r *Repository) ListAISamples(ctx context.Context, filter AISampleFilter) (*RecordPage, error) {
	var (
		clauses []string
		values  []any
	)
	if filter.ChatID != nil {
		clauses = append(clauses, "chat_id=?")
		values = append(values, *filter.ChatID)
	}
	if filter.Label != "" {
		clauses = append(clauses, "label=?")
		values = append(values, filter.Label)
	}
	if filter.Source != "" {
		clauses = append(clauses, "(source=? OR JSON_CONTAINS(payload,JSON_OBJECT('source',?),'$.enforcement'))")
		values = append(values, filter.Source, filter.Source)
	}
	if filter.Status != "" {
		clauses = append(clauses, "status=?")
		values = append(values, filter.Status)
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}

	limit := clampLimit(filter.Limit, 50, 200)
	offset := max(0, filter.Offset)
	dataArgs := append(append([]any{}, values...), limit, offset)

	var (
		totalRow Row
		rows     []Row
	)
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		totalRow, e = s.One(ctx, "SELECT COUNT(*) AS total FROM moderation_ai_samples"+where, values...)
		if e != nil {
			return e
		}
		rows, e = s.All(ctx,
			"SELECT * FROM moderation_ai_samples"+where+" ORDER BY created_at DESC LIMIT ? OFFSET ?",
			dataArgs...)
		return e
	})
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []Row{}
	}
	return &RecordPage{Rows: rows, Total: rowInt(totalRow, "total")}, nil
}

// CleanupAISamples 清理已完结且未被任何规则引用的过期样本（ai_learning_repository.py:556-567）。
func (r *Repository) CleanupAISamples(ctx context.Context, days int) (int64, error) {
	if days <= 0 {
		days = 90
	}
	cutoff := db.MysqlDatetime6(db.NowUTC().Add(-time.Duration(days) * 24 * time.Hour))
	var deleted int64
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		deleted, e = s.Exec(ctx, `
			DELETE FROM moderation_ai_samples
			WHERE status IN ('learned','skipped','invalid')
			  AND created_at<?
			  AND NOT EXISTS (
				SELECT 1 FROM moderation_ai_rules
				WHERE JSON_CONTAINS(moderation_ai_rules.sample_ids, JSON_QUOTE(moderation_ai_samples.id))
			  )
			LIMIT 200
		`, cutoff)
		return e
	})
	return deleted, err
}

// ────────────────────────────── AI Learning Jobs ──────────────────────────────

// EnqueueAILearningJob 为样本幂等创建唯一学习任务（ai_learning_repository.py:181-198）。
func (s *Store) EnqueueAILearningJob(ctx context.Context, sampleID string, chatID, userID int64) error {
	now := db.MysqlDatetime6(db.NowUTC())
	_, err := s.Exec(ctx, `
		INSERT INTO moderation_ai_learning_jobs
		(id,sample_id,chat_id,user_id,status,token,attempts,payload,retry_at,created_at,updated_at)
		VALUES (?,?,?,?,'queued',?,0,'{}',?,?,?)
		ON DUPLICATE KEY UPDATE id=id
	`, uuid4Hex(), sampleID, chatID, userID, uuid4Hex(), now, now, now)
	return err
}

// GetAILearningJob 按 sample_id 读取学习任务（ai_learning_repository.py:209-214）。
func (s *Store) GetAILearningJob(ctx context.Context, sampleID string) (Row, error) {
	return s.One(ctx, "SELECT * FROM moderation_ai_learning_jobs WHERE sample_id=?", sampleID)
}

// ClaimAILearningJob 带默认 6 分钟租约与 token 乐观锁认领到期学习任务（ai_learning_repository.py:216-231）。
func (s *Store) ClaimAILearningJob(ctx context.Context, sampleID, expectedToken string, lease time.Duration) (Row, error) {
	if lease <= 0 {
		lease = AILearningLeaseDuration
	}
	newToken := uuid4Hex()
	now := db.NowUTC()
	nowStr := db.MysqlDatetime6(now)
	leaseStr := db.MysqlDatetime6(now.Add(lease))

	changed, err := s.Exec(ctx, `
		UPDATE moderation_ai_learning_jobs
		SET status='running',token=?,attempts=attempts+1,retry_at=?,updated_at=?
		WHERE sample_id=? AND token=?
		  AND status IN ('queued','running') AND retry_at<=?
	`, newToken, leaseStr, nowStr, sampleID, expectedToken, nowStr)
	if err != nil || changed == 0 {
		return nil, err
	}
	return s.GetAILearningJob(ctx, sampleID)
}

// SaveAILearningJob 按当前持有 token 保存学习任务状态（ai_learning_repository.py:233-249）。
func (s *Store) SaveAILearningJob(ctx context.Context, sampleID, token, state string, payload any, delay time.Duration, errStr *string) (bool, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	if delay < 0 {
		delay = 0
	}
	var errArg any
	if errStr != nil && *errStr != "" {
		errArg = truncateUTF8(*errStr, 128)
	}
	now := db.NowUTC()
	nowStr := db.MysqlDatetime6(now)
	retryStr := db.MysqlDatetime6(now.Add(delay))

	changed, err := s.Exec(ctx, `
		UPDATE moderation_ai_learning_jobs
		SET status=?,payload=?,error=?,retry_at=?,updated_at=?
		WHERE sample_id=? AND token=?
	`, state, db.EncodeJSONMust(payload), errArg, retryStr, nowStr, sampleID, token)
	return changed > 0, err
}

// RetryAILearningJobLater 暂缓执行并退回本次尝试计数（ai_learning_repository.py:251-257）。
func (s *Store) RetryAILearningJobLater(ctx context.Context, sampleID, token string, delay time.Duration, errMsg string) error {
	if delay < 0 {
		delay = 0
	}
	var errArg any
	if errMsg != "" {
		errArg = truncateUTF8(errMsg, 128)
	}
	now := db.NowUTC()
	_, err := s.Exec(ctx, `
		UPDATE moderation_ai_learning_jobs
		SET status='queued',attempts=GREATEST(0,attempts-1),retry_at=?,error=?,updated_at=?
		WHERE sample_id=? AND token=?
	`, db.MysqlDatetime6(now.Add(delay)), errArg, db.MysqlDatetime6(now), sampleID, token)
	return err
}

// FailOrRetryAILearningJob 根据最大尝试次数（5 次）决定退避重试或转人工（manual）。
func (s *Store) FailOrRetryAILearningJob(ctx context.Context, job Row, payload any, retryDelay time.Duration, errMsg string) (string, bool, error) {
	attempts := int(rowInt(job, "attempts"))
	nextState := "queued"
	delay := retryDelay
	if attempts >= AILearningMaxAttempts {
		nextState = "manual"
		delay = 0
	} else if delay <= 0 {
		delay = time.Duration(30*(1<<max(0, attempts-1))) * time.Second
	}
	var errPtr *string
	if errMsg != "" {
		errPtr = &errMsg
	}
	saved, err := s.SaveAILearningJob(ctx, rowString(job, "sample_id"), rowString(job, "token"), nextState, payload, delay, errPtr)
	return nextState, saved, err
}

// RequeueAILearningJob 管理员手动重排队转人工任务：轮换 token 作废旧租约并清零 attempts（ai_learning_repository.py:259-268）。
func (s *Store) RequeueAILearningJob(ctx context.Context, sampleID string, afterSampleReview bool) (bool, error) {
	now := db.MysqlDatetime6(db.NowUTC())
	query := `
		UPDATE moderation_ai_learning_jobs
		SET status='queued',token=?,attempts=0,retry_at=?,error=NULL,updated_at=?
		WHERE sample_id=? AND status IN ('manual')
	`
	if afterSampleReview {
		query = `
			UPDATE moderation_ai_learning_jobs
			SET status='queued',token=?,attempts=0,retry_at=?,error=NULL,updated_at=?
			WHERE sample_id=? AND status IN ('manual','skipped')
		`
	}
	changed, err := s.Exec(ctx, query, uuid4Hex(), now, now, sampleID)
	return changed > 0, err
}

// DueAILearningJobs 免锁轮询到期的学习任务（ai_learning_repository.py:200-207）。
func (r *Repository) DueAILearningJobs(ctx context.Context, limit int) ([]Row, error) {
	limit = clampLimit(limit, 10, 50)
	nowStr := db.MysqlDatetime6(db.NowUTC())
	var out []Row
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		out, e = s.All(ctx, `
			SELECT * FROM moderation_ai_learning_jobs
			WHERE status IN ('queued','running') AND retry_at<=?
			ORDER BY retry_at, created_at
			LIMIT ?
		`, nowStr, limit)
		return e
	})
	return out, err
}

// ListAILearningJobs 分页查询学习任务并返回各状态分布计数（ai_learning_repository.py:270-296）。
func (r *Repository) ListAILearningJobs(ctx context.Context, chatID *int64, status string, limit, offset int) (*AILearningJobPage, error) {
	var (
		clauses []string
		values  []any
	)
	if chatID != nil {
		clauses = append(clauses, "chat_id=?")
		values = append(values, *chatID)
	}
	if status != "" {
		clauses = append(clauses, "status=?")
		values = append(values, status)
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}
	limit = clampLimit(limit, 50, 200)
	offset = max(0, offset)
	dataArgs := append(append([]any{}, values...), limit, offset)

	var (
		totalRow  Row
		countRows []Row
		rows      []Row
	)
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		totalRow, e = s.One(ctx, "SELECT COUNT(*) AS total FROM moderation_ai_learning_jobs"+where, values...)
		if e != nil {
			return e
		}
		countRows, e = s.All(ctx, "SELECT status, COUNT(*) AS total FROM moderation_ai_learning_jobs GROUP BY status")
		if e != nil {
			return e
		}
		rows, e = s.All(ctx,
			"SELECT * FROM moderation_ai_learning_jobs"+where+" ORDER BY updated_at DESC LIMIT ? OFFSET ?",
			dataArgs...)
		return e
	})
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []Row{}
	}
	counts := make(map[string]int64, len(countRows))
	for _, cr := range countRows {
		counts[rowString(cr, "status")] = rowInt(cr, "total")
	}
	return &AILearningJobPage{
		Rows:   rows,
		Total:  rowInt(totalRow, "total"),
		Counts: counts,
	}, nil
}

// ────────────────────────────── AI Rules / Hits / Scopes / Feedback ──────────────────────────────

func decodeAIRuleRow(row Row) Row {
	if row == nil {
		return nil
	}
	row["sample_ids"] = parseStringListField(row["sample_ids"])
	return row
}

func decodeAIRuleRows(rows []Row) []Row {
	for i := range rows {
		rows[i] = decodeAIRuleRow(rows[i])
	}
	return rows
}

func decodeAIHitRow(row Row) Row {
	if row == nil {
		return nil
	}
	if raw, ok := row["evidence"].(string); ok && raw != "" {
		var parsed any
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			row["evidence"] = parsed
		}
	}
	return row
}

func decodeAIHitRows(rows []Row) []Row {
	for i := range rows {
		rows[i] = decodeAIHitRow(rows[i])
	}
	return rows
}

func parseStringListField(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if t == "" {
			return []string{}
		}
		var out []string
		if err := json.Unmarshal([]byte(t), &out); err == nil && out != nil {
			return out
		}
	}
	return []string{}
}

// UpsertAIRule 按 rule_key 幂等创建或合并支持样本（已拒绝规则不自动复活，ai_learning_repository.py:305-349）。
func (s *Store) UpsertAIRule(ctx context.Context, in AIRuleUpsert) (Row, error) {
	now := db.MysqlDatetime6(db.NowUTC())
	existing, err := s.AIRuleByKey(ctx, in.RuleKey)
	if err != nil {
		return nil, err
	}
	var modelArg, actorArg any
	if in.Model != nil && *in.Model != "" {
		modelArg = *in.Model
	}
	if in.Actor != nil && *in.Actor != "" {
		actorArg = *in.Actor
	}

	if existing != nil {
		merged := dedupStrings(append(parseStringListField(existing["sample_ids"]), in.SampleIDs...))
		if _, err := s.Exec(ctx, `
			UPDATE moderation_ai_rules
			SET sample_ids=?,confidence=GREATEST(confidence,?),explanation=?,model=?,updated_at=?
			WHERE id=?
		`, db.EncodeJSONMust(merged), in.Confidence, truncateUTF8(in.Explanation, 1024),
			modelArg, now, rowString(existing, "id")); err != nil {
			return nil, err
		}
		return s.AIRuleByKey(ctx, in.RuleKey)
	}

	id := in.ID
	if id == "" {
		id = uuid4Hex()
	}
	status := in.Status
	if status == "" {
		status = "candidate"
	}
	action := in.Action
	if action == "" {
		action = "delete"
	}
	sampleIDs := dedupStrings(in.SampleIDs)

	if _, err := s.Exec(ctx, `
		INSERT INTO moderation_ai_rules
		(id,rule_key,chat_id,rule_type,match_kind,value,normalized_value,action,status,
		 confidence,model,actor,explanation,sample_ids,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`, id, in.RuleKey, in.ChatID, in.RuleType, in.MatchKind,
		truncateUTF8(in.Value, 512), truncateUTF8(in.NormalizedValue, 512),
		action, status, in.Confidence, modelArg, actorArg,
		truncateUTF8(in.Explanation, 1024), db.EncodeJSONMust(sampleIDs), now, now); err != nil {
		return nil, err
	}
	return s.AIRuleByKey(ctx, in.RuleKey)
}

// AIRuleByKey 按 rule_key 查询规则（ai_learning_repository.py:350-353）。
func (s *Store) AIRuleByKey(ctx context.Context, ruleKey string) (Row, error) {
	row, err := s.One(ctx, "SELECT * FROM moderation_ai_rules WHERE rule_key=?", ruleKey)
	return decodeAIRuleRow(row), err
}

// GetAIRule 按规则 ID 查询规则（ai_learning_repository.py:355-358）。
func (s *Store) GetAIRule(ctx context.Context, ruleID string) (Row, error) {
	row, err := s.One(ctx, "SELECT * FROM moderation_ai_rules WHERE id=?", ruleID)
	return decodeAIRuleRow(row), err
}

// GetAIRuleLocked 在当前事务中加排他锁读取规则（FOR UPDATE，ai_learning_repository.py:360-363）。
func (s *Store) GetAIRuleLocked(ctx context.Context, ruleID string) (Row, error) {
	row, err := s.One(ctx, "SELECT * FROM moderation_ai_rules WHERE id=? FOR UPDATE", ruleID)
	return decodeAIRuleRow(row), err
}

// RefreshAIRuleStats 根据支持样本最新标签重算正负样本数、独立举报人数与独立目标用户数（ai_learning_repository.py:365-379）。
func (s *Store) RefreshAIRuleStats(ctx context.Context, ruleID string, samples []Row) error {
	positives := 0
	negatives := 0
	reporters := make(map[int64]bool)
	targetUsers := make(map[int64]bool)

	for _, smp := range samples {
		switch rowString(smp, "label") {
		case "positive":
			positives++
		case "negative":
			negatives++
		}
		if uid := rowInt(smp, "user_id"); uid != 0 {
			targetUsers[uid] = true
		}
		if payload, ok := smp["payload"].(map[string]any); ok && payload != nil {
			if repList, ok := payload["reporters"].([]any); ok {
				for _, item := range repList {
					if repMap, ok := item.(map[string]any); ok {
						if ruid := rowInt(Row(repMap), "user_id"); ruid != 0 {
							reporters[ruid] = true
						}
					}
				}
			}
		}
	}

	_, err := s.Exec(ctx, `
		UPDATE moderation_ai_rules
		SET positives=?,negatives=?,reporters=?,target_users=?,updated_at=?
		WHERE id=?
	`, positives, negatives, len(reporters), len(targetUsers), db.MysqlDatetime6(db.NowUTC()), ruleID)
	return err
}

// SetAIRuleStatus 更新规则状态、版本与过期时间（ai_learning_repository.py:381-406）。
func (s *Store) SetAIRuleStatus(ctx context.Context, rule Row, status string, opts AIRuleStatusOptions) error {
	actor := opts.Actor
	if actor == "" {
		actor = rowString(rule, "actor")
	}
	var actorArg, errArg, expArg any
	if actor != "" {
		actorArg = actor
	}
	if opts.Error != nil && *opts.Error != "" {
		errArg = truncateUTF8(*opts.Error, 128)
	}
	versionDelta := 0
	if opts.BumpVersion {
		versionDelta = 1
	}
	now := db.NowUTC()
	if opts.Activate {
		days := opts.ExpiresDays
		if days <= 0 {
			days = 30
		}
		expArg = db.MysqlDatetime6(now.Add(time.Duration(days) * 24 * time.Hour))
	}

	_, err := s.Exec(ctx, `
		UPDATE moderation_ai_rules
		SET status=?,actor=?,error=?,version=version+?,
		    expires_at=IF(?,?,expires_at),updated_at=?
		WHERE id=?
	`, status, actorArg, errArg, versionDelta, opts.Activate, expArg, db.MysqlDatetime6(now), rowString(rule, "id"))
	return err
}

// DeleteAIRuleCascade 在单事务内级联删除动态规则及其命中、共享作用域和反馈记录（对齐 guard.py:749-765）。
func (s *Store) DeleteAIRuleCascade(ctx context.Context, ruleID string) error {
	return s.Atomic(ctx, func(s *Store) error {
		if _, err := s.Exec(ctx, "DELETE FROM moderation_ai_rule_hits WHERE rule_id=?", ruleID); err != nil {
			return err
		}
		if _, err := s.Exec(ctx, "DELETE FROM moderation_ai_rule_scopes WHERE rule_id=?", ruleID); err != nil {
			return err
		}
		if _, err := s.Exec(ctx, "DELETE FROM moderation_ai_rule_feedback WHERE rule_id=?", ruleID); err != nil {
			return err
		}
		_, err := s.Exec(ctx, "DELETE FROM moderation_ai_rules WHERE id=?", ruleID)
		return err
	})
}

// RecordAIRuleHit 写入动态规则命中审计并递增命中计数（ai_learning_repository.py:408-420）。
func (s *Store) RecordAIRuleHit(ctx context.Context, rule Row, chatID, userID int64, caseID *string, action string, evidence any) error {
	if evidence == nil {
		evidence = map[string]any{}
	}
	var caseArg any
	if caseID != nil && *caseID != "" {
		caseArg = *caseID
	}
	now := db.MysqlDatetime6(db.NowUTC())
	ruleID := rowString(rule, "id")
	version := rowInt(rule, "version")
	if version <= 0 {
		version = 1
	}

	if _, err := s.Exec(ctx, `
		INSERT INTO moderation_ai_rule_hits
		(rule_id,chat_id,user_id,case_id,version,action,evidence,created_at)
		VALUES (?,?,?,?,?,?,?,?)
	`, ruleID, chatID, userID, caseArg, version, action, db.EncodeJSONMust(evidence), now); err != nil {
		return err
	}
	_, err := s.Exec(ctx,
		"UPDATE moderation_ai_rules SET hits=hits+1,last_hit_at=?,updated_at=? WHERE id=?",
		now, now, ruleID)
	return err
}

// AIRuleHitsFor 查询同群同用户的最近规则命中审计记录（ai_learning_repository.py:422-428）。
func (s *Store) AIRuleHitsFor(ctx context.Context, chatID, userID int64, limit int) ([]Row, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.All(ctx, `
		SELECT * FROM moderation_ai_rule_hits
		WHERE chat_id=? AND user_id=?
		ORDER BY id DESC LIMIT ?
	`, chatID, userID, limit)
	return decodeAIHitRows(rows), err
}

// HitAIRuleIDs 查询指定群用户曾命中的全部去重规则 ID（ai_learning_repository.py:430-435）。
func (s *Store) HitAIRuleIDs(ctx context.Context, chatID, userID int64) ([]string, error) {
	rows, err := s.All(ctx,
		"SELECT DISTINCT rule_id FROM moderation_ai_rule_hits WHERE chat_id=? AND user_id=?",
		chatID, userID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if id := rowString(r, "rule_id"); id != "" {
			out = append(out, id)
		}
	}
	return out, nil
}

// AIRulesForSample 查询引用了指定 sample_id 的全部规则（ai_learning_repository.py:437-442）。
func (s *Store) AIRulesForSample(ctx context.Context, sampleID string) ([]Row, error) {
	quoted, _ := db.EncodeJSON(sampleID)
	rows, err := s.All(ctx,
		"SELECT * FROM moderation_ai_rules WHERE JSON_CONTAINS(sample_ids, ?)",
		quoted)
	return decodeAIRuleRows(rows), err
}

// AIRulesForChat 查询本群创建的动态规则列表（ai_learning_repository.py:444-449）。
func (s *Store) AIRulesForChat(ctx context.Context, chatID int64, limit int) ([]Row, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.All(ctx,
		"SELECT * FROM moderation_ai_rules WHERE chat_id=? ORDER BY updated_at DESC LIMIT ?",
		chatID, limit)
	return decodeAIRuleRows(rows), err
}

// AddAIRuleCorrection 递增规则误判纠偏次数（ai_learning_repository.py:451-455）。
func (s *Store) AddAIRuleCorrection(ctx context.Context, ruleID string) error {
	_, err := s.Exec(ctx,
		"UPDATE moderation_ai_rules SET corrections=corrections+1,updated_at=? WHERE id=?",
		db.MysqlDatetime6(db.NowUTC()), ruleID)
	return err
}

// SetAIRuleAction 修改规则命中动作并递增版本号（ai_learning_repository.py:457-463）。
func (s *Store) SetAIRuleAction(ctx context.Context, ruleID, action string) (Row, error) {
	if _, err := s.Exec(ctx,
		"UPDATE moderation_ai_rules SET action=?,version=version+1,updated_at=? WHERE id=?",
		action, db.MysqlDatetime6(db.NowUTC()), ruleID); err != nil {
		return nil, err
	}
	return s.GetAIRule(ctx, ruleID)
}

// AIRuleScopes 读取规则已显式授权共享的群组 ID 列表（升序，ai_learning_repository.py:483-488）。
func (s *Store) AIRuleScopes(ctx context.Context, ruleID string) ([]int64, error) {
	rows, err := s.All(ctx,
		"SELECT chat_id FROM moderation_ai_rule_scopes WHERE rule_id=? ORDER BY chat_id",
		ruleID)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		out = append(out, rowInt(r, "chat_id"))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// SetAIRuleScopes 增量同步规则的跨群共享授权集合（ai_learning_repository.py:490-504）。
func (s *Store) SetAIRuleScopes(ctx context.Context, ruleID string, chatIDs []int64, actor string) error {
	wantedSet := make(map[int64]bool, len(chatIDs))
	wanted := make([]int64, 0, len(chatIDs))
	for _, cid := range chatIDs {
		if !wantedSet[cid] {
			wantedSet[cid] = true
			wanted = append(wanted, cid)
		}
	}
	sort.Slice(wanted, func(i, j int) bool { return wanted[i] < wanted[j] })

	current, err := s.AIRuleScopes(ctx, ruleID)
	if err != nil {
		return err
	}
	currentSet := make(map[int64]bool, len(current))
	for _, cid := range current {
		currentSet[cid] = true
	}

	now := db.MysqlDatetime6(db.NowUTC())
	for _, cid := range current {
		if !wantedSet[cid] {
			if _, err := s.Exec(ctx,
				"DELETE FROM moderation_ai_rule_scopes WHERE rule_id=? AND chat_id=?",
				ruleID, cid); err != nil {
				return err
			}
		}
	}
	for _, cid := range wanted {
		if !currentSet[cid] {
			if _, err := s.Exec(ctx, `
				INSERT INTO moderation_ai_rule_scopes (rule_id,chat_id,actor,created_at)
				VALUES (?,?,?,?)
			`, ruleID, cid, actor, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// RecordAIRuleFeedback 写入正负反馈可追溯审计记录（ai_learning_repository.py:583-613）。
func (s *Store) RecordAIRuleFeedback(ctx context.Context, in AIRuleFeedbackInsert) error {
	payload := in.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	var ruleArg, sampleArg, caseArg any
	if in.RuleID != nil && *in.RuleID != "" {
		ruleArg = *in.RuleID
	}
	if in.SampleID != nil && *in.SampleID != "" {
		sampleArg = *in.SampleID
	}
	if in.CaseID != nil && *in.CaseID != "" {
		caseArg = *in.CaseID
	}
	_, err := s.Exec(ctx, `
		INSERT INTO moderation_ai_rule_feedback
		(rule_id,sample_id,case_id,chat_id,user_id,kind,polarity,actor,payload,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)
	`, ruleArg, sampleArg, caseArg, in.ChatID, in.UserID, in.Kind, in.Polarity,
		in.Actor, db.EncodeJSONMust(payload), db.MysqlDatetime6(db.NowUTC()))
	return err
}

// ActiveAIRulesRows 加载全部未过期已激活规则连同显式共享群组列表（Row 形态，对齐 ai_learning_repository.py:465-481）。
func (r *Repository) ActiveAIRulesRows(ctx context.Context) ([]Row, error) {
	nowStr := db.MysqlDatetime6(db.NowUTC())
	var (
		rules  []Row
		scopes []Row
	)
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		rules, e = s.All(ctx, `
			SELECT * FROM moderation_ai_rules
			WHERE status='active' AND (expires_at IS NULL OR expires_at>?)
			ORDER BY created_at
		`, nowStr)
		if e != nil {
			return e
		}
		scopes, e = s.All(ctx, "SELECT rule_id, chat_id FROM moderation_ai_rule_scopes")
		return e
	})
	if err != nil {
		return nil, err
	}

	shared := make(map[string][]int64)
	for _, sc := range scopes {
		rid := rowString(sc, "rule_id")
		shared[rid] = append(shared[rid], rowInt(sc, "chat_id"))
	}
	for k := range shared {
		sort.Slice(shared[k], func(i, j int) bool { return shared[k][i] < shared[k][j] })
	}

	rules = decodeAIRuleRows(rules)
	for _, rule := range rules {
		rid := rowString(rule, "id")
		chats := shared[rid]
		if chats == nil {
			chats = []int64{}
		}
		rule["shared_chats"] = chats
	}
	return rules, nil
}

// ActiveAIRulesSnapshot 加载强类型已激活规则快照供进程内 AIRuleCache 原子切换。
func (r *Repository) ActiveAIRulesSnapshot(ctx context.Context) ([]ActiveAIRule, error) {
	rows, err := r.ActiveAIRulesRows(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ActiveAIRule, 0, len(rows))
	for _, row := range rows {
		var conf float64
		switch v := row["confidence"].(type) {
		case float64:
			conf = v
		case float32:
			conf = float64(v)
		}
		shared, _ := row["shared_chats"].([]int64)
		if shared == nil {
			shared = []int64{}
		}
		out = append(out, ActiveAIRule{
			ID:              rowString(row, "id"),
			RuleKey:         rowString(row, "rule_key"),
			ChatID:          rowInt(row, "chat_id"),
			RuleType:        rowString(row, "rule_type"),
			MatchKind:       rowString(row, "match_kind"),
			Value:           rowString(row, "value"),
			NormalizedValue: rowString(row, "normalized_value"),
			Action:          rowString(row, "action"),
			Confidence:      conf,
			Version:         int(rowInt(row, "version")),
			SharedChats:     shared,
			SampleIDs:       parseStringListField(row["sample_ids"]),
		})
	}
	return out, nil
}

// ExpiringAIRules 查询已到达 expires_at 的 active 规则列表（ai_learning_repository.py:506-513）。
func (r *Repository) ExpiringAIRules(ctx context.Context, now time.Time) ([]Row, error) {
	if now.IsZero() {
		now = db.NowUTC()
	}
	nowStr := db.MysqlDatetime6(now.UTC())
	var rows []Row
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		rows, e = s.All(ctx, `
			SELECT * FROM moderation_ai_rules
			WHERE status='active' AND expires_at IS NOT NULL AND expires_at<=?
		`, nowStr)
		return e
	})
	return decodeAIRuleRows(rows), err
}

// ExpireAIRules 批量将已到期的 active 规则置为 expired（ai_learning_repository.py:547-554）。
func (r *Repository) ExpireAIRules(ctx context.Context, now time.Time) (int64, error) {
	if now.IsZero() {
		now = db.NowUTC()
	}
	updatedStr := db.MysqlDatetime6(db.NowUTC())
	deadlineStr := db.MysqlDatetime6(now.UTC())
	var affected int64
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		affected, e = s.Exec(ctx, `
			UPDATE moderation_ai_rules
			SET status='expired',updated_at=?
			WHERE status='active' AND expires_at IS NOT NULL AND expires_at<=?
		`, updatedStr, deadlineStr)
		return e
	})
	return affected, err
}

// ListAIRules 分页筛选动态规则列表（ai_learning_repository.py:515-537）。
func (r *Repository) ListAIRules(ctx context.Context, chatID *int64, ruleType, status string, limit, offset int) (*RecordPage, error) {
	var (
		clauses []string
		values  []any
	)
	if chatID != nil {
		clauses = append(clauses, "chat_id=?")
		values = append(values, *chatID)
	}
	if ruleType != "" {
		clauses = append(clauses, "rule_type=?")
		values = append(values, ruleType)
	}
	if status != "" {
		clauses = append(clauses, "status=?")
		values = append(values, status)
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}
	limit = clampLimit(limit, 50, 200)
	offset = max(0, offset)
	dataArgs := append(append([]any{}, values...), limit, offset)

	var (
		totalRow Row
		rows     []Row
	)
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		totalRow, e = s.One(ctx, "SELECT COUNT(*) AS total FROM moderation_ai_rules"+where, values...)
		if e != nil {
			return e
		}
		rows, e = s.All(ctx,
			"SELECT * FROM moderation_ai_rules"+where+" ORDER BY updated_at DESC LIMIT ? OFFSET ?",
			dataArgs...)
		return e
	})
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []Row{}
	}
	return &RecordPage{
		Rows:  decodeAIRuleRows(rows),
		Total: rowInt(totalRow, "total"),
	}, nil
}

// RecentAIRuleHits 查询单条规则的最近命中审计列表（ai_learning_repository.py:539-545）。
func (r *Repository) RecentAIRuleHits(ctx context.Context, ruleID string, limit int) ([]Row, error) {
	limit = clampLimit(limit, 50, 200)
	var rows []Row
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		rows, e = s.All(ctx,
			"SELECT * FROM moderation_ai_rule_hits WHERE rule_id=? ORDER BY id DESC LIMIT ?",
			ruleID, limit)
		return e
	})
	return decodeAIHitRows(rows), err
}

// CleanupAIRuleHits 清理超过 days 天的旧规则命中审计行（ai_learning_repository.py:569-574）。
func (r *Repository) CleanupAIRuleHits(ctx context.Context, days int) (int64, error) {
	if days <= 0 {
		days = 90
	}
	cutoff := db.MysqlDatetime6(db.NowUTC().Add(-time.Duration(days) * 24 * time.Hour))
	var deleted int64
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		deleted, e = s.Exec(ctx, "DELETE FROM moderation_ai_rule_hits WHERE created_at<? LIMIT 500", cutoff)
		return e
	})
	return deleted, err
}

// ListAIRuleFeedback 分页查询规则反馈审计记录（ai_learning_repository.py:615-634）。
func (r *Repository) ListAIRuleFeedback(ctx context.Context, ruleID string, chatID *int64, limit, offset int) (*RecordPage, error) {
	var (
		clauses []string
		values  []any
	)
	if ruleID != "" {
		clauses = append(clauses, "rule_id=?")
		values = append(values, ruleID)
	}
	if chatID != nil {
		clauses = append(clauses, "chat_id=?")
		values = append(values, *chatID)
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}
	limit = clampLimit(limit, 50, 200)
	offset = max(0, offset)
	dataArgs := append(append([]any{}, values...), limit, offset)

	var (
		totalRow Row
		rows     []Row
	)
	err := r.Reader(ctx, func(s *Store) error {
		var e error
		totalRow, e = s.One(ctx, "SELECT COUNT(*) AS total FROM moderation_ai_rule_feedback"+where, values...)
		if e != nil {
			return e
		}
		rows, e = s.All(ctx,
			"SELECT * FROM moderation_ai_rule_feedback"+where+" ORDER BY id DESC LIMIT ? OFFSET ?",
			dataArgs...)
		return e
	})
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []Row{}
	}
	return &RecordPage{Rows: rows, Total: rowInt(totalRow, "total")}, nil
}

func dedupStrings(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, s := range items {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func clampLimit(v, def, hi int) int {
	if v <= 0 {
		return def
	}
	if v < 1 {
		return 1
	}
	if v > hi {
		return hi
	}
	return v
}
