// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// Package db 是 QA 进程的 MySQL 数据访问层，逐方法移植
// core/infrastructure/database/mysql.py 中 QA 闭包消费的仓储方法：
// 配额、用户、对话历史、订阅、总结/频道目录、请求队列与通知队列。
//
// 行为对齐要点：
//   - autocommit：go-sql-driver/database/sql 不显式开事务即为自动提交，
//     等价 mysql.py:102 的 autocommit=True（跨进程立即可见）；
//   - 时区：DSN 携带 time_zone='+00:00'，等价 init_command（mysql.py:108）；
//     一律 time.Now().UTC()，DATETIME 参数用 "2006-01-02 15:04:05" 格式；
//   - SQL 文本与 Python 版逐字一致（仅 %s 占位符换成 ?）。
package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/Sakura520222/Sakura-Bot/internal/config"
)

// 池参数（设计 §4.1 / 调研 §3【B】：空闲连接常驻、避免 churn；
// MaxIdle ≤ MaxOpen，避开 go-sql-driver#1279）。
const (
	maxOpenConns    = 6
	maxIdleConns    = 3
	connMaxLifetime = 30 * time.Minute
	connMaxIdleTime = 10 * time.Minute
)

// DSN 连接超时参数（对齐 mysql.py:105 的 connect_timeout=pool_timeout(30)
// 与设计 §4.1 的 read/write 60s）。
const (
	connectTimeout = 30 * time.Second
	readTimeout    = 60 * time.Second
	writeTimeout   = 60 * time.Second
)

// MySQLConfig 汇集 DSN 所需连接参数；默认值对齐 mysql.py:72-77。
// ConnectTimeout 为 0 时回退默认 30s（保持已有构造字面量 100% 兼容）。
type MySQLConfig struct {
	Host           string
	Port           string
	User           string
	Password       string
	Database       string
	Charset        string
	ConnectTimeout time.Duration
}

// MySQLConfigFromEnv 从 internal/config 的环境快照读取 MYSQL_* 键，
// 缺失时回退 mysql.py:72-77 的默认值。
func MySQLConfigFromEnv(env *config.Env) MySQLConfig {
	return MySQLConfig{
		Host:     env.GetDefault(config.KeyMySQLHost, "localhost"),
		Port:     strconv.FormatInt(env.GetIntDef(config.KeyMySQLPort, 3306), 10),
		User:     env.GetDefault(config.KeyMySQLUser, "sakura_bot"),
		Password: env.Get(config.KeyMySQLPassword),
		Database: env.GetDefault(config.KeyMySQLDatabase, "sakura_bot_db"),
		Charset:  env.GetDefault(config.KeyMySQLCharset, "utf8mb4"),
	}
}

// BuildDSN 构造冻结规格的 DSN：
//
//	user:pass@tcp(host:port)/db?charset=utf8mb4&parseTime=true&loc=UTC
//	  &timeout=30s&readTimeout=60s&writeTimeout=60s&time_zone=%27%2B00%3A00%27
//
// time_zone 由 go-sql-driver 作为连接级 SET 下发（复刻 init_command，
// mysql.py:108）；FormatDSN 会把 "'+00:00'" 百分号编码为 %27%2B00%3A00%27。
func BuildDSN(c MySQLConfig) string {
	mc := mysql.NewConfig()
	mc.User = c.User
	mc.Passwd = c.Password
	mc.Net = "tcp"
	mc.Addr = net.JoinHostPort(c.Host, c.Port)
	mc.DBName = c.Database
	// go-sql-driver v1.10 的 Charset 改为 Functional Option、Config 字段不导出，
	// FormatDSN 不会输出 charset 键；此处显式追加（驱动解析 case "charset"，
	// 连接建立时下发 SET NAMES utf8mb4，对齐 aiomysql charset=utf8mb4）。
	mc.ParseTime = true
	mc.Loc = time.UTC
	if c.ConnectTimeout > 0 {
		mc.Timeout = c.ConnectTimeout
	} else {
		mc.Timeout = connectTimeout
	}
	mc.ReadTimeout = readTimeout
	mc.WriteTimeout = writeTimeout
	mc.Params = map[string]string{"time_zone": "'+00:00'"}
	dsn := mc.FormatDSN()
	// 规格归一：loc 参数仅在非 UTC 时输出（UTC 即驱动默认，语义等价），
	// 为与冻结规格逐字一致显式补写；60s 会被格式化为 1m0s，同样归一。
	if !strings.Contains(dsn, "loc=") {
		dsn += "&loc=UTC"
	}
	dsn = strings.ReplaceAll(dsn, "readTimeout=1m0s", "readTimeout=60s")
	dsn = strings.ReplaceAll(dsn, "writeTimeout=1m0s", "writeTimeout=60s")
	return dsn + "&charset=" + url.QueryEscape(c.Charset)
}

// Manager 是 QA 进程的 MySQL 管理器（对齐 MySQLManager）。
// 所有仓储方法挂载其上；cfg 仅供 GetAllChannels 合并 config.CHANNELS 使用，
// 允许为 nil（测试场景，跳过合并并告警）。
type Manager struct {
	// DB 是底层连接池；Open 成功后可用。
	DB *sql.DB

	cfg *config.Config
}

// New 构造管理器并记录与 Python 版一致的初始化日志（mysql.py:86-89）。
func New(cfg *config.Config) *Manager {
	mc := MySQLConfigFromEnv(cfg.Env)
	slog.Info(fmt.Sprintf(
		"MySQL管理器初始化: %s@%s:%s/%s (charset=%s, pool_size=%d)",
		mc.User, mc.Host, mc.Port, mc.Database, mc.Charset, maxOpenConns,
	))
	return &Manager{cfg: cfg}
}

// NewMain 构造主 Bot MySQL 管理器，按 MYSQL_POOL_SIZE / MYSQL_MAX_OVERFLOW 记录日志。
func NewMain(cfg *config.Config) *Manager {
	mc := MySQLConfigFromEnv(cfg.Env)
	poolSize, maxOverflow := mainPoolParams(cfg)
	slog.Info(fmt.Sprintf(
		"MySQL管理器初始化: %s@%s:%s/%s (charset=%s, pool_size=%d, max_overflow=%d)",
		mc.User, mc.Host, mc.Port, mc.Database, mc.Charset, poolSize, maxOverflow,
	))
	return &Manager{cfg: cfg}
}

func mainPoolParams(cfg *config.Config) (poolSize, maxOverflow int) {
	poolSize = 5
	maxOverflow = 10
	if cfg != nil && cfg.Env != nil {
		if v := int(cfg.Env.GetIntDef(config.KeyMySQLPoolSize, 5)); v > 0 {
			poolSize = v
		}
		if v := int(cfg.Env.GetIntDef(config.KeyMySQLMaxOverflow, 10)); v >= 0 {
			maxOverflow = v
		}
	}
	return poolSize, maxOverflow
}

// newFromDB 以现成连接池构造（测试用；跳过 DSN 与池参数设置）。
func newFromDB(db *sql.DB, cfg *config.Config) *Manager {
	return &Manager{DB: db, cfg: cfg}
}

// Open 建立 *sql.DB 连接池并验证连通性。
// 池参数按冻结规格设置；失败返回与 Python 版逐字一致的连接错误文案
// （mysql.py:119-133：连接失败 → 启动失败，由 main 以非零码退出）。
func (m *Manager) Open(ctx context.Context) error {
	return m.OpenWithPoolSize(ctx, maxOpenConns, maxIdleConns)
}

// OpenMain 按主 Bot 环境变量 MYSQL_POOL_SIZE + MYSQL_MAX_OVERFLOW 打开连接池。
func (m *Manager) OpenMain(ctx context.Context) error {
	poolSize, maxOverflow := mainPoolParams(m.cfg)
	return m.OpenWithPoolSize(ctx, poolSize+maxOverflow, poolSize)
}

// OpenWithPoolSize 以指定池大小建立 *sql.DB 连接池（maxOpen<=0 时读取主 Bot 环境变量）。
func (m *Manager) OpenWithPoolSize(ctx context.Context, maxOpen, maxIdle int) error {
	mc := MySQLConfigFromEnv(m.cfg.Env)
	if m.cfg != nil && m.cfg.Env != nil {
		if sec := m.cfg.Env.GetIntDef(config.KeyMySQLPoolTimeout, 30); sec > 0 {
			mc.ConnectTimeout = time.Duration(sec) * time.Second
		}
	}
	if maxOpen <= 0 {
		poolSize, maxOverflow := mainPoolParams(m.cfg)
		maxOpen = poolSize + maxOverflow
		if maxIdle <= 0 {
			maxIdle = poolSize
		}
	}
	if maxIdle <= 0 {
		maxIdle = 1
	}
	if maxIdle > maxOpen {
		maxIdle = maxOpen
	}

	db, err := sql.Open("mysql", BuildDSN(mc))
	if err != nil {
		return fmt.Errorf("MySQL 连接失败: %s\n请检查 MySQL 服务是否运行，确认配置是否正确", err)
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(connMaxLifetime)
	db.SetConnMaxIdleTime(connMaxIdleTime)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("MySQL 连接失败: %s\n请检查 MySQL 服务是否运行，确认配置是否正确", err)
	}
	m.DB = db
	slog.Info("MySQL连接池创建成功（时区: UTC, autocommit=True）")
	return nil
}

// Close 关闭连接池（幂等）。
func (m *Manager) Close() error {
	if m.DB == nil {
		return nil
	}
	err := m.DB.Close()
	m.DB = nil
	return err
}

// InitDatabase 幂等初始化业务表结构（对齐 mysql.py:91-136 init_database
// 与 qa_bot.py post_init 的调用点；失败返回 error，启动即失败）。
func (m *Manager) InitDatabase(ctx context.Context) error {
	return initSchema(ctx, m.DB)
}

// InitMainDatabase 幂等初始化主 Bot 全部核心表/媒体表并执行增量迁移
// （对齐 core/initializers/database_initializer.py DatabaseInitializer.initialize）。
func (m *Manager) InitMainDatabase(ctx context.Context) error {
	if err := m.InitDatabase(ctx); err != nil {
		return err
	}
	// WebUI can backfill comments before the assistant process is started.
	if err := m.InitComments(ctx); err != nil {
		return err
	}
	return m.RunMigrations(ctx)
}

// HoldMainBotLease 获取主进程单实例 MySQL 命名锁（main_runtime:<db>），
// 防止双主实例并发运行（对齐 HoldAssistantLease 的心跳守护与释放语义）。
func (m *Manager) HoldMainBotLease(ctx context.Context, stop context.CancelFunc) (func(), error) {
	conn, err := m.DB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var name string
	if err := conn.QueryRowContext(ctx, `SELECT CONCAT('main_runtime:',DATABASE())`).Scan(&name); err != nil {
		_ = conn.Close()
		return nil, err
	}
	var locked sql.NullInt64
	if err := conn.QueryRowContext(ctx, `SELECT GET_LOCK(?,0)`, name).Scan(&locked); err != nil || !locked.Valid || locked.Int64 != 1 {
		if err != nil {
			killCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_, _ = conn.ExecContext(killCtx, `KILL CONNECTION_ID()`)
			cancel()
		}
		_ = conn.Close()
		return nil, fmt.Errorf("another main bot runtime holds the database lease")
	}
	monitor, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-monitor.Done():
				return
			case <-ticker.C:
				check, finish := context.WithTimeout(monitor, 3*time.Second)
				var valid int
				e := conn.QueryRowContext(check, `SELECT IS_USED_LOCK(?)=CONNECTION_ID()`, name).Scan(&valid)
				finish()
				if e != nil || valid != 1 {
					if stop != nil {
						stop()
					}
					return
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		var released sql.NullInt64
		if err := conn.QueryRowContext(cleanup, `SELECT RELEASE_LOCK(?)`, name).Scan(&released); err != nil || !released.Valid || released.Int64 != 1 {
			_, _ = conn.ExecContext(cleanup, `KILL CONNECTION_ID()`)
		}
		_ = conn.Close()
	}, nil
}

// DBVersion 读取 db_version 表的当前版本号（缺表/无行返回 0）。
// 对齐 Python 侧 _db_version = 6 的启动核对点。
func (m *Manager) DBVersion(ctx context.Context) (int64, error) {
	var v sql.NullInt64
	err := m.DB.QueryRowContext(ctx,
		"SELECT version FROM db_version ORDER BY version DESC LIMIT 1").Scan(&v)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	return v.Int64, nil
}
