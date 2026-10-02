package middleware

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"neilico/control-plane/internal/models"
)

const DefaultAPITokenUsageInterval = time.Minute

type usageEntry struct {
	lastAttempt time.Time
	updating    bool
}

type APITokenUsageTracker struct {
	mu       sync.Mutex
	interval time.Duration
	entries  map[uuid.UUID]usageEntry
	db       *gorm.DB
	logger   *slog.Logger
	now      func() time.Time
}

func NewAPITokenUsageTracker(db *gorm.DB, logger *slog.Logger, interval time.Duration) *APITokenUsageTracker {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = DefaultAPITokenUsageInterval
	}
	return &APITokenUsageTracker{
		interval: interval,
		entries:  make(map[uuid.UUID]usageEntry),
		db:       db,
		logger:   logger,
		now:      time.Now,
	}
}

func (t *APITokenUsageTracker) ShouldUpdate(id uuid.UUID) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	entry, ok := t.entries[id]
	if ok && (entry.updating || now.Sub(entry.lastAttempt) <= t.interval) {
		return false
	}
	entry.lastAttempt = now
	entry.updating = true
	t.entries[id] = entry
	return true
}

func (t *APITokenUsageTracker) Track(id uuid.UUID, ip string) {
	if t == nil || !t.ShouldUpdate(id) {
		return
	}
	go t.update(id, ip)
}

func (t *APITokenUsageTracker) update(id uuid.UUID, ip string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	now := t.now().UTC()
	err := t.db.WithContext(ctx).Model(&models.APIToken{}).Where("id = ?", id).Updates(map[string]any{
		"last_used_at": now,
		"last_used_ip": ip,
	}).Error
	t.mu.Lock()
	entry := t.entries[id]
	entry.updating = false
	if err != nil {
		entry.lastAttempt = time.Time{}
	}
	t.entries[id] = entry
	t.mu.Unlock()
	if err != nil {
		t.logger.Error("failed to update API token usage", "error", err)
	}
}
