// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package db

import (
	"fmt"
	"time"
)

// 时间纪律（行为规格 R3）：全 UTC；MySQL DATETIME 参数不带时区后缀；
// guard 表 DATETIME(6) 参数带 6 位微秒（对齐 pymysql escape_datetime
// 恒定输出 .ffffff 的行为）；isoformat 字符串按 Python datetime.isoformat
// 语义（秒或 6 位微秒，无时区）。
const (
	// timeLayout 是业务表 DATETIME 参数格式（任务规格冻结：
	// Format("2006-01-02 15:04:05")）。
	timeLayout = "2006-01-02 15:04:05"
	// datetime6Layout 是 guard 表 DATETIME(6) 参数格式（恒 6 位微秒）。
	datetime6Layout = "2006-01-02 15:04:05.000000"
	// dateLayout 是配额日键格式（quota 的 %Y-%m-%d，UTC）。
	dateLayout = "2006-01-02"
)

// NowUTC 返回截断到微秒精度的当前 UTC 时间。
// Go time.Now() 携带纳秒，Python datetime 只有微秒；统一截断避免
// DATETIME(6) 写入时被驱动四舍五入产生与 Python 不同的边界值。
func NowUTC() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

// QuotaDate 返回 UTC 配额日键（datetime.now(UTC).strftime("%Y-%m-%d")）。
func QuotaDate(t time.Time) string {
	return t.UTC().Format(dateLayout)
}

// MysqlDatetime 格式化业务表 DATETIME 参数。
func MysqlDatetime(t time.Time) string {
	return t.Format(timeLayout)
}

// MysqlDatetime6 格式化 guard 表 DATETIME(6) 参数。
func MysqlDatetime6(t time.Time) string {
	return t.Format(datetime6Layout)
}

// PythonISOFormat 复刻 Python datetime.isoformat()（无时区、无参数）：
// 微秒为 0 时输出 "YYYY-MM-DDTHH:MM:SS"，否则追加恒 6 位微秒
// （Python 补零到 6 位，如 .100000）。guard 表 payload 里的 at/error
// 时间戳依赖该精确格式（行为规格 R3）。
func PythonISOFormat(t time.Time) string {
	base := t.Format("2006-01-02T15:04:05")
	micros := t.Nanosecond() / 1000
	if micros != 0 {
		return fmt.Sprintf("%s.%06d", base, micros)
	}
	return base
}
