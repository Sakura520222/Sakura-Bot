// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package wiki

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/scheduler"
)

var (
	cronMonths   = strings.Fields("jan feb mar apr may jun jul aug sep oct nov dec")
	cronWeekdays = strings.Fields("mon tue wed thu fri sat sun")
)

type wikiCron struct {
	times   *scheduler.CronSpec
	days    *scheduler.CronSpec
	lastDay bool
	dow     *scheduler.CronSpec
}

// Wiki schedules retain APScheduler's Monday-based weekday numbers and require
// both the day-of-month and weekday to match. Other Go jobs use standard Cron.
func parseWikiCron(expr string) (*wikiCron, error) {
	fields := strings.Fields(strings.ToLower(expr))
	if len(fields) != 5 {
		return nil, fmt.Errorf("expected five Cron fields")
	}
	minutes, err := normalizeCronField(fields[0], 0, 59, nil)
	if err != nil {
		return nil, err
	}
	hours, err := normalizeCronField(fields[1], 0, 23, nil)
	if err != nil {
		return nil, err
	}
	months, err := normalizeCronField(fields[3], 1, 12, cronMonths)
	if err != nil {
		return nil, err
	}
	weekdays, err := normalizeCronField(fields[4], 0, 6, cronWeekdays)
	if err != nil {
		return nil, err
	}
	timing := minutes + " " + hours + " * " + months + " *"
	times, err := scheduler.ParseCron(timing)
	if err != nil {
		return nil, err
	}
	// Use the shared Cron numeric value matcher with Monday at index zero.
	dow, err := scheduler.ParseCron(weekdays + " * * * *")
	if err != nil {
		return nil, err
	}
	result := &wikiCron{times: times, dow: dow}
	var dayParts []string
	for part := range strings.SplitSeq(fields[2], ",") {
		if part == "last" {
			result.lastDay = true
			continue
		}
		dayParts = append(dayParts, part)
	}
	if len(dayParts) != 0 {
		dayRange, err := normalizeCronField(strings.Join(dayParts, ","), 1, 31, nil)
		if err != nil {
			return nil, err
		}
		days, err := scheduler.ParseCron("0 0 " + dayRange + " * *")
		if err != nil {
			return nil, err
		}
		result.days = days
	}
	return result, nil
}

func normalizeCronField(raw string, minValue, maxValue int, names []string) (string, error) {
	value := func(raw string) (int, error) {
		for index, name := range names {
			if raw == name {
				return index + minValue, nil
			}
		}
		number, err := strconv.Atoi(raw)
		if err != nil || number < minValue || number > maxValue {
			return 0, fmt.Errorf("invalid Cron value %q", raw)
		}
		return number, nil
	}
	var parts []string
	for part := range strings.SplitSeq(raw, ",") {
		span, interval, hasStep := strings.Cut(part, "/")
		if span == "*" && !hasStep {
			parts = append(parts, fmt.Sprintf("%d-%d", minValue, maxValue))
			continue
		}
		start, end := minValue, maxValue
		if span != "*" {
			first, last, hasEnd := strings.Cut(span, "-")
			var err error
			start, err = value(first)
			if err != nil {
				return "", err
			}
			if hasEnd {
				end, err = value(last)
				if err != nil {
					return "", err
				}
			} else if !hasStep {
				end = start
			}
		}
		if start > end {
			return "", fmt.Errorf("invalid Cron range %q", part)
		}
		if !hasStep {
			parts = append(parts, fmt.Sprintf("%d-%d", start, end))
			continue
		}
		step, err := strconv.Atoi(interval)
		if err != nil || step < 1 || step > end-start {
			return "", fmt.Errorf("invalid Cron step %q", part)
		}
		parts = append(parts, fmt.Sprintf("%d-%d/%d", start, end, step))
	}
	return strings.Join(parts, ","), nil
}

func (c *wikiCron) matchesDay(t time.Time) bool {
	weekday := (int(t.Weekday()) + 6) % 7
	if !c.dow.Matches(time.Date(2000, 1, 1, 0, weekday, 0, 0, time.UTC)) {
		return false
	}
	return (c.lastDay && t.AddDate(0, 0, 1).Month() != t.Month()) ||
		(c.days != nil && c.days.Matches(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)))
}

func scheduleDue(cfg WikiConfig, previous *time.Time, now time.Time) bool {
	var expr string
	switch cfg.Frequency {
	case "daily":
		expr = "0 3 * * *"
	case "weekly":
		expr = "0 3 * * 0"
	case "cron":
		expr = cfg.Cron
	default:
		return false
	}
	cron, err := parseWikiCron(expr)
	if err != nil {
		return false
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return false
	}
	start := now.Add(-7 * 24 * time.Hour)
	if previous != nil {
		start = *previous
	}
	t := start.Truncate(time.Minute).Add(time.Minute).In(loc)
	for !t.After(now) {
		if !cron.matchesDay(t) {
			next := time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
			if next.After(t) && next.Day() != t.Day() {
				t = next
				continue
			}
		} else if cron.times.Matches(t) {
			return true
		}
		// Adding absolute minutes preserves repeated local times around DST rollback.
		t = t.Add(time.Minute)
	}
	return false
}
