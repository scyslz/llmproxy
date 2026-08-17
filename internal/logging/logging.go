package logging

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"llmproxy/internal/config"
	"llmproxy/internal/domain"
	"llmproxy/internal/logstore"
)

// SystemLogLevel / constants to keep call sites readable.
const (
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

type memLog struct {
	Timestamp string
	Level     string
	Category  string
	Message   string
	RequestID string
	File      int
}

// Logger writes system/proxy logs to the console and the sqlite system store,
// maintains a small in-memory ring for hasRelatedLogs lookups, and performs
// automated rotation when the database file grows beyond maxLogSizeMB.
type Logger struct {
	mu      sync.Mutex
	cfg     *config.Manager
	sys     *logstore.SystemStore
	recent  []memLog
	recentN int
}

// New creates the Logger.
func New(cfg *config.Manager, sys *logstore.SystemStore) *Logger {
	return &Logger{cfg: cfg, sys: sys, recentN: 200}
}

// ShortRequestID returns a random 16-hex request identifier.
func ShortRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// MaskKey shortens an API key: first 8 chars + **** + last 4.
func MaskKey(key string) string {
	if len(key) <= 8 {
		return key
	}
	return key[:8] + "****" + key[len(key)-4:]
}

// HasRelatedLogs reports whether any recent log carried the given request id.
func (l *Logger) HasRelatedLogs(requestID string) bool {
	if requestID == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, m := range l.recent {
		if m.RequestID == requestID {
			return true
		}
	}
	return false
}

// Log writes one entry. System-level logs are always recorded; proxy-level
// callers should gate with DetailEnabled.
func (l *Logger) Log(level, message, category string, requestID string) {
	now := time.Now()
	fileNum := l.checkRotate()
	requestIDPtr := (*string)(nil)
	if requestID != "" {
		requestIDPtr = &requestID
	}
	fmt.Printf("[%s] [%s] %s\n", upper(level), upper(orDefault(category, "system")), message)
	l.mu.Lock()
	l.recent = append(l.recent, memLog{
		Timestamp: now.Format(time.RFC3339Nano), Level: level,
		Category: orDefault(category, "system"), Message: message,
		RequestID: requestID, File: fileNum,
	})
	if len(l.recent) > l.recentN {
		l.recent = l.recent[1:]
	}
	l.mu.Unlock()
	_ = l.sys.Insert(now.UnixMilli(), level, orDefault(category, "system"), fileNum, message, requestIDPtr)
}

// checkRotate rotates storage (bumping activeLogFile and dropping the other
// bucket) if the sqlite file has exceeded the configured size cap.
func (l *Logger) checkRotate() int {
	cfg := l.cfg.Get()
	if cfg.MaxLogSizeMB <= 0 {
		return cfg.ActiveLogFile
	}
	if l.sys.Size() >= int64(cfg.MaxLogSizeMB)*1024*1024 {
		next := 2
		if cfg.ActiveLogFile == 2 {
			next = 1
		}
		_ = l.sys.ClearFile(next)
		_ = l.cfg.Update(func(c *domain.Config) { c.ActiveLogFile = next })
		return next
	}
	return cfg.ActiveLogFile
}

func upper(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[0] >= 'a' && b[0] <= 'z' {
		b[0] -= 32
	}
	return string(b)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}