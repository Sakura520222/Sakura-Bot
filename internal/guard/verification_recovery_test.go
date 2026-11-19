// Copyright 2026 Sakura-Bot
// 本项目采用 AGPL-3.0 许可
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot

package guard

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	guarddb "github.com/Sakura520222/Sakura-Bot/internal/db/guard"
	"github.com/Sakura520222/Sakura-Bot/internal/mtproto"
	"github.com/go-sql-driver/mysql"
)

const legacyVerificationID = "11111111111111111111111111111111"
const legacyAdminCaseID = "22222222222222222222222222222222"

func TestLegacyNoticeOriginRejectsTombstonesAndForeignMessages(t *testing.T) {
	session := guarddb.Row{"id": legacyVerificationID, "chat_id": int64(-1000000000200), "user_id": int64(71)}
	row := guarddb.Row{
		"id": legacyAdminCaseID, "action": "verify_start", "chat_id": session["chat_id"], "user_id": session["user_id"],
		"payload": map[string]any{"verification_session_id": legacyVerificationID, "verification_state": "held"},
	}
	notice := guarddb.Row{"target_id": int64(42), "message_id": int64(23), "synced_status": "done"}
	if !validNoticeOrigin(session, row, []guarddb.Row{notice}, 42, 23) {
		t.Fatal("original notice was rejected")
	}
	if validNoticeOrigin(session, row, []guarddb.Row{notice}, 42, 24) {
		t.Fatal("another message was accepted")
	}
	for _, state := range []string{"deleting", "removed"} {
		notice["synced_status"] = state
		if validNoticeOrigin(session, row, []guarddb.Row{notice}, 42, 23) {
			t.Fatalf("retired notice in %s state accepted", state)
		}
	}
	buttons := BuildReviewButtons(row, DefaultGuardGroup())
	if len(buttons) != 4 || buttons[0][0].Data != "sg:n:"+legacyAdminCaseID+":b" {
		t.Fatalf("legacy callback controls missing: %+v", buttons)
	}
	rowPayload(row)["verification_state"] = "passed"
	if buttons := BuildReviewButtons(row, DefaultGuardGroup()); len(buttons) != 3 {
		t.Fatalf("post-verification controls include skip: %+v", buttons)
	}
	rowPayload(row)["notification_replaced_by"] = "replacement"
	if len(BuildReviewButtons(row, DefaultGuardGroup())) != 0 {
		t.Fatal("replaced notice kept actionable controls")
	}
}

func TestLegacyAdminSkipRequiresPersistedExactAuthorization(t *testing.T) {
	now := time.Now().UTC()
	session := guarddb.Row{
		"id": legacyVerificationID, "status": "admin_pending", "chat_id": int64(-1000000000200),
		"user_id": int64(61), "message_id": int64(90),
	}
	intent := map[string]any{"action": "skip", "actor_id": int64(42), "case_id": legacyAdminCaseID}
	if validAdminIntent(session, intent, now) {
		t.Fatal("skip without written authorization was accepted")
	}
	intent["authorized_at"] = now.Add(-time.Second).Format(time.RFC3339)
	intent["session_id"], intent["chat_id"], intent["message_id"] = session["id"], session["chat_id"], session["message_id"]
	if !validAdminIntent(session, intent, now) {
		t.Fatal("bound skip authorization rejected")
	}
	caseRow := guarddb.Row{
		"id": legacyAdminCaseID, "action": "verify_skip", "chat_id": session["chat_id"],
		"user_id": session["user_id"], "reviewer_id": int64(42),
		"payload": map[string]any{"verification_admin": intent},
	}
	if !matchingAdminCase(session, caseRow, intent) {
		t.Fatal("stored case could not be matched to its verification")
	}
	caseRow["reviewer_id"] = int64(43)
	if matchingAdminCase(session, caseRow, intent) {
		t.Fatal("another reviewer matched stored authorization")
	}
	intent["message_id"] = int64(91)
	if validAdminIntent(session, intent, now) {
		t.Fatal("authorization for a different card was accepted")
	}
}

type recoveryTelegram struct {
	*fakeTelegramOps
	banFails  bool
	sendFails bool
	bans      int
	sends     int
	events    []string
}

func (f *recoveryTelegram) Permissions(_ context.Context, chat, user int64) (*mtproto.ParticipantPermissions, error) {
	return &mtproto.ParticipantPermissions{UserID: user, IsMember: true, IsCreator: user == 42}, nil
}

func (f *recoveryTelegram) Ban(context.Context, int64, int64) error {
	f.bans++
	if f.banFails {
		return errors.New("simulated ambiguous RPC")
	}
	return nil
}

func (f *recoveryTelegram) SendOnce(_ context.Context, target int64, _ string, _ mtproto.SendOptions, _ int64) (int, error) {
	f.sends++
	if f.sendFails {
		return 0, errors.New("simulated delivery failure")
	}
	f.events = append(f.events, "publish:"+strconv.FormatInt(target, 10))
	return 33, nil
}

func (f *recoveryTelegram) DeleteMessages(_ context.Context, _ int64, list []int) error {
	for _, id := range list {
		f.events = append(f.events, "delete:"+strconv.Itoa(id))
	}
	return nil
}

type approvingBeforeMembership struct {
	*fakeTelegramOps
	approvals int
}

func (f *approvingBeforeMembership) AnswerJoinRequestQuery(_ context.Context, _ int64, action string, _ float64) error {
	if action == "approve" {
		f.approvals++
	}
	return nil
}

func (f *approvingBeforeMembership) Rights(context.Context, int64, int64) (mtproto.BannedRights, error) {
	return mtproto.BannedRights{}, errors.New("USER_NOT_PARTICIPANT")
}

func TestMySQLApprovalQueuesVerificationBeforeMembershipIsVisible(t *testing.T) {
	repo := isolatedGuardRepository(t)
	ctx := t.Context()
	const chat, user = int64(-1000000000200), int64(71)
	ops := &approvingBeforeMembership{fakeTelegramOps: newFakeOps(9)}
	svc := NewService(repo, nil, ops, nil, nil)
	cfg := DefaultGuardConfig()
	grp := DefaultGuardGroup()
	grp.Enabled, grp.VerifyJoin, grp.NotificationEnabled = true, true, false
	cfg.Groups[strconv.FormatInt(chat, 10)] = grp
	svc.cfgPtr.Store(&cfg)
	id := newUUIDHex()
	err := repo.WithLock(ctx, user, func(st *guarddb.Store) error {
		if err := st.CreateCase(ctx, guarddb.CaseInsert{
			ID: id, EventKey: sha256Hex(id), ChatID: chat, UserID: user, Action: "approve", Status: "executing",
			Payload: map[string]any{"join_request_query": map[string]any{"id": "123", "expires_at": time.Now().Add(time.Minute).Unix()}},
		}); err != nil {
			return err
		}
		row, err := st.Case(ctx, id)
		if err != nil {
			return err
		}
		if err := svc.ExecuteCase(ctx, st, row); err != nil {
			return err
		}
		current, err := st.Case(ctx, id)
		if err != nil {
			return err
		}
		session, err := st.VerificationSession(ctx, chat, user)
		if err != nil {
			return err
		}
		if rowStr(current, "status") != "done" || rowStr(session, "status") != "checking" ||
			rowStr(rowPayload(session), "join_token") != "request:"+id || rowBool(rowPayload(session), "recheck_join_rules") {
			t.Fatalf("unexpected approval state %v with session %v", current, session)
		}
		return nil
	})
	if err != nil || ops.approvals != 1 {
		t.Fatalf("approval was not recorded once: approvals=%d, error=%v", ops.approvals, err)
	}
}

func TestMySQLLegacyAdminPendingResumesAfterTelegramFailure(t *testing.T) {
	repo := isolatedGuardRepository(t)
	ctx := t.Context()
	const chat, user = int64(-1000000000200), int64(71)
	ops := &recoveryTelegram{fakeTelegramOps: newFakeOps(9), banFails: true}
	svc := NewService(repo, nil, ops, nil, nil)
	settings := DefaultGuardConfig()
	grp := DefaultGuardGroup()
	grp.Enabled, grp.NotificationEnabled = true, false
	settings.Groups[strconv.FormatInt(chat, 10)] = grp
	svc.cfgPtr.Store(&settings)
	msgID := int64(20)
	if err := repo.WithLock(ctx, user, func(st *guarddb.Store) error {
		return st.SaveVerificationSession(ctx, guarddb.VerificationSessionSave{
			ID: legacyVerificationID, ChatID: chat, UserID: user, Status: "admin_pending", MessageID: &msgID,
			ExpiresAt: time.Now().Add(time.Minute),
			Payload: map[string]any{
				// The oldest administrative bans did not record the new release-proof fields.
				"admin_action": map[string]any{"actor_id": int64(42), "case_id": legacyAdminCaseID, "action": "ban"},
			},
		})
	}); err != nil {
		t.Fatal(err)
	}
	raw := guarddb.Row{"id": legacyVerificationID, "chat_id": chat, "user_id": user}
	svc.verification.ProcessDueSession(ctx, raw)
	row, err := repo.GetVerificationSession(ctx, legacyVerificationID)
	if err != nil || rowStr(row, "status") != "admin_pending" || ops.bans != 1 {
		t.Fatalf("failed RPC did not preserve pending state: row=%v bans=%d err=%v", row, ops.bans, err)
	}
	ops.banFails = false
	svc.verification.ProcessDueSession(ctx, raw)
	svc.verification.ProcessDueSession(ctx, raw)
	row, err = repo.GetVerificationSession(ctx, legacyVerificationID)
	if err != nil || rowStr(row, "status") != "admin_banned" || ops.bans != 2 {
		t.Fatalf("old case was not recovered once: row=%v bans=%d err=%v", row, ops.bans, err)
	}
	row, err = repo.GetCase(ctx, legacyAdminCaseID)
	if err != nil || rowStr(row, "status") != "pending" {
		t.Fatalf("recovered moderation case has state %q (%v)", rowStr(row, "status"), err)
	}
}

func TestMySQLLegacyVerificationNoticeRetiresOnlyAfterReplacement(t *testing.T) {
	repo := isolatedGuardRepository(t)
	ctx := t.Context()
	const chat, user, target = int64(-1000000000200), int64(71), int64(42)
	ops := &recoveryTelegram{fakeTelegramOps: newFakeOps(9), sendFails: true}
	svc := NewService(repo, nil, ops, nil, nil)
	grp := DefaultGuardGroup()
	grp.Enabled = true
	grp.NotificationIDs = []int64{target}
	startID, doneID := newUUIDHex(), newUUIDHex()
	err := repo.WithLock(ctx, user, func(st *guarddb.Store) error {
		for _, row := range []struct{ id, action string }{{startID, "verify_start"}, {doneID, "verify_pass"}} {
			if err := st.CreateCase(ctx, guarddb.CaseInsert{
				ID: row.id, EventKey: verificationLifecycleKey(chat, user, row.action, legacyVerificationID),
				ChatID: chat, UserID: user, Action: row.action, Status: "done",
				Payload: map[string]any{"verification_session_id": legacyVerificationID},
			}); err != nil {
				return err
			}
		}
		oldMessage := int64(19)
		if err := st.MarkCaseNoticeSynced(ctx, startID, target, &oldMessage, "done"); err != nil {
			return err
		}
		row, err := st.Case(ctx, doneID)
		if err != nil {
			return err
		}
		if err := svc.notifier.syncCaseNotices(ctx, st, row, grp); err != nil {
			return err
		}
		if len(ops.events) != 0 || !rowBool(rowPayload(row), "notification_cleanup_pending") {
			t.Fatalf("old card removed before a result was delivered: %v", ops.events)
		}
		if _, err := st.Exec(ctx, "UPDATE moderation_notifications SET retry_at=UTC_TIMESTAMP() WHERE case_id=?", doneID); err != nil {
			return err
		}
		ops.sendFails = false
		if err := svc.notifier.syncCaseNotices(ctx, st, row, grp); err != nil {
			return err
		}
		if !slices.Equal(ops.events, []string{"publish:42", "delete:19"}) {
			t.Fatalf("notice operations occurred out of order: %v", ops.events)
		}
		if err := st.MarkCaseNoticeSynced(ctx, startID, target, &oldMessage, "done"); err != nil {
			return err
		}
		records, err := st.CaseNotices(ctx, startID)
		if err != nil {
			return err
		}
		if len(records) != 1 || rowStr(records[0], "synced_status") != "removed" {
			t.Fatalf("late delivery resurrected retired notice: %v", records)
		}
		previous, err := st.Case(ctx, startID)
		if err != nil {
			return err
		}
		if err := svc.notifier.syncCaseNotices(ctx, st, previous, grp); err != nil {
			return err
		}
		if ops.sends != 2 || len(ops.events) != 2 {
			t.Fatalf("a retired notice was resent: sends=%d, operations=%v", ops.sends, ops.events)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func isolatedGuardRepository(t *testing.T) *guarddb.Repository {
	t.Helper()
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN is not configured for integration tests")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName, cfg.ParseTime, cfg.Loc = "", true, time.UTC
	server, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	name := "sakura_guard_test_" + newUUIDHex()[:12]
	if _, err := server.ExecContext(t.Context(), "CREATE DATABASE `"+name+"` CHARACTER SET utf8mb4"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := server.ExecContext(ctx, "DROP DATABASE `"+name+"`"); err != nil {
			t.Error(err)
		}
	})
	cfg.DBName = name
	mysqlDB, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mysqlDB.Close() })
	repo := &guarddb.Repository{DB: mysqlDB}
	if err := repo.Initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	return repo
}
