// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// Package scheduler 提供基于标准库的时区感知定时调度器：
// 支持标准 5 段 Cron 表达式（如 Wiki "0 */6 * * *"）、每日/每周频道总结计划、
// 固定间隔任务、单实例互斥执行（max_instances=1）、提前唤醒（Wake）、
// 全局暂停/恢复（/pause、/resume）及热重载动态替换。
package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CronSpec 表示解析后的标准 5 段 Cron 表达式（分 时 日 月 周）。
type CronSpec struct {
	minutes [60]bool
	hours   [24]bool
	dom     [32]bool // 1..31
	months  [13]bool // 1..12
	dow     [7]bool  // 0=Sun .. 6=Sat
	domStar bool
	dowStar bool
}

var dowNames = map[string]int{
	"sun": 0, "sunday": 0,
	"mon": 1, "monday": 1,
	"tue": 2, "tuesday": 2,
	"wed": 3, "wednesday": 3,
	"thu": 4, "thursday": 4,
	"fri": 5, "friday": 5,
	"sat": 6, "saturday": 6,
}

// ParseCron 解析标准 5 字段 Cron 表达式（minute hour dom month dow）。
func ParseCron(expr string) (*CronSpec, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron 表达式须包含 5 个字段: %q", expr)
	}
	spec := &CronSpec{
		domStar: fields[2] == "*",
		dowStar: fields[4] == "*",
	}
	if err := parseCronField(fields[0], 0, 59, nil, spec.minutes[:]); err != nil {
		return nil, fmt.Errorf("cron minute 字段错误: %w", err)
	}
	if err := parseCronField(fields[1], 0, 23, nil, spec.hours[:]); err != nil {
		return nil, fmt.Errorf("cron hour 字段错误: %w", err)
	}
	if err := parseCronField(fields[2], 1, 31, nil, spec.dom[:]); err != nil {
		return nil, fmt.Errorf("cron dom 字段错误: %w", err)
	}
	if err := parseCronField(fields[3], 1, 12, nil, spec.months[:]); err != nil {
		return nil, fmt.Errorf("cron month 字段错误: %w", err)
	}
	var rawDow [8]bool
	if err := parseCronField(fields[4], 0, 7, dowNames, rawDow[:]); err != nil {
		return nil, fmt.Errorf("cron dow 字段错误: %w", err)
	}
	for i := 0; i < 7; i++ {
		spec.dow[i] = rawDow[i]
	}
	if rawDow[7] {
		spec.dow[0] = true
	}
	return spec, nil
}

// DailyCron 构造每日固定时刻的 CronSpec。
func DailyCron(hour, minute int) (*CronSpec, error) {
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return nil, fmt.Errorf("时间越界: %02d:%02d", hour, minute)
	}
	return ParseCron(fmt.Sprintf("%d %d * * *", minute, hour))
}

// WeeklyCron 构造每周指定星期几（如 ["mon","fri"]）固定时刻的 CronSpec。
func WeeklyCron(days []string, hour, minute int) (*CronSpec, error) {
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return nil, fmt.Errorf("时间越界: %02d:%02d", hour, minute)
	}
	if len(days) == 0 {
		return nil, fmt.Errorf("weekly 计划至少需要一个星期几")
	}
	cleaned := make([]string, 0, len(days))
	for _, d := range days {
		norm := strings.ToLower(strings.TrimSpace(d))
		if _, ok := dowNames[norm]; !ok {
			return nil, fmt.Errorf("无效星期缩写: %q", d)
		}
		cleaned = append(cleaned, norm)
	}
	return ParseCron(fmt.Sprintf("%d %d * * %s", minute, hour, strings.Join(cleaned, ",")))
}

// Matches 判断给定时间（使用其自身 Location）是否命中 Cron 表达式。
func (c *CronSpec) Matches(t time.Time) bool {
	if !c.minutes[t.Minute()] || !c.hours[t.Hour()] || !c.months[int(t.Month())] {
		return false
	}
	domMatch := c.dom[t.Day()]
	dowMatch := c.dow[int(t.Weekday())]
	// 标准 Vixie Cron 语义：dom 与 dow 均非 * 时取并集，否则取交集。
	if !c.domStar && !c.dowStar {
		return domMatch || dowMatch
	}
	return domMatch && dowMatch
}

// Next 返回严格晚于 from 的下一次触发时间（对齐到整分 0 秒）；4 年内无匹配返回零值。
func (c *CronSpec) Next(from time.Time) time.Time {
	t := from.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(4, 0, 0)
	for t.Before(limit) {
		if !c.months[int(t.Month())] {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		domMatch := c.dom[t.Day()]
		dowMatch := c.dow[int(t.Weekday())]
		dayOK := (domMatch && dowMatch)
		if !c.domStar && !c.dowStar {
			dayOK = domMatch || dowMatch
		}
		if !dayOK {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !c.hours[t.Hour()] {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			continue
		}
		if !c.minutes[t.Minute()] {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

func parseCronField(expr string, minVal, maxVal int, names map[string]int, out []bool) error {
	for part := range strings.SplitSeq(expr, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return fmt.Errorf("空子项")
		}
		step := 1
		rangePart := part
		if slash := strings.IndexByte(part, '/'); slash >= 0 {
			s, err := strconv.Atoi(part[slash+1:])
			if err != nil || s <= 0 {
				return fmt.Errorf("无效步长 %q", part)
			}
			step = s
			rangePart = part[:slash]
		}
		var start, end int
		switch {
		case rangePart == "*":
			start, end = minVal, maxVal
		case strings.Contains(rangePart, "-"):
			bounds := strings.SplitN(rangePart, "-", 2)
			a, err := parseCronAtom(bounds[0], minVal, maxVal, names)
			if err != nil {
				return err
			}
			b, err := parseCronAtom(bounds[1], minVal, maxVal, names)
			if err != nil || b < a {
				return fmt.Errorf("无效范围 %q", part)
			}
			start, end = a, b
		default:
			v, err := parseCronAtom(rangePart, minVal, maxVal, names)
			if err != nil {
				return err
			}
			if step == 1 {
				out[v] = true
				continue
			}
			start, end = v, maxVal
		}
		for v := start; v <= end; v += step {
			out[v] = true
		}
	}
	return nil
}

func parseCronAtom(tok string, minVal, maxVal int, names map[string]int) (int, error) {
	lower := strings.ToLower(strings.TrimSpace(tok))
	if names != nil {
		if v, ok := names[lower]; ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(lower)
	if err != nil || v < minVal || v > maxVal {
		return 0, fmt.Errorf("值 %q 超出范围 [%d,%d]", tok, minVal, maxVal)
	}
	return v, nil
}
