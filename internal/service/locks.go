package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"time"

	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
)

// withLocks runs fn while holding the given operation-lock keys. Keys are
// acquired in sorted order (so multi-lock flows cannot deadlock against each
// other) and renewed by a heartbeat at one third of the TTL, so an operation
// legitimately longer than the TTL still excludes concurrent mutators. When
// renewal fails — the lock was taken over after expiry — the operation context
// is cancelled so fn aborts instead of continuing unprotected. Locks are
// released on a background context afterwards, so cancelling the caller (e.g.
// Ctrl-C) cannot strand a path for the remaining TTL.
func (a *App) withLocks(ctx context.Context, keys []string, fn func(ctx context.Context) error) error {
	ttl := time.Duration(a.Cfg.Locks.TTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = 900 * time.Second
	}
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	owner := newOwnerToken()
	var held []string
	for _, k := range sorted {
		if err := a.DB.AcquireLock(ctx, k, owner, ttl); err != nil {
			releaseLocks(context.Background(), a.DB, owner, held)
			return err
		}
		held = append(held, k)
	}
	defer releaseLocks(context.Background(), a.DB, owner, held)

	opCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	renewStop := make(chan struct{})
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		interval := ttl / 3
		if interval <= 0 {
			interval = time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-renewStop:
				return
			case <-ticker.C:
				for _, k := range held {
					if err := a.DB.RenewLock(context.Background(), k, owner, ttl); err != nil {
						// Lock lost — or the renewal itself failed (e.g. a
						// database hiccup). Either way abort the operation
						// safely rather than continue unprotected.
						cancel()
						return
					}
				}
			}
		}
	}()

	// Stop the heartbeat on panic as well as on return, so the goroutine
	// never outlives the lock holder.
	defer func() {
		close(renewStop)
		<-renewDone
	}()
	return fn(opCtx)
}

func releaseLocks(ctx context.Context, db *sqlitestore.DB, owner string, keys []string) {
	for _, k := range keys {
		_ = db.ReleaseLock(ctx, k, owner)
	}
}

// lockKeysForPaths builds the operation-lock keys for canonical paths.
func lockKeysForPaths(channelID int64, paths ...string) []string {
	keys := make([]string, 0, len(paths))
	for _, p := range paths {
		keys = append(keys, sqlitestore.LockKey(channelID, p))
	}
	return keys
}

// newOwnerToken mints a random operation-lock owner token.
func newOwnerToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
