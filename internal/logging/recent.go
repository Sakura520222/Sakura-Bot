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
	"bytes"
	"io"
	"os"
	"strings"
)

const (
	// tailChunkBytes 单次从文件尾部回溯读取的分块大小（64KiB）。
	tailChunkBytes = 64 * 1024
	// maxTailScanBytes 单次 ReadRecentLogs 最多从文件尾部向前扫描的字节上限（4MiB），
	// 确保即使面对数百 MB 的异常日志文件也绝不全量载入内存。
	maxTailScanBytes = 4 * 1024 * 1024

	defaultRecentLines = 100
	minRecentLines     = 1
	maxRecentLines     = 1000
)

// ReadRecentLogs 从日志文件尾部有界读取最近日志行（对齐 system.py:get_recent_logs）：
//   - 文件不存在或不是普通文件时返回空切片与 nil error；
//   - 从文件末尾按 64KiB 分块逆向扫描（最多回溯 4MiB），绝不全量读入大文件；
//   - 支持可选的日志级别过滤（levelFilter，不区分大小写）与关键字过滤（keyword，不区分大小写）；
//   - 按时间正序返回最多 maxLines 条（范围 1..1000，0 取默认 100）。
func ReadRecentLogs(logPath string, maxLines int, levelFilter string, keyword string) ([]string, error) {
	if maxLines == 0 {
		maxLines = defaultRecentLines
	} else if maxLines < minRecentLines {
		maxLines = minRecentLines
	} else if maxLines > maxRecentLines {
		maxLines = maxRecentLines
	}

	fi, err := os.Stat(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return []string{}, nil
	}

	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	fi, err = f.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()
	if size <= 0 {
		return []string{}, nil
	}

	levelUpper := strings.ToUpper(strings.TrimSpace(levelFilter))
	keywordLower := strings.ToLower(keyword)

	minStart := size - maxTailScanBytes
	if minStart < 0 {
		minStart = 0
	}

	matchedRev := make([]string, 0, min(maxLines, 128))
	var carry []byte
	endPos := size

	for endPos > minStart && len(matchedRev) < maxLines {
		readLen := int64(tailChunkBytes)
		if rem := endPos - minStart; rem < readLen {
			readLen = rem
		}
		startPos := endPos - readLen

		buf := make([]byte, int(readLen)+len(carry))
		n, readErr := readFullAt(f, buf[:int(readLen)], startPos)
		if readErr != nil && readErr != io.EOF {
			return nil, readErr
		}
		copy(buf[n:], carry)
		buf = buf[:n+len(carry)]

		// 文件最末尾的单个换行符仅表示最后一行结束，不产生额外空行。
		if endPos == size && len(buf) > 0 && buf[len(buf)-1] == '\n' {
			buf = buf[:len(buf)-1]
		}

		for len(matchedRev) < maxLines {
			idx := bytes.LastIndexByte(buf, '\n')
			if idx < 0 {
				break
			}
			line := trimLineCR(string(buf[idx+1:]))
			buf = buf[:idx]
			if matchesLogFilter(line, levelUpper, keywordLower) {
				matchedRev = append(matchedRev, line)
			}
		}

		carry = append(carry[:0], buf...)
		endPos = startPos
	}

	// 仅当回溯到文件物理起点（endPos == 0）时，carry 才是完整的第一行；
	// 若因触及 4MiB 上限停止（endPos > 0），carry 为被截断的半行，予以丢弃以防乱码或残缺。
	if len(matchedRev) < maxLines && endPos == 0 {
		line := trimLineCR(string(carry))
		if matchesLogFilter(line, levelUpper, keywordLower) {
			matchedRev = append(matchedRev, line)
		}
	}

	// 逆序还原为时间正序（旧 → 新）。
	for i, j := 0, len(matchedRev)-1; i < j; i, j = i+1, j-1 {
		matchedRev[i], matchedRev[j] = matchedRev[j], matchedRev[i]
	}
	return matchedRev, nil
}

func readFullAt(f *os.File, buf []byte, off int64) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := f.ReadAt(buf[total:], off+int64(total))
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func trimLineCR(s string) string {
	return strings.TrimSuffix(s, "\r")
}

func matchesLogFilter(line, levelUpper, keywordLower string) bool {
	if levelUpper != "" && !strings.Contains(strings.ToUpper(line), levelUpper) {
		return false
	}
	if keywordLower != "" && !strings.Contains(strings.ToLower(line), keywordLower) {
		return false
	}
	return true
}
