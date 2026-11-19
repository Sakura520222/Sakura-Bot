// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrTableNotAllowed 表名不在 WebUI 15 表严格白名单中。
	ErrTableNotAllowed = errors.New("db: 表不存在或不允许访问")
	// ErrInvalidColumn 列名未通过标识符语法或表结构校验。
	ErrInvalidColumn = errors.New("db: 非法列名")
	// ErrNoPrimaryKey 目标表未定义主键列。
	ErrNoPrimaryKey = errors.New("db: 表没有主键，无法定位行")
	// ErrRowNotFound 未命中指定主键行。
	ErrRowNotFound = errors.New("db: 未找到匹配的行")
)

// allowedTablesSorted 严格对齐 mysql.py:3043-3059 _ALLOWED_TABLES 的 15 张表（排序副本）。
var allowedTablesSorted = []string{
	"channel_profiles",
	"conversation_history",
	"db_version",
	"forwarded_messages",
	"forwarding_stats",
	"notification_queue",
	"poll_regenerations",
	"poll_voters",
	"request_queue",
	"submissions",
	"subscriptions",
	"summaries",
	"system_audit_logs",
	"usage_quota",
	"users",
}

// allowedTablesCanonical 小写表名 -> 静态白名单字面量（SQL 仅拼接白名单常量，绝不拼接外部输入）。
var allowedTablesCanonical = func() map[string]string {
	m := make(map[string]string, len(allowedTablesSorted))
	for _, t := range allowedTablesSorted {
		m[t] = t
	}
	return m
}()

// clearAllTablesOrder 严格对齐 mysql.py:2109-2124 clear_all_data 的 14 张表及外键顺序（保留 db_version）。
var clearAllTablesOrder = []string{
	"poll_voters",
	"poll_regenerations",
	"submissions",
	"forwarded_messages",
	"forwarding_stats",
	"notification_queue",
	"request_queue",
	"subscriptions",
	"conversation_history",
	"users",
	"channel_profiles",
	"usage_quota",
	"summaries",
	"system_audit_logs",
}

// AuditEntry WebUI 系统运维审计写入条目（mysql.py:2962-2996）。
type AuditEntry struct {
	Action        string
	Actor         string
	Target        string
	ParamsSummary string
	Success       bool
	Message       string
	DurationMs    int
}

// AuditRow WebUI 系统运维审计查询记录（mysql.py:2998-3016 与 system.py:701-713）。
type AuditRow struct {
	ID            int64     `json:"id"`
	Action        string    `json:"action"`
	Actor         string    `json:"actor"`
	Target        string    `json:"target"`
	ParamsSummary string    `json:"params_summary"`
	Success       bool      `json:"success"`
	Message       string    `json:"message"`
	DurationMs    int       `json:"duration_ms"`
	CreatedAt     time.Time `json:"created_at"`
}

// TableSummary WebUI 数据库管理表概览（mysql.py:3065-3106）。
type TableSummary struct {
	TableName  string  `json:"table_name"`
	TableRows  int64   `json:"table_rows"`
	DataSizeMB float64 `json:"data_size_mb"`
	Engine     string  `json:"engine"`
}

// ColumnMeta 表字段元数据（mysql.py:3108-3131 describe_table）。
type ColumnMeta struct {
	Field   string  `json:"field"`
	Type    string  `json:"type"`
	Null    string  `json:"null"`
	Key     string  `json:"key"`
	Default *string `json:"default"`
	Extra   string  `json:"extra"`
}

// TablePage 通用分页表数据返回（mysql.py:3155-3217 query_table）。
type TablePage struct {
	Rows     []map[string]any `json:"rows"`
	Total    int64            `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}

// AllowedTables 返回 WebUI 允许管理的 15 张表名拷贝（按字典序）。
func AllowedTables() []string {
	cp := make([]string, len(allowedTablesSorted))
	copy(cp, allowedTablesSorted)
	return cp
}

// IsAllowedTable 检查表名是否在 WebUI 15 表白名单中（大小写不敏感）。
func IsAllowedTable(table string) bool {
	_, ok := canonicalAllowedTable(table)
	return ok
}

// AllowedTables Manager 方法形式：返回允许管理的 15 张表名。
func (m *Manager) AllowedTables() []string {
	return AllowedTables()
}

// TableExists 检查表名是否在 WebUI 白名单中（对齐 mysql.py:3061-3063 table_exists）。
func (m *Manager) TableExists(table string) bool {
	return IsAllowedTable(table)
}

func canonicalAllowedTable(table string) (string, bool) {
	trimmed := strings.ToLower(strings.TrimSpace(table))
	if !isSafeSQLIdentifier(trimmed) {
		return "", false
	}
	c, ok := allowedTablesCanonical[trimmed]
	return c, ok
}

// isSafeSQLIdentifier 严格校验 SQL 标识符仅由 ASCII 字母、数字、下划线组成且非空（<=64 字符）。
func isSafeSQLIdentifier(ident string) bool {
	if len(ident) == 0 || len(ident) > 64 {
		return false
	}
	for i := 0; i < len(ident); i++ {
		ch := ident[i]
		isAlpha := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
		isDigit := ch >= '0' && ch <= '9'
		if !isAlpha && !isDigit && ch != '_' {
			return false
		}
	}
	return true
}

func quoteIdent(canonical string) string {
	return "`" + canonical + "`"
}

// RecordAuditLog 写入一条 WebUI 系统运维审计记录（mysql.py:2962-2996）。
func (m *Manager) RecordAuditLog(ctx context.Context, entry AuditEntry) error {
	paramsSummary := entry.ParamsSummary
	if paramsSummary == "" {
		paramsSummary = "{}"
	}
	_, err := m.DB.ExecContext(ctx, `
		INSERT INTO system_audit_logs
		(action, actor, target, params_summary, success, message, duration_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, entry.Action, entry.Actor, entry.Target, paramsSummary, entry.Success, entry.Message, entry.DurationMs)
	if err != nil {
		slog.Error(fmt.Sprintf("添加系统审计记录失败: %T: %s", err, err))
		return err
	}
	return nil
}

// ListAuditLogs 获取最近 limit 条系统审计记录（mysql.py:2998-3016）。
func (m *Manager) ListAuditLogs(ctx context.Context, limit int) ([]AuditRow, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := m.DB.QueryContext(ctx, `
		SELECT id, action, actor, target, params_summary, success,
		       message, duration_ms, created_at
		FROM system_audit_logs
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		slog.Error(fmt.Sprintf("获取系统审计记录失败: %T: %s", err, err))
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	out := make([]AuditRow, 0)
	for rows.Next() {
		var (
			r       AuditRow
			target  sql.NullString
			summary sql.NullString
			success sql.NullBool
			msg     sql.NullString
			dur     sql.NullInt64
			created sql.NullTime
		)
		if err := rows.Scan(&r.ID, &r.Action, &r.Actor, &target, &summary, &success, &msg, &dur, &created); err != nil {
			return nil, err
		}
		r.Target = target.String
		r.ParamsSummary = summary.String
		r.Success = success.Bool
		r.Message = msg.String
		r.DurationMs = int(dur.Int64)
		if created.Valid {
			r.CreatedAt = created.Time
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CleanupAuditLogs 清理超过 days 天的旧审计记录（mysql.py:3018-3038）。
func (m *Manager) CleanupAuditLogs(ctx context.Context, days int) (int64, error) {
	cutoff := MysqlDatetime(NowUTC().Add(-time.Duration(days) * 24 * time.Hour))
	res, err := m.DB.ExecContext(ctx, "DELETE FROM system_audit_logs WHERE created_at < ?", cutoff)
	if err != nil {
		slog.Error(fmt.Sprintf("清理系统审计记录失败: %T: %s", err, err))
		return 0, err
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if deleted > 0 {
		slog.Info(fmt.Sprintf("已清理 %d 条超过 %d 天的系统审计记录", deleted, days))
	}
	return deleted, nil
}

// ListTablesWithCounts 列出全部白名单表的精确行数、空间大小和存储引擎（mysql.py:3065-3106）。
func (m *Manager) ListTablesWithCounts(ctx context.Context) ([]TableSummary, error) {
	placeholders := strings.Repeat("?,", len(allowedTablesSorted))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(allowedTablesSorted))
	for i, name := range allowedTablesSorted {
		args[i] = name
	}

	rows, err := m.DB.QueryContext(ctx, `
		SELECT
			TABLE_NAME AS table_name,
			COALESCE(TABLE_ROWS, 0) AS table_rows,
			COALESCE(ROUND(DATA_LENGTH / 1024 / 1024, 3), 0) AS data_size_mb,
			COALESCE(ENGINE, '') AS engine
		FROM INFORMATION_SCHEMA.TABLES
		WHERE TABLE_SCHEMA = DATABASE()
		  AND TABLE_NAME IN (`+placeholders+`)
		ORDER BY TABLE_NAME
	`, args...)
	if err != nil {
		slog.Error(fmt.Sprintf("列出数据库表失败: %T: %s", err, err))
		return nil, err
	}
	var summaries []TableSummary
	for rows.Next() {
		var ts TableSummary
		if scanErr := rows.Scan(&ts.TableName, &ts.TableRows, &ts.DataSizeMB, &ts.Engine); scanErr != nil {
			_ = rows.Close()
			return nil, scanErr
		}
		if canonical, ok := canonicalAllowedTable(ts.TableName); ok {
			ts.TableName = canonical
			summaries = append(summaries, ts)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	if len(summaries) == 0 {
		return []TableSummary{}, nil
	}

	// UNION ALL 一次获取全部已存在白名单表的精确行数（对齐 mysql.py:3091-3102）。
	unionParts := make([]string, 0, len(summaries))
	unionArgs := make([]any, 0, len(summaries))
	for _, ts := range summaries {
		unionParts = append(unionParts, fmt.Sprintf("SELECT ? AS table_name, COUNT(*) AS cnt FROM %s", quoteIdent(ts.TableName)))
		unionArgs = append(unionArgs, ts.TableName)
	}

	cntRows, err := m.DB.QueryContext(ctx, strings.Join(unionParts, " UNION ALL "), unionArgs...)
	if err != nil {
		return nil, err
	}
	defer cntRows.Close() //nolint:errcheck // 只读游标

	exactCounts := make(map[string]int64, len(summaries))
	for cntRows.Next() {
		var (
			tName string
			cnt   sql.NullInt64
		)
		if err := cntRows.Scan(&tName, &cnt); err != nil {
			return nil, err
		}
		exactCounts[tName] = cnt.Int64
	}
	if err := cntRows.Err(); err != nil {
		return nil, err
	}

	for i := range summaries {
		summaries[i].TableRows = exactCounts[summaries[i].TableName]
	}
	return summaries, nil
}

// GetTableSchema 获取白名单表的列定义与主键列名（对齐 database.py:66-85 与 mysql.py:3108-3153）。
func (m *Manager) GetTableSchema(ctx context.Context, table string) ([]ColumnMeta, string, error) {
	canonical, ok := canonicalAllowedTable(table)
	if !ok {
		return nil, "", ErrTableNotAllowed
	}

	rows, err := m.DB.QueryContext(ctx, `
		SELECT
			COLUMN_NAME AS `+"`field`"+`,
			COLUMN_TYPE AS `+"`type`"+`,
			IS_NULLABLE AS `+"`null`"+`,
			COLUMN_KEY AS `+"`key`"+`,
			COLUMN_DEFAULT AS `+"`default`"+`,
			EXTRA AS `+"`extra`"+`
		FROM INFORMATION_SCHEMA.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
		ORDER BY ORDINAL_POSITION
	`, canonical)
	if err != nil {
		return nil, "", err
	}
	cols := make([]ColumnMeta, 0)
	var fallbackPK string
	for rows.Next() {
		var (
			c      ColumnMeta
			defVal sql.NullString
		)
		if scanErr := rows.Scan(&c.Field, &c.Type, &c.Null, &c.Key, &defVal, &c.Extra); scanErr != nil {
			_ = rows.Close()
			return nil, "", scanErr
		}
		if !isSafeSQLIdentifier(c.Field) {
			_ = rows.Close()
			return nil, "", fmt.Errorf("%w: %s", ErrInvalidColumn, c.Field)
		}
		if defVal.Valid {
			v := defVal.String
			c.Default = &v
		}
		if fallbackPK == "" && strings.EqualFold(c.Key, "PRI") {
			fallbackPK = c.Field
		}
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, "", err
	}
	_ = rows.Close()

	pk, err := m.GetPrimaryKey(ctx, canonical)
	if err != nil {
		return nil, "", err
	}
	if pk == "" {
		pk = fallbackPK
	}
	return cols, pk, nil
}

// GetPrimaryKey 查询白名单表的单主键列名（mysql.py:3133-3153）。
func (m *Manager) GetPrimaryKey(ctx context.Context, table string) (string, error) {
	canonical, ok := canonicalAllowedTable(table)
	if !ok {
		return "", ErrTableNotAllowed
	}
	var pk sql.NullString
	err := m.DB.QueryRowContext(ctx, `
		SELECT COLUMN_NAME
		FROM INFORMATION_SCHEMA.KEY_COLUMN_USAGE
		WHERE TABLE_SCHEMA = DATABASE()
		  AND TABLE_NAME = ?
		  AND CONSTRAINT_NAME = 'PRIMARY'
		LIMIT 1
	`, canonical).Scan(&pk)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if pk.Valid && isSafeSQLIdentifier(pk.String) {
		return pk.String, nil
	}
	return "", nil
}

func columnWhitelistFromMeta(cols []ColumnMeta) map[string]string {
	m := make(map[string]string, len(cols))
	for _, c := range cols {
		if isSafeSQLIdentifier(c.Field) {
			m[c.Field] = c.Field
			m[strings.ToLower(c.Field)] = c.Field
		}
	}
	return m
}

func resolveValidatedColumn(validCols map[string]string, col string) (string, error) {
	trimmed := strings.TrimSpace(col)
	if !isSafeSQLIdentifier(trimmed) {
		return "", fmt.Errorf("%w: %s", ErrInvalidColumn, col)
	}
	if canonical, ok := validCols[trimmed]; ok {
		return canonical, nil
	}
	if canonical, ok := validCols[strings.ToLower(trimmed)]; ok {
		return canonical, nil
	}
	return "", fmt.Errorf("%w: %s", ErrInvalidColumn, col)
}

// QueryTableRows 分页查询白名单表数据：search 非空时在全部已校验列上执行参数化模糊检索。
func (m *Manager) QueryTableRows(ctx context.Context, table string, page, pageSize int, search string, sortBy, sortOrder string) (*TablePage, error) {
	return m.queryTableInternal(ctx, table, page, pageSize, "", search, sortBy, sortOrder)
}

// QueryTableRowsFiltered 按指定列名模糊搜索并分页查询白名单表数据（对齐 mysql.py:3155-3217 query_table）。
func (m *Manager) QueryTableRowsFiltered(ctx context.Context, table string, page, pageSize int, searchColumn, searchKeyword, sortBy, sortOrder string) (*TablePage, error) {
	return m.queryTableInternal(ctx, table, page, pageSize, searchColumn, searchKeyword, sortBy, sortOrder)
}

func (m *Manager) queryTableInternal(ctx context.Context, table string, page, pageSize int, searchColumn, searchKeyword, sortBy, sortOrder string) (*TablePage, error) {
	canonicalTable, ok := canonicalAllowedTable(table)
	if !ok {
		return nil, ErrTableNotAllowed
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	cols, _, err := m.GetTableSchema(ctx, canonicalTable)
	if err != nil {
		return nil, err
	}
	validCols := columnWhitelistFromMeta(cols)

	whereSQL, whereArgs, err := buildTableSearchWhere(cols, validCols, searchColumn, searchKeyword)
	if err != nil {
		return nil, err
	}

	orderSQL, err := buildTableOrderClause(validCols, sortBy, sortOrder)
	if err != nil {
		return nil, err
	}

	var total int64
	countQuery := fmt.Sprintf("SELECT COUNT(*) AS cnt FROM %s %s", quoteIdent(canonicalTable), whereSQL)
	if err := m.DB.QueryRowContext(ctx, countQuery, whereArgs...).Scan(&total); err != nil {
		return nil, err
	}

	offset := (page - 1) * pageSize
	dataArgs := make([]any, 0, len(whereArgs)+2)
	dataArgs = append(dataArgs, whereArgs...)
	dataArgs = append(dataArgs, pageSize, offset)

	dataQuery := strings.TrimSpace(fmt.Sprintf(
		"SELECT * FROM %s %s %s LIMIT ? OFFSET ?",
		quoteIdent(canonicalTable), whereSQL, orderSQL,
	))
	rows, err := m.DB.QueryContext(ctx, dataQuery, dataArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	list, err := scanGenericMapRows(rows)
	if err != nil {
		return nil, err
	}
	return &TablePage{
		Rows:     list,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func buildTableSearchWhere(cols []ColumnMeta, validCols map[string]string, searchColumn, searchKeyword string) (string, []any, error) {
	kw := strings.TrimSpace(searchKeyword)
	if kw == "" {
		if strings.TrimSpace(searchColumn) != "" {
			if _, err := resolveValidatedColumn(validCols, searchColumn); err != nil {
				return "", nil, err
			}
		}
		return "", nil, nil
	}
	pattern := "%" + escapeLikeWildcards(kw) + "%"
	if strings.TrimSpace(searchColumn) != "" {
		canonicalCol, err := resolveValidatedColumn(validCols, searchColumn)
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("WHERE CAST(%s AS CHAR) LIKE ? ESCAPE '='", quoteIdent(canonicalCol)), []any{pattern}, nil
	}
	if len(cols) == 0 {
		return "", nil, nil
	}
	orParts := make([]string, 0, len(cols))
	args := make([]any, 0, len(cols))
	for _, c := range cols {
		orParts = append(orParts, fmt.Sprintf("CAST(%s AS CHAR) LIKE ? ESCAPE '='", quoteIdent(c.Field)))
		args = append(args, pattern)
	}
	return "WHERE (" + strings.Join(orParts, " OR ") + ")", args, nil
}

func buildTableOrderClause(validCols map[string]string, sortBy, sortOrder string) (string, error) {
	if strings.TrimSpace(sortBy) == "" {
		return "", nil
	}
	canonicalCol, err := resolveValidatedColumn(validCols, sortBy)
	if err != nil {
		return "", err
	}
	dir := "ASC"
	if strings.EqualFold(strings.TrimSpace(sortOrder), "DESC") {
		dir = "DESC"
	}
	return fmt.Sprintf("ORDER BY %s %s", quoteIdent(canonicalCol), dir), nil
}

func escapeLikeWildcards(s string) string {
	r := strings.NewReplacer("=", "==", "%", "=%", "_", "=_")
	return r.Replace(s)
}

func (m *Manager) resolveTableAndPK(ctx context.Context, table, pkCol string) (string, map[string]string, string, error) {
	canonicalTable, ok := canonicalAllowedTable(table)
	if !ok {
		return "", nil, "", ErrTableNotAllowed
	}
	cols, detectedPK, err := m.GetTableSchema(ctx, canonicalTable)
	if err != nil {
		return "", nil, "", err
	}
	validCols := columnWhitelistFromMeta(cols)

	if strings.TrimSpace(pkCol) == "" {
		if detectedPK == "" {
			return "", nil, "", ErrNoPrimaryKey
		}
		return canonicalTable, validCols, detectedPK, nil
	}
	validatedPK, err := resolveValidatedColumn(validCols, pkCol)
	if err != nil {
		return "", nil, "", err
	}
	return canonicalTable, validCols, validatedPK, nil
}

func coercePrimaryKeyArg(pkVal string) any {
	if n, err := strconv.ParseInt(pkVal, 10, 64); err == nil {
		return n
	}
	return pkVal
}

// GetTableRow 按主键获取白名单表的单行数据（mysql.py:3227-3239；不存在返回 nil, nil）。
func (m *Manager) GetTableRow(ctx context.Context, table, pkCol, pkVal string) (map[string]any, error) {
	canonicalTable, _, validatedPK, err := m.resolveTableAndPK(ctx, table, pkCol)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf("SELECT * FROM %s WHERE %s = ? LIMIT 1", quoteIdent(canonicalTable), quoteIdent(validatedPK))
	rows, err := m.DB.QueryContext(ctx, query, coercePrimaryKeyArg(pkVal))
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	list, err := scanGenericMapRows(rows)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return list[0], nil
}

// InsertTableRow 向白名单表插入单行数据（所有列名均严格对照表结构白名单校验，mysql.py:3241-3260）。
func (m *Manager) InsertTableRow(ctx context.Context, table string, data map[string]any) (int64, error) {
	canonicalTable, ok := canonicalAllowedTable(table)
	if !ok {
		return 0, ErrTableNotAllowed
	}
	if len(data) == 0 {
		return 0, fmt.Errorf("%w: 空写入字段", ErrInvalidColumn)
	}
	cols, _, err := m.GetTableSchema(ctx, canonicalTable)
	if err != nil {
		return 0, err
	}
	validCols := columnWhitelistFromMeta(cols)

	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	quotedCols := make([]string, 0, len(keys))
	placeholders := make([]string, 0, len(keys))
	values := make([]any, 0, len(keys))
	for _, k := range keys {
		canonicalCol, err := resolveValidatedColumn(validCols, k)
		if err != nil {
			return 0, err
		}
		quotedCols = append(quotedCols, quoteIdent(canonicalCol))
		placeholders = append(placeholders, "?")
		val, err := normalizeWriteValue(data[k])
		if err != nil {
			return 0, err
		}
		values = append(values, val)
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		quoteIdent(canonicalTable),
		strings.Join(quotedCols, ", "),
		strings.Join(placeholders, ", "))
	res, err := m.DB.ExecContext(ctx, query, values...)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// UpdateTableRow 按主键更新白名单表单行数据（mysql.py:3262-3282；未找到匹配行返回 ErrRowNotFound）。
func (m *Manager) UpdateTableRow(ctx context.Context, table, pkCol, pkVal string, updates map[string]any) error {
	if len(updates) == 0 {
		return fmt.Errorf("%w: 空更新字段", ErrInvalidColumn)
	}
	canonicalTable, validCols, validatedPK, err := m.resolveTableAndPK(ctx, table, pkCol)
	if err != nil {
		return err
	}

	keys := make([]string, 0, len(updates))
	for k := range updates {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	setParts := make([]string, 0, len(keys))
	values := make([]any, 0, len(keys)+1)
	for _, k := range keys {
		canonicalCol, err := resolveValidatedColumn(validCols, k)
		if err != nil {
			return err
		}
		setParts = append(setParts, fmt.Sprintf("%s = ?", quoteIdent(canonicalCol)))
		val, err := normalizeWriteValue(updates[k])
		if err != nil {
			return err
		}
		values = append(values, val)
	}
	values = append(values, coercePrimaryKeyArg(pkVal))

	query := fmt.Sprintf("UPDATE %s SET %s WHERE %s = ? LIMIT 1",
		quoteIdent(canonicalTable),
		strings.Join(setParts, ", "),
		quoteIdent(validatedPK))
	res, err := m.DB.ExecContext(ctx, query, values...)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		// MySQL 默认 RowsAffected 在新值与旧值相同时返回 0：再核验一次主键是否存在。
		existing, chkErr := m.GetTableRow(ctx, canonicalTable, validatedPK, pkVal)
		if chkErr != nil {
			return chkErr
		}
		if existing == nil {
			return ErrRowNotFound
		}
	}
	return nil
}

// DeleteTableRow 按主键删除白名单表单行数据（mysql.py:3284-3297）。
func (m *Manager) DeleteTableRow(ctx context.Context, table, pkCol, pkVal string) (int64, error) {
	canonicalTable, _, validatedPK, err := m.resolveTableAndPK(ctx, table, pkCol)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf("DELETE FROM %s WHERE %s = ? LIMIT 1",
		quoteIdent(canonicalTable), quoteIdent(validatedPK))
	res, err := m.DB.ExecContext(ctx, query, coercePrimaryKeyArg(pkVal))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// BatchDeleteTableRows 批量按主键删除白名单表多行数据。
func (m *Manager) BatchDeleteTableRows(ctx context.Context, table, pkCol string, ids []any) (int64, error) {
	canonicalTable, _, validatedPK, err := m.resolveTableAndPK(ctx, table, pkCol)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	query := fmt.Sprintf("DELETE FROM %s WHERE %s IN (%s)",
		quoteIdent(canonicalTable), quoteIdent(validatedPK), placeholders)
	res, err := m.DB.ExecContext(ctx, query, ids...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ClearTable 清空单个白名单表数据并返回删除行数。
func (m *Manager) ClearTable(ctx context.Context, table string) (int64, error) {
	canonicalTable, ok := canonicalAllowedTable(table)
	if !ok {
		return 0, ErrTableNotAllowed
	}
	res, err := m.DB.ExecContext(ctx, "DELETE FROM "+quoteIdent(canonicalTable))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ClearAllData 按外键依赖顺序清空全部 14 张业务表（保留表结构与 db_version，对齐 mysql.py:2106-2158）。
// 在单一专用连接上临时关闭 FOREIGN_KEY_CHECKS 并由 defer 强制恢复，绝不污染连接池。
func (m *Manager) ClearAllData(ctx context.Context) (map[string]int64, int64, error) {
	conn, err := m.DB.Conn(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer conn.Close() //nolint:errcheck // 归还池连接

	if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		return nil, 0, err
	}
	defer func() {
		restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(restoreCtx, "SET FOREIGN_KEY_CHECKS = 1")
	}()

	results := make(map[string]int64, len(clearAllTablesOrder))
	var (
		totalDeleted int64
		errs         []error
	)
	for _, table := range clearAllTablesOrder {
		res, delErr := conn.ExecContext(ctx, "DELETE FROM "+quoteIdent(table))
		if delErr != nil {
			slog.Error(fmt.Sprintf("清空表 %s 失败: %v", table, delErr))
			results[table] = -1
			errs = append(errs, fmt.Errorf("%s: %w", table, delErr))
			continue
		}
		cnt, _ := res.RowsAffected()
		results[table] = cnt
		if cnt > 0 {
			totalDeleted += cnt
		}
		slog.Info(fmt.Sprintf("已清空表 %s，删除 %d 行", table, cnt))
	}

	return results, totalDeleted, errors.Join(errs...)
}

// DatabaseStatus 汇总数据库可用性、MySQL 版本、schema 版本、表计数及总结统计
// （对齐 core/web_api/routes/system.py:653-671 _collect_database_status）。
func (m *Manager) DatabaseStatus(ctx context.Context) (map[string]any, error) {
	if m.DB == nil {
		return map[string]any{
			"available": false,
			"message":   "数据库管理器不可用",
		}, nil
	}

	var mysqlVer string
	if err := m.DB.QueryRowContext(ctx, "SELECT VERSION()").Scan(&mysqlVer); err != nil {
		return map[string]any{
			"available": false,
			"message":   err.Error(),
		}, err
	}

	schemaVer, err := m.DBVersion(ctx)
	if err != nil {
		schemaVer = 6
	}

	tables, err := m.ListTablesWithCounts(ctx)
	if err != nil {
		return nil, err
	}
	tableCounts := make(map[string]int64, len(tables))
	for _, t := range tables {
		tableCounts[t.TableName] = t.TableRows
	}

	stats, err := m.GetWebStats(ctx)
	if err != nil {
		stats = map[string]any{}
	}

	return map[string]any{
		"available":     true,
		"type":          "mysql",
		"version":       schemaVer,
		"mysql_version": mysqlVer,
		"tables":        tables,
		"table_counts":  tableCounts,
		"statistics":    stats,
	}, nil
}

func normalizeWriteValue(v any) (any, error) {
	switch x := v.(type) {
	case nil, string, bool, int, int64, int32, float64, float32, []byte:
		return x, nil
	case time.Time:
		return MysqlDatetime(x.UTC()), nil
	default:
		return EncodeJSON(v)
	}
}

func scanGenericMapRows(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0)
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
				row[col] = MysqlDatetime(v.UTC())
			default:
				row[col] = v
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
