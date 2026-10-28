// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

//go:build windows

package config

import "os"

// platformIno 在 Windows 上无通用 Ino 可取（Python st_ino 亦不可靠），
// 退化为 (mtime_ns, size) 二元组版本号——评审 #6/设计 §17-6 的既定取舍；
// 生产环境为 Linux，三元组完整。
func platformIno(fi os.FileInfo) (uint64, bool) {
	_ = fi
	return 0, false
}
