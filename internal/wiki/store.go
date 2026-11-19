// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package wiki

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/Sakura520222/Sakura-Bot/internal/db"
)

const (
	// JobLeaseSeconds 是任务租约时长（90 秒，配合 20 秒心跳续租）。
	JobLeaseSeconds = 90
	// DefaultMaxJobAttempts 是后台任务最大重试次数（5 次，指数退避）。
	DefaultMaxJobAttempts = 5

	legacyCaptionMediaSQL = `FROM wiki_sources m
    JOIN wiki_sources i ON i.channel_id=m.channel_id AND i.source_type='image'
        AND i.message_id=m.message_id AND i.media_key=m.media_key
    WHERE m.channel_id=? AND m.source_type='message' AND (m.media_group IS NULL OR m.media_group='')
    AND (i.media_group IS NULL OR i.media_group='') AND m.verified=TRUE AND i.verified=TRUE
    AND m.deleted=FALSE AND m.hidden=FALSE AND m.excluded=FALSE
    AND i.deleted=FALSE AND i.hidden=FALSE AND i.excluded=FALSE AND TRIM(m.body)<>''`
)

var (
	// ErrLeaseLost 表示任务租约已过期、被其他 Worker 接管或被管理员暂停/取消。
	ErrLeaseLost = errors.New("Wiki job lease expired or was cancelled")

	terminalCursorKeys = []string{
		"page_id",
		"base_revision",
		"revisions",
		"dropped_pages",
		"profile_narrative",
		"profile_generation",
		"review_policy",
		"review_revisions",
		"review_rejected_revisions",
		"summary_topics",
	}

	exclusionLabels = map[string]string{
		"service":              "频道系统事件",
		"generated_summary":    "机器人已生成的重复总结",
		"before_history_since": "早于设置的历史起点",
		"message_type":         "未选择的消息类型",
		"unavailable":          "原消息暂不可读取",
		"":                     "旧记录，等待重新核对",
	}
)

// WikiSchemaError 表示建表或数据库迁移失败。
type WikiSchemaError struct {
	Table     string
	MySQLCode uint16
	Cause     error
}

func (e *WikiSchemaError) Error() string {
	if e.MySQLCode != 0 {
		return fmt.Sprintf("Wiki 建表失败：%s（MySQL %d）", e.Table, e.MySQLCode)
	}
	return fmt.Sprintf("Wiki 建表失败：%s（%v）", e.Table, e.Cause)
}

func (e *WikiSchemaError) Unwrap() error { return e.Cause }

// sqlExecutor 统一 *sql.DB 与 *sql.Tx 的查询签名。
type sqlExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type snapshotCtxKey struct{}

type snapshotHolder struct {
	store *Store
	tx    *sql.Tx
}

// ChannelEntity 描述用于注册或刷新频道元数据的 Telegram 实体信息。
type ChannelEntity struct {
	ID              int64
	Title           string
	Username        string
	ActiveUsernames []string
	Broadcast       bool
	Restricted      bool
	NoForwards      bool
}

// ChannelRecord 表示 wiki_channels 行及其解析后的配置和封面摘要。
type ChannelRecord struct {
	ID                string         `json:"id"`
	Title             string         `json:"title"`
	Username          string         `json:"username,omitempty"`
	Enabled           bool           `json:"enabled"`
	Public            bool           `json:"public"`
	AccessVerified    bool           `json:"access_verified"`
	VerifiedUntil     *time.Time     `json:"verified_until,omitempty"`
	Config            WikiConfig     `json:"config"`
	Coverage          map[string]any `json:"coverage"`
	PublishedRevision string         `json:"published_revision,omitempty"`
	SourceGeneration  int64          `json:"source_generation"`
	ActiveJob         string         `json:"active_job,omitempty"`
	DirtyAt           *time.Time     `json:"dirty_at,omitempty"`
	ScheduledAt       *time.Time     `json:"scheduled_at,omitempty"`
	ReconciledAt      *time.Time     `json:"reconciled_at,omitempty"`
	UpdatedAt         time.Time      `json:"updated_at"`
	CoverDigest       string         `json:"cover_digest,omitempty"`
}

// SourceRecord 表示 wiki_sources 行。
type SourceRecord struct {
	ID              string
	ChannelID       string
	SourceType      string
	ExternalID      string
	Category        string
	MessageID       *int64
	Body            string
	OriginalLength  int
	ContentHash     string
	Revision        int64
	CreatedAt       time.Time
	EditedAt        time.Time
	ObservedAt      time.Time
	Verified        bool
	Deleted         bool
	Hidden          bool
	Excluded        bool
	MediaGroup      string
	MediaKey        string
	ExclusionReason string
}

// PutSourceInput 是 Store.PutSource 的写入参数。
type PutSourceInput struct {
	ChannelID       string
	SourceType      string
	ExternalID      string
	Body            string
	CreatedAt       time.Time
	EditedAt        *time.Time
	Verified        bool
	MessageID       *int64
	OriginalLength  int
	Excluded        bool
	ExclusionReason string
	MediaGroup      string
	Category        string
	MediaKey        string
}

// PageRecord 表示 wiki_pages 行。
type PageRecord struct {
	ID           string
	ChannelID    string
	EntityKey    string
	Title        string
	Slug         string
	Kind         string
	Topic        string
	Locked       bool
	Hidden       bool
	OverrideJSON map[string]any
	UpdatedAt    time.Time
}

// JobRecord 表示被认领或查询的 wiki_jobs 行。
type JobRecord struct {
	Mu          sync.Mutex `json:"-"`
	checkpoint  sync.Mutex
	ID          string         `json:"id"`
	ChannelID   string         `json:"channel_id"`
	Kind        string         `json:"kind"`
	Status      string         `json:"status"`
	Phase       string         `json:"phase"`
	Cursor      map[string]any `json:"cursor"`
	Progress    map[string]any `json:"progress"`
	Attempts    int            `json:"attempts"`
	LeaseToken  string         `json:"lease_token,omitempty"`
	LeaseUntil  *time.Time     `json:"lease_until,omitempty"`
	NextAttempt time.Time      `json:"next_attempt"`
	ErrorCode   string         `json:"error_code,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// Store 封装全部 34 张 wiki_* 表的持久化、一致性只读快照与租约围栏任务队列。
type Store struct {
	DB *sql.DB
}

// NewStore 创建基于 *sql.DB 的 Wiki 仓储实例。
func NewStore(sqlDB *sql.DB) *Store {
	return &Store{DB: sqlDB}
}

func (s *Store) exec(ctx context.Context) sqlExecutor {
	if h, ok := ctx.Value(snapshotCtxKey{}).(*snapshotHolder); ok && h != nil && h.store == s && h.tx != nil {
		return h.tx
	}
	return s.DB
}

// WithSnapshot 在 REPEATABLE READ 只读事务快照内运行 fn；嵌套调用自动复用同一事务。
func (s *Store) WithSnapshot(ctx context.Context, fn func(ctx context.Context, tx *sql.Tx) error) error {
	if s == nil || s.DB == nil {
		return errors.New("wiki database pool is not initialized")
	}
	if h, ok := ctx.Value(snapshotCtxKey{}).(*snapshotHolder); ok && h != nil && h.store == s && h.tx != nil {
		return fn(ctx, h.tx)
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		return fmt.Errorf("begin wiki snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	snapCtx := context.WithValue(ctx, snapshotCtxKey{}, &snapshotHolder{store: s, tx: tx})
	if err := fn(snapCtx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// WithTx 在读写事务内运行 fn；发生错误或 panic 时自动回滚。
func (s *Store) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if s == nil || s.DB == nil {
		return errors.New("wiki database pool is not initialized")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// Initialize 幂等创建全部 34 张 wiki_* 表、记录版本 1 并执行增量列/索引升级（忽略 MySQL 1060/1061）。
func (s *Store) Initialize(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return errors.New("wiki database pool is not initialized")
	}
	var currentVer sql.NullInt64
	err := s.DB.QueryRowContext(ctx, "SELECT MAX(version) FROM wiki_schema").Scan(&currentVer)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		var myErr *mysql.MySQLError
		if !errors.As(err, &myErr) || myErr.Number != 1146 {
			return fmt.Errorf("check wiki_schema: %w", err)
		}
		currentVer = sql.NullInt64{}
	}

	if currentVer.Valid {
		if currentVer.Int64 != SchemaVersion {
			return fmt.Errorf("unsupported Wiki schema version: %d", currentVer.Int64)
		}
		// 确保所有增量表与增量列/索引幂等就绪
		extraTables := []string{ddlDirtyPages, ddlChannelCovers, ddlAlbums, ddlRevisionReviews}
		extraTables = append(extraTables, WikiAccountTables...)
		extraTables = append(extraTables, WikiCommunityTables...)
		for _, stmt := range extraTables {
			if _, execErr := s.DB.ExecContext(ctx, stmt); execErr != nil {
				return wrapSchemaError(ExtractTableName(stmt), execErr)
			}
		}
		for _, alter := range AdditiveAlterStatements {
			if _, alterErr := s.DB.ExecContext(ctx, alter); alterErr != nil && !isIgnorableAlterError(alterErr) {
				return fmt.Errorf("wiki additive migration failed: %w", alterErr)
			}
		}
		return nil
	}

	for _, stmt := range SchemaStatements {
		if _, execErr := s.DB.ExecContext(ctx, stmt); execErr != nil {
			return wrapSchemaError(ExtractTableName(stmt), execErr)
		}
	}
	for _, alter := range AdditiveAlterStatements {
		if _, alterErr := s.DB.ExecContext(ctx, alter); alterErr != nil && !isIgnorableAlterError(alterErr) {
			return fmt.Errorf("wiki additive migration failed: %w", alterErr)
		}
	}
	_, err = s.DB.ExecContext(ctx, "INSERT IGNORE INTO wiki_schema VALUES (?, UTC_TIMESTAMP(6))", SchemaVersion)
	return err
}

func isIgnorableAlterError(err error) bool {
	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		// 1060: Duplicate column name; 1061: Duplicate key name
		return myErr.Number == 1060 || myErr.Number == 1061
	}
	return false
}

func wrapSchemaError(table string, err error) error {
	var code uint16
	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		code = myErr.Number
	}
	return &WikiSchemaError{Table: table, MySQLCode: code, Cause: err}
}

// Audit 在当前事务或连接上写入管理员/系统审计记录。
func (s *Store) Audit(ctx context.Context, exec sqlExecutor, channelID, action, targetID, actor string, details any) error {
	if exec == nil {
		exec = s.exec(ctx)
	}
	if len(actor) > 128 {
		actor = actor[:128]
	}
	var targetArg any
	if targetID != "" {
		targetArg = targetID
	}
	if details == nil {
		details = map[string]any{}
	}
	_, err := exec.ExecContext(ctx, `
		INSERT INTO wiki_review_actions
		(channel_id, action, target_id, actor, details, created_at)
		VALUES (?, ?, ?, ?, ?, UTC_TIMESTAMP(6))
	`, channelID, action, targetArg, actor, EncodeJSON(details))
	return err
}

// GetChannel 通过别名、链接或数字 ID 解析频道记录（不存在时返回 nil, nil）。
func (s *Store) GetChannel(ctx context.Context, key string) (*ChannelRecord, error) {
	alias, err := AliasKey(key)
	if err != nil {
		return nil, nil
	}
	row := s.exec(ctx).QueryRowContext(ctx, `
		SELECT c.id, c.title, c.username, c.enabled, c.public, c.access_verified, c.verified_until,
		       c.config, c.coverage, c.published_revision, c.source_generation, c.active_job,
		       c.dirty_at, c.scheduled_at, c.reconciled_at, c.updated_at, cover.digest
		FROM wiki_channels c
		JOIN wiki_channel_aliases a ON a.channel_id = c.id
		LEFT JOIN wiki_channel_covers cover ON cover.channel_id = c.id
		WHERE a.alias = ?
	`, alias)
	rec, scanErr := scanChannelRow(row)
	if errors.Is(scanErr, sql.ErrNoRows) {
		return nil, nil
	}
	return rec, scanErr
}

// ListChannels 列出所有已注册 Wiki 频道（按标题排序）。
func (s *Store) ListChannels(ctx context.Context) ([]*ChannelRecord, error) {
	rows, err := s.exec(ctx).QueryContext(ctx, `
		SELECT c.id, c.title, c.username, c.enabled, c.public, c.access_verified, c.verified_until,
		       c.config, c.coverage, c.published_revision, c.source_generation, c.active_job,
		       c.dirty_at, c.scheduled_at, c.reconciled_at, c.updated_at, cover.digest
		FROM wiki_channels c
		LEFT JOIN wiki_channel_covers cover ON cover.channel_id = c.id
		ORDER BY c.title
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*ChannelRecord
	for rows.Next() {
		rec, err := scanChannelRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func scanChannelRow(sc interface{ Scan(...any) error }) (*ChannelRecord, error) {
	var (
		rec                                     ChannelRecord
		username, pubRev, activeJob, coverDig   sql.NullString
		verUntil, dirtyAt, schedAt, recAt, upAt any
		cfgRaw, covRaw                          []byte
	)
	err := sc.Scan(
		&rec.ID, &rec.Title, &username, &rec.Enabled, &rec.Public, &rec.AccessVerified, &verUntil,
		&cfgRaw, &covRaw, &pubRev, &rec.SourceGeneration, &activeJob,
		&dirtyAt, &schedAt, &recAt, &upAt, &coverDig,
	)
	if err != nil {
		return nil, err
	}
	rec.Username = username.String
	rec.PublishedRevision = pubRev.String
	rec.ActiveJob = activeJob.String
	rec.CoverDigest = coverDig.String
	rec.VerifiedUntil = scanNullableTime(verUntil)
	rec.DirtyAt = scanNullableTime(dirtyAt)
	rec.ScheduledAt = scanNullableTime(schedAt)
	rec.ReconciledAt = scanNullableTime(recAt)
	if t := scanNullableTime(upAt); t != nil {
		rec.UpdatedAt = *t
	}
	cfg, cfgErr := ParseWikiConfig(cfgRaw)
	if cfgErr != nil {
		cfg = DefaultWikiConfig()
		_ = json.Unmarshal(cfgRaw, &cfg)
	}
	rec.Config = cfg
	rec.Coverage = decodeMapJSON(covRaw)
	return &rec, nil
}

// RegisterChannel 注册或刷新频道公开核实状态，并检测重分配用户名以立即吊销旧频道绑定（对齐 store.py:register_channel）。
func (s *Store) RegisterChannel(ctx context.Context, entity ChannelEntity, configuredAlias string) (*ChannelRecord, error) {
	channelID, err := CanonicalChannelID(fmt.Sprintf("%d", entity.ID))
	if err != nil {
		return nil, err
	}
	username := strings.TrimSpace(entity.Username)
	if username == "" {
		for _, u := range entity.ActiveUsernames {
			if strings.TrimSpace(u) != "" {
				username = strings.TrimSpace(u)
				break
			}
		}
	}
	accessVerified := username != "" && entity.Broadcast && !entity.Restricted && !entity.NoForwards
	title := strings.TrimSpace(entity.Title)
	if title == "" {
		title = channelID
	}

	var verifiedUntilArg any
	if accessVerified {
		verifiedUntilArg = db.MysqlDatetime6(NowUTC().Add(3 * time.Minute))
	}
	var usernameArg any
	if username != "" {
		usernameArg = username
	}

	aliases := map[string]struct{}{channelID: {}}
	if configuredAlias != "" {
		if ak, err := AliasKey(configuredAlias); err == nil {
			aliases[ak] = struct{}{}
		}
	}
	if username != "" {
		if ak, err := AliasKey(username); err == nil {
			aliases[ak] = struct{}{}
		}
	}

	err = s.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO wiki_channels
			(id, title, username, access_verified, verified_until, config, coverage, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(6))
			ON DUPLICATE KEY UPDATE
				title = VALUES(title),
				username = VALUES(username),
				access_verified = VALUES(access_verified),
				verified_until = VALUES(verified_until)
		`,
			channelID,
			title,
			usernameArg,
			accessVerified,
			verifiedUntilArg,
			EncodeJSON(DefaultWikiConfig()),
			EncodeJSON(map[string]any{"status": "unavailable", "reason": "not_scanned"}),
		)
		if err != nil {
			return err
		}
		for alias := range aliases {
			var existingChannel string
			qErr := tx.QueryRowContext(ctx, "SELECT channel_id FROM wiki_channel_aliases WHERE alias = ? FOR UPDATE", alias).Scan(&existingChannel)
			if qErr == nil && existingChannel != channelID {
				if _, err := tx.ExecContext(ctx, "UPDATE wiki_channels SET access_verified=FALSE, verified_until=NULL WHERE id=?", existingChannel); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, "DELETE FROM wiki_channel_aliases WHERE alias=?", alias); err != nil {
					return err
				}
			} else if qErr != nil && !errors.Is(qErr, sql.ErrNoRows) {
				return qErr
			}
			if _, err := tx.ExecContext(ctx, "INSERT IGNORE INTO wiki_channel_aliases VALUES (?, ?)", alias, channelID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetChannel(ctx, channelID)
}

// ConfigureChannel 更新频道 WikiConfig，必要时立刻重算来源排除策略、标记脏页面并暂停在途任务。
func (s *Store) ConfigureChannel(ctx context.Context, channelID string, cfg WikiConfig, actor string) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		var prevRaw []byte
		if err := tx.QueryRowContext(ctx, "SELECT config FROM wiki_channels WHERE id=? FOR UPDATE", channelID).Scan(&prevRaw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("Unknown channel")
			}
			return err
		}
		oldCfg, _ := ParseWikiConfig(prevRaw)
		rebuild := shouldRebuildOnConfigChange(oldCfg, cfg)
		rebuildInt := 0
		if rebuild {
			rebuildInt = 1
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE wiki_channels
			SET config=?, enabled=?, public=?,
			    updated_at=UTC_TIMESTAMP(6),
			    dirty_at=IF(?, UTC_TIMESTAMP(6), dirty_at),
			    source_generation=source_generation+?
			WHERE id=?
		`, EncodeJSON(cfg), cfg.Enabled, cfg.Public, rebuild, rebuildInt, channelID)
		if err != nil {
			return err
		}
		if rebuild {
			if err := s.invalidateChannelPolicyTx(ctx, tx, channelID, cfg); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE wiki_albums SET pending=TRUE WHERE channel_id=?", channelID); err != nil {
				return err
			}
		}
		if err := s.Audit(ctx, tx, channelID, "configure", channelID, actor, cfg); err != nil {
			return err
		}
		if !cfg.Enabled || cfg.Paused {
			_, err := tx.ExecContext(ctx, `
				UPDATE wiki_jobs
				SET status='paused', lease_token=NULL, lease_until=NULL
				WHERE channel_id=? AND kind<>'page_edit' AND status IN ('running','pending','partial')
			`, channelID)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func shouldRebuildOnConfigChange(oldCfg, newCfg WikiConfig) bool {
	oldMap := decodeMapJSON([]byte(EncodeJSON(oldCfg)))
	newMap := decodeMapJSON([]byte(EncodeJSON(newCfg)))
	ignored := map[string]bool{"ai_review_enabled": true}
	if !newCfg.AIReviewEnabled {
		ignored["fact_check"] = true
		ignored["fact_check_required"] = true
	}
	for k, v := range newMap {
		if ignored[k] {
			continue
		}
		if EncodeJSON(oldMap[k]) != EncodeJSON(v) {
			return true
		}
	}
	return false
}

func (s *Store) invalidateChannelPolicyTx(ctx context.Context, tx *sql.Tx, channelID string, cfg WikiConfig) error {
	var histArg any
	if ht, ok := cfg.HistorySinceTime(); ok {
		histArg = db.MysqlDatetime6(ht)
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE wiki_sources SET excluded=(excluded OR
			(source_type='summary' AND ?=FALSE) OR
			(source_type='image' AND ?=FALSE) OR
			(source_type='message' AND NOT JSON_CONTAINS(?, JSON_QUOTE(category))) OR
			(? IS NOT NULL AND created_at < ?))
		WHERE channel_id=?
	`, cfg.IncludeSummaries, cfg.IncludeImages, EncodeJSON(cfg.MessageTypes), histArg, histArg, channelID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT IGNORE INTO wiki_dirty_pages SELECT ?, id FROM wiki_pages WHERE channel_id=?", channelID, channelID)
	return err
}

// SetCover 上传或移除频道封面图（image 为 nil 表示删除封面）。
func (s *Store) SetCover(ctx context.Context, channelID, digestHex string, image []byte, actor string) (bool, error) {
	var found bool
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		var cid string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM wiki_channels WHERE id=? FOR UPDATE", channelID).Scan(&cid); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		found = true
		if len(image) == 0 {
			res, err := tx.ExecContext(ctx, "DELETE FROM wiki_channel_covers WHERE channel_id=?", channelID)
			if err != nil {
				return err
			}
			if affected, _ := res.RowsAffected(); affected == 0 {
				return nil
			}
		} else {
			var prevDigest string
			err := tx.QueryRowContext(ctx, "SELECT digest FROM wiki_channel_covers WHERE channel_id=?", channelID).Scan(&prevDigest)
			if err == nil && prevDigest == digestHex {
				return nil
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			_, err = tx.ExecContext(ctx, `
				INSERT INTO wiki_channel_covers (channel_id, digest, image)
				VALUES (?, ?, ?)
				ON DUPLICATE KEY UPDATE digest=VALUES(digest), image=VALUES(image)
			`, channelID, digestHex, image)
			if err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE wiki_channels SET updated_at=UTC_TIMESTAMP(6) WHERE id=?", channelID); err != nil {
			return err
		}
		action := "upload_cover"
		if len(image) == 0 {
			action = "remove_cover"
		}
		return s.Audit(ctx, tx, channelID, action, channelID, actor, nil)
	})
	return found, err
}

// GetPublicCover 读取已公开频道的封面二进制数据与哈希摘要。
func (s *Store) GetPublicCover(ctx context.Context, channelKey, version string) (string, []byte, error) {
	ch, err := s.GetChannel(ctx, channelKey)
	if err != nil || ch == nil {
		return "", nil, err
	}
	var verArg any
	if version != "" {
		verArg = version
	}
	var digestHex string
	var image []byte
	q := fmt.Sprintf(`
		SELECT cover.digest, cover.image
		FROM wiki_channels c
		JOIN wiki_channel_covers cover ON cover.channel_id = c.id
		WHERE c.id = ? AND (? IS NULL OR cover.digest = ?) AND %s
	`, ChannelVisibleSQL("c"))
	err = s.exec(ctx).QueryRowContext(ctx, q, ch.ID, verArg, verArg).Scan(&digestHex, &image)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil
	}
	return digestHex, image, err
}

// PutSource 幂等写入或更新单条来源及其确定性分块，同时联动相册队列与频道脏时间戳（对齐 store.py:put_source）。
func (s *Store) PutSource(ctx context.Context, in PutSourceInput) (string, error) {
	if in.Category == "" {
		in.Category = "text"
	}
	sourceID := Digest(in.ChannelID, in.SourceType, in.ExternalID)
	mediaGroup := strings.TrimSpace(in.MediaGroup)
	var mediaKeyArg any
	if in.MediaKey != "" {
		mediaKeyArg = in.MediaKey
	}
	contentHash := Digest(in.Body, mediaKeyArg)
	createdAt := in.CreatedAt.UTC().Truncate(time.Microsecond)
	if createdAt.IsZero() {
		createdAt = NowUTC()
	}
	changedAt := createdAt
	if in.EditedAt != nil && !in.EditedAt.IsZero() {
		changedAt = in.EditedAt.UTC().Truncate(time.Microsecond)
	}

	var resultID string
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		var cid string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM wiki_channels WHERE id=? FOR UPDATE", in.ChannelID).Scan(&cid); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}

		old, err := sourceByIdentity(ctx, tx, in.ChannelID, in.SourceType, in.ExternalID, true)
		if err != nil {
			return err
		}
		if old != nil {
			sourceID = old.ID
			if old.Body == in.Body && old.MediaKey == in.MediaKey {
				contentHash = old.ContentHash
			}
			if old.Deleted ||
				(changedAt.Before(old.EditedAt) && (old.Verified || !in.Verified)) ||
				(old.Verified && !in.Verified) {
				resultID = sourceID
				return nil
			}
		}

		if in.Verified && in.MediaKey != "" && in.MessageID != nil && *in.MessageID > 0 && in.SourceType == "message" && mediaGroup == "" {
			group := fmt.Sprintf("%s%d", MessageMediaPrefix, *in.MessageID)
			if (!in.Excluded && strings.TrimSpace(in.Body) != "") || (old != nil && old.MediaGroup == group) {
				mediaGroup = group
			}
		}
		if in.MediaKey != "" && in.MessageID != nil && in.SourceType == "image" {
			var parentGroup sql.NullString
			pErr := tx.QueryRowContext(ctx, `
				SELECT media_group FROM wiki_sources
				WHERE channel_id=? AND source_type='message' AND external_id=?
				  AND media_key=? AND verified=TRUE AND deleted=FALSE
			`, in.ChannelID, fmt.Sprintf("%d", *in.MessageID), in.MediaKey).Scan(&parentGroup)
			if pErr == nil && parentGroup.Valid && parentGroup.String != "" {
				mediaGroup = parentGroup.String
			}
		}

		if old != nil &&
			old.ContentHash == contentHash &&
			old.Verified == in.Verified &&
			old.Excluded == in.Excluded &&
			old.Category == in.Category &&
			old.MediaGroup == mediaGroup {
			if in.Verified {
				var reasonArg any
				if in.ExclusionReason != "" {
					reasonArg = in.ExclusionReason
				}
				_, err := tx.ExecContext(ctx, `
					UPDATE wiki_sources
					SET observed_at=UTC_TIMESTAMP(6), exclusion_reason=COALESCE(?, exclusion_reason)
					WHERE id=?
				`, reasonArg, sourceID)
				if err != nil {
					return err
				}
			}
			resultID = sourceID
			return nil
		}

		var msgIDArg, groupArg any
		if in.MessageID != nil {
			msgIDArg = *in.MessageID
		}
		if mediaGroup != "" {
			groupArg = mediaGroup
		}

		_, err = tx.ExecContext(ctx, `
			INSERT INTO wiki_sources
			(id, channel_id, source_type, external_id, message_id, body, original_length, content_hash,
			 created_at, edited_at, observed_at, verified, excluded, media_group, category, media_key, exclusion_reason)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(6), ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				body = VALUES(body),
				original_length = VALUES(original_length),
				created_at = VALUES(created_at),
				content_hash = VALUES(content_hash),
				revision = revision + 1,
				edited_at = VALUES(edited_at),
				observed_at = VALUES(observed_at),
				verified = VALUES(verified),
				excluded = VALUES(excluded),
				media_group = VALUES(media_group),
				category = VALUES(category),
				media_key = VALUES(media_key),
				exclusion_reason = VALUES(exclusion_reason)
		`,
			sourceID, in.ChannelID, in.SourceType, in.ExternalID, msgIDArg,
			in.Body, in.OriginalLength, contentHash,
			db.MysqlDatetime6(createdAt), db.MysqlDatetime6(changedAt),
			in.Verified, in.Excluded, groupArg, in.Category, mediaKeyArg, in.ExclusionReason,
		)
		if err != nil {
			return err
		}

		if in.SourceType == "message" && mediaGroup != "" && in.MessageID != nil && in.MediaKey != "" {
			rows, err := tx.QueryContext(ctx, `
				SELECT id, media_group FROM wiki_sources
				WHERE channel_id=? AND message_id=? AND source_type='image' AND media_key=?
				  AND NOT (media_group <=> ?)
			`, in.ChannelID, *in.MessageID, in.MediaKey, mediaGroup)
			if err != nil {
				return err
			}
			type imgPair struct{ id, group string }
			var imgs []imgPair
			for rows.Next() {
				var id string
				var grp sql.NullString
				if err := rows.Scan(&id, &grp); err != nil {
					_ = rows.Close()
					return err
				}
				imgs = append(imgs, imgPair{id: id, group: grp.String})
			}
			_ = rows.Close()
			for _, img := range imgs {
				if _, err := tx.ExecContext(ctx, "UPDATE wiki_sources SET media_group=? WHERE id=?", mediaGroup, img.id); err != nil {
					return err
				}
				if img.group != "" {
					if err := markAlbumTx(ctx, tx, in.ChannelID, img.group); err != nil {
						return err
					}
				}
			}
		}

		if old == nil || old.ContentHash != contentHash {
			if err := insertSourceChunks(ctx, tx, sourceID, in.Body, contentHash); err != nil {
				return err
			}
		}

		groups := map[string]bool{}
		if mediaGroup != "" {
			groups[mediaGroup] = true
		}
		if old != nil && old.MediaGroup != "" {
			groups[old.MediaGroup] = true
		}
		for grp := range groups {
			if err := markAlbumTx(ctx, tx, in.ChannelID, grp); err != nil {
				return err
			}
		}

		if !in.Excluded || (old != nil && !old.Excluded) {
			if _, err := tx.ExecContext(ctx, `
				UPDATE wiki_channels
				SET source_generation=source_generation+1, dirty_at=UTC_TIMESTAMP(6)
				WHERE id=?
			`, in.ChannelID); err != nil {
				return err
			}
		}
		resultID = sourceID
		return nil
	})
	return resultID, err
}

func markAlbumTx(ctx context.Context, tx *sql.Tx, channelID, group string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO wiki_albums (channel_id, media_group)
		VALUES (?, ?)
		ON DUPLICATE KEY UPDATE pending=TRUE
	`, channelID, group)
	return err
}

func sourceByIdentity(ctx context.Context, exec sqlExecutor, channelID, kind, externalID string, forUpdate bool) (*SourceRecord, error) {
	query := `
		SELECT id, channel_id, source_type, external_id, category, message_id, body,
		       original_length, content_hash, revision, created_at, edited_at, observed_at,
		       verified, deleted, hidden, excluded, media_group, media_key, exclusion_reason
		FROM wiki_sources WHERE channel_id=? AND source_type=? AND external_id=?`
	if forUpdate {
		query += " FOR UPDATE"
	}
	rec, err := scanSourceRow(exec.QueryRowContext(ctx, query, channelID, kind, externalID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return rec, err
}

func scanSourceRow(sc interface{ Scan(...any) error }) (*SourceRecord, error) {
	var (
		r                  SourceRecord
		mid                sql.NullInt64
		mGroup, mKey       sql.NullString
		created, edit, obs any
	)
	err := sc.Scan(
		&r.ID, &r.ChannelID, &r.SourceType, &r.ExternalID, &r.Category, &mid, &r.Body,
		&r.OriginalLength, &r.ContentHash, &r.Revision, &created, &edit, &obs,
		&r.Verified, &r.Deleted, &r.Hidden, &r.Excluded, &mGroup, &mKey, &r.ExclusionReason,
	)
	if err != nil {
		return nil, err
	}
	if mid.Valid {
		v := mid.Int64
		r.MessageID = &v
	}
	r.MediaGroup = mGroup.String
	r.MediaKey = mKey.String
	if t := scanNullableTime(created); t != nil {
		r.CreatedAt = *t
	}
	if t := scanNullableTime(edit); t != nil {
		r.EditedAt = *t
	}
	if t := scanNullableTime(obs); t != nil {
		r.ObservedAt = *t
	}
	return &r, nil
}

// GetSource 按主键查询单条 wiki_sources 行。
func (s *Store) GetSource(ctx context.Context, sourceID string) (*SourceRecord, error) {
	row := s.exec(ctx).QueryRowContext(ctx, `
		SELECT id, channel_id, source_type, external_id, category, message_id, body,
		       original_length, content_hash, revision, created_at, edited_at, observed_at,
		       verified, deleted, hidden, excluded, media_group, media_key, exclusion_reason
		FROM wiki_sources WHERE id=?
	`, sourceID)
	rec, err := scanSourceRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return rec, err
}

// MarkMessageDeleted 将指定 Telegram 消息及其关联图片写入删除墓碑，并立刻失效所有引用页面（对齐 store.py:revoke(deleted=True)）。
func (s *Store) MarkMessageDeleted(ctx context.Context, channelID string, messageIDs []int64, actor string) error {
	if len(messageIDs) == 0 {
		return nil
	}
	return s.revokeInternal(ctx, channelID, "", messageIDs, actor, true)
}

// ExcludeSource 隐藏指定来源并立即使引用该来源的所有公开修订失效（对齐 store.py:revoke(source_id=...)）。
func (s *Store) ExcludeSource(ctx context.Context, channelID, sourceID, actor string) error {
	if sourceID == "" {
		return errors.New("source_id is required")
	}
	return s.revokeInternal(ctx, channelID, sourceID, nil, actor, false)
}

func (s *Store) revokeInternal(ctx context.Context, channelID, sourceID string, messageIDs []int64, actor string, deleted bool) error {
	if actor == "" {
		actor = "telegram"
	}
	deletedHash := Digest("deleted")
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		var cid string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM wiki_channels WHERE id=? FOR UPDATE", channelID).Scan(&cid); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		var changed int64
		if sourceID != "" {
			res, err := tx.ExecContext(ctx, "UPDATE wiki_sources SET hidden=TRUE WHERE id=? AND channel_id=? AND hidden=FALSE", sourceID, channelID)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			changed += n
		} else {
			for _, mid := range messageIDs {
				for _, kind := range []string{"message", "image"} {
					sid := Digest(channelID, kind, fmt.Sprintf("%d", mid))
					res, err := tx.ExecContext(ctx, `
						INSERT INTO wiki_sources
						(id, channel_id, source_type, external_id, message_id, body, content_hash, created_at, edited_at, observed_at, deleted)
						VALUES (?, ?, ?, ?, ?, '', ?, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6), UTC_TIMESTAMP(6), TRUE)
						ON DUPLICATE KEY UPDATE
							revision = revision + IF(deleted=FALSE, 1, 0),
							deleted = TRUE,
							body = '',
							content_hash = VALUES(content_hash)
					`, sid, channelID, kind, fmt.Sprintf("%d", mid), mid, deletedHash)
					if err != nil {
						return err
					}
					n, _ := res.RowsAffected()
					changed += n
				}
			}
		}
		if changed == 0 {
			return nil
		}

		if sourceID != "" {
			_, err := tx.ExecContext(ctx, `
				UPDATE wiki_albums a
				JOIN wiki_sources s ON s.channel_id=a.channel_id AND s.media_group=a.media_group
				SET a.pending=TRUE
				WHERE s.channel_id=? AND s.id=?
			`, channelID, sourceID)
			if err != nil {
				return err
			}
		} else if len(messageIDs) > 0 {
			marks, mArgs := int64Placeholders(messageIDs)
			args := append([]any{channelID}, mArgs...)
			_, err := tx.ExecContext(ctx, fmt.Sprintf(`
				UPDATE wiki_albums a
				JOIN wiki_sources s ON s.channel_id=a.channel_id AND s.media_group=a.media_group
				SET a.pending=TRUE
				WHERE s.channel_id=? AND s.message_id IN (%s)
			`, marks), args...)
			if err != nil {
				return err
			}
		}

		if _, err := tx.ExecContext(ctx, "UPDATE wiki_channels SET source_generation=source_generation+1, dirty_at=UTC_TIMESTAMP(6) WHERE id=?", channelID); err != nil {
			return err
		}

		if sourceID != "" {
			_, err := tx.ExecContext(ctx, `
				INSERT IGNORE INTO wiki_dirty_pages
				SELECT ?, f.page_id FROM wiki_facts f
				JOIN wiki_chunks ch ON ch.id=f.chunk_id
				WHERE ch.source_id=?
			`, channelID, sourceID)
			if err != nil {
				return err
			}
		} else if len(messageIDs) > 0 {
			marks, mArgs := int64Placeholders(messageIDs)
			args := append([]any{channelID, channelID}, mArgs...)
			_, err := tx.ExecContext(ctx, fmt.Sprintf(`
				INSERT IGNORE INTO wiki_dirty_pages
				SELECT DISTINCT ?, f.page_id FROM wiki_facts f
				JOIN wiki_chunks ch ON ch.id=f.chunk_id
				JOIN wiki_sources s ON s.id=ch.source_id
				WHERE s.channel_id=? AND s.message_id IN (%s)
			`, marks), args...)
			if err != nil {
				return err
			}
		}

		action := "hide_source"
		if deleted {
			action = "delete_source"
		}
		msgs := messageIDs
		if msgs == nil {
			msgs = []int64{}
		}
		return s.Audit(ctx, tx, channelID, action, sourceID, actor, map[string]any{"messages": msgs})
	})
}

// HasLegacyCaptionMedia 检查是否存在尚未归组为 message:<id> 的旧版单条图文消息。
func (s *Store) HasLegacyCaptionMedia(ctx context.Context, channelID string) (bool, error) {
	var one int
	err := s.exec(ctx).QueryRowContext(ctx, "SELECT 1 "+legacyCaptionMediaSQL+" LIMIT 1", channelID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && one == 1, err
}

// GroupLegacyCaptionMedia 批量把旧版单贴配文与识别图绑定到 message:<id> 相册分组。
func (s *Store) GroupLegacyCaptionMedia(ctx context.Context, job *JobRecord) error {
	for {
		var done bool
		err := s.WithTx(ctx, func(tx *sql.Tx) error {
			var cid string
			if err := tx.QueryRowContext(ctx, "SELECT id FROM wiki_channels WHERE id=? FOR UPDATE", job.ChannelID).Scan(&cid); err != nil {
				return err
			}
			if err := s.FenceJob(ctx, tx, job); err != nil {
				return err
			}
			rows, err := tx.QueryContext(ctx, `SELECT m.id, i.id, m.message_id `+legacyCaptionMediaSQL+` ORDER BY m.message_id LIMIT 200`, job.ChannelID)
			if err != nil {
				return err
			}
			type pair struct {
				caption string
				image   string
				msgID   int64
			}
			var pairs []pair
			for rows.Next() {
				var p pair
				if err := rows.Scan(&p.caption, &p.image, &p.msgID); err != nil {
					_ = rows.Close()
					return err
				}
				pairs = append(pairs, p)
			}
			_ = rows.Close()
			if len(pairs) == 0 {
				done = true
				return nil
			}
			for _, p := range pairs {
				grp := fmt.Sprintf("%s%d", MessageMediaPrefix, p.msgID)
				if _, err := tx.ExecContext(ctx, "UPDATE wiki_sources SET media_group=? WHERE id IN (?,?)", grp, p.caption, p.image); err != nil {
					return err
				}
				if err := markAlbumTx(ctx, tx, job.ChannelID, grp); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil || done {
			return err
		}
	}
}

// AlbumSources 读取指定相册下全部已核实且可见的文本与图片源。
func (s *Store) AlbumSources(ctx context.Context, exec sqlExecutor, channelID, group string) ([]SourceRecord, error) {
	if exec == nil {
		exec = s.exec(ctx)
	}
	rows, err := exec.QueryContext(ctx, `
		SELECT s.id, s.channel_id, s.source_type, s.external_id, s.category, s.message_id, s.body,
		       s.original_length, s.content_hash, s.revision, s.created_at, s.edited_at, s.observed_at,
		       s.verified, s.deleted, s.hidden, s.excluded, s.media_group, s.media_key, s.exclusion_reason
		FROM wiki_sources s
		WHERE s.channel_id=? AND s.media_group=?
		  AND s.source_type IN ('message','image')
		  AND s.verified=TRUE AND s.deleted=FALSE AND s.hidden=FALSE AND s.excluded=FALSE
		  AND (s.source_type='message' OR EXISTS (
		      SELECT 1 FROM wiki_sources parent
		      WHERE parent.channel_id=s.channel_id AND parent.source_type='message'
		        AND parent.message_id=s.message_id AND parent.media_key=s.media_key
		        AND parent.verified=TRUE AND parent.deleted=FALSE AND parent.hidden=FALSE AND parent.excluded=FALSE
		  ))
		ORDER BY s.message_id, s.source_type DESC, s.id
	`, channelID, group)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []SourceRecord
	for rows.Next() {
		rec, err := scanSourceRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

// SourceBatch 分窗枚举有效来源（供主题规划与增量提取使用）。
func (s *Store) SourceBatch(ctx context.Context, channelID, after string, limit int, pageID, group string) ([]SourceRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	cols := `s.id, s.channel_id, s.source_type, s.external_id, s.category, s.message_id, s.body,
		s.original_length, s.content_hash, s.revision, s.created_at, s.edited_at, s.observed_at,
		s.verified, s.deleted, s.hidden, s.excluded, s.media_group, s.media_key, s.exclusion_reason`
	var (
		q    string
		args []any
	)
	if pageID != "" {
		q = `SELECT ` + cols + ` FROM wiki_sources s
			WHERE s.channel_id=? AND s.id>? AND s.deleted=FALSE AND s.hidden=FALSE AND s.excluded=FALSE
			AND EXISTS (SELECT 1 FROM wiki_facts f JOIN wiki_chunks ch ON ch.id=f.chunk_id WHERE ch.source_id=s.id AND f.page_id=?)
			ORDER BY s.id LIMIT ?`
		args = []any{channelID, after, pageID, limit}
	} else {
		filter := ""
		switch group {
		case "summary":
			filter = " AND s.source_type='summary'"
		case "sources":
			filter = " AND s.source_type<>'summary'"
		}
		q = `SELECT ` + cols + ` FROM wiki_sources s
			WHERE s.channel_id=? AND s.id>? AND s.deleted=FALSE AND s.hidden=FALSE AND s.excluded=FALSE` + filter + `
			ORDER BY s.id LIMIT ?`
		args = []any{channelID, after, limit}
	}
	rows, err := s.exec(ctx).QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SourceRecord
	for rows.Next() {
		rec, err := scanSourceRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

// NormalizeJobKind 归一任务类型名称（兼容 bootstrap->backfill 与 rebuild->reindex）。
func NormalizeJobKind(kind string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "bootstrap", "backfill":
		return "backfill", nil
	case "rebuild", "reindex":
		return "reindex", nil
	case "update", "reconcile", "publish", "page", "proposal", "albums":
		return strings.ToLower(strings.TrimSpace(kind)), nil
	default:
		return "", fmt.Errorf("Unknown Wiki job kind: %q", kind)
	}
}

// EnqueueJob 将频道后台构建任务排入队列（若已有活动任务且 requireNew=false 则返回既有任务 ID）。
func (s *Store) EnqueueJob(ctx context.Context, tx *sql.Tx, channelID, kind, actor, pageID string, requireNew bool) (string, error) {
	normKind, err := NormalizeJobKind(kind)
	if err != nil {
		return "", err
	}
	if tx == nil {
		var jid string
		err := s.WithTx(ctx, func(newTx *sql.Tx) error {
			var e error
			jid, e = s.EnqueueJob(ctx, newTx, channelID, normKind, actor, pageID, requireNew)
			return e
		})
		return jid, err
	}

	var enabled bool
	var activeJob sql.NullString
	var cfgRaw []byte
	if err := tx.QueryRowContext(ctx, "SELECT enabled, active_job, config FROM wiki_channels WHERE id=? FOR UPDATE", channelID).Scan(&enabled, &activeJob, &cfgRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", errors.New("Wiki is disabled")
		}
		return "", err
	}
	if !enabled {
		return "", errors.New("Wiki is disabled")
	}
	if normKind == "page" {
		var pid string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM wiki_pages WHERE id=? AND channel_id=?", pageID, channelID).Scan(&pid); err != nil {
			return "", errors.New("A page in this channel is required")
		}
	}
	if activeJob.Valid && activeJob.String != "" {
		if requireNew {
			var st string
			_ = tx.QueryRowContext(ctx, "SELECT status FROM wiki_jobs WHERE id=?", activeJob.String).Scan(&st)
			if st == "paused" {
				return "", errors.New("本频道有已暂停的任务。请恢复或取消该任务后再启动新任务。")
			}
			return "", errors.New("本频道已有任务在处理，请在任务列表查看进度。")
		}
		return activeJob.String, nil
	}
	cfg, _ := ParseWikiConfig(cfgRaw)
	if requireNew && cfg.Paused {
		return "", errors.New("频道更新已暂停，请关闭「暂停自动更新」后再启动任务。")
	}

	jobID := NewID()
	cursor := map[string]any{"page_id": nilIfEmpty(pageID)}
	progress := map[string]any{
		"enumerated":          0,
		"processed":           0,
		"excluded":            0,
		"input_tokens":        0,
		"output_tokens":       0,
		"cache_hits":          0,
		"generation_complete": false,
		"review_complete":     false,
		"published":           false,
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO wiki_jobs (id,channel_id,kind,`cursor`,progress,next_attempt,created_at,updated_at) VALUES (?,?,?,?,?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))",
		jobID, channelID, normKind, EncodeJSON(cursor), EncodeJSON(progress))
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE wiki_channels SET active_job=? WHERE id=?", jobID, channelID); err != nil {
		return "", err
	}
	if err := s.Audit(ctx, tx, channelID, "enqueue", jobID, actor, map[string]any{"kind": normKind}); err != nil {
		return "", err
	}
	return jobID, nil
}

// EnqueuePageEdit 排入独立单页修订审校/发布任务（不阻塞频道主 active_job）。
func (s *Store) EnqueuePageEdit(ctx context.Context, tx *sql.Tx, channelID, pageID, revisionID, baseRevision, actor string) (string, error) {
	if tx == nil {
		var jid string
		err := s.WithTx(ctx, func(newTx *sql.Tx) error {
			var e error
			jid, e = s.EnqueuePageEdit(ctx, newTx, channelID, pageID, revisionID, baseRevision, actor)
			return e
		})
		return jid, err
	}
	jobID := NewID()
	cursor := map[string]any{
		"page_id":       pageID,
		"revisions":     map[string]any{pageID: revisionID},
		"base_revision": nilIfEmpty(baseRevision),
	}
	progress := map[string]any{
		"generation_complete": true,
		"published":           false,
		"pages":               1,
		"activity":            "等待词条审校",
		"input_tokens":        0,
		"output_tokens":       0,
		"cache_hits":          0,
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO wiki_jobs (id,channel_id,kind,phase,`cursor`,progress,next_attempt,created_at,updated_at) VALUES (?,?,'page_edit','review',?,?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))",
		jobID, channelID, EncodeJSON(cursor), EncodeJSON(progress))
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE wiki_page_revisions SET job_id=? WHERE id=?", jobID, revisionID); err != nil {
		return "", err
	}
	if err := s.Audit(ctx, tx, channelID, "enqueue", jobID, actor, map[string]any{"kind": "page_edit", "page_id": pageID}); err != nil {
		return "", err
	}
	return jobID, nil
}

// ClaimJob 使用 FOR UPDATE SKIP LOCKED 认领一条可执行任务并授予 90 秒租约。
func (s *Store) ClaimJob(ctx context.Context) (*JobRecord, error) {
	var claimed *JobRecord
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		var jobID string
		err := tx.QueryRowContext(ctx, `
			SELECT j.id FROM wiki_jobs j
			JOIN wiki_channels c ON c.id = j.channel_id
			WHERE c.enabled = TRUE AND (
				(j.kind = 'page_edit' AND NOT EXISTS (
					SELECT 1 FROM wiki_jobs running
					WHERE running.channel_id = j.channel_id AND running.kind = 'page_edit'
					  AND running.status = 'running' AND running.lease_until >= UTC_TIMESTAMP(6)
				))
				OR (c.active_job = j.id AND COALESCE(JSON_EXTRACT(c.config,'$.paused') = FALSE, TRUE))
			) AND (
				(j.status IN ('pending','partial') AND j.next_attempt <= UTC_TIMESTAMP(6))
				OR (j.status = 'running' AND j.lease_until < UTC_TIMESTAMP(6))
			)
			ORDER BY (j.kind = 'page_edit') DESC, c.public DESC, j.created_at
			LIMIT 1 FOR UPDATE SKIP LOCKED
		`).Scan(&jobID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}

		leaseToken := NewID()
		_, err = tx.ExecContext(ctx, fmt.Sprintf(`
			UPDATE wiki_jobs
			SET status='running', error_code=NULL,
			    progress=JSON_SET(progress, '$.error_detail', '', '$.reserved_tokens', 0, '$.active_requests', JSON_OBJECT()),
			    lease_token=?,
			    lease_until=DATE_ADD(UTC_TIMESTAMP(6), INTERVAL %d SECOND),
			    updated_at=UTC_TIMESTAMP(6)
			WHERE id=?
		`, JobLeaseSeconds), leaseToken, jobID)
		if err != nil {
			return err
		}

		job, err := getJobTx(ctx, tx, jobID)
		if err != nil {
			return err
		}
		claimed = job
		return nil
	})
	return claimed, err
}

func getJobTx(ctx context.Context, exec sqlExecutor, jobID string) (*JobRecord, error) {
	var (
		j                                    JobRecord
		curRaw, progRaw                      []byte
		leaseTok, errCode                    sql.NullString
		leaseUntil, nextAtt, created, update any
	)
	err := exec.QueryRowContext(ctx, "SELECT id,channel_id,kind,status,phase,`cursor`,progress,attempts,lease_token,lease_until,next_attempt,error_code,created_at,updated_at FROM wiki_jobs WHERE id=?", jobID).Scan(
		&j.ID, &j.ChannelID, &j.Kind, &j.Status, &j.Phase, &curRaw, &progRaw,
		&j.Attempts, &leaseTok, &leaseUntil, &nextAtt, &errCode, &created, &update,
	)
	if err != nil {
		return nil, err
	}
	j.LeaseToken = leaseTok.String
	j.ErrorCode = errCode.String
	j.LeaseUntil = scanNullableTime(leaseUntil)
	if t := scanNullableTime(nextAtt); t != nil {
		j.NextAttempt = *t
	}
	if t := scanNullableTime(created); t != nil {
		j.CreatedAt = *t
	}
	if t := scanNullableTime(update); t != nil {
		j.UpdatedAt = *t
	}
	j.Cursor = decodeMapJSON(curRaw)
	j.Progress = decodeMapJSON(progRaw)
	return &j, nil
}

// FenceJob 在写事务内校验当前 Worker 仍持有未过期的有效租约，否则返回 ErrLeaseLost。
func (s *Store) FenceJob(ctx context.Context, tx *sql.Tx, job *JobRecord) error {
	var id string
	err := tx.QueryRowContext(ctx, `
		SELECT id FROM wiki_jobs
		WHERE id=? AND lease_token=? AND status='running' AND lease_until > UTC_TIMESTAMP(6)
		FOR UPDATE
	`, job.ID, job.LeaseToken).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLeaseLost
		}
		return err
	}
	return nil
}

// HeartbeatJob 续租当前运行中的任务租约 90 秒；若租约已丢失返回 ErrLeaseLost。
func (s *Store) HeartbeatJob(ctx context.Context, job *JobRecord) error {
	res, err := s.DB.ExecContext(ctx, fmt.Sprintf(`
		UPDATE wiki_jobs
		SET lease_until = DATE_ADD(UTC_TIMESTAMP(6), INTERVAL %d SECOND)
		WHERE id=? AND lease_token=? AND status='running' AND lease_until > UTC_TIMESTAMP(6)
	`, JobLeaseSeconds), job.ID, job.LeaseToken)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrLeaseLost
	}
	return nil
}

// CheckpointJob 持租约围栏原子持久化任务游标与进度。
func (s *Store) CheckpointJob(ctx context.Context, job *JobRecord, phase string, progressOnly bool) error {
	job.checkpoint.Lock()
	defer job.checkpoint.Unlock()

	job.Mu.Lock()
	if phase != "" {
		job.Phase = phase
	}
	curPhase := job.Phase
	curJSON := EncodeJSON(job.Cursor)
	progJSON := EncodeJSON(job.Progress)
	job.Mu.Unlock()

	return s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := s.FenceJob(ctx, tx, job); err != nil {
			return err
		}
		if progressOnly {
			_, err := tx.ExecContext(ctx, "UPDATE wiki_jobs SET progress=?, updated_at=UTC_TIMESTAMP(6) WHERE id=?", progJSON, job.ID)
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE wiki_jobs SET phase=?, `cursor`=?, progress=?, updated_at=UTC_TIMESTAMP(6) WHERE id=?",
			curPhase, curJSON, progJSON, job.ID)
		return err
	})
}

// ReportJob 更新任务人类可读活动描述、追加最近 12 条事件流并持久化检查点。
func (s *Store) ReportJob(ctx context.Context, job *JobRecord, activity string, progressOnly bool, details map[string]any) error {
	job.Mu.Lock()
	prevActivity, _ := job.Progress["activity"].(string)
	if activity != "" && activity != prevActivity {
		var events []any
		if existing, ok := job.Progress["events"].([]any); ok {
			events = existing
		}
		events = append(events, map[string]any{
			"time": FormatISO(NowUTC()),
			"text": activity,
		})
		if len(events) > 12 {
			events = events[len(events)-12:]
		}
		job.Progress["events"] = events
	}
	if activity != "" {
		job.Progress["activity"] = activity
	}
	for k, v := range details {
		job.Progress[k] = v
	}
	job.Mu.Unlock()
	return s.CheckpointJob(ctx, job, "", progressOnly)
}

// ReconcileManualReviews 核对被 AI 审校拦截但已被管理员人工批准的修订。
func (s *Store) ReconcileManualReviews(ctx context.Context, tx *sql.Tx, cursor, progress map[string]any) (bool, error) {
	remaining := mapInt(progress, "review_rejections")
	if remaining <= 0 {
		return false, nil
	}
	rejected := mapStringSlice(cursor["review_rejected_revisions"])
	if len(rejected) == 0 {
		revMap := extractRevisionMap(cursor)
		if len(revMap) == 0 {
			return false, nil
		}
		var revIDs []string
		for _, rid := range revMap {
			revIDs = append(revIDs, rid)
		}
		marks, rArgs := stringPlaceholders(revIDs)
		policy, _ := cursor["review_policy"].(string)
		args := append(rArgs, policy)
		rows, err := tx.QueryContext(ctx, fmt.Sprintf(`
			SELECT rv.revision_id FROM wiki_revision_reviews rv
			WHERE rv.revision_id IN (%s) AND rv.policy_key=?
			GROUP BY rv.revision_id HAVING MAX(rv.approved)=FALSE
		`, marks), args...)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var rid string
			if err := rows.Scan(&rid); err == nil {
				rejected = append(rejected, rid)
			}
		}
		_ = rows.Close()
		if len(rejected) != remaining {
			return false, nil
		}
	}
	if len(rejected) == 0 {
		return false, nil
	}

	marks, rArgs := stringPlaceholders(rejected)
	args := append(rArgs, ManualReviewPolicy)
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`
		SELECT DISTINCT rv.revision_id FROM wiki_revision_reviews rv
		WHERE rv.revision_id IN (%s) AND rv.policy_key=? AND rv.approved=TRUE
	`, marks), args...)
	if err != nil {
		return false, err
	}
	approved := map[string]bool{}
	for rows.Next() {
		var rid string
		if err := rows.Scan(&rid); err == nil {
			approved[rid] = true
		}
	}
	_ = rows.Close()
	if len(approved) == 0 {
		return false, nil
	}

	var nextRejected []string
	for _, rid := range rejected {
		if !approved[rid] {
			nextRejected = append(nextRejected, rid)
		}
	}
	cursor["review_rejected_revisions"] = nextRejected
	progress["review_complete"] = len(nextRejected) == 0
	progress["review_rejections"] = len(nextRejected)
	if len(nextRejected) == 0 {
		progress["activity"] = "审校已完成，等待发布"
	}
	return true, nil
}

// CompleteJob 正常收尾已完成或已发布的任务（释放租约与频道 active_job）。
func (s *Store) CompleteJob(ctx context.Context, job *JobRecord, published bool) error {
	status := "completed"
	if published {
		status = "published"
	}
	return s.FinishJob(ctx, job, status, "", 0)
}

// FailJob 记录一次任务执行失败，按指数退避 min(3600, 30*2^(attempts-1)) 安排重试，超出 maxAttempts 后转入终态 failed。
func (s *Store) FailJob(ctx context.Context, job *JobRecord, errCode string, maxAttempts int, overrideDelaySec int) error {
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxJobAttempts
	}
	nextAttemptNum := job.Attempts + 1
	if nextAttemptNum >= maxAttempts {
		return s.FinishJob(ctx, job, "failed", errCode, 0)
	}
	delay := overrideDelaySec
	if delay <= 0 {
		shift := max(0, nextAttemptNum-1)
		if shift > 10 {
			shift = 10
		}
		delay = min(3600, 30*(1<<shift))
	}
	return s.FinishJob(ctx, job, "partial", errCode, delay)
}

// FinishJob 通用终态/挂起状态写入（对齐 core/wiki/store.py:finish）。
func (s *Store) FinishJob(ctx context.Context, job *JobRecord, status, errCode string, delaySec int) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		var cid string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM wiki_channels WHERE id=? FOR UPDATE", job.ChannelID).Scan(&cid); err != nil {
			return err
		}
		if err := s.FenceJob(ctx, tx, job); err != nil {
			return err
		}

		job.Mu.Lock()
		cursor := cloneMap(job.Cursor)
		progress := cloneMap(job.Progress)
		job.Mu.Unlock()

		if status == "completed" && job.Phase == "ready" {
			reconciled, err := s.ReconcileManualReviews(ctx, tx, cursor, progress)
			if err != nil {
				return err
			}
			if reconciled && job.Kind == "page_edit" && mapBool(progress, "review_complete") {
				var mode sql.NullString
				_ = tx.QueryRowContext(ctx, "SELECT JSON_UNQUOTE(JSON_EXTRACT(config,'$.publication_mode')) FROM wiki_channels WHERE id=?", job.ChannelID).Scan(&mode)
				if mode.String == "auto" {
					status = "pending"
				}
			}
		}

		if status == "completed" || status == "published" {
			trimmed := make(map[string]any)
			for _, k := range terminalCursorKeys {
				if v, ok := cursor[k]; ok {
					trimmed[k] = v
				}
			}
			cursor = trimmed
		}

		incAttempt := 0
		var errArg any
		if errCode != "" {
			errArg = errCode
			if status != "paused" {
				incAttempt = 1
			}
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE wiki_jobs
			SET status=?, error_code=?, attempts=attempts+?,
			    `+"`cursor`"+`=?, progress=?, lease_token=NULL, lease_until=NULL,
			    next_attempt=DATE_ADD(UTC_TIMESTAMP(6), INTERVAL ? SECOND),
			    updated_at=UTC_TIMESTAMP(6)
			WHERE id=?
		`, status, errArg, incAttempt, EncodeJSON(cursor), EncodeJSON(progress), delaySec, job.ID)
		if err != nil {
			return err
		}

		if job.Kind != "page_edit" && (status == "completed" || status == "published" || status == "failed") {
			if _, err := tx.ExecContext(ctx, "UPDATE wiki_channels SET active_job=NULL WHERE id=? AND active_job=?", job.ChannelID, job.ID); err != nil {
				return err
			}
		}
		if job.Kind != "page_edit" && status == "failed" {
			if _, err := tx.ExecContext(ctx, "UPDATE wiki_channels SET dirty_at=NULL, scheduled_at=UTC_TIMESTAMP(6) WHERE id=?", job.ChannelID); err != nil {
				return err
			}
		}
		return nil
	})
}

// CancelJob 取消已暂停的频道任务。
func (s *Store) CancelJob(ctx context.Context, channelID, jobID, actor string) error {
	return s.ControlJob(ctx, channelID, jobID, "cancel", actor)
}

// ControlJob 执行管理员对任务的 pause / resume / retry / cancel 控制（对齐 store.py:control_job）。
func (s *Store) ControlJob(ctx context.Context, channelID, jobID, action, actor string) error {
	switch action {
	case "pause", "resume", "retry", "cancel":
	default:
		return errors.New("Unknown job action")
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		var activeJob sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT active_job FROM wiki_channels WHERE id=? FOR UPDATE", channelID).Scan(&activeJob); err != nil {
			return errors.New("Unknown job")
		}
		var kind, status string
		var progRaw []byte
		if err := tx.QueryRowContext(ctx, "SELECT kind, status, progress FROM wiki_jobs WHERE id=? AND channel_id=? FOR UPDATE", jobID, channelID).Scan(&kind, &status, &progRaw); err != nil {
			return errors.New("Unknown job")
		}

		isPageEdit := kind == "page_edit"
		if status == "cancelled" {
			if action == "cancel" {
				return nil
			}
			return errors.New("该任务已取消，请启动新任务")
		}
		if !isPageEdit && activeJob.Valid && activeJob.String != "" && activeJob.String != jobID {
			return errors.New("本频道已有其他任务")
		}
		if status == "completed" || status == "published" {
			return errors.New("任务已结束，请启动新任务")
		}

		progress := decodeMapJSON(progRaw)
		progress["active_requests"] = map[string]any{}
		if action == "cancel" {
			if isPageEdit || status != "paused" {
				return errors.New("仅可取消已暂停的频道任务")
			}
			progress["activity"] = "已取消"
			progress["error_detail"] = ""
			progress["reserved_tokens"] = 0
			if _, err := tx.ExecContext(ctx, `
				UPDATE wiki_jobs
				SET status='cancelled', lease_token=NULL, lease_until=NULL, error_code=NULL,
				    updated_at=UTC_TIMESTAMP(6), progress=?
				WHERE id=?
			`, EncodeJSON(progress), jobID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE wiki_channels SET active_job=NULL, scheduled_at=UTC_TIMESTAMP(6) WHERE id=? AND active_job=?", channelID, jobID); err != nil {
				return err
			}
			return s.Audit(ctx, tx, channelID, action, jobID, actor, nil)
		}

		if action != "pause" {
			progress["error_detail"] = ""
			progress["activity"] = "等待后台执行"
		}
		newStatus := "pending"
		if action == "pause" {
			newStatus = "paused"
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE wiki_jobs
			SET status=?, lease_token=NULL, lease_until=NULL, attempts=0, error_code=NULL,
			    next_attempt=UTC_TIMESTAMP(6), updated_at=UTC_TIMESTAMP(6), progress=?
			WHERE id=?
		`, newStatus, EncodeJSON(progress), jobID); err != nil {
			return err
		}
		if !isPageEdit {
			if _, err := tx.ExecContext(ctx, "UPDATE wiki_channels SET active_job=? WHERE id=?", jobID, channelID); err != nil {
				return err
			}
		}
		return s.Audit(ctx, tx, channelID, action, jobID, actor, nil)
	})
}

// ListJobs 分页查询频道后台任务列表。
func (s *Store) ListJobs(ctx context.Context, channelID string, page int) ([]map[string]any, error) {
	if page < 1 {
		page = 1
	}
	rows, err := s.exec(ctx).QueryContext(ctx, `
		SELECT id, kind, status, phase, progress, attempts, error_code, created_at, updated_at,
		       next_attempt, lease_until, UTC_TIMESTAMP(6) server_time
		FROM wiki_jobs
		WHERE channel_id=?
		ORDER BY created_at DESC
		LIMIT 50 OFFSET ?
	`, channelID, (page-1)*50)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []map[string]any
	for rows.Next() {
		var (
			id, kind, status, phase                string
			progRaw                                []byte
			attempts                               int
			errCode                                sql.NullString
			created, updated, nextAtt, lease, serv any
		)
		if err := rows.Scan(&id, &kind, &status, &phase, &progRaw, &attempts, &errCode, &created, &updated, &nextAtt, &lease, &serv); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id":           id,
			"kind":         kind,
			"status":       status,
			"phase":        phase,
			"progress":     decodeMapJSON(progRaw),
			"attempts":     attempts,
			"error_code":   nullStringAny(errCode),
			"created_at":   formatNullableTime(created),
			"updated_at":   formatNullableTime(updated),
			"next_attempt": formatNullableTime(nextAtt),
			"lease_until":  formatNullableTime(lease),
			"server_time":  formatNullableTime(serv),
		})
	}
	return out, rows.Err()
}

// ListExcludedSources 分页查询频道内被排除的消息统计与明细（对齐 routes/wiki.py:excluded_sources）。
func (s *Store) ListExcludedSources(ctx context.Context, channelID, username string, page int, reason *string) (map[string]any, error) {
	if page < 1 {
		page = 1
	}
	exec := s.exec(ctx)
	baseWhere := "channel_id=? AND source_type='message' AND excluded=TRUE AND deleted=FALSE"
	grpRows, err := exec.QueryContext(ctx, "SELECT exclusion_reason, COUNT(*) FROM wiki_sources WHERE "+baseWhere+" GROUP BY exclusion_reason", channelID)
	if err != nil {
		return nil, err
	}
	var reasons []map[string]any
	for grpRows.Next() {
		var key string
		var cnt int
		if err := grpRows.Scan(&key, &cnt); err != nil {
			_ = grpRows.Close()
			return nil, err
		}
		lbl := exclusionLabels[key]
		if lbl == "" {
			lbl = key
		}
		reasons = append(reasons, map[string]any{"key": key, "label": lbl, "count": cnt})
	}
	_ = grpRows.Close()

	where := baseWhere
	args := []any{channelID}
	if reason != nil {
		where += " AND exclusion_reason=?"
		args = append(args, *reason)
	}
	var total, unverified int
	if err := exec.QueryRowContext(ctx, "SELECT COUNT(*) FROM wiki_sources WHERE "+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	if err := exec.QueryRowContext(ctx, "SELECT COUNT(*) FROM wiki_sources WHERE channel_id=? AND source_type='message' AND verified=FALSE AND excluded=FALSE AND deleted=FALSE", channelID).Scan(&unverified); err != nil {
		return nil, err
	}

	qArgs := append(append([]any{}, args...), (page-1)*20)
	rows, err := exec.QueryContext(ctx, "SELECT message_id, created_at, body, category, exclusion_reason FROM wiki_sources WHERE "+where+" ORDER BY message_id LIMIT 20 OFFSET ?", qArgs...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var items []map[string]any
	for rows.Next() {
		var mid sql.NullInt64
		var created any
		var body, cat, exReason string
		if err := rows.Scan(&mid, &created, &body, &cat, &exReason); err != nil {
			return nil, err
		}
		lbl := exclusionLabels[exReason]
		if lbl == "" {
			lbl = exReason
		}
		var link any
		if username != "" && mid.Valid {
			link = fmt.Sprintf("https://t.me/%s/%d", username, mid.Int64)
		}
		items = append(items, map[string]any{
			"message_id":       nullInt64Any(mid),
			"created_at":       formatNullableTime(created),
			"body":             body,
			"category":         cat,
			"exclusion_reason": exReason,
			"reason":           lbl,
			"url":              link,
		})
	}
	return map[string]any{
		"total":            total,
		"page":             page,
		"page_size":        20,
		"unverified_total": unverified,
		"reasons":          reasons,
		"items":            items,
	}, rows.Err()
}

// PurgeChannel 清空频道 Wiki 生成内容，仅保留已删除/已隐藏来源的空墓碑防止重新导入复活（对齐 routes/wiki.py:purge）。
func (s *Store) PurgeChannel(ctx context.Context, channelID, confirmChannel, actor string) error {
	if confirmChannel != channelID {
		return errors.New("请输入频道数字 ID 确认清除")
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			UPDATE wiki_channels
			SET enabled=FALSE, public=FALSE, published_revision=NULL, active_job=NULL, config=?
			WHERE id=?
		`, EncodeJSON(DefaultWikiConfig()), channelID); err != nil {
			return err
		}
		for _, tbl := range []string{
			"wiki_channel_covers",
			"wiki_publications",
			"wiki_jobs",
			"wiki_pages",
			"wiki_extractions",
			"wiki_profile_cache",
			"wiki_albums",
		} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+tbl+" WHERE channel_id=?", channelID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM wiki_sources WHERE channel_id=? AND deleted=FALSE AND hidden=FALSE", channelID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE wiki_sources SET body='' WHERE channel_id=?", channelID); err != nil {
			return err
		}
		return s.Audit(ctx, tx, channelID, "purge", channelID, actor, nil)
	})
}

// WithdrawChannel 撤回频道当前公开发布快照（保留历史快照记录）。
func (s *Store) WithdrawChannel(ctx context.Context, channelID, actor string) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		var pubRev sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT published_revision FROM wiki_channels WHERE id=? FOR UPDATE", channelID).Scan(&pubRev); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE wiki_channels SET published_revision=NULL WHERE id=?", channelID); err != nil {
			return err
		}
		return s.Audit(ctx, tx, channelID, "withdraw", pubRev.String, actor, nil)
	})
}

// ConsumeLegacyReplay 登记 WebUI 旧版 Telegram Login Widget 签名防重放（10 分钟窗口，首次返回 true）。
func (s *Store) ConsumeLegacyReplay(ctx context.Context, signatureHex string, now time.Time) (bool, error) {
	sigBytes, err := hex.DecodeString(strings.TrimSpace(signatureHex))
	if err != nil || len(sigBytes) != 32 {
		return false, errors.New("invalid signature hex: expected 32 bytes")
	}
	if now.IsZero() {
		now = NowUTC()
	}
	expiresAt := db.MysqlDatetime6(now.UTC().Add(10 * time.Minute))
	res, err := s.exec(ctx).ExecContext(ctx, "INSERT IGNORE INTO wiki_legacy_login_replays VALUES (?, ?)", sigBytes, expiresAt)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected == 1, err
}

// ────────────────────────────── 内部扫描与转换辅助 ──────────────────────────────

func scanNullableTime(v any) *time.Time {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case time.Time:
		if val.IsZero() {
			return nil
		}
		u := val.UTC().Truncate(time.Microsecond)
		return &u
	case []byte:
		if len(val) == 0 {
			return nil
		}
		if t, err := ParseUTCTime(string(val)); err == nil {
			return &t
		}
	case string:
		if val == "" {
			return nil
		}
		if t, err := ParseUTCTime(val); err == nil {
			return &t
		}
	}
	return nil
}

func formatNullableTime(v any) any {
	t := scanNullableTime(v)
	if t == nil {
		return nil
	}
	return FormatISO(*t)
}

func decodeMapJSON(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}

func cloneMap(src map[string]any) map[string]any {
	if src == nil {
		return map[string]any{}
	}
	b, _ := json.Marshal(src)
	return decodeMapJSON(b)
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullStringAny(ns sql.NullString) any {
	if !ns.Valid {
		return nil
	}
	return ns.String
}

func nullInt64Any(ni sql.NullInt64) any {
	if !ni.Valid {
		return nil
	}
	return ni.Int64
}

func int64Placeholders(vals []int64) (string, []any) {
	marks := make([]string, len(vals))
	args := make([]any, len(vals))
	for i, v := range vals {
		marks[i] = "?"
		args[i] = v
	}
	return strings.Join(marks, ","), args
}

func stringPlaceholders(vals []string) (string, []any) {
	marks := make([]string, len(vals))
	args := make([]any, len(vals))
	for i, v := range vals {
		marks[i] = "?"
		args[i] = v
	}
	return strings.Join(marks, ","), args
}

func mapInt(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	}
	return 0
}

func mapInt64(m map[string]any, key string) int64 {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}

func mapBool(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	b, _ := m[key].(bool)
	return b
}

func mapString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func mapStringSlice(raw any) []string {
	switch arr := raw.(type) {
	case []string:
		return append([]string(nil), arr...)
	case []any:
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func extractRevisionMap(cursor map[string]any) map[string]string {
	out := map[string]string{}
	raw := cursor["review_revisions"]
	if raw == nil {
		raw = cursor["revisions"]
	}
	switch m := raw.(type) {
	case map[string]string:
		for k, v := range m {
			out[k] = v
		}
	case map[string]any:
		for k, v := range m {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
	}
	return out
}

// QueryRowsMap 执行固定查询并将每行扫描为 map[string]any（[]byte 自动转换为 string，时间转为 ISO 字符串）。
func QueryRowsMap(ctx context.Context, exec sqlExecutor, query string, args ...any) ([]map[string]any, error) {
	rows, err := exec.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			switch v := vals[i].(type) {
			case []byte:
				row[col] = string(v)
			case time.Time:
				row[col] = FormatISO(v)
			default:
				row[col] = v
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
