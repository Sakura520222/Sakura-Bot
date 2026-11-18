// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package config

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/contracts"
	"github.com/Sakura520222/Sakura-Bot/internal/filelock"
)

// MaxConfigBackups 历史有效备份最大保留份数（对齐 core/config/persistence.py:BACKUP_LIMIT = 30）。
const MaxConfigBackups = 30

var (
	// ErrConfigValidation 表示配置结构或业务规则校验失败（对应 HTTP 400）。
	ErrConfigValidation = errors.New("config validation failed")
	// ErrConfigConflict 表示乐观锁版本或并发字段冲突（对应 HTTP 409）。
	ErrConfigConflict = errors.New("config conflict")
	// ErrConfigPersistence 表示底层文件锁或磁盘持久化失败。
	ErrConfigPersistence = errors.New("config persistence error")
	// ErrProfileNotFound 表示指定的 AI 档案或厂商不存在（对应 HTTP 404）。
	ErrProfileNotFound = errors.New("ai profile or provider not found")
)

var validLogLevels = map[string]bool{
	"DEBUG":    true,
	"INFO":     true,
	"WARNING":  true,
	"ERROR":    true,
	"CRITICAL": true,
}

func (c *Config) setSnapshot(doc map[string]any) {
	c.snapMu.Lock()
	defer c.snapMu.Unlock()
	c.lastSnap = deepCopyMap(doc)
}

func (c *Config) getSnapshot() map[string]any {
	c.snapMu.Lock()
	defer c.snapMu.Unlock()
	if c.lastSnap == nil {
		return nil
	}
	return deepCopyMap(c.lastSnap)
}

// ValidateConfig 校验候选配置字典的完整性与业务约束（对齐 core/config/validator.py:ConfigValidator.validate_config），
// 并确保错误文本绝不回显原始密钥。
func ValidateConfig(doc map[string]any) error {
	_, _, err := normalizeAndValidateConfig(doc)
	return err
}

// normalizeAndValidateConfig 将任意内存 map 经 JSON 编解码归一为 UseNumber() 形态后执行全量严格校验。
func normalizeAndValidateConfig(doc map[string]any) (map[string]any, []byte, error) {
	if doc == nil {
		return nil, nil, fmt.Errorf("%w: 配置根节点必须是对象", ErrConfigValidation)
	}
	if hasNonFiniteFloat(doc) {
		return nil, nil, fmt.Errorf("%w: 配置不能包含 NaN 或 Infinity", ErrConfigValidation)
	}

	var rawBuf bytes.Buffer
	enc := json.NewEncoder(&rawBuf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, nil, fmt.Errorf("%w: 配置无法序列化为 JSON", ErrConfigValidation)
	}
	encoded := rawBuf.Bytes()

	dec := json.NewDecoder(bytes.NewReader(encoded))
	dec.UseNumber()
	var normalized map[string]any
	if err := dec.Decode(&normalized); err != nil {
		return nil, nil, fmt.Errorf("%w: JSON 解析失败", ErrConfigValidation)
	}
	if normalized == nil {
		normalized = map[string]any{}
	}

	if err := validateNormalizedMap(normalized); err != nil {
		return nil, nil, err
	}
	return normalized, encoded, nil
}

func validateNormalizedMap(cfg map[string]any) error {
	// 1. AI 档案/厂商/路由/视觉严格校验。
	if _, err := parseAIProfiles(cfg); err != nil {
		return fmt.Errorf("%w: %v", ErrConfigValidation, err)
	}

	// 2. 顶层旧版 AI 字段类型检查。
	for _, field := range []string{"api_key", "base_url", "model"} {
		if raw, exists := cfg[field]; exists && raw != nil {
			if _, ok := raw.(string); !ok {
				return fmt.Errorf("%w: %s 必须是字符串或 null", ErrConfigValidation, field)
			}
		}
	}

	// 3. channels 必须是字符串数组。
	if raw, exists := cfg["channels"]; exists {
		arr, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("%w: channels 必须是字符串数组", ErrConfigValidation)
		}
		for _, item := range arr {
			if _, ok := item.(string); !ok {
				return fmt.Errorf("%w: channels 元素必须是字符串", ErrConfigValidation)
			}
		}
	}

	// 4. qa_bot_quota 严格整数与范围校验。
	if raw, exists := cfg["qa_bot_quota"]; exists {
		obj, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("%w: qa_bot_quota 必须是对象", ErrConfigValidation)
		}
		for k := range obj {
			if k != "user_daily_limit" && k != "total_daily_limit" {
				return fmt.Errorf("%w: qa_bot_quota 含未知字段: %s", ErrConfigValidation, k)
			}
		}
		if v, ok := obj["user_daily_limit"]; ok {
			n, err := strictJSONInt(v)
			if err != nil || n < contracts.QuotaUserMin || n > contracts.QuotaMax {
				return fmt.Errorf("%w: qa_bot_quota.user_daily_limit 无效", ErrConfigValidation)
			}
		}
		if v, ok := obj["total_daily_limit"]; ok {
			n, err := strictJSONInt(v)
			if err != nil || n < contracts.QuotaTotalMin || n > contracts.QuotaMax {
				return fmt.Errorf("%w: qa_bot_quota.total_daily_limit 无效", ErrConfigValidation)
			}
		}
	}

	// 5. qa_bot_runtime 枚举校验。
	if raw, exists := cfg["qa_bot_runtime"]; exists {
		s, ok := raw.(string)
		val := strings.ToLower(strings.TrimSpace(s))
		if !ok || (val != "python" && val != "go") {
			return fmt.Errorf("%w: qa_bot_runtime 只能是 python 或 go", ErrConfigValidation)
		}
	}

	// 6. log_level 枚举校验。
	if raw, exists := cfg["log_level"]; exists {
		s, ok := raw.(string)
		if !ok || !validLogLevels[strings.ToUpper(strings.TrimSpace(s))] {
			return fmt.Errorf("%w: log_level 必须是有效的日志级别", ErrConfigValidation)
		}
	}

	// 7. summary_schedules 校验。
	if raw, exists := cfg["summary_schedules"]; exists {
		scheds, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("%w: summary_schedules 必须是对象类型", ErrConfigValidation)
		}
		for _, rawItem := range scheds {
			item, ok := rawItem.(map[string]any)
			if !ok {
				return fmt.Errorf("%w: 频道时间配置必须是对象类型", ErrConfigValidation)
			}
			if rawFreq, hasFreq := item["frequency"]; hasFreq && rawFreq != nil {
				freq, ok := rawFreq.(string)
				if !ok || (freq != "daily" && freq != "weekly") {
					return fmt.Errorf("%w: 时间配置的 frequency 必须是 daily 或 weekly", ErrConfigValidation)
				}
				if freq == "weekly" {
					if rawDays, hasDays := item["days"]; hasDays {
						daysArr, ok := rawDays.([]any)
						if !ok || len(daysArr) == 0 {
							return fmt.Errorf("%w: weekly 模式 days 必须是非空数组", ErrConfigValidation)
						}
						for _, d := range daysArr {
							ds, ok := d.(string)
							if !ok || !validWeekDays[strings.ToLower(strings.TrimSpace(ds))] {
								return fmt.Errorf("%w: 时间配置含无效的星期值", ErrConfigValidation)
							}
						}
					}
				}
			}
			if rawHour, hasHour := item["hour"]; hasHour && rawHour != nil {
				h, err := strictJSONInt(rawHour)
				if err != nil || h < 0 || h > 23 {
					return fmt.Errorf("%w: 时间配置的 hour 必须是 0-23 的整数", ErrConfigValidation)
				}
			}
			if rawMin, hasMin := item["minute"]; hasMin && rawMin != nil {
				m, err := strictJSONInt(rawMin)
				if err != nil || m < 0 || m > 59 {
					return fmt.Errorf("%w: 时间配置的 minute 必须是 0-59 的整数", ErrConfigValidation)
				}
			}
		}
	}

	// 8. 其余复合对象字段类型检查。
	for _, objField := range []string{"channel_poll_settings", "forwarding", "channel_auto_poll_settings"} {
		if raw, exists := cfg[objField]; exists {
			if _, ok := raw.(map[string]any); !ok {
				return fmt.Errorf("%w: %s 必须是对象类型", ErrConfigValidation, objField)
			}
		}
	}

	return nil
}

// SaveConfigMap 原子验证并完整保存 doc 到 config.json（包含互斥锁、.lock 文件锁、.last_valid、
// 最多 30 份历史备份轮转、.tmp + Sync + Rename、立即刷新 RuntimeJSON 缓存及发布 ConfigChangedEvent）。
func (c *Config) SaveConfigMap(doc map[string]any) error {
	if doc == nil {
		return fmt.Errorf("%w: 配置不能为空", ErrConfigValidation)
	}
	normalized, encoded, err := normalizeAndValidateConfig(doc)
	if err != nil {
		return err
	}

	var ev *ConfigChangedEvent
	err = c.withExclusiveLock(func(cfgPath string) error {
		var current map[string]any
		if diskDoc, _, readErr := readAndValidateConfigFile(cfgPath); readErr == nil {
			current = diskDoc
		} else if prev := c.getSnapshot(); prev != nil {
			current = prev
		}

		var commitErr error
		ev, commitErr = c.commitLocked(cfgPath, current, normalized, encoded)
		return commitErr
	})
	if err != nil {
		return err
	}
	if ev != nil {
		c.EventBus().Publish(context.Background(), *ev)
	}
	return nil
}

// UpdateConfig 在同一进程锁与文件锁事务内读取最新配置、调用 mutator 修改、校验并原子落盘；
// 若 mutator 返回错误或修改后的配置未通过校验，磁盘与内存缓存均保持原状（回滚修改）。
func (c *Config) UpdateConfig(mutator func(doc map[string]any) error) error {
	if mutator == nil {
		return nil
	}

	var ev *ConfigChangedEvent
	err := c.withExclusiveLock(func(cfgPath string) error {
		var current map[string]any
		if _, statErr := os.Stat(cfgPath); statErr == nil {
			diskDoc, _, readErr := readAndValidateConfigFile(cfgPath)
			if readErr != nil {
				return readErr
			}
			current = diskDoc
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("%w: 读取配置文件状态失败: %v", ErrConfigPersistence, statErr)
		}

		working := map[string]any{}
		if current != nil {
			working = deepCopyMap(current)
		}
		if err := mutator(working); err != nil {
			return err
		}

		normalized, encoded, err := normalizeAndValidateConfig(working)
		if err != nil {
			return err
		}

		var commitErr error
		ev, commitErr = c.commitLocked(cfgPath, current, normalized, encoded)
		return commitErr
	})
	if err != nil {
		return err
	}
	if ev != nil {
		c.EventBus().Publish(context.Background(), *ev)
	}
	return nil
}

// Reload 从磁盘重载并验证 config.json（供 FileWatcher 或手动 /config/reload 调用）。
// 若外部写入导致配置损坏或校验失败，在同一文件锁内自动从 .backups / .last_valid / 内存快照回滚磁盘文件，
// 并触发 ConfigValidationErrorEvent。
func (c *Config) Reload(ctx context.Context) (rolledBack bool, err error) {
	if ctx == nil {
		ctx = context.Background()
	}

	var (
		changedEv *ConfigChangedEvent
		valErrEv  *ConfigValidationErrorEvent
	)

	lockErr := c.withExclusiveLock(func(cfgPath string) error {
		previous := c.getSnapshot()
		diskDoc, fi, readErr := readAndValidateConfigFile(cfgPath)
		if readErr != nil {
			recovery := c.findRecoveryLocked(cfgPath, previous)
			if recovery != nil {
				if normRec, encRec, recErr := normalizeAndValidateConfig(recovery); recErr == nil {
					if writeErr := atomicWriteFile(cfgPath, encRec, 0o600); writeErr == nil {
						rolledBack = true
						if rfi, statErr := os.Stat(cfgPath); statErr == nil {
							c.runtime.setValidated(normRec, rfi)
						} else {
							c.runtime.setValidated(normRec, nil)
						}
						c.setSnapshot(normRec)
					}
				}
			}
			valErrEv = &ConfigValidationErrorEvent{
				Error:      readErr.Error(),
				ConfigPath: cfgPath,
				RolledBack: rolledBack,
				Timestamp:  time.Now(),
			}
			return readErr
		}

		if previous != nil && configsEqualMap(previous, diskDoc) {
			c.runtime.setValidated(diskDoc, fi)
			return nil
		}

		// 新配置校验通过：归档旧版与当前有效快照，并更新 .last_valid 恢复点。
		if previous != nil {
			_ = c.backupValidLocked(cfgPath, previous, false)
		}
		_ = c.backupValidLocked(cfgPath, diskDoc, true)
		pruneConfigBackups(backupsDirPath(cfgPath))

		c.runtime.setValidated(diskDoc, fi)
		c.setSnapshot(diskDoc)
		ver := c.version.Add(1)
		changedEv = &ConfigChangedEvent{
			Config:        deepCopyMap(diskDoc),
			OldConfig:     deepCopyMap(previous),
			Version:       ver,
			ChangedFields: CalculateChangedFields(previous, diskDoc),
			Timestamp:     time.Now(),
		}
		return nil
	})

	if valErrEv != nil {
		c.EventBus().PublishValidationError(ctx, *valErrEv)
	}
	if changedEv != nil {
		c.EventBus().Publish(ctx, *changedEv)
	}
	return rolledBack, lockErr
}

func (c *Config) commitLocked(cfgPath string, current, candidate map[string]any, encoded []byte) (*ConfigChangedEvent, error) {
	changed := current == nil || !configsEqualMap(current, candidate)
	if changed {
		if current != nil {
			if err := c.backupValidLocked(cfgPath, current, false); err != nil {
				return nil, err
			}
		}
		if err := atomicWriteFile(cfgPath, encoded, 0o600); err != nil {
			return nil, err
		}
	}

	persisted, fi, err := readAndValidateConfigFile(cfgPath)
	if err != nil {
		return nil, err
	}
	if !configsEqualMap(persisted, candidate) {
		return nil, fmt.Errorf("%w: 配置写入后回读不一致", ErrConfigPersistence)
	}

	if changed {
		if err := c.backupValidLocked(cfgPath, persisted, true); err != nil {
			return nil, err
		}
		pruneConfigBackups(backupsDirPath(cfgPath))
	}

	c.runtime.setValidated(persisted, fi)
	c.setSnapshot(persisted)

	if !changed {
		return nil, nil
	}
	ver := c.version.Add(1)
	return &ConfigChangedEvent{
		Config:        deepCopyMap(persisted),
		OldConfig:     deepCopyMap(current),
		Version:       ver,
		ChangedFields: CalculateChangedFields(current, persisted),
		Timestamp:     time.Now(),
	}, nil
}

func (c *Config) backupValidLocked(cfgPath string, validDoc map[string]any, replaceLastValid bool) error {
	_, encoded, err := normalizeAndValidateConfig(validDoc)
	if err != nil {
		return err
	}
	if err := archiveBackupSnapshot(backupsDirPath(cfgPath), validDoc, encoded); err != nil {
		return err
	}
	lastValid := lastValidPath(cfgPath)
	if !replaceLastValid {
		if _, _, err := readAndValidateConfigFile(lastValid); err == nil {
			return nil
		}
	}
	return atomicWriteFile(lastValid, encoded, 0o600)
}

func archiveBackupSnapshot(historyDir string, doc map[string]any, encoded []byte) error {
	existing := listBackupFilesDesc(historyDir)
	if len(existing) > 0 {
		if latest, _, err := readAndValidateConfigFile(existing[0]); err == nil && configsEqualMap(latest, doc) {
			return nil
		}
	}
	if err := os.MkdirAll(historyDir, 0o700); err != nil {
		return fmt.Errorf("%w: 创建配置备份目录失败: %v", ErrConfigPersistence, err)
	}
	var rnd [4]byte
	_, _ = rand.Read(rnd[:])
	ts := time.Now().UTC().Format("20060102T150405.000000Z")
	fileName := fmt.Sprintf("%s-%s.json", ts, hex.EncodeToString(rnd[:]))
	target := filepath.Join(historyDir, fileName)
	return atomicWriteFile(target, encoded, 0o600)
}

func pruneConfigBackups(historyDir string) {
	files := listBackupFilesDesc(historyDir)
	if len(files) <= MaxConfigBackups {
		return
	}
	for _, old := range files[MaxConfigBackups:] {
		_ = os.Remove(old)
	}
}

func listBackupFilesDesc(historyDir string) []string {
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		return nil
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		paths = append(paths, filepath.Join(historyDir, e.Name()))
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	return paths
}

func (c *Config) findRecoveryLocked(cfgPath string, inMemory map[string]any) map[string]any {
	candidates := append(listBackupFilesDesc(backupsDirPath(cfgPath)), lastValidPath(cfgPath))
	for _, path := range candidates {
		if doc, _, err := readAndValidateConfigFile(path); err == nil {
			return doc
		}
	}
	if inMemory != nil {
		if norm, _, err := normalizeAndValidateConfig(inMemory); err == nil {
			return norm
		}
	}
	return nil
}

func (c *Config) withExclusiveLock(fn func(cfgPath string) error) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	cfgPath := c.ConfigPath()
	parent := filepath.Dir(cfgPath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("%w: 创建配置目录失败: %v", ErrConfigPersistence, err)
	}

	lockCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lf, err := filelock.Acquire(lockCtx, cfgPath+".lock")
	if err != nil {
		return fmt.Errorf("%w: 无法获取配置文件锁: %v", ErrConfigPersistence, err)
	}
	defer lf.Close()

	return fn(cfgPath)
}

func lastValidPath(cfgPath string) string {
	return cfgPath + ".last_valid"
}

func backupsDirPath(cfgPath string) string {
	return cfgPath + ".backups"
}

func readAndValidateConfigFile(path string) (map[string]any, os.FileInfo, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: 配置文件不可访问", ErrConfigValidation)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: 无法打开配置文件", ErrConfigValidation)
	}
	defer func() { _ = f.Close() }()

	dec := json.NewDecoder(f)
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, nil, fmt.Errorf("%w: JSON 格式无效", ErrConfigValidation)
	}
	if out == nil {
		return nil, nil, fmt.Errorf("%w: 配置根节点必须是对象", ErrConfigValidation)
	}
	// 确保文件末尾不存在多余垃圾 token。
	var trailing any
	if dec.Decode(&trailing) == nil {
		return nil, nil, fmt.Errorf("%w: JSON 包含多余内容", ErrConfigValidation)
	}
	if err := validateNormalizedMap(out); err != nil {
		return nil, nil, err
	}
	return out, fi, nil
}

// atomicWriteFile 将字节写入同目录临时文件、fsync 刷盘后通过 os.Rename 原子替换目标路径。
func atomicWriteFile(path string, content []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("%w: 创建目录失败", ErrConfigPersistence)
	}
	if fi, err := os.Stat(path); err == nil {
		if existingMode := fi.Mode().Perm(); existingMode != 0 {
			perm = existingMode
		}
	}

	tmp, err := os.CreateTemp(dir, ".cfg-write-*.tmp")
	if err != nil {
		return fmt.Errorf("%w: 创建临时文件失败", ErrConfigPersistence)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("%w: 写入临时文件失败", ErrConfigPersistence)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("%w: 同步临时文件失败", ErrConfigPersistence)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("%w: 关闭临时文件失败", ErrConfigPersistence)
	}
	_ = os.Chmod(tmpName, perm)

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("%w: 原子重命名失败", ErrConfigPersistence)
	}
	cleanup = false
	return nil
}

func configsEqualMap(left, right map[string]any) bool {
	if len(left) != len(right) {
		return false
	}
	for k, lv := range left {
		rv, ok := right[k]
		if !ok || !configsEqualValue(lv, rv) {
			return false
		}
	}
	return true
}

func configsEqualValue(left, right any) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	switch lv := left.(type) {
	case map[string]any:
		rv, ok := right.(map[string]any)
		return ok && configsEqualMap(lv, rv)
	case []any:
		rv, ok := right.([]any)
		if !ok || len(lv) != len(rv) {
			return false
		}
		for i := range lv {
			if !configsEqualValue(lv[i], rv[i]) {
				return false
			}
		}
		return true
	case json.Number:
		if rv, ok := right.(json.Number); ok {
			return lv.String() == rv.String()
		}
		return false
	default:
		return reflect.DeepEqual(left, right)
	}
}

func hasNonFiniteFloat(v any) bool {
	switch val := v.(type) {
	case float64:
		return math.IsNaN(val) || math.IsInf(val, 0)
	case float32:
		return math.IsNaN(float64(val)) || math.IsInf(float64(val), 0)
	case map[string]any:
		for _, item := range val {
			if hasNonFiniteFloat(item) {
				return true
			}
		}
	case []any:
		for _, item := range val {
			if hasNonFiniteFloat(item) {
				return true
			}
		}
	}
	return false
}
