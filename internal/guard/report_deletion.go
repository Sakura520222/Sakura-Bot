// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package guard

import (
	"context"
	"math"
	"slices"
	"time"

	"github.com/Sakura520222/Sakura-Bot/internal/db"
	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
)

func (rh *ReviewHandler) requestMessageDeletion(ctx context.Context, st *guarddb.Store, row guarddb.Row, actor string, automatic bool) error {
	p := rowPayload(row)
	id, chat, user := rowInt64(p, "message_id"), rowInt64(row, "chat_id"), rowInt64(row, "user_id")
	if id <= 0 || id > math.MaxInt32 || (rowStr(row, "action") != "report" && !(automatic && rowStr(row, "action") == "ban")) {
		return GuardError("guard.invalid_callback")
	}
	d, _ := p["report_message_deletion"].(map[string]any)
	if d == nil {
		d = map[string]any{
			"status": "pending", "chat_id": chat, "user_id": user, "message_id": id, "automatic": automatic,
			"actor": actor, "requested_at": db.PythonISOFormat(db.NowUTC()), "attempts": 0,
		}
		if err := saveMessageDeletion(ctx, st, row, p, d); err != nil {
			return err
		}
	}
	return rh.resumeMessageDeletion(ctx, st, row, true)
}

func saveMessageDeletion(ctx context.Context, st *guarddb.Store, row guarddb.Row, payload, deletion map[string]any) error {
	payload["report_message_deletion"] = deletion
	payload["report_notice_dirty"] = true
	var previousError *string
	if saved := rowStr(row, "error"); saved != "" {
		previousError = &saved
	}
	return st.UpdateCase(ctx, rowStr(row, "id"), rowStr(row, "status"), previousError, payload)
}

func (rh *ReviewHandler) resumeMessageDeletion(ctx context.Context, st *guarddb.Store, row guarddb.Row, force bool) error {
	payload := rowPayload(row)
	d, _ := payload["report_message_deletion"].(map[string]any)
	auto := rowBool(d, "automatic")
	if rowStr(d, "status") != "pending" || (rowStr(row, "action") != "report" && !auto) {
		return nil
	}
	now := db.NowUTC()
	if !force && parseRowTimeOrNow(d, "retry_at", now).After(now) {
		return nil
	}
	chat, user, id := rowInt64(row, "chat_id"), rowInt64(row, "user_id"), rowInt64(payload, "message_id")
	if auto {
		grp := rh.svc.Group(chat)
		whitelisted, err := st.Whitelisted(ctx, chat, user)
		if err != nil {
			return err
		}
		if whitelisted && grp.ShareGlobalBlacklist {
			listed, err := st.Blacklist(ctx, user)
			if err != nil {
				return err
			}
			whitelisted = listed == nil
		}
		if !grp.Enabled || whitelisted || rh.svc.checkProtection(ctx, chat, user, grp, true) != protectionUnprotected ||
			!slices.Contains([]string{"executing", "pending", "blacklisted", "execution_failed"}, rowStr(row, "status")) {
			d["status"], d["error"] = "cancelled", "guard.permissions_changed"
			return saveMessageDeletion(ctx, st, row, payload, d)
		}
	}
	err := rh.confirmMessageDeletion(ctx, chat, user, id, d)
	if err == nil {
		d["status"] = "deleted"
		d["completed_at"] = db.PythonISOFormat(now)
		d["error"], d["retry_at"] = nil, nil
		return saveMessageDeletion(ctx, st, row, payload, d)
	}
	attempts := rowInt64(d, "attempts") + 1
	delay := time.Duration(min(900, 30<<min(max(attempts-1, 0), 5))) * time.Second
	d["attempts"], d["error"] = attempts, err.Error()
	d["retry_at"] = db.PythonISOFormat(now.Add(delay))
	if saveErr := saveMessageDeletion(ctx, st, row, payload, d); saveErr != nil {
		return saveErr
	}
	return err
}

func (rh *ReviewHandler) confirmMessageDeletion(ctx context.Context, chat, user, id int64, d map[string]any) error {
	if id <= 0 || id > math.MaxInt32 || rowInt64(d, "chat_id") != chat ||
		rowInt64(d, "user_id") != user || rowInt64(d, "message_id") != id {
		return GuardError("guard.report_delete_target_changed")
	}
	if rh.svc.ops == nil {
		return GuardError("guard.unavailable")
	}
	perm, err := rh.svc.ops.Permissions(ctx, chat, rh.svc.BotID())
	if err != nil {
		return err
	}
	if perm == nil || (!perm.IsAdmin && !perm.IsCreator) {
		return GuardError("guard.report_delete_permission_error")
	}
	ids := []int{int(id)}
	found, err := rh.svc.ops.GetMessages(ctx, chat, ids)
	if err != nil {
		return err
	}
	if len(found) == 0 || found[0] == nil {
		return nil
	}
	msg := found[0]
	if msg.ID != int(id) || msg.ChatID != chat || msg.SenderID != user {
		return GuardError("guard.report_delete_target_changed")
	}
	if err := rh.svc.ops.DeleteMessages(ctx, chat, ids); err != nil {
		return err
	}
	found, err = rh.svc.ops.GetMessages(ctx, chat, ids)
	if err != nil {
		return err
	}
	if len(found) != 0 && found[0] != nil {
		return GuardError("guard.report_delete_unconfirmed")
	}
	return nil
}
