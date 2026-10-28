// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package contracts

import (
	"testing"
	"time"
)

// TestControlMarkersLiteral 保证控制标记字符串不被误改（README 硬规则 9）。
func TestControlMarkersLiteral(t *testing.T) {
	if CtrlDone != "__DONE__" {
		t.Fatalf("CtrlDone 被改动: %q", CtrlDone)
	}
	if CtrlNewSession != "__NEW_SESSION__" {
		t.Fatalf("CtrlNewSession 被改动: %q", CtrlNewSession)
	}
	if CtrlErrPrefix != "__ERROR__:" {
		t.Fatalf("CtrlErrPrefix 被改动: %q", CtrlErrPrefix)
	}
}

// TestQuotaBounds 固化配额 clamp 边界常量。
func TestQuotaBounds(t *testing.T) {
	if QuotaUserMin != 1 || QuotaTotalMin != 10 || QuotaMax != 2147483647 {
		t.Fatalf("配额边界常量被改动: %d %d %d", QuotaUserMin, QuotaTotalMin, QuotaMax)
	}
}

// TestChannelInfoZeroTime 语义说明：零值 LastSummaryTime 等价 Python None。
func TestChannelInfoZeroTime(t *testing.T) {
	var info ChannelInfo
	if !info.LastSummaryTime.IsZero() {
		t.Fatal("ChannelInfo 零值 LastSummaryTime 应为零值 time.Time")
	}
	if (time.Time{}).IsZero() != true {
		t.Fatal("time.Time 零值判断异常")
	}
}
