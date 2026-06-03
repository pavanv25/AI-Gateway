package metrics

import (
	"sync"
	"testing"
	"time"
)

// fixedClock returns a clock function that always returns t.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// TestStore_EmptyQuery verifies that querying an empty store returns zero values
// and does not panic (guards against nil-slice percentile index out of bounds).
func TestStore_EmptyQuery(t *testing.T) {
	s := newStore(fixedClock(time.Now()))
	snap := s.Query(time.Hour)
	if snap.Totals.RequestCount != 0 {
		t.Fatalf("expected 0 requests, got %d", snap.Totals.RequestCount)
	}
	if snap.Totals.RequestLatencyP50 != 0 || snap.Totals.RequestLatencyP95 != 0 {
		t.Fatal("expected zero percentiles on empty store")
	}
	if len(snap.Breakdowns) != 0 {
		t.Fatalf("expected no breakdowns, got %d", len(snap.Breakdowns))
	}
}

func TestStore_SingleEventTotals(t *testing.T) {
	now := time.Date(2024, 1, 1, 12, 0, 30, 0, time.UTC)
	s := newStore(fixedClock(now))

	s.Record(MetricEvent{
		Timestamp:         now,
		Provider:          "openai",
		Model:             "gpt-4o",
		PromptTokens:      100,
		CompletionTokens:  50,
		TotalTokens:       150,
		CostUSD:           0.003,
		RequestLatencyMs:  200,
		ProviderLatencyMs: 180,
	})

	snap := s.Query(time.Hour)
	agg := snap.Totals
	if agg.RequestCount != 1 {
		t.Fatalf("RequestCount: want 1, got %d", agg.RequestCount)
	}
	if agg.PromptTokens != 100 {
		t.Fatalf("PromptTokens: want 100, got %d", agg.PromptTokens)
	}
	if agg.CompletionTokens != 50 {
		t.Fatalf("CompletionTokens: want 50, got %d", agg.CompletionTokens)
	}
	if agg.TotalTokens != 150 {
		t.Fatalf("TotalTokens: want 150, got %d", agg.TotalTokens)
	}
	if agg.CostUSD != 0.003 {
		t.Fatalf("CostUSD: want 0.003, got %f", agg.CostUSD)
	}
	if agg.CacheMisses != 1 {
		t.Fatalf("CacheMisses: want 1, got %d", agg.CacheMisses)
	}
}

func TestStore_CacheHitMissCount(t *testing.T) {
	now := time.Now()
	s := newStore(fixedClock(now))

	s.Record(MetricEvent{Timestamp: now, Provider: "cache", CacheHit: true, RequestLatencyMs: 5})
	s.Record(MetricEvent{Timestamp: now, Provider: "openai", CacheHit: false, RequestLatencyMs: 200})

	snap := s.Query(time.Hour)
	if snap.Totals.CacheHits != 1 {
		t.Fatalf("CacheHits: want 1, got %d", snap.Totals.CacheHits)
	}
	if snap.Totals.CacheMisses != 1 {
		t.Fatalf("CacheMisses: want 1, got %d", snap.Totals.CacheMisses)
	}
}

func TestStore_ErrorCount(t *testing.T) {
	now := time.Now()
	s := newStore(fixedClock(now))

	s.Record(MetricEvent{Timestamp: now, Provider: "openai", ErrorType: "5xx"})
	s.Record(MetricEvent{Timestamp: now, Provider: "openai"}) // success

	snap := s.Query(time.Hour)
	if snap.Totals.ErrorCount != 1 {
		t.Fatalf("ErrorCount: want 1, got %d", snap.Totals.ErrorCount)
	}
}

func TestStore_BreakdownByProviderModel(t *testing.T) {
	now := time.Now()
	s := newStore(fixedClock(now))

	s.Record(MetricEvent{Timestamp: now, Provider: "openai", Model: "gpt-4o", TotalTokens: 100})
	s.Record(MetricEvent{Timestamp: now, Provider: "anthropic", Model: "claude-3-5-sonnet", TotalTokens: 200})
	s.Record(MetricEvent{Timestamp: now, Provider: "openai", Model: "gpt-4o", TotalTokens: 50})

	snap := s.Query(time.Hour)
	if len(snap.Breakdowns) != 2 {
		t.Fatalf("expected 2 breakdowns, got %d", len(snap.Breakdowns))
	}

	byKey := make(map[string]BreakdownEntry)
	for _, bd := range snap.Breakdowns {
		byKey[bd.Provider+"/"+bd.Model] = bd
	}

	gpt, ok := byKey["openai/gpt-4o"]
	if !ok {
		t.Fatal("openai/gpt-4o breakdown missing")
	}
	if gpt.TotalTokens != 150 {
		t.Fatalf("openai/gpt-4o TotalTokens: want 150, got %d", gpt.TotalTokens)
	}
	if gpt.RequestCount != 2 {
		t.Fatalf("openai/gpt-4o RequestCount: want 2, got %d", gpt.RequestCount)
	}

	claude, ok := byKey["anthropic/claude-3-5-sonnet"]
	if !ok {
		t.Fatal("anthropic/claude-3-5-sonnet breakdown missing")
	}
	if claude.TotalTokens != 200 {
		t.Fatalf("anthropic/claude-3-5-sonnet TotalTokens: want 200, got %d", claude.TotalTokens)
	}
}

// TestStore_WindowFiltering verifies that events outside the query window are excluded.
func TestStore_WindowFiltering(t *testing.T) {
	base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	// Clock is at base+2m; a 1m window should exclude the event at base.
	s := newStore(fixedClock(base.Add(2 * time.Minute)))

	s.Record(MetricEvent{Timestamp: base, Provider: "openai", TotalTokens: 100})                    // 2 minutes ago — outside 1m window
	s.Record(MetricEvent{Timestamp: base.Add(time.Minute), Provider: "openai", TotalTokens: 200})   // 1 minute ago — inside 1m window

	snap := s.Query(time.Minute)
	if snap.Totals.TotalTokens != 200 {
		t.Fatalf("1m window TotalTokens: want 200, got %d", snap.Totals.TotalTokens)
	}
	if snap.Totals.RequestCount != 1 {
		t.Fatalf("1m window RequestCount: want 1, got %d", snap.Totals.RequestCount)
	}

	// 1h window should see both events.
	snap = s.Query(time.Hour)
	if snap.Totals.TotalTokens != 300 {
		t.Fatalf("1h window TotalTokens: want 300, got %d", snap.Totals.TotalTokens)
	}
}

// TestStore_OldBucketEviction verifies that buckets older than 1 hour are evicted
// during Query and excluded from the result.
func TestStore_OldBucketEviction(t *testing.T) {
	base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	// Record an event at base, then advance clock 70 minutes — outside retention.
	s := newStore(fixedClock(base.Add(70 * time.Minute)))
	s.Record(MetricEvent{Timestamp: base, Provider: "openai", TotalTokens: 999})

	snap := s.Query(time.Hour)
	if snap.Totals.RequestCount != 0 {
		t.Fatalf("expected 0 requests after eviction, got %d", snap.Totals.RequestCount)
	}

	s.mu.Lock()
	remaining := len(s.buckets)
	s.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("expected 0 buckets after eviction, got %d", remaining)
	}
}

// TestStore_PercentileCorrectness inserts 10 events with known latencies [1..10] ms
// and verifies that p50 and p95 match expected values from the ceil formula.
func TestStore_PercentileCorrectness(t *testing.T) {
	now := time.Now()
	s := newStore(fixedClock(now))

	for i := 1; i <= 10; i++ {
		s.Record(MetricEvent{
			Timestamp:         now,
			Provider:          "mock",
			RequestLatencyMs:  float64(i),
			ProviderLatencyMs: float64(i * 2),
		})
	}

	snap := s.Query(time.Hour)

	// p50: ceil(0.5 * 10) - 1 = 4 → sorted[4] = 5
	if snap.Totals.RequestLatencyP50 != 5 {
		t.Fatalf("RequestLatencyP50: want 5, got %f", snap.Totals.RequestLatencyP50)
	}
	// p95: ceil(0.95 * 10) - 1 = 9 → sorted[9] = 10
	if snap.Totals.RequestLatencyP95 != 10 {
		t.Fatalf("RequestLatencyP95: want 10, got %f", snap.Totals.RequestLatencyP95)
	}
	// provider latencies are [2, 4, 6, 8, 10, 12, 14, 16, 18, 20]
	// p50: sorted[4] = 10, p95: sorted[9] = 20
	if snap.Totals.ProviderLatencyP50 != 10 {
		t.Fatalf("ProviderLatencyP50: want 10, got %f", snap.Totals.ProviderLatencyP50)
	}
	if snap.Totals.ProviderLatencyP95 != 20 {
		t.Fatalf("ProviderLatencyP95: want 20, got %f", snap.Totals.ProviderLatencyP95)
	}
}

// TestStore_ConcurrentRecord verifies there are no data races under concurrent writes.
// Run with: go test -race ./internal/metrics/...
func TestStore_ConcurrentRecord(t *testing.T) {
	s := NewStore()
	now := time.Now()

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Record(MetricEvent{Timestamp: now, Provider: "mock", TotalTokens: 1, RequestLatencyMs: 10})
		}()
	}
	wg.Wait()

	snap := s.Query(time.Hour)
	if snap.Totals.RequestCount != 200 {
		t.Fatalf("concurrent RequestCount: want 200, got %d", snap.Totals.RequestCount)
	}
	if snap.Totals.TotalTokens != 200 {
		t.Fatalf("concurrent TotalTokens: want 200, got %d", snap.Totals.TotalTokens)
	}
}
