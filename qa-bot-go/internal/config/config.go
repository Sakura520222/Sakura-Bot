// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// Package config 复刻 Python 侧 QA 进程消费的配置读取链：
//
//   - env：进程环境快照 + data/.env 覆盖（对齐 core/settings.py:33-34 的
//     override=True 语义，.env 值优先于进程环境）；
//   - data/config.json：按 (ino, mtime_ns, size) 三元组 stat 热读
//     （对齐 core/config/runtime.py:35-39；Windows 无 Ino 时退化为
//     (mtime_ns, size)，评审 #6/设计 §3.2）；
//   - 派生访问器：EffectiveAIConfig / EffectiveQuota / Channels /
//     Persona / IsAdmin / Language，每次调用都重新 Read，
//     对齐“quota/AI 每次调用刷新”的现状。
package config

import (
	"strings"
	"sync"
)

// 运行期数据文件相对路径（运行目录即主进程 spawn 的 cwd，见 process_manager.py:70）。
const (
	// DataEnvPath 是 data/.env。
	DataEnvPath = "data/.env"
	// DataConfigPath 是 data/config.json。
	DataConfigPath = "data/config.json"
	// DataDir 是默认数据目录。
	DataDir = "data"
)

// Config 聚合 env 快照与 config.json 热读器。
type Config struct {
	// Env 是不可变的进程环境 + data/.env 快照。
	Env *Env

	runtime *RuntimeJSON
	dataDir string
}

// New 以显式路径构建配置（缺 .env 文件时按“仅进程环境”处理，
// 对齐 python-dotenv 文件缺失时不报错的行为）。
func New(envPath, jsonPath string) *Config {
	return &Config{
		Env:     NewEnv(envPath),
		runtime: NewRuntimeJSON(jsonPath),
		dataDir: DataDir,
	}
}

// Default 返回进程级单例（对齐 Python 模块级单例的装配习惯）。
func Default() *Config {
	defaultOnce.Do(func() {
		defaultConfig = New(DataEnvPath, DataConfigPath)
	})
	return defaultConfig
}

var (
	defaultOnce   sync.Once
	defaultConfig *Config
)

// Init 完成首次 config.json 读取；失败返回 error。
// Python 侧此失败发生在 import 阶段（进程即崩，健康 job 拉起重试），
// Go 侧由 main 在启动期调用并以非零码退出，语义等价。
func (c *Config) Init() error {
	return c.runtime.Init()
}

// readConfig 每次调用都重新 stat/热读 config.json（对齐 runtime_config.read()）。
func (c *Config) readConfig() map[string]any {
	return c.runtime.Read()
}

// QABotToken 返回 QA_BOT_TOKEN（可能为空）。
func (c *Config) QABotToken() string {
	return c.Env.Get(KeyQABotToken)
}

// QABotEnabled 复刻 qa_bot.py:94：os.getenv("QA_BOT_ENABLED", "True").lower() == "true"。
// 注意“已设置但为空”按禁用处理（与 Python 一致，不回退默认值）。
func (c *Config) QABotEnabled() bool {
	v, ok := c.Env.Lookup(KeyQABotEnabled)
	if !ok {
		v = "True"
	}
	return strings.ToLower(v) == "true"
}
