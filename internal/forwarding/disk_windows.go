//go:build windows

// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package forwarding

import (
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceEx = kernel32.NewProc("GetDiskFreeSpaceExW")
)

func availableDiskBytes(dir string) (uint64, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}
	ptr, err := syscall.UTF16PtrFromString(absDir)
	if err != nil {
		return 0, err
	}
	var freeBytes uint64
	r1, _, callErr := procGetDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(unsafe.Pointer(&freeBytes)),
		0,
		0,
	)
	if r1 == 0 {
		return 0, callErr
	}
	return freeBytes, nil
}
