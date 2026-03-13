package storage_test

import (
	"encoding/json"
	"os"
	"testing"

	"goai-test/storage"
)

func tempDB(t *testing.T) *storage.DB {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "test-*.db")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	f.Close()
	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpenClose(t *testing.T) {
	db := tempDB(t)
	if db == nil {
		t.Fatal("expected non-nil DB")
	}
}

func TestSession_InsertGet(t *testing.T) {
	db := tempDB(t)
	s := storage.Session{ID: "sess-1", CWD: "/tmp/project", Title: "My Session", TimeCreated: 1000, TimeUpdated: 1000}
	if err := db.InsertSession(s); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	got, err := db.GetSession("sess-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.CWD != s.CWD {
		t.Errorf("cwd: want %q got %q", s.CWD, got.CWD)
	}
	if got.Title != s.Title {
		t.Errorf("title: want %q got %q", s.Title, got.Title)
	}
}

func TestSession_Update(t *testing.T) {
	db := tempDB(t)
	s := storage.Session{ID: "sess-2", CWD: "/a", Title: "old", TimeCreated: 1, TimeUpdated: 1}
	if err := db.InsertSession(s); err != nil {
		t.Fatalf("insert: %v", err)
	}
	s.Title = "new"
	if err := db.UpdateSession(s); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := db.GetSession("sess-2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "new" {
		t.Errorf("title: want %q got %q", "new", got.Title)
	}
}

func TestMessage_InsertGet(t *testing.T) {
	db := tempDB(t)
	_ = db.InsertSession(storage.Session{ID: "s1", CWD: "/", TimeCreated: 1, TimeUpdated: 1})
	data, _ := json.Marshal(map[string]string{"role": "user", "text": "hello"})
	m := storage.Message{ID: "msg-1", SessionID: "s1", TimeCreated: 2000, Data: json.RawMessage(data)}
	if err := db.InsertMessage(m); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	msgs, err := db.GetMessagesBySession("s1")
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	if msgs[0].ID != "msg-1" {
		t.Errorf("id: want msg-1 got %s", msgs[0].ID)
	}
}

func TestPart_InsertGetUpdate(t *testing.T) {
	db := tempDB(t)
	_ = db.InsertSession(storage.Session{ID: "s1", CWD: "/", TimeCreated: 1, TimeUpdated: 1})
	_ = db.InsertMessage(storage.Message{ID: "m1", SessionID: "s1", TimeCreated: 1, Data: json.RawMessage(`{}`)})

	p := storage.Part{
		ID: "part-1", MessageID: "m1", SessionID: "s1", TimeCreated: 3000,
		Data: json.RawMessage(`{"type":"text","text":"hi"}`),
	}
	if err := db.InsertPart(p); err != nil {
		t.Fatalf("insert part: %v", err)
	}
	parts, err := db.GetPartsByMessage("m1")
	if err != nil {
		t.Fatalf("get parts by message: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("want 1 part, got %d", len(parts))
	}

	p.Data = json.RawMessage(`{"type":"text","text":"updated"}`)
	if err := db.UpdatePart(p); err != nil {
		t.Fatalf("update part: %v", err)
	}
	parts2, err := db.GetPartsBySession("s1")
	if err != nil {
		t.Fatalf("get parts by session: %v", err)
	}
	if len(parts2) != 1 {
		t.Fatalf("want 1 part after update, got %d", len(parts2))
	}
	if string(parts2[0].Data) != `{"type":"text","text":"updated"}` {
		t.Errorf("unexpected data: %s", parts2[0].Data)
	}
}
