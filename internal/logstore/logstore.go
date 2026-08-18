package logstore

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const maxOpenConns = 1

// SystemLog is the row model for the system_logs table.
type SystemLog struct {
	ID        int64   `json:"id"`
	Timestamp string  `json:"timestamp"`
	Level     string  `json:"level"`
	Category  string  `json:"category"`
	File      int     `json:"file"`
	Message   string  `json:"message"`
	RequestID *string `json:"requestId"`
}

// RequestLog mirrors the request_logs table.
type RequestLog struct {
	ID             string `json:"id"`
	Timestamp      string `json:"timestamp"`
	KeyName        string `json:"keyName"`
	KeyID          string `json:"keyId"`
	Model          string `json:"model"`
	Provider       string `json:"provider"`
	Path           string `json:"path"`
	Method         string `json:"method"`
	PromptTokens   int    `json:"promptTokens"`
	CompletionToks int    `json:"completionTokens"`
	CachedTokens   int    `json:"cachedTokens"`
	TotalTokens    int    `json:"totalTokens"`
	Status         int    `json:"status"`
	DurationMS     int    `json:"durationMs"`
	Stream         bool   `json:"stream"`
	Error          string `json:"error,omitempty"`
	RequestID      string `json:"requestId,omitempty"`
	HasDetail      bool   `json:"hasDetail"`
}

// QueryFilter carries the optional filters for request log queries.
type QueryFilter struct {
	Key      string
	Model    string
	Provider string
	From     *int64
	To       *int64
	Status   string // "", "2xx", "4xx", "5xx"
}

// SystemStore persists system/proxy logs.
type SystemStore struct {
	mu       sync.Mutex
	db       *sql.DB
	sizePath string
}

// RequestStore persists per-request usage logs.
type RequestStore struct {
	mu  sync.Mutex
	db  *sql.DB
	max int
}

func openDB(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(1)
	return db, nil
}

// ensureColumn migrates legacy tables by adding a column if it is missing.
func ensureColumn(db *sql.DB, table, column, ddl string) error {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	_, err = db.Exec("ALTER TABLE " + table + " ADD COLUMN " + ddl)
	return err
}

// OpenSystem opens (creating if needed) the system log database at path.
func OpenSystem(path string) (*SystemStore, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS system_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			time INTEGER NOT NULL,
			level TEXT NOT NULL,
			category TEXT,
			file INTEGER,
			message TEXT,
			request_id TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_syslog_time ON system_logs(time);
		CREATE INDEX IF NOT EXISTS idx_syslog_level ON system_logs(level);
	`); err != nil {
		db.Close()
		return nil, err
	}
	if err := ensureColumn(db, "system_logs", "request_id", "request_id TEXT"); err != nil {
		db.Close()
		return nil, err
	}
	return &SystemStore{db: db, sizePath: path}, nil
}

// Size returns the current on-disk size in bytes of the database file.
func (s *SystemStore) Size() int64 {
	fi, err := os.Stat(s.sizePath)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// Insert appends a system log row.
func (s *SystemStore) Insert(ts int64, level, category string, file int, message string, requestID *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		"INSERT INTO system_logs (time, level, category, file, message, request_id) VALUES (?,?,?,?,?,?)",
		ts, level, nullStr(category), nullInt(file), message, nullStrPtr(requestID))
	return err
}

// Query returns system logs filtered by the given criteria.
// With sinceID the rows are returned ascending (live polling); otherwise newest
// first, then reversed into ascending order for the dashboard.
func (s *SystemStore) Query(level, category, requestID string, sinceID int64, limit int) (logs []SystemLog, total int64, err error) {
	var clauses []string
	var args []interface{}
	if sinceID > 0 {
		clauses = append(clauses, "id > ?")
		args = append(args, sinceID)
	}
	if level != "" {
		clauses = append(clauses, "level = ?")
		args = append(args, level)
	}
	if category != "" {
		clauses = append(clauses, "category = ?")
		args = append(args, category)
	}
	if requestID != "" {
		clauses = append(clauses, "request_id = ?")
		args = append(args, requestID)
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE "
		for i, c := range clauses {
			if i > 0 {
				where += " AND "
			}
			where += c
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if sinceID <= 0 {
		if err := s.db.QueryRow("SELECT COUNT(*) FROM system_logs"+where, args...).Scan(&total); err != nil {
			return nil, 0, err
		}
	}

	order := "DESC"
	if sinceID > 0 {
		order = "ASC"
	}
	query := "SELECT id, time, level, category, file, message, request_id FROM system_logs" +
		where + " ORDER BY id " + order + " LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, total, err
	}
	defer rows.Close()
	logs = []SystemLog{}
	for rows.Next() {
		var l SystemLog
		var t int64
		var reqID sql.NullString
		var file sql.NullInt64
		var category sql.NullString
		if err := rows.Scan(&l.ID, &t, &l.Level, &category, &file, &l.Message, &reqID); err != nil {
			return nil, total, err
		}
		l.Category = category.String
		l.Timestamp = time.UnixMilli(t).UTC().Format("2006-01-02T15:04:05.000Z")
		if file.Valid {
			l.File = int(file.Int64)
		}
		if reqID.Valid && reqID.String != "" {
			l.RequestID = &reqID.String
		}
		logs = append(logs, l)
	}
	if err := rows.Err(); err != nil {
		return nil, total, err
	}
	// Reverse to ascending order unless there was an explicit sinceID (live tail).
	if sinceID <= 0 {
		for i, j := 0, len(logs)-1; i < j; i, j = i+1, j-1 {
			logs[i], logs[j] = logs[j], logs[i]
		}
	}
	return logs, total, nil
}

// Clear removes all system logs and resets the autoincrement sequence.
func (s *SystemStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec("DELETE FROM system_logs"); err != nil {
		return err
	}
	_, _ = s.db.Exec("DELETE FROM sqlite_sequence WHERE name = 'system_logs'")
	// VACUUM reclaims the freed pages so Size() reflects the real footprint.
	_, _ = s.db.Exec("VACUUM")
	return nil
}

// ClearFile drops logs belonging to the given file bucket (log rotation).
func (s *SystemStore) ClearFile(file int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("DELETE FROM system_logs WHERE file = ? OR file IS NULL", file)
	return err
}

// Count returns the total number of stored system logs.
func (s *SystemStore) Count() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var c int64
	err := s.db.QueryRow("SELECT COUNT(*) FROM system_logs").Scan(&c)
	return c, err
}

// Close closes the underlying database.
func (s *SystemStore) Close() error { return s.db.Close() }

// OpenRequest opens (creating if needed) the request log database at path.
func OpenRequest(path string, max int) (*RequestStore, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS request_logs (
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
			error TEXT,
			request_id TEXT,
			has_detail INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_req_time ON request_logs(time);
		CREATE INDEX IF NOT EXISTS idx_req_key ON request_logs(key_name);
		CREATE INDEX IF NOT EXISTS idx_req_model ON request_logs(model);
	`); err != nil {
		db.Close()
		return nil, err
	}
	if err := ensureColumn(db, "request_logs", "request_id", "request_id TEXT"); err != nil {
		db.Close()
		return nil, err
	}
	if err := ensureColumn(db, "request_logs", "has_detail", "has_detail INTEGER NOT NULL DEFAULT 0"); err != nil {
		db.Close()
		return nil, err
	}
	if max <= 0 {
		max = 10000
	}
	return &RequestStore{db: db, max: max}, nil
}

// Insert appends a request log and trims the oldest rows above the max.
func (s *RequestStore) Insert(l *RequestLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts := parseISOTime(l.Timestamp)
	stream := 0
	if l.Stream {
		stream = 1
	}
	detail := 0
	if l.HasDetail {
		detail = 1
	}
	if _, err := s.db.Exec(`
		INSERT INTO request_logs
		  (id, time, key_name, key_id, model, provider, path, method,
		   prompt_tokens, completion_tokens, cached_tokens, total_tokens,
		   status, duration_ms, stream, error, request_id, has_detail)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		l.ID, ts, nullStr(l.KeyName), nullStr(l.KeyID), nullStr(l.Model),
		nullStr(l.Provider), nullStr(l.Path), nullStr(l.Method),
		l.PromptTokens, l.CompletionToks, l.CachedTokens, l.TotalTokens,
		l.Status, l.DurationMS, stream, nullStr(l.Error), nullStr(l.RequestID), detail); err != nil {
		return err
	}
	if s.max > 0 {
		var count int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM request_logs").Scan(&count); err == nil && count > s.max {
			excess := count - s.max
			_, _ = s.db.Exec(
				"DELETE FROM request_logs WHERE id IN (SELECT id FROM request_logs ORDER BY time ASC LIMIT ?)",
				excess)
		}
	}
	return nil
}

// Query returns request logs with filters, newest first, paginated.
func (s *RequestStore) Query(f QueryFilter, limit, offset int) ([]RequestLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	where, args := buildFilters(f)
	rows, err := s.db.Query(
		"SELECT id, time, key_name, key_id, model, provider, path, method, "+
			"prompt_tokens, completion_tokens, cached_tokens, total_tokens, status, duration_ms, stream, error, request_id, has_detail FROM request_logs"+
			where+" ORDER BY time DESC LIMIT ? OFFSET ?",
		append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RequestLog
	for rows.Next() {
		var l RequestLog
		var t int64
		var id string
		var keyName, keyID, model, provider, path, method sql.NullString
		var pt, ct, cat, tt, dur, stream int
		var status int
		var hasDetail int
		var errCol, rid sql.NullString
		if err := rows.Scan(&id, &t, &keyName, &keyID, &model, &provider, &path, &method,
			&pt, &ct, &cat, &tt, &status, &dur, &stream, &errCol, &rid, &hasDetail); err != nil {
			return nil, err
		}
		l = RequestLog{
			ID: id, Timestamp: time.UnixMilli(t).UTC().Format("2006-01-02T15:04:05.000Z"),
			KeyName: keyName.String, KeyID: keyID.String, Model: model.String, Provider: provider.String,
			Path: path.String, Method: method.String, PromptTokens: pt, CompletionToks: ct,
			CachedTokens: cat, TotalTokens: tt, Status: status, DurationMS: dur,
			Stream: stream == 1, Error: errCol.String, HasDetail: hasDetail == 1,
			RequestID: rid.String,
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Stats aggregates token usage over filtered rows.
func (s *RequestStore) Stats(f QueryFilter) (count, prompt, completion, cached, total int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	where, args := buildFilters(f)
	err = s.db.QueryRow(
		"SELECT COUNT(*), COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0), "+
			"COALESCE(SUM(cached_tokens),0), COALESCE(SUM(total_tokens),0) FROM request_logs"+where, args...).
		Scan(&count, &prompt, &completion, &cached, &total)
	return
}

// Clear removes all request logs.
func (s *RequestStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec("DELETE FROM request_logs"); err != nil {
		return err
	}
	// VACUUM reclaims the freed pages so the on-disk size reflects reality.
	_, _ = s.db.Exec("VACUUM")
	return nil
}

// Close closes the underlying database.
func (s *RequestStore) Close() error { return s.db.Close() }

func buildFilters(f QueryFilter) (string, []interface{}) {
	var clauses []string
	var args []interface{}
	if f.Key != "" {
		clauses = append(clauses, "key_name LIKE ?")
		args = append(args, "%"+f.Key+"%")
	}
	if f.Model != "" {
		clauses = append(clauses, "model LIKE ?")
		args = append(args, "%"+f.Model+"%")
	}
	if f.Provider != "" {
		clauses = append(clauses, "provider = ?")
		args = append(args, f.Provider)
	}
	if f.From != nil {
		clauses = append(clauses, "time >= ?")
		args = append(args, *f.From)
	}
	if f.To != nil {
		clauses = append(clauses, "time <= ?")
		args = append(args, *f.To)
	}
	switch f.Status {
	case "2xx":
		clauses = append(clauses, "status >= 200 AND status < 300")
	case "4xx":
		clauses = append(clauses, "status >= 400 AND status < 500")
	case "5xx":
		clauses = append(clauses, "status >= 500 AND status < 600")
	}
	if len(clauses) == 0 {
		return "", args
	}
	where := " WHERE "
	for i, c := range clauses {
		if i > 0 {
			where += " AND "
		}
		where += c
	}
	return where, args
}

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullStrPtr(s *string) interface{} {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

func nullInt(v int) interface{} {
	if v == 0 {
		return nil
	}
	return v
}

func parseISOTime(s string) int64 {
	if t, err := time.Parse("2006-01-02T15:04:05.000Z", s); err == nil {
		return t.UnixMilli()
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UnixMilli()
	}
	return time.Now().UnixMilli()
}