package relayd

import (
	"context"
	"sync"
	"time"

	"github.com/yingtexu/eidopsyche/internal/store"
)

// WhitelistSource provides O(1) membership checks against a SQLite RO view,
// refreshing on a polling cadence.
type WhitelistSource struct {
	db       *store.DB
	interval time.Duration

	mu  sync.RWMutex
	set map[string]struct{}
}

func NewWhitelistSource(db *store.DB, interval time.Duration) *WhitelistSource {
	return &WhitelistSource{db: db, interval: interval, set: map[string]struct{}{}}
}

func (w *WhitelistSource) Run(ctx context.Context) error {
	if err := w.refresh(ctx); err != nil {
		return err
	}
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			_ = w.refresh(ctx)
		}
	}
}

// RefreshNow forces an immediate whitelist refresh.
func (w *WhitelistSource) RefreshNow(ctx context.Context) error { return w.refresh(ctx) }

func (w *WhitelistSource) refresh(ctx context.Context) error {
	rows, err := w.db.QueryContext(ctx, `SELECT pubkey FROM relay_whitelist`)
	if err != nil {
		return err
	}
	defer rows.Close()
	next := make(map[string]struct{}, 32)
	for rows.Next() {
		var pk string
		if err := rows.Scan(&pk); err != nil {
			return err
		}
		next[pk] = struct{}{}
	}
	w.mu.Lock()
	w.set = next
	w.mu.Unlock()
	return rows.Err()
}

func (w *WhitelistSource) Contains(pk string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	_, ok := w.set[pk]
	return ok
}
