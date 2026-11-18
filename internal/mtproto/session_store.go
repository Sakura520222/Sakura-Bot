// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package mtproto

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gotd/td/crypto"
	"github.com/gotd/td/session"
	_ "modernc.org/sqlite" // 注册纯 Go "sqlite" 驱动，支持只读 URI 导入 Telethon session
)

// NormalizeSessionPaths 将任意形式的会话路径归一化为基础路径与三个派生文件路径：
//   - basePath:   不带扩展名的基础路径
//   - gotdPath:   <basePath>.gotd.json（gotd/td 原生 JSON 会话文件）
//   - sqlitePath: <basePath>.session（Telethon SQLite 会话文件，只读导入，永不修改或删除）
//   - peersPath:  <basePath>.peers.json（本地 Peer 实体路由缓存）
func NormalizeSessionPaths(raw string) (basePath, gotdPath, sqlitePath, peersPath string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", "", ""
	}
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasSuffix(lower, ".gotd.json"):
		basePath = trimmed[:len(trimmed)-len(".gotd.json")]
		sqlitePath = basePath + ".session"
	case strings.HasSuffix(lower, ".peers.json"):
		basePath = trimmed[:len(trimmed)-len(".peers.json")]
		sqlitePath = basePath + ".session"
	case strings.HasSuffix(lower, ".session"):
		basePath = trimmed[:len(trimmed)-len(".session")]
		sqlitePath = trimmed
	default:
		basePath = trimmed
		sqlitePath = basePath + ".session"
	}
	gotdPath = basePath + ".gotd.json"
	peersPath = basePath + ".peers.json"
	return basePath, gotdPath, sqlitePath, peersPath
}

// buildReadOnlySQLiteDSN 构造只读 SQLite URI：file:...?mode=ro&_busy_timeout=5000。
func buildReadOnlySQLiteDSN(file string) string {
	p := strings.ReplaceAll(file, "\\", "/")
	p = strings.ReplaceAll(p, "%", "%25")
	p = strings.ReplaceAll(p, "?", "%3F")
	p = strings.ReplaceAll(p, "#", "%23")
	return "file:" + p + "?mode=ro&_busy_timeout=5000"
}

// atomicWriteFile0600 将数据以 0600 权限写入同目录临时文件后原子重命名到目标路径。
func atomicWriteFile0600(destPath string, data []byte) error {
	dir := filepath.Dir(destPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create session dir %q: %w", dir, err)
		}
	}

	tmpFile, err := os.CreateTemp(dir, filepath.Base(destPath)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file for %q: %w", destPath, err)
	}
	tmpPath := tmpFile.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("chmod temp file %q: %w", tmpPath, err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write temp file %q: %w", tmpPath, err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("sync temp file %q: %w", tmpPath, err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file %q: %w", tmpPath, err)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("rename %q -> %q: %w", tmpPath, destPath, err)
	}
	cleanupTmp = false
	_ = os.Chmod(destPath, 0o600)
	return nil
}

// HybridSessionStorage 实现 gotd/td 的 session.Storage 接口：
//   - 日常读写原子保存在 <basePath>.gotd.json（0600）；
//   - 当 <basePath>.gotd.json 尚不存在时，自动探测同前缀的 Telethon <basePath>.session，
//     只读提取 256 字节 auth_key 与 DC 地址并转换为 gotd 会话格式落盘；
//   - 绝不修改或删除原有的 .session 文件。
type HybridSessionStorage struct {
	mu         sync.Mutex
	basePath   string
	gotdPath   string
	sqlitePath string
	mem        []byte
	skipImport bool
	imported   bool
}

var _ session.Storage = (*HybridSessionStorage)(nil)

// NewHybridSessionStorage 创建混合会话存储实例。sessionPath 为空时降级为纯内存会话存储。
func NewHybridSessionStorage(sessionPath string) *HybridSessionStorage {
	basePath, gotdPath, sqlitePath, _ := NormalizeSessionPaths(sessionPath)
	return &HybridSessionStorage{
		basePath:   basePath,
		gotdPath:   gotdPath,
		sqlitePath: sqlitePath,
	}
}

// BasePath 返回不带扩展名的会话基础路径。
func (s *HybridSessionStorage) BasePath() string { return s.basePath }

// GotdPath 返回 <basePath>.gotd.json 路径。
func (s *HybridSessionStorage) GotdPath() string { return s.gotdPath }

// SQLitePath 返回 <basePath>.session 路径。
func (s *HybridSessionStorage) SQLitePath() string { return s.sqlitePath }

// ImportedFromTelethon 报告当前会话是否曾从 Telethon .session 导入。
func (s *HybridSessionStorage) ImportedFromTelethon() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.imported
}

// LoadSession 加载 gotd 会话字节；若 .gotd.json 不存在则尝试从 Telethon .session 只读迁移。
func (s *HybridSessionStorage) LoadSession(ctx context.Context) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.gotdPath == "" {
		if len(s.mem) == 0 {
			return nil, session.ErrNotFound
		}
		return append([]byte(nil), s.mem...), nil
	}

	data, err := os.ReadFile(s.gotdPath)
	if err == nil && len(data) > 0 {
		return data, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read gotd session %q: %w", s.gotdPath, err)
	}

	if s.skipImport || s.sqlitePath == "" {
		return nil, session.ErrNotFound
	}

	fi, statErr := os.Stat(s.sqlitePath)
	if statErr != nil || fi.IsDir() {
		return nil, session.ErrNotFound
	}

	importedBytes, impErr := importTelethonSQLiteSession(ctx, s.sqlitePath)
	if impErr != nil {
		if errors.Is(impErr, session.ErrNotFound) {
			return nil, session.ErrNotFound
		}
		return nil, impErr
	}

	if err := atomicWriteFile0600(s.gotdPath, importedBytes); err != nil {
		return nil, fmt.Errorf("persist imported gotd session %q: %w", s.gotdPath, err)
	}
	s.imported = true
	return append([]byte(nil), importedBytes...), nil
}

// StoreSession 将 gotd 会话字节原子写入 <basePath>.gotd.json（权限 0600）。
func (s *HybridSessionStorage) StoreSession(_ context.Context, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.gotdPath == "" {
		s.mem = append([]byte(nil), data...)
		return nil
	}
	return atomicWriteFile0600(s.gotdPath, data)
}

// Clear 删除失效的 <basePath>.gotd.json（并跳过再次导入旧 .session），以便重新鉴权。
// 绝不修改或删除原有的 <basePath>.session 文件。
func (s *HybridSessionStorage) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.mem = nil
	s.skipImport = true
	s.imported = false
	if s.gotdPath == "" {
		return nil
	}
	if err := os.Remove(s.gotdPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove gotd session %q: %w", s.gotdPath, err)
	}
	return nil
}

func importTelethonSQLiteSession(ctx context.Context, sqlitePath string) ([]byte, error) {
	db, err := sql.Open("sqlite", buildReadOnlySQLiteDSN(sqlitePath))
	if err != nil {
		return nil, fmt.Errorf("open telethon sqlite %q: %w", sqlitePath, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	var (
		dcID    int
		addr    string
		port    int
		authKey []byte
	)
	err = db.QueryRowContext(
		ctx,
		`SELECT dc_id, COALESCE(server_address, ''), COALESCE(port, 443), auth_key FROM sessions WHERE length(auth_key) = 256 LIMIT 1`,
	).Scan(&dcID, &addr, &port, &authKey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || isNoSuchTableErr(err) {
			return nil, session.ErrNotFound
		}
		return nil, fmt.Errorf("query telethon sessions %q: %w", sqlitePath, err)
	}
	if len(authKey) != 256 {
		return nil, session.ErrNotFound
	}

	var k crypto.Key
	copy(k[:], authKey)
	id := k.WithID().ID

	addrStr := ""
	addr = strings.TrimSpace(addr)
	if addr != "" {
		if port <= 0 {
			port = 443
		}
		addrStr = net.JoinHostPort(addr, strconv.Itoa(port))
	}

	mem := &session.StorageMemory{}
	loader := session.Loader{Storage: mem}
	if err := loader.Save(ctx, &session.Data{
		DC:        dcID,
		Addr:      addrStr,
		AuthKey:   append([]byte(nil), authKey...),
		AuthKeyID: append([]byte(nil), id[:]...),
	}); err != nil {
		return nil, fmt.Errorf("encode imported session: %w", err)
	}
	return mem.LoadSession(ctx)
}

func isNoSuchTableErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table")
}
