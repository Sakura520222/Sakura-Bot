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

// DefaultLanguage 是最终回退语言（config.py:195 的 os.getenv("LANGUAGE", "zh-CN")）。
const DefaultLanguage = "zh-CN"

// Language 返回语言解析结果，优先级对齐 core/config.py:424-432：
// config.json 的 language → env LANGUAGE → zh-CN。
// 注意 Python 侧 set_language 仅接受 zh-CN/en-US（其余保持 zh-CN），
// 校验由 internal/i18n 的 SetLanguage 承担，由 main 装配调用。
func (c *Config) Language() string {
	if s := stringField(c.readConfig(), "language"); s != "" {
		return s
	}
	if v, ok := c.Env.Lookup(KeyLanguage); ok && v != "" {
		return v
	}
	return DefaultLanguage
}
