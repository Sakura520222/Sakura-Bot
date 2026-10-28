// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

// store.go：GuardStore 面上 QA 闭包消费的方法与申诉侧纯函数。
// 移植自 core/moderation/repository.py（Case/Blacklist/SetWhitelist）与
// core/moderation/appeal_repository.py（ActiveKey/TERMINAL）。

package guard

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	db "github.com/Sakura520222/Sakura-Bot/qa-bot-go/internal/db"
)

// Case 按 ID 取 moderation_cases 行（repository.py:59-60）。
func (s *Store) Case(ctx context.Context, caseID string) (Row, error) {
	return s.One(ctx, "SELECT * FROM moderation_cases WHERE id=?", caseID)
}

// Blacklist 查全局黑名单行（repository.py:123-124）。nil = 不在名单。
func (s *Store) Blacklist(ctx context.Context, userID int64) (Row, error) {
	return s.One(ctx, "SELECT * FROM moderation_blacklist WHERE user_id=?", userID)
}

// SetWhitelist 写白名单（repository.py:140-153 的 enabled=true 分支）。
// QA 侧仅 finish_approval 使用该路径：INSERT ... ON DUPLICATE KEY UPDATE
// reviewer_id/reviewer_actor；reviewerActor 传空串回退 "telegram:<id>"
// （对齐 Python reviewer_actor or f"telegram:{reviewer_id}"）。
func (s *Store) SetWhitelist(ctx context.Context, chatID, userID, reviewerID int64, reviewerActor string) error {
	actor := reviewerActor
	if actor == "" {
		actor = fmt.Sprintf("telegram:%d", reviewerID)
	}
	_, err := s.Exec(ctx,
		"INSERT INTO moderation_whitelist (chat_id,user_id,reviewer_id,reviewer_actor,created_at) VALUES (?,?,?,?,?) ON DUPLICATE KEY UPDATE reviewer_id=VALUES(reviewer_id),reviewer_actor=VALUES(reviewer_actor)",
		chatID, userID, reviewerID, actor, db.MysqlDatetime6(db.NowUTC()))
	return err
}

// ActiveKey 申诉激活键（appeal_repository.py:20-21）：
// sha256("{chat_id}:{user_id}") 十六进制。
func ActiveKey(chatID, userID int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d", chatID, userID)))
	return hex.EncodeToString(sum[:])
}

// IsTerminal 报告 status 是否终态（appeal_repository.py:15 的 TERMINAL 集合）。
func IsTerminal(status string) bool {
	switch status {
	case "approved", "rejected", "withdrawn", "superseded":
		return true
	}
	return false
}

// uuid4Hex 生成 32 位十六进制 UUID4（对齐 Python uuid.uuid4().hex；
// 用 stdlib crypto/rand 实现，避免把 go.mod 中间接依赖 google/uuid 转为直接依赖）。
func uuid4Hex() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 失败属系统级灾难：对齐 uuid4().hex 的不可能失败路径。
		panic(fmt.Sprintf("crypto/rand 读取失败: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // 版本 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 变体
	return hex.EncodeToString(b[:])
}
