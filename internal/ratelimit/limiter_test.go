package ratelimit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/pavanv25/ai-gateway/internal/ratelimit"
)

// newTestSetup starts an in-process Redis, returns a wired Limiter and the
// underlying client so tests can seed the sorted set directly if needed.
func newTestSetup(t *testing.T, tpmLimit int) (*ratelimit.Limiter, *redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return ratelimit.New(rdb, ratelimit.Config{TPMLimit: tpmLimit}), rdb, mr
}

func TestReserve_WithinLimit(t *testing.T) {
	l, _, _ := newTestSetup(t, 1000)
	token, err := l.Reserve(context.Background(), "key1", 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token == "" {
		t.Error("expected non-empty reservation token")
	}
}

func TestReserve_ExceedsLimit(t *testing.T) {
	l, _, _ := newTestSetup(t, 1000)
	ctx := context.Background()

	if _, err := l.Reserve(ctx, "key1", 600); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	_, err := l.Reserve(ctx, "key1", 600)
	if !errors.Is(err, ratelimit.ErrLimitExceeded) {
		t.Errorf("got %v, want ErrLimitExceeded", err)
	}
}

func TestReserve_ZeroMaxTokensUsesDefault(t *testing.T) {
	l, _, _ := newTestSetup(t, 2000)
	ctx := context.Background()

	// Reserve with 0 should consume defaultMaxTokens (1000).
	if _, err := l.Reserve(ctx, "key1", 0); err != nil {
		t.Fatalf("reserve(0): %v", err)
	}
	// 1001 more would exceed 2000 total.
	_, err := l.Reserve(ctx, "key1", 1001)
	if !errors.Is(err, ratelimit.ErrLimitExceeded) {
		t.Errorf("got %v, want ErrLimitExceeded", err)
	}
}

func TestCommit_ReducesUsage(t *testing.T) {
	l, _, _ := newTestSetup(t, 1000)
	ctx := context.Background()

	token, err := l.Reserve(ctx, "key1", 800)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := l.Commit(ctx, "key1", token, 200); err != nil {
		t.Fatalf("commit: %v", err)
	}
	// After commit, window has 200 tokens used; 700 more should succeed.
	if _, err := l.Reserve(ctx, "key1", 700); err != nil {
		t.Errorf("reserve after commit: %v", err)
	}
}

func TestCommit_ZeroActual(t *testing.T) {
	l, _, _ := newTestSetup(t, 1000)
	ctx := context.Background()

	token, err := l.Reserve(ctx, "key1", 500)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := l.Commit(ctx, "key1", token, 0); err != nil {
		t.Fatalf("commit(0): %v", err)
	}
	// Window is now empty; full limit is available.
	if _, err := l.Reserve(ctx, "key1", 1000); err != nil {
		t.Errorf("reserve after zero commit: %v", err)
	}
}

func TestCommit_NegativeActualClamped(t *testing.T) {
	l, _, _ := newTestSetup(t, 1000)
	ctx := context.Background()

	token, err := l.Reserve(ctx, "key1", 500)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := l.Commit(ctx, "key1", token, -50); err != nil {
		t.Fatalf("commit(-50): %v", err)
	}
	// Negative is clamped to 0; full limit should be available.
	if _, err := l.Reserve(ctx, "key1", 1000); err != nil {
		t.Errorf("reserve after negative commit: %v", err)
	}
}

func TestReserve_SlidingWindow_Expiry(t *testing.T) {
	l, rdb, _ := newTestSetup(t, 1000)
	ctx := context.Background()

	// Seed an entry with a score 61 seconds in the past — outside the window.
	oldScore := float64(time.Now().Add(-61 * time.Second).UnixMilli())
	rdb.ZAdd(ctx, "rl:keyExpiry", redis.Z{Score: oldScore, Member: "stale:1000"})

	// Reserve should succeed: the stale entry is pruned by ZREMRANGEBYSCORE.
	if _, err := l.Reserve(ctx, "keyExpiry", 1000); err != nil {
		t.Errorf("reserve with expired entries: %v", err)
	}
}

func TestReserve_IsolatedByAPIKey(t *testing.T) {
	l, _, _ := newTestSetup(t, 1000)
	ctx := context.Background()

	if _, err := l.Reserve(ctx, "keyA", 1000); err != nil {
		t.Fatalf("keyA reserve: %v", err)
	}
	if _, err := l.Reserve(ctx, "keyB", 1000); err != nil {
		t.Errorf("keyB should be unaffected by keyA: %v", err)
	}
}

func TestUsage_ReflectsReservations(t *testing.T) {
	l, _, _ := newTestSetup(t, 1000)
	ctx := context.Background()

	if _, err := l.Reserve(ctx, "key1", 300); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	used, limit, err := l.Usage(ctx, "key1")
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if used != 300 {
		t.Errorf("used = %d, want 300", used)
	}
	if limit != 1000 {
		t.Errorf("limit = %d, want 1000", limit)
	}

	// Usage must not mutate the window — calling it again returns the same value.
	used2, _, err := l.Usage(ctx, "key1")
	if err != nil {
		t.Fatalf("second usage: %v", err)
	}
	if used2 != 300 {
		t.Errorf("second used = %d, want 300 (Usage must be idempotent)", used2)
	}

	// A subsequent Reserve should still see only the original 300 tokens used.
	if _, err := l.Reserve(ctx, "key1", 700); err != nil {
		t.Errorf("reserve after Usage polls: %v", err)
	}
}

func TestUsage_PrunesExpiredEntries(t *testing.T) {
	l, rdb, _ := newTestSetup(t, 1000)
	ctx := context.Background()

	oldScore := float64(time.Now().Add(-61 * time.Second).UnixMilli())
	rdb.ZAdd(ctx, "rl:keyExpiry", redis.Z{Score: oldScore, Member: "stale:900"})

	used, _, err := l.Usage(ctx, "keyExpiry")
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if used != 0 {
		t.Errorf("used = %d, want 0 (expired entry should be pruned)", used)
	}
}

func TestUsage_EmptyKey(t *testing.T) {
	l, _, _ := newTestSetup(t, 500)
	ctx := context.Background()

	used, limit, err := l.Usage(ctx, "neverUsed")
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if used != 0 {
		t.Errorf("used = %d, want 0", used)
	}
	if limit != 500 {
		t.Errorf("limit = %d, want 500", limit)
	}
}
