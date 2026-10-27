// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// Package guard 移植 core/moderation/repository.py 的 GuardRepository（QA 闭包
// 消费部分：schema 执行、用户级 GET_LOCK、case/blacklist/whitelist 读写）与
// core/moderation/appeal_repository.py 的 AppealStore/AppealRepository 仓储方法：
// 草稿输入、状态机 transition、审计事件、通知发布/投递、审核请求队列与
// finish_approval 三表事务。
//
// 硬规则（qa-bot-go/README.md 规则 6）：GET_LOCK 取锁/工作/释放在同一 db.Conn
// 上；等待 ctx 6s、GET_LOCK 自身 5s；defer 双保险；NULL/Scan 错误按获取失败
// 处理（fail-closed）；释放失败或取锁竞态时物理关闭连接，绝不让带锁连接回池。
package guard

import (
	"context"
	"database/sql"
	_ "embed" // go:embed 字符串变量所需的空导入
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

//go:embed schema.sql
var schemaSQL string

// ErrLockUnavailable 对齐 Python TimeoutError("Sakura Guard user lock unavailable")
// （repository.py:390）：GET_LOCK 返回 0/NULL/Scan 失败统一归为此错误（fail-closed）。
var ErrLockUnavailable = errors.New("Sakura Guard user lock unavailable")

// Repository 对齐 GuardRepository/AppealRepository：持有连接池；
// 写路径经 WithLock（同连接用户级锁），只读工作经 Reader。
type Repository struct {
	DB *sql.DB
}

// New 创建申诉仓储。
func New(db *sql.DB) *Repository {
	return &Repository{DB: db}
}

// Initialize 幂等执行 schema.sql（repository.py:332-338：按 “;” 切分，
// 跳过空语句；启动时调用一次，失败由 main 以非零码退出）。
// 切分前先剥离整行 `--` 注释：注释文本可能合法包含分号（如本文件头的
// AGPL 说明），裸切分会把注释残片当 SQL 执行（MySQL Error 1064）。
func (r *Repository) Initialize(ctx context.Context) error {
	var lines []string
	for _, line := range strings.Split(schemaSQL, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		lines = append(lines, line)
	}
	for _, statement := range strings.Split(strings.Join(lines, "\n"), ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := r.DB.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("guard schema 执行失败: %w", err)
		}
	}
	return nil
}

// Row 是 aiomysql DictCursor 行等价物：列名 → 驱动值。
// go-sql-driver 的文本列返回 []byte，All 已统一转 string；JSON 列 payload
// 在 decodeRow 解析为 map/list（对齐 repository.py:33-36 的 decode）。
type Row map[string]any

// decodeRow 对齐 repository.py:33-36 的 decode：payload 为文本列时解析，
// 解析失败向上传播（Python 无 try 包裹）。
func decodeRow(row Row) (Row, error) {
	v, ok := row["payload"]
	if !ok || v == nil {
		return row, nil
	}
	switch t := v.(type) {
	case []byte:
		var parsed any
		if err := json.Unmarshal(t, &parsed); err != nil {
			return nil, fmt.Errorf("payload JSON 解析失败: %w", err)
		}
		row["payload"] = parsed
	case string:
		var parsed any
		if err := json.Unmarshal([]byte(t), &parsed); err != nil {
			return nil, fmt.Errorf("payload JSON 解析失败: %w", err)
		}
		row["payload"] = parsed
	}
	return row, nil
}

// executor 覆盖 *sql.Conn 与 *sql.Tx 两条执行路径（Store 在事务内外复用）。
type executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store 是「同一连接上的工作单元」（对齐 GuardStore/AppealStore）：
// conn 为池内专用连接，tx 非空时所有操作走当前事务。
type Store struct {
	conn *sql.Conn
	tx   *sql.Tx
}

// NewStore 以池连接构造只读工作单元（测试与 Reader 内部使用）。
func NewStore(conn *sql.Conn) *Store { return &Store{conn: conn} }

// runner 返回当前执行器：事务优先。
func (s *Store) runner() executor {
	if s.tx != nil {
		return s.tx
	}
	return s.conn
}

// Exec 执行写语句并返回受影响行数（对齐 GuardStore.execute 的 rowcount）。
func (s *Store) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	res, err := s.runner().ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// All 查询多行（对齐 GuardStore.all + decode）。
func (s *Store) All(ctx context.Context, query string, args ...any) ([]Row, error) {
	rows, err := s.runner().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // 只读游标

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var out []Row
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(Row, len(cols))
		for i, c := range cols {
			v := vals[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			row[c] = v
		}
		decoded, err := decodeRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, decoded)
	}
	return out, rows.Err()
}

// One 查询单行（对齐 GuardStore.one）：无行返回 (nil, nil)。
func (s *Store) One(ctx context.Context, query string, args ...any) (Row, error) {
	all, err := s.All(ctx, query, args...)
	if err != nil || len(all) == 0 {
		return nil, err
	}
	return all[0], nil
}

// Atomic 在当前连接上开事务执行 fn（对齐 AppealStore.atomic，repository
// appeal_repository.py:28-36）：成功提交、任何错误回滚并上抛；不支持嵌套。
func (s *Store) Atomic(ctx context.Context, fn func(*Store) error) error {
	if s.tx != nil {
		return errors.New("guard: 不支持嵌套事务")
	}
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	s.tx = tx
	defer func() { s.tx = nil }()
	if err := fn(s); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// WithLock 在专用连接上取用户级 GET_LOCK 并在锁内执行 fn
// （repository.py:374-403；README 硬规则 6）：
//   - GET_LOCK(?, 5) 运行在等待上限 6s 的 ctx 上；NULL/Scan 错误/返回非 1
//     统一按获取失败处理（fail-closed），错误为 ErrLockUnavailable；仅 Scan
//     错误弃置连接，确定未获锁的 0/NULL 归还池（审计 B-5）；
//   - fn 与释放共用同一连接；defer 双保险保证 panic 路径也执行释放；
//   - 释放前先 ROLLBACK（对齐 Python connection.rollback()：清掉 fn 内
//     可能遗留的半开事务；autocommit 下为无害 no-op）；
//   - RELEASE_LOCK 失败/返回非 1 时物理关闭连接（database/sql 的 Conn.Close
//     只会归还池，Python connection.close() 是物理关闭——discardConn 用
//     KILL CONNECTION_ID() 等价实现「带锁连接绝不回池」）。
func (r *Repository) WithLock(ctx context.Context, userID int64, fn func(*Store) error) error {
	conn, err := r.DB.Conn(ctx)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("sakura_guard:user:%d", userID)

	lockCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	var acquired sql.NullInt64
	scanErr := conn.QueryRowContext(lockCtx, "SELECT GET_LOCK(?, 5) AS acquired", name).Scan(&acquired)
	cancel()
	if scanErr != nil || !acquired.Valid || acquired.Int64 != 1 {
		// 处置分层（审计 B-5）：Scan 错误（ctx 取消/超时竞态）下服务端可能刚
		// 拿到锁 → 物理关闭（KILL），锁绝不回池；确定未获锁的 0/NULL 与
		// Python 语义一致（仅 raise、连接回池），Close 归还避免锁竞争 churn。
		if scanErr != nil {
			discardConn(conn)
			return fmt.Errorf("guard 取锁失败: %w", scanErr)
		}
		_ = conn.Close() // GET_LOCK 返回 0/NULL：未持锁，归还池连接
		return ErrLockUnavailable
	}

	store := &Store{conn: conn}
	defer func() {
		// 释放必须在同一连接上；panic 路径同样执行（defer 双保险）。
		// 对齐 Python wait_for(RELEASE_LOCK, 5)：父 ctx 取消后仍应尝试释放。
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer releaseCancel()
		_, _ = conn.ExecContext(releaseCtx, "ROLLBACK")
		var released sql.NullInt64
		if err := conn.QueryRowContext(releaseCtx, "SELECT RELEASE_LOCK(?) AS released", name).Scan(&released); err != nil || !released.Valid || released.Int64 != 1 {
			discardConn(conn)
			return
		}
		_ = conn.Close()
	}()
	return fn(store)
}

// Reader 在普通池连接上执行只读工作（对齐 AppealRepository.reader()）。
func (r *Repository) Reader(ctx context.Context, fn func(*Store) error) error {
	conn, err := r.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close() //nolint:errcheck // 归还池连接
	return fn(&Store{conn: conn})
}

// discardConn 物理关闭池连接（Python connection.close() 的 database/sql
// 等价物）。database/sql 的 Conn.Close 只把连接归还池，因此这里用
// KILL CONNECTION_ID() 由服务端立即终结会话（锁随之消亡），驱动连接
// 变为 bad-conn 后被 database/sql 丢弃，绝不带锁回池。
func discardConn(conn *sql.Conn) {
	killCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = conn.ExecContext(killCtx, "KILL CONNECTION_ID()")
	_ = conn.Close()
}
