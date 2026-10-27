// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package tg

import "log"

// Logger tg 包内部消费的窄日志接口（Go 惯例：接口定义在消费者包内）。
// 生产实现由 main 装配 internal/logging 提供；方法语义对齐 Python logging
// 的 debug/info/warning/error（qa_bot.py 内的 logger.* 调用点逐行对应）。
type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warningf(format string, args ...any)
	Errorf(format string, args ...any)
}

// NopLogger 空日志实现（测试与静默部署用）。
type NopLogger struct{}

// Debugf 空实现。
func (NopLogger) Debugf(string, ...any) {}

// Infof 空实现。
func (NopLogger) Infof(string, ...any) {}

// Warningf 空实现。
func (NopLogger) Warningf(string, ...any) {}

// Errorf 空实现。
func (NopLogger) Errorf(string, ...any) {}

// StdLogger 把标准库 *log.Logger 适配为 Logger（单测/极简部署用）。
type StdLogger struct {
	L *log.Logger
}

// Debugf 输出调试日志。
func (s StdLogger) Debugf(format string, args ...any) { s.L.Printf(format, args...) }

// Infof 输出信息日志。
func (s StdLogger) Infof(format string, args ...any) { s.L.Printf(format, args...) }

// Warningf 输出警告日志。
func (s StdLogger) Warningf(format string, args ...any) { s.L.Printf(format, args...) }

// Errorf 输出错误日志。
func (s StdLogger) Errorf(format string, args ...any) { s.L.Printf(format, args...) }
