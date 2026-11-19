// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

func (c *Config) LookupEnv(key string) (string, bool) { return c.Env.Lookup(key) }

func (c *Config) ConfiguredQARuntime() string { return c.QABotRuntime() }

func (c *Config) SaveQARuntime(runtime string) error {
	return c.UpdateConfig(func(doc map[string]any) error {
		doc["qa_bot_runtime"] = runtime
		return nil
	})
}
