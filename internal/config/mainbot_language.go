// Copyright 2026 Sakura-Bot
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Sakura520222/Sakura-Bot/internal/filelock"
	"github.com/Sakura520222/Sakura-Bot/internal/i18n"
)

func (c *Config) MainBotLanguage(userID int64) (string, error) {
	languages, err := readMainBotLanguages(filepath.Join(c.DataDirectory(), "mainbot-languages.json"))
	if err != nil {
		return "", err
	}
	return languages[strconv.FormatInt(userID, 10)], nil
}

func (c *Config) SaveMainBotLanguage(ctx context.Context, userID int64, lang string) error {
	if userID <= 0 || (lang != i18n.LangZhCN && lang != i18n.LangEnUS) {
		return errors.New("invalid main bot language preference")
	}
	dir := c.DataDirectory()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "mainbot-languages.json")
	lock, err := filelock.Acquire(ctx, path+".lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	languages, err := readMainBotLanguages(path)
	if err != nil {
		return err
	}
	languages[strconv.FormatInt(userID, 10)] = lang
	data, err := json.Marshal(languages)
	if err != nil {
		return err
	}
	return atomicWriteFile(path, data, 0o600)
}

func readMainBotLanguages(path string) (map[string]string, error) {
	languages := make(map[string]string)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return languages, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &languages); err != nil {
		return nil, err
	}
	if languages == nil {
		return nil, errors.New("invalid main bot language preference file")
	}
	return languages, nil
}
