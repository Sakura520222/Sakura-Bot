// Copyright 2026 Sakura-Bot
//
// 本项目采用 GNU Affero General Public License Version 3.0 (AGPL-3.0) 许可
//
// - 署名：必须提供本项目的原始来源链接
// - 相同方式共享：衍生作品必须采用相同的许可证
//
// 本项目源代码：https://github.com/Sakura520222/Sakura-Bot
// 许可证全文：参见 LICENSE 文件

package mtproto

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/crypto"
	"github.com/gotd/td/session"
	"github.com/gotd/td/tg"
)

type mockInvokerFunc func(ctx context.Context, input bin.Encoder, output bin.Decoder) error

func (f mockInvokerFunc) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	return f(ctx, input, output)
}

func createFakeTelethonSessionDB(t *testing.T, sqlitePath string, authKey []byte) {
	t.Helper()
	db, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		t.Fatalf("open sqlite %q: %v", sqlitePath, err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE TABLE sessions (
			dc_id INTEGER PRIMARY KEY,
			server_address TEXT,
			port INTEGER,
			auth_key BLOB,
			takeout_id INTEGER
		)`,
		`CREATE TABLE entities (
			id INTEGER PRIMARY KEY,
			hash INTEGER NOT NULL,
			username TEXT,
			phone INTEGER,
			name TEXT,
			date INTEGER
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec schema: %v", err)
		}
	}

	if _, err := db.Exec(
		`INSERT INTO sessions (dc_id, server_address, port, auth_key, takeout_id) VALUES (?, ?, ?, ?, NULL)`,
		4, "149.154.167.91", 443, authKey,
	); err != nil {
		t.Fatalf("insert session row: %v", err)
	}

	entities := []struct {
		id       int64
		hash     int64
		username sql.NullString
		name     string
	}{
		{
			id:       -1001234567890,
			hash:     777888999111,
			username: sql.NullString{String: "sakura_news", Valid: true},
			name:     "Sakura News Channel",
		},
		{
			id:       -555666,
			hash:     0,
			username: sql.NullString{Valid: false},
			name:     "Sakura Basic Group",
		},
		{
			id:       200300,
			hash:     -444555666777,
			username: sql.NullString{String: "alice_dev", Valid: true},
			name:     "Alice",
		},
	}
	for _, ent := range entities {
		if _, err := db.Exec(
			`INSERT INTO entities (id, hash, username, phone, name, date) VALUES (?, ?, ?, NULL, ?, 1700000000)`,
			ent.id, ent.hash, ent.username, ent.name,
		); err != nil {
			t.Fatalf("insert entity %d: %v", ent.id, err)
		}
	}
}

func TestTelethonSQLiteImportAndPeerCache(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	basePath := filepath.Join(tmpDir, "bot_session")
	sqlitePath := basePath + ".session"
	gotdPath := basePath + ".gotd.json"
	peersPath := basePath + ".peers.json"

	authKey := make([]byte, 256)
	for i := range authKey {
		authKey[i] = byte((i*37 + 13) & 0xFF)
	}
	createFakeTelethonSessionDB(t, sqlitePath, authKey)

	origSQLiteBytes, err := os.ReadFile(sqlitePath)
	if err != nil {
		t.Fatalf("read original .session: %v", err)
	}
	origHash := sha256.Sum256(origSQLiteBytes)

	// 1. 验证 HybridSessionStorage 导入 256 字节 auth_key 并写入 .gotd.json
	store := NewHybridSessionStorage(basePath)
	rawGotd, err := store.LoadSession(ctx)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}
	if !store.ImportedFromTelethon() {
		t.Fatalf("expected ImportedFromTelethon() == true")
	}
	if _, err := os.Stat(gotdPath); err != nil {
		t.Fatalf("expected .gotd.json to exist: %v", err)
	}

	mem := &session.StorageMemory{}
	if err := mem.StoreSession(ctx, rawGotd); err != nil {
		t.Fatalf("store in memory: %v", err)
	}
	loader := session.Loader{Storage: mem}
	decoded, err := loader.Load(ctx)
	if err != nil {
		t.Fatalf("loader.Load failed: %v", err)
	}
	if decoded.DC != 4 {
		t.Fatalf("expected DC=4, got %d", decoded.DC)
	}
	if decoded.Addr != "149.154.167.91:443" {
		t.Fatalf("expected Addr=149.154.167.91:443, got %q", decoded.Addr)
	}
	if !bytes.Equal(decoded.AuthKey, authKey) {
		t.Fatalf("decoded AuthKey mismatch")
	}
	var expectedKey crypto.Key
	copy(expectedKey[:], authKey)
	expectedKeyID := expectedKey.WithID().ID
	if !bytes.Equal(decoded.AuthKeyID, expectedKeyID[:]) {
		t.Fatalf("AuthKeyID mismatch: got %x, want %x", decoded.AuthKeyID, expectedKeyID[:])
	}

	// 2. 验证 PeerCache 导入 entities 并写入 .peers.json
	peers, err := NewPeerCache(sqlitePath) // 同时测试传入带 .session 后缀的路径
	if err != nil {
		t.Fatalf("NewPeerCache failed: %v", err)
	}
	if _, err := os.Stat(peersPath); err != nil {
		t.Fatalf("expected .peers.json to exist: %v", err)
	}

	groups := peers.KnownGroupEntities()
	if len(groups) != 2 {
		t.Fatalf("expected 2 known group/channel entities, got %d: %+v", len(groups), groups)
	}

	// 验证各类 target 解析
	chPeer, err := peers.ResolveInputChannel(ctx, nil, "https://t.me/sakura_news/123")
	if err != nil {
		t.Fatalf("ResolveInputChannel URL failed: %v", err)
	}
	ic, ok := chPeer.(*tg.InputChannel)
	if !ok || ic.ChannelID != 1234567890 || ic.AccessHash != 777888999111 {
		t.Fatalf("unexpected resolved InputChannel: %#v", chPeer)
	}

	cLinkPeer, err := peers.ResolveInputPeer(ctx, nil, "https://t.me/c/1234567890/99")
	if err != nil {
		t.Fatalf("ResolveInputPeer t.me/c failed: %v", err)
	}
	if ipc, ok := cLinkPeer.(*tg.InputPeerChannel); !ok || ipc.ChannelID != 1234567890 || ipc.AccessHash != 777888999111 {
		t.Fatalf("unexpected InputPeerChannel from t.me/c: %#v", cLinkPeer)
	}

	chatPeer, err := peers.ResolveInputPeer(ctx, nil, int(-555666))
	if err != nil {
		t.Fatalf("ResolveInputPeer basic chat failed: %v", err)
	}
	if ipChat, ok := chatPeer.(*tg.InputPeerChat); !ok || ipChat.ChatID != 555666 {
		t.Fatalf("unexpected InputPeerChat: %#v", chatPeer)
	}

	userIn, err := peers.ResolveInputUser(ctx, nil, "@Alice_Dev")
	if err != nil {
		t.Fatalf("ResolveInputUser @Alice_Dev failed: %v", err)
	}
	if iu, ok := userIn.(*tg.InputUser); !ok || iu.UserID != 200300 || iu.AccessHash != -444555666777 {
		t.Fatalf("unexpected InputUser: %#v", userIn)
	}

	// 3. 验证缓存未命中时自动调用 contacts.ResolveUsername 并落盘缓存
	resolveCalled := 0
	mockAPI := tg.NewClient(mockInvokerFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.ContactsResolveUsernameRequest)
		if !ok {
			t.Fatalf("unexpected RPC request %T", input)
		}
		resolveCalled++
		if req.Username != "fresh_chan" {
			t.Fatalf("unexpected username %q", req.Username)
		}
		ch := &tg.Channel{
			ID:        2223334445,
			Title:     "Fresh Channel",
			Username:  "fresh_chan",
			Photo:     &tg.ChatPhotoEmpty{},
			Broadcast: true,
		}
		ch.SetAccessHash(9988776655)
		resp := &tg.ContactsResolvedPeer{
			Peer:  &tg.PeerChannel{ChannelID: 2223334445},
			Chats: []tg.ChatClass{ch},
		}
		var buf bin.Buffer
		if err := resp.Encode(&buf); err != nil {
			return err
		}
		return output.Decode(&buf)
	}))

	freshPeer, err := peers.ResolveInputChannel(ctx, mockAPI, "@fresh_chan")
	if err != nil {
		t.Fatalf("ResolveInputChannel miss failed: %v", err)
	}
	if resolveCalled != 1 {
		t.Fatalf("expected 1 ContactsResolveUsername call, got %d", resolveCalled)
	}
	if ic2, ok := freshPeer.(*tg.InputChannel); !ok || ic2.ChannelID != 2223334445 || ic2.AccessHash != 9988776655 {
		t.Fatalf("unexpected resolved fresh InputChannel: %#v", freshPeer)
	}
	// 第二次应直接命中内存/磁盘缓存，不再发起 RPC
	if _, err := peers.ResolveInputPeer(ctx, mockAPI, "https://t.me/fresh_chan"); err != nil {
		t.Fatalf("second ResolveInputPeer failed: %v", err)
	}
	if resolveCalled != 1 {
		t.Fatalf("expected cached lookup without extra RPC, got %d calls", resolveCalled)
	}

	// 4. 验证 Clear() 只清除 .gotd.json 并不重新导入旧 .session，且原 .session 文件字节分毫未改
	if err := store.Clear(); err != nil {
		t.Fatalf("store.Clear() failed: %v", err)
	}
	if _, err := store.LoadSession(ctx); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after Clear(), got %v", err)
	}

	afterSQLiteBytes, err := os.ReadFile(sqlitePath)
	if err != nil {
		t.Fatalf("read .session after operations: %v", err)
	}
	afterHash := sha256.Sum256(afterSQLiteBytes)
	if origHash != afterHash {
		t.Fatalf(".session file was modified! before=%x after=%x", origHash, afterHash)
	}
}
