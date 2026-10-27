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
	"strconv"
	"strings"

	"github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/contracts"
)

// env 侧配额默认值与 clamp 下限（qa_quota.py:21-24）。
const (
	defaultUserDailyLimit  = int64(3)
	defaultTotalDailyLimit = int64(200)
)

// EffectiveQuota 返回配额限额，语义逐分支对齐 core/config/qa_quota.py:19-30：
//
//  1. env：QA_BOT_USER_LIMIT（默认 3，clamp [1, 2^31-1]）、
//     QA_BOT_DAILY_LIMIT（默认 200，clamp [10, 2^31-1]）；
//     非法整数回退默认值（Python ValueError → default）；
//  2. config.json 的 qa_bot_quota 覆盖 env；此处是 pydantic
//     strict + extra="forbid" 的 model_validate：
//     非整数 / 越界 / 未知字段 / 非对象 → 返回 error，
//     调用方（quota manager 等价物）必须整体拒绝本次配额检查
//     （评审 #10：config.json 越界是“拒绝”而非 clamp 放宽）。
func (c *Config) EffectiveQuota() (contracts.QuotaLimits, error) {
	limits := contracts.QuotaLimits{
		UserDaily:  envQuotaInt(c.Env, KeyQABotUserLimit, defaultUserDailyLimit, contracts.QuotaUserMin),
		TotalDaily: envQuotaInt(c.Env, KeyQABotDailyLimit, defaultTotalDailyLimit, contracts.QuotaTotalMin),
	}

	raw, ok := c.readConfig()["qa_bot_quota"]
	if !ok {
		return limits, nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return limits, fmt.Errorf("qa_bot_quota 配置必须是对象")
	}
	// extra="forbid"：出现未知字段即校验失败。
	for k := range obj {
		if k != "user_daily_limit" && k != "total_daily_limit" {
			return limits, fmt.Errorf("qa_bot_quota 含未知字段: %s", k)
		}
	}
	if v, ok := obj["user_daily_limit"]; ok {
		n, err := strictJSONInt(v)
		if err != nil {
			return limits, fmt.Errorf("user_daily_limit 校验失败: %w", err)
		}
		if n < contracts.QuotaUserMin || n > contracts.QuotaMax {
			return limits, fmt.Errorf("user_daily_limit 越界: %d", n)
		}
		limits.UserDaily = n
	}
	if v, ok := obj["total_daily_limit"]; ok {
		n, err := strictJSONInt(v)
		if err != nil {
			return limits, fmt.Errorf("total_daily_limit 校验失败: %w", err)
		}
		if n < contracts.QuotaTotalMin || n > contracts.QuotaMax {
			return limits, fmt.Errorf("total_daily_limit 越界: %d", n)
		}
		limits.TotalDaily = n
	}
	return limits, nil
}

// envQuotaInt 复刻 min(2147483647, max(minimum, int(os.getenv(env, str(default)))))
// 及 ValueError → default。
func envQuotaInt(env *Env, key string, def, minimum int64) int64 {
	v := env.GetIntDef(key, def)
	if v < minimum {
		v = minimum
	}
	if v > contracts.QuotaMax {
		v = contracts.QuotaMax
	}
	return v
}

// strictJSONInt 要求 JSON 整数（pydantic strict=True 拒绝 float 与 bool）。
// json.Number 形如 "3" 视为整数；"3.0"、"true" 等一律失败。
func strictJSONInt(v any) (int64, error) {
	num, ok := v.(json.Number)
	if !ok {
		return 0, fmt.Errorf("必须是整数，实际 %T", v)
	}
	s := num.String()
	if !isJSONInteger(s) {
		return 0, fmt.Errorf("必须是整数，实际 %s", s)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("整数越界或非法: %s", s)
	}
	return n, nil
}

// isJSONInteger 判断 json.Number 文本是否为纯整数字面量。
func isJSONInteger(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '-' {
		s = s[1:]
	}
	if s == "" {
		return false
	}
	return strings.IndexFunc(s, func(r rune) bool {
		return r < '0' || r > '9'
	}) < 0
}
