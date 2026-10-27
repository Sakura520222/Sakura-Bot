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
	"os"
	"strconv"
	"strings"
)

// QA 进程消费的全部环境键（清单来源：行为规格 §4，逐项对齐 core/settings.py
// 与 qa_bot.py 的取值点）。QA_BOT_RUNTIME/QA_BOT_BINARY 为主进程侧开关键，
// 一并声明避免常量散落。
const (
	// 令牌与开关。
	KeyQABotToken   = "QA_BOT_TOKEN"
	KeyQABotEnabled = "QA_BOT_ENABLED"

	// 组件日志（常量本体在 internal/logging 声明，这里列出供查阅：
	// SAKURA_LOG_COMPONENT / SAKURA_COMPONENT_LOG_FILE / LOG_LEVEL /
	// LOG_TO_FILE / LOG_FILE_PATH / LOG_FILE_MAX_SIZE /
	// LOG_FILE_BACKUP_COUNT / LOG_TO_CONSOLE / LOG_COLORIZE）。

	// MySQL。
	KeyMySQLHost        = "MYSQL_HOST"
	KeyMySQLPort        = "MYSQL_PORT"
	KeyMySQLUser        = "MYSQL_USER"
	KeyMySQLPassword    = "MYSQL_PASSWORD"
	KeyMySQLDatabase    = "MYSQL_DATABASE"
	KeyMySQLCharset     = "MYSQL_CHARSET"
	KeyMySQLPoolSize    = "MYSQL_POOL_SIZE"
	KeyMySQLMaxOverflow = "MYSQL_MAX_OVERFLOW"
	KeyMySQLPoolTimeout = "MYSQL_POOL_TIMEOUT"

	// LLM（LLM_API_KEY 缺失时回退 DEEPSEEK_API_KEY）。
	KeyLLMAPIKey      = "LLM_API_KEY"
	KeyDeepseekAPIKey = "DEEPSEEK_API_KEY"
	KeyLLMBaseURL     = "LLM_BASE_URL"
	KeyLLMModel       = "LLM_MODEL"

	// 向量与嵌入。
	KeyVectorDBPath       = "VECTOR_DB_PATH"
	KeyEmbeddingAPIKey    = "EMBEDDING_API_KEY"
	KeyEmbeddingAPIBase   = "EMBEDDING_API_BASE"
	KeyEmbeddingModel     = "EMBEDDING_MODEL"
	KeyEmbeddingDimension = "EMBEDDING_DIMENSION"

	// 重排。
	KeyRerankerAPIKey  = "RERANKER_API_KEY"
	KeyRerankerAPIBase = "RERANKER_API_BASE"
	KeyRerankerModel   = "RERANKER_MODEL"
	KeyRerankerTopK    = "RERANKER_TOP_K"
	KeyRerankerFinal   = "RERANKER_FINAL"

	// 配额与管理员。
	KeyQABotUserLimit  = "QA_BOT_USER_LIMIT"
	KeyQABotDailyLimit = "QA_BOT_DAILY_LIMIT"
	KeyReportAdminIDs  = "REPORT_ADMIN_IDS"

	// 频道 / 语言 / 人格。
	KeyTargetChannel = "TARGET_CHANNEL"
	KeyLanguage      = "LANGUAGE"
	KeyQABotPersona  = "QA_BOT_PERSONA"

	// 主进程侧运行时开关（QA 进程自身不消费）。
	KeyQABotRuntime = "QA_BOT_RUNTIME"
	KeyQABotBinary  = "QA_BOT_BINARY"

	// Go 运行时内存上限（README 硬规则 5：默认 96MiB，env 可调）。
	KeyGoMemLimit = "GOMEMLIMIT"
)

// Env 是不可变的进程环境 + data/.env 覆盖快照。
// .env 值覆盖进程环境（对齐 settings.py:34 的 override=True）。
type Env struct {
	values map[string]string
}

// NewEnv 构建环境快照：先拷贝进程环境，再解析 dataEnvPath（存在时）覆盖同名键。
func NewEnv(dataEnvPath string) *Env {
	values := make(map[string]string, 64)
	for _, kv := range os.Environ() {
		if k, v, ok := splitEnv(kv); ok {
			values[k] = v
		}
	}
	if dataEnvPath != "" {
		if content, err := os.ReadFile(dataEnvPath); err == nil {
			for k, v := range ParseDotEnv(string(content)) {
				values[k] = v
			}
		}
		// 文件缺失/不可读：对齐 python-dotenv 宽松行为，直接退回进程环境。
	}
	return &Env{values: values}
}

// newEnvFromMap 以给定键值构建快照（测试与装配用）。
func newEnvFromMap(values map[string]string) *Env {
	copied := make(map[string]string, len(values))
	for k, v := range values {
		copied[k] = v
	}
	return &Env{values: copied}
}

// splitEnv 按“第一个 =”切分 KEY=VALUE。
func splitEnv(kv string) (string, string, bool) {
	idx := strings.IndexByte(kv, '=')
	if idx <= 0 {
		return "", "", false
	}
	return kv[:idx], kv[idx+1:], true
}

// ParseDotEnv 手写解析器（设计 §3.1，对齐 python-dotenv 宽松行为）：
//   - 仅整行注释：# 开头（允许前导空白）跳过；
//   - 可选 export 前缀；
//   - KEY=VALUE 按“第一个 =”切分，键值两侧空白剔除；
//   - 成对单/双引号剥离；
//   - 无 = 或空键的坏行跳过。
func ParseDotEnv(content string) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue // 坏行跳过
		}
		key := strings.TrimSpace(line[:idx])
		if key == "" {
			continue
		}
		value := strings.TrimSpace(line[idx+1:])
		value = stripPairedQuotes(value)
		out[key] = value
	}
	return out
}

// stripPairedQuotes 剥离成对的单/双引号（各一层）。
func stripPairedQuotes(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// Lookup 返回键值与是否存在；快照内“存在但为空”会以 ("", true) 返回，
// 与 os.LookupEnv 语义一致（qa_bot.py:94 依赖该区分）。
func (e *Env) Lookup(key string) (string, bool) {
	v, ok := e.values[key]
	return v, ok
}

// Get 等价 os.getenv(key)：不存在返回空串。
func (e *Env) Get(key string) string {
	return e.values[key]
}

// GetDefault 等价 os.getenv(key, def)：仅当键不存在时用默认值。
func (e *Env) GetDefault(key, def string) string {
	if v, ok := e.values[key]; ok {
		return v
	}
	return def
}

// GetIntDef 解析整数；缺失或非法时返回默认值
// （对齐 qa_quota.py:26-28 的 int(os.getenv(...)) + ValueError → default）。
func (e *Env) GetIntDef(key string, def int64) int64 {
	raw, ok := e.values[key]
	if !ok {
		return def
	}
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return def
	}
	return n
}
