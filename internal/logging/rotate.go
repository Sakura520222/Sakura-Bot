// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package logging

import (
	"os"
	"strconv"
	"sync"
)

// rotatingWriter 是对 CPython logging.RotatingFileHandler 轮转语义的
// 手写 rename 链移植（10MB×5）：
//
//   - 写入前若 size+len(msg) >= maxBytes（且 maxBytes>0）触发轮转；
//   - 轮转：qa-bot.log.N-1 → .N、…、.1 → .2（先删目标再改名），
//     当前文件 → .1，最后以追加模式重新打开（对齐 mode="a"）；
//   - backupCount=0：不重命名，仅关闭重开（与 Python 现状一致，文件继续追加）；
//   - maxBytes<=0：从不轮转。
type rotatingWriter struct {
	mu     sync.Mutex
	path   string
	max    int64
	backup int

	f    *os.File
	size int64
}

// newRotatingWriter 打开（或创建）日志文件并记录当前大小。
func newRotatingWriter(path string, maxSizeBytes, backupCount int) (*rotatingWriter, error) {
	w := &rotatingWriter{
		path:   path,
		max:    int64(maxSizeBytes),
		backup: backupCount,
	}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

// open 以追加模式打开文件并同步当前大小（对齐 self._open() + seek(0,2)/tell()）。
func (w *rotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.f = f
	w.size = fi.Size()
	return nil
}

// Write 在必要时轮转后追加写入。
func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.f == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	// 对齐 shouldRollover：stream.tell() + len(msg) >= maxBytes 且 maxBytes>0。
	if w.max > 0 && w.size+int64(len(p)) >= w.max {
		if err := w.rollover(); err != nil {
			// 轮转失败时继续写入当前文件（SafeRotatingFileHandler 吞错语义）。
			// 文件句柄可能已失效，尝试重开。
			_ = w.open()
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rollover 执行 rename 链轮转（对齐 RotatingFileHandler.doRollover）。
func (w *rotatingWriter) rollover() error {
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	if w.backup > 0 {
		for i := w.backup - 1; i >= 1; i-- {
			src := w.path + "." + strconv.Itoa(i)
			dst := w.path + "." + strconv.Itoa(i+1)
			if !fileExists(src) {
				continue
			}
			if fileExists(dst) {
				if err := os.Remove(dst); err != nil {
					return err
				}
			}
			if err := os.Rename(src, dst); err != nil {
				return err
			}
		}
		// 当前文件 → .1（先删旧 .1）。
		first := w.path + ".1"
		if fileExists(first) {
			if err := os.Remove(first); err != nil {
				return err
			}
		}
		if err := os.Rename(w.path, first); err != nil {
			return err
		}
	}
	// backupCount=0：不重命名，按 Python 行为以追加模式重开（文件继续增长）。
	return w.open()
}

// Close 关闭底层文件（供进程优雅退出调用）。
func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
