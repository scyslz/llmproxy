package logstore

import (
	"path/filepath"
	"testing"
	"time"
)

func tempDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "logs")
}

func TestSystemStoreCRUDAndQuery(t *testing.T) {
	dir := tempDir(t)
	s, err := OpenSystem(dir)
	if err != nil {
		t.Fatalf("OpenSystem: %v", err)
	}
	defer s.Close()

	rid := "req-abc"
	now := time.Now().UnixMilli()
	if err := s.Insert(now, "info", "proxy", 1, "hello", &rid); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := s.Insert(now+1, "error", "system", 1, "boom", nil); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	logs, total, err := s.Query("", "", "", 0, 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	// newest first then reversed ascending -> logs[0] is oldest
	if len(logs) != 2 {
		t.Fatalf("len = %d, want 2", len(logs))
	}
	if logs[0].Message != "hello" || logs[1].Message != "boom" {
		t.Fatalf("order wrong: %+v", logs)
	}
	if logs[0].RequestID == nil || *logs[0].RequestID != rid {
		t.Fatalf("requestID not preserved: %v", logs[0].RequestID)
	}

	filtered, _, err := s.Query("error", "", "", 0, 10)
	if err != nil {
		t.Fatalf("Query level: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Message != "boom" {
		t.Fatalf("level filter wrong: %+v", filtered)
	}

	byRID, _, err := s.Query("", "", rid, 0, 10)
	if err != nil {
		t.Fatalf("Query requestID: %v", err)
	}
	if len(byRID) != 1 || byRID[0].Message != "hello" {
		t.Fatalf("requestID filter wrong: %+v", byRID)
	}
}

func TestSystemStoreLiveTailSinceID(t *testing.T) {
	dir := tempDir(t)
	s, err := OpenSystem(dir)
	if err != nil {
		t.Fatalf("OpenSystem: %v", err)
	}
	defer s.Close()

	now := time.Now().UnixMilli()
	if err := s.Insert(now, "info", "proxy", 1, "one", nil); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	logs, _, err := s.Query("", "", "", 0, 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	lastID := logs[len(logs)-1].ID

	if err := s.Insert(now+1, "info", "proxy", 1, "two", nil); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	tail, _, err := s.Query("", "", "", lastID, 10)
	if err != nil {
		t.Fatalf("Query since: %v", err)
	}
	if len(tail) != 1 || tail[0].Message != "two" {
		t.Fatalf("tail wrong: %+v", tail)
	}
}

func TestSystemStoreClearShrinksFile(t *testing.T) {
	dir := tempDir(t)
	s, err := OpenSystem(dir)
	if err != nil {
		t.Fatalf("OpenSystem: %v", err)
	}
	defer s.Close()

	now := time.Now().UnixMilli()
	for i := 0; i < 2000; i++ {
		if err := s.Insert(now+int64(i), "info", "proxy", 1, "fill fill fill fill fill fill fill fill fill fill fill fill fill fill fill fill", nil); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}
	before := s.Size()
	if before == 0 {
		t.Fatal("size should be non-zero before clear")
	}
	if err := s.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	after := s.Size()
	if after >= before {
		t.Fatalf("size did not shrink after clear: before=%d after=%d", before, after)
	}
	cnt, err := s.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if cnt != 0 {
		t.Fatalf("count after clear = %d, want 0", cnt)
	}
}

func TestSystemStoreClearFile(t *testing.T) {
	dir := tempDir(t)
	s, err := OpenSystem(dir)
	if err != nil {
		t.Fatalf("OpenSystem: %v", err)
	}
	defer s.Close()

	now := time.Now().UnixMilli()
	_ = s.Insert(now, "info", "proxy", 1, "f1", nil)
	_ = s.Insert(now+1, "info", "proxy", 2, "f2", nil)
	if err := s.ClearFile(1); err != nil {
		t.Fatalf("ClearFile: %v", err)
	}
	logs, _, err := s.Query("", "", "", 0, 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(logs) != 1 || logs[0].Message != "f2" {
		t.Fatalf("ClearFile(1) wrong: %+v", logs)
	}
}

func TestSystemStoreHasRequestIDs(t *testing.T) {
	dir := tempDir(t)
	s, err := OpenSystem(dir)
	if err != nil {
		t.Fatalf("OpenSystem: %v", err)
	}
	defer s.Close()

	r1 := "req-1"
	now := time.Now().UnixMilli()
	_ = s.Insert(now, "info", "proxy", 1, "a", &r1)
	_ = s.Insert(now+1, "info", "proxy", 1, "b", &r1)
	_ = s.Insert(now+2, "info", "proxy", 1, "c", nil)

	present, err := s.HasRequestIDs([]string{"req-1", "req-2", "", "req-1"})
	if err != nil {
		t.Fatalf("HasRequestIDs: %v", err)
	}
	if !present["req-1"] {
		t.Error("req-1 should be present")
	}
	if present["req-2"] {
		t.Error("req-2 should not be present")
	}

	empty, err := s.HasRequestIDs(nil)
	if err != nil {
		t.Fatalf("HasRequestIDs(nil): %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected empty map, got %v", empty)
	}
}

func TestRequestStoreCRUDAndStats(t *testing.T) {
	dir := tempDir(t)
	s, err := OpenRequest(dir, 100)
	if err != nil {
		t.Fatalf("OpenRequest: %v", err)
	}
	defer s.Close()

	base := time.Now().UnixMilli()
	mk := func(ts int64, id, key, model string, pt, ct, cached int, status int) *RequestLog {
		return &RequestLog{
			ID: id, Timestamp: time.UnixMilli(ts).UTC().Format("2006-01-02T15:04:05.000Z"),
			KeyName: key, Model: model, Provider: "p", PromptTokens: pt, CompletionToks: ct,
			CachedTokens: cached, TotalTokens: pt + ct, Status: status, DurationMS: 10,
			RequestID: "req-" + model,
		}
	}
	_ = s.Insert(mk(base, "id-1", "alice", "gpt-4o", 10, 20, 5, 200))
	_ = s.Insert(mk(base+1, "id-2", "bob", "claude", 100, 50, 0, 500))
	_ = s.Insert(mk(base+2, "id-3", "alice", "gpt-4o", 30, 40, 7, 200))

	logs, err := s.Query(QueryFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(logs) != 3 {
		t.Fatalf("len = %d, want 3", len(logs))
	}
	// newest first
	if logs[0].Model != "gpt-4o" {
		t.Fatalf("newest first wrong: %+v", logs[0])
	}

	filtered, err := s.Query(QueryFilter{Key: "alice"}, 10, 0)
	if err != nil {
		t.Fatalf("Query key: %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("key filter len = %d, want 2", len(filtered))
	}

	status2xx, err := s.Query(QueryFilter{Status: "2xx"}, 10, 0)
	if err != nil {
		t.Fatalf("Query status: %v", err)
	}
	if len(status2xx) != 2 {
		t.Fatalf("status 2xx len = %d, want 2", len(status2xx))
	}

	count, prompt, completion, cached, total, err := s.Stats(QueryFilter{})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if count != 3 || prompt != 140 || completion != 110 || cached != 12 || total != 250 {
		t.Fatalf("stats mismatch: count=%d prompt=%d completion=%d cached=%d total=%d",
			count, prompt, completion, cached, total)
	}

	countA, _, _, _, _, _ := s.Stats(QueryFilter{Key: "alice"})
	if countA != 2 {
		t.Fatalf("stats key filter count = %d, want 2", countA)
	}
}

func TestRequestStoreTrimAndClear(t *testing.T) {
	dir := tempDir(t)
	s, err := OpenRequest(dir, 5)
	if err != nil {
		t.Fatalf("OpenRequest: %v", err)
	}
	defer s.Close()

	base := time.Now().UnixMilli()
	for i := 0; i < 20; i++ {
		_ = s.Insert(&RequestLog{
			ID: "id" + itoa2(i), Timestamp: time.UnixMilli(base + int64(i)).UTC().Format("2006-01-02T15:04:05.000Z"),
			KeyName: "k", Model: "m", TotalTokens: 1, Status: 200,
		})
	}
	logs, err := s.Query(QueryFilter{}, 50, 0)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(logs) != 5 {
		t.Fatalf("trimmed len = %d, want 5", len(logs))
	}
	if logs[0].ID != "id19" {
		t.Fatalf("newest should be kept, got %s", logs[0].ID)
	}

	if err := s.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	logs, err = s.Query(QueryFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("Query after clear: %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("len after clear = %d, want 0", len(logs))
	}
}

func itoa2(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestOpenRequestMigratesLegacySchema(t *testing.T) {
	dir := tempDir(t)
	legacy, err := openDB(dir)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	if _, err := legacy.Exec(`CREATE TABLE request_logs (
		id TEXT PRIMARY KEY,
		time INTEGER NOT NULL,
		key_name TEXT,
		key_id TEXT,
		model TEXT,
		provider TEXT,
		path TEXT,
		method TEXT,
		prompt_tokens INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		cached_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens INTEGER NOT NULL DEFAULT 0,
		status INTEGER NOT NULL DEFAULT 0,
		duration_ms INTEGER NOT NULL DEFAULT 0,
		stream INTEGER NOT NULL DEFAULT 0,
		error TEXT
	)`); err != nil {
		t.Fatalf("create legacy: %v", err)
	}
	legacy.Close()

	// legacy rows with NULL text columns
	db, err := openDB(dir)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	_, _ = db.Exec("INSERT INTO request_logs (id, time, model, status) VALUES ('legacy-1', ?, 'm', 200)", time.Now().UnixMilli())
	db.Close()

	s, err := OpenRequest(dir, 100)
	if err != nil {
		t.Fatalf("OpenRequest migrate: %v", err)
	}
	defer s.Close()
	logs, err := s.Query(QueryFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("Query legacy: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("legacy rows = %d, want 1", len(logs))
	}
	if logs[0].ID != "legacy-1" || logs[0].HasDetail {
		t.Fatalf("legacy row wrong: %+v", logs[0])
	}
}

func TestOpenSystemMigratesLegacySchema(t *testing.T) {
	dir := tempDir(t)
	legacy, err := openDB(dir)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	if _, err := legacy.Exec(`CREATE TABLE system_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		time INTEGER NOT NULL,
		level TEXT NOT NULL,
		category TEXT,
		file INTEGER,
		message TEXT
	)`); err != nil {
		t.Fatalf("create legacy: %v", err)
	}
	legacy.Close()

	s, err := OpenSystem(dir)
	if err != nil {
		t.Fatalf("OpenSystem migrate: %v", err)
	}
	defer s.Close()
	if err := s.Insert(time.Now().UnixMilli(), "info", "system", 1, "post-migrate", nil); err != nil {
		t.Fatalf("Insert after migrate: %v", err)
	}
	logs, _, err := s.Query("", "", "", 0, 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(logs) != 1 || logs[0].Message != "post-migrate" {
		t.Fatalf("unexpected: %+v", logs)
	}
}
