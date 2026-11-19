// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

//go:build unix

package config

import (
	"os"
	"syscall"
)

// platformIno 在类 Unix 平台提取 inode（runtime.py:34 的 st_ino）。
func platformIno(fi os.FileInfo) (uint64, bool) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Ino, true
	}
	return 0, false
}
