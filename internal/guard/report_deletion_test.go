// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package guard

import (
	"context"
	"testing"

	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
)

type reportDeletionOps struct {
	*fakeTelegramOps
	message             *mtproto.IncomingMessage
	deleted             bool
	refusesConfirmation bool
}

func (o *reportDeletionOps) GetMessages(context.Context, int64, []int) ([]*mtproto.IncomingMessage, error) {
	return []*mtproto.IncomingMessage{o.message}, nil
}

func (o *reportDeletionOps) DeleteMessages(context.Context, int64, []int) error {
	o.deleted = true
	if !o.refusesConfirmation {
		o.message = nil
	}
	return nil
}

func TestConfirmReportMessageDeletionChecksLiveBindingAndOutcome(t *testing.T) {
	const chat = int64(-1000000002000)
	ops := &reportDeletionOps{fakeTelegramOps: newFakeOps(75)}
	ops.permissions[chat] = &mtproto.ParticipantPermissions{IsAdmin: true}
	reviews := NewService(nil, nil, ops, nil, nil).Reviews()
	deletion := map[string]any{"chat_id": chat, "user_id": int64(74), "message_id": 101}
	ops.message = &mtproto.IncomingMessage{ID: 101, ChatID: chat, SenderID: 72}
	if err := reviews.confirmMessageDeletion(t.Context(), chat, 74, 101, deletion); err != GuardError("guard.report_delete_target_changed") || ops.deleted {
		t.Fatalf("mismatched sender: error=%v delete=%t", err, ops.deleted)
	}
	ops.message.SenderID = 74
	ops.refusesConfirmation = true
	if err := reviews.confirmMessageDeletion(t.Context(), chat, 74, 101, deletion); err != GuardError("guard.report_delete_unconfirmed") || !ops.deleted {
		t.Fatalf("unconfirmed removal: error=%v delete=%t", err, ops.deleted)
	}
	ops.refusesConfirmation = false
	if err := reviews.confirmMessageDeletion(t.Context(), chat, 74, 101, deletion); err != nil || ops.message != nil {
		t.Fatalf("expected confirmed removal: error=%v message=%+v", err, ops.message)
	}
	ops.deleted = false
	if err := reviews.confirmMessageDeletion(t.Context(), chat, 74, 101, deletion); err != nil || ops.deleted {
		t.Fatalf("already removed was not idempotent: error=%v delete=%t", err, ops.deleted)
	}
}
