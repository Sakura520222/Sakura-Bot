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
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
)

// RuntimeJSON 跨进程热读 data/config.json：
// 每次 Read 都 stat，以 (ino, mtime_ns, size) 三元组判断文件替换
// （对齐 core/config/runtime.py:32-50）。
//
// 行为要点：
//   - stat 失败（文件被临时替换/删除）：从未成功加载 → 空 dict；
//     已加载 → 保留旧值（“临时缺失不能把已生效的限额或凭据退回环境变量”）；
//   - stat 三元组变化但解析失败：从未加载 → 视为空（Init 阶段即报错退出）；
//     已加载 → 记 warning 并保留旧值；版本号照常更新
//     （对齐 Python except 分支后仍执行 self._version = version）；
//   - Windows 无 Ino：platformIno 返回 (0,false)，三元组退化为 (mtime_ns, size)
//     （评审 #6/设计 §17-6；生产为 Linux，不受影响）。
type RuntimeJSON struct {
	mu    sync.Mutex
	path  string
	ver   fileVersion
	have  bool
	data  map[string]any
	ready bool
}

// NewRuntimeJSON 以显式路径构建热读器（不立即读盘，读盘由 Init/Read 触发）。
func NewRuntimeJSON(path string) *RuntimeJSON {
	return &RuntimeJSON{path: path}
}

// fileVersion 是可比较的 stat 版本三元组。
type fileVersion struct {
	ino     uint64
	hasIno  bool
	mtimeNs int64
	size    int64
}

// Init 完成首次加载；文件缺失或解析失败返回 error（进程启动失败语义）。
func (r *RuntimeJSON) Init() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	fi, err := os.Stat(r.path)
	if err != nil {
		return fmt.Errorf("config.json 不可访问: %w", err)
	}
	data, err := readJSONObject(r.path)
	if err != nil {
		return fmt.Errorf("config.json 解析失败: %w", err)
	}
	r.data = data
	r.ready = true
	r.ver = versionOf(fi)
	r.have = true
	return nil
}

// Read 返回最新配置（顶层 map 拷贝）。永不失败：
// 首次失败返回空 dict（启动失败由 Init 报告），后续失败保留旧值。
func (r *RuntimeJSON) Read() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()

	fi, err := os.Stat(r.path)
	if err != nil {
		if !r.ready {
			return map[string]any{}
		}
		// 文件临时缺失：保留旧值（runtime.py:35-39）。
		return snapshotLocked(r.data)
	}

	ver := versionOf(fi)
	if !r.have || ver != r.ver {
		data, err := readJSONObject(r.path)
		if err != nil {
			if !r.ready {
				// Python 侧未加载时 raise；此处返回空 dict，Init 负责启动失败。
				r.ver, r.have = ver, true
				return map[string]any{}
			}
			slog.Warn("运行时配置暂时无效，继续使用最近一次有效配置", "error", err)
		} else {
			r.data = data
			r.ready = true
		}
		// 版本号照常更新（对齐 runtime.py:49 在 except 分支后仍执行）。
		r.ver, r.have = ver, true
	}
	return snapshotLocked(r.data)
}

// snapshotLocked 返回顶层 map 浅拷贝，避免调用方改动缓存。
func snapshotLocked(data map[string]any) map[string]any {
	out := make(map[string]any, len(data))
	for k, v := range data {
		out[k] = v
	}
	return out
}

// versionOf 计算文件的 stat 版本三元组（ino, mtime_ns, size）。
func versionOf(fi os.FileInfo) fileVersion {
	ino, hasIno := platformIno(fi)
	return fileVersion{
		ino:     ino,
		hasIno:  hasIno,
		mtimeNs: fi.ModTime().UnixNano(),
		size:    fi.Size(),
	}
}

// readJSONObject 读取并解析 JSON 顶层对象；数值保留 json.Number
// 以便 EffectiveQuota 严格区分 3 与 3.0（对齐 pydantic strict=True）。
func readJSONObject(path string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	dec := json.NewDecoder(f)
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}
