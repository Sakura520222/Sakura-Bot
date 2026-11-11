package config

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type PoolConfig struct {
	Workers        int `json:"workers"`
	QueueCapacity  int `json:"queue_capacity"`
	TimeoutSeconds int `json:"timeout_seconds"`
}

// AIPoolConfig is read at startup. Absent configuration preserves safe defaults;
// explicitly invalid values fail closed instead of silently widening limits.
func (c *Config) AIPoolConfig() (PoolConfig, error) {
	result := PoolConfig{Workers: 4, QueueCapacity: 64, TimeoutSeconds: 180}
	if raw, ok := c.readConfig()["qa_ai_pool"]; ok {
		if _, ok := raw.(map[string]any); !ok {
			return result, fmt.Errorf("qa_ai_pool must be an object")
		}
		for key, value := range raw.(map[string]any) {
			if value == nil {
				return result, fmt.Errorf("qa_ai_pool.%s cannot be null", key)
			}
		}
		b, err := json.Marshal(raw)
		if err != nil {
			return result, err
		}
		decoder := json.NewDecoder(bytes.NewReader(b))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&result); err != nil {
			return result, err
		}
	}
	if result.Workers < 1 || result.Workers > 32 || result.QueueCapacity < 1 || result.QueueCapacity > 1024 || result.TimeoutSeconds < 1 || result.TimeoutSeconds > 1800 {
		return result, fmt.Errorf("invalid qa_ai_pool: workers 1..32, queue_capacity 1..1024, timeout_seconds 1..1800")
	}
	return result, nil
}
