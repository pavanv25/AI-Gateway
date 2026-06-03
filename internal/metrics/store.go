package metrics

import (
	"math"
	"math/rand"
	"sort"
	"sync"
	"time"
)

const (
	bucketRetention = time.Hour
	reservoirCap    = 1000
)

type pmKey struct {
	Provider string
	Model    string
}

// bucket accumulates metrics for one 1-minute interval.
// reqLatencies and provLatencies are reservoir-sampled (max reservoirCap entries)
// so memory is bounded regardless of request rate.
type bucket struct {
	requestCount     int
	promptTokens     int
	completionTokens int
	totalTokens      int
	cacheHits        int
	cacheMisses      int
	errorCount       int
	costUSD          float64
	reqLatencies     []float64
	provLatencies    []float64
	reqSeen          int // total samples seen, for reservoir sampling
	provSeen         int
}

type bucketSet struct {
	total *bucket
	byPM  map[pmKey]*bucket
}

// Store is a thread-safe in-memory metrics aggregator.
// It accumulates MetricEvents into 1-minute buckets and retains up to 1 hour of history.
//
// A single sync.Mutex guards all state. Avoid replacing with sync.RWMutex without
// care: Query copies latency slices out before sorting, so the lock must be held
// across both the copy and the release — a subtle invariant to preserve.
type Store struct {
	mu      sync.Mutex
	buckets map[int64]*bucketSet // key: UTC minute-truncated Unix timestamp
	now     func() time.Time
}

// NewStore returns a Store backed by the real wall clock.
func NewStore() *Store {
	return newStore(time.Now)
}

// newStore creates a Store with an injected clock for deterministic tests.
func newStore(now func() time.Time) *Store {
	return &Store{buckets: make(map[int64]*bucketSet), now: now}
}

// Record implements Collector. Safe to call concurrently.
func (s *Store) Record(e MetricEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := e.Timestamp.UTC().Truncate(time.Minute).Unix()
	bs, ok := s.buckets[key]
	if !ok {
		bs = &bucketSet{total: &bucket{}, byPM: make(map[pmKey]*bucket)}
		s.buckets[key] = bs
	}
	addToBucket(bs.total, e)

	pm := pmKey{Provider: e.Provider, Model: e.Model}
	b, ok := bs.byPM[pm]
	if !ok {
		b = &bucket{}
		bs.byPM[pm] = b
	}
	addToBucket(b, e)
}

func addToBucket(b *bucket, e MetricEvent) {
	b.requestCount++
	b.promptTokens += e.PromptTokens
	b.completionTokens += e.CompletionTokens
	b.totalTokens += e.TotalTokens
	if e.CacheHit {
		b.cacheHits++
	} else {
		b.cacheMisses++
	}
	if e.ErrorType != "" {
		b.errorCount++
	}
	b.costUSD += e.CostUSD
	reservoirAdd(&b.reqLatencies, &b.reqSeen, e.RequestLatencyMs)
	if e.ProviderLatencyMs > 0 {
		reservoirAdd(&b.provLatencies, &b.provSeen, e.ProviderLatencyMs)
	}
}

func reservoirAdd(samples *[]float64, seen *int, v float64) {
	*seen++
	if len(*samples) < reservoirCap {
		*samples = append(*samples, v)
		return
	}
	if j := rand.Intn(*seen); j < reservoirCap {
		(*samples)[j] = v
	}
}

// Aggregate holds computed metrics over a time window.
type Aggregate struct {
	RequestCount       int
	PromptTokens       int
	CompletionTokens   int
	TotalTokens        int
	CacheHits          int
	CacheMisses        int
	ErrorCount         int
	CostUSD            float64
	RequestLatencyP50  float64
	RequestLatencyP95  float64
	ProviderLatencyP50 float64
	ProviderLatencyP95 float64
}

// BreakdownEntry holds aggregated metrics for a single (provider, model) pair.
// Per-APIKeyHash breakdown is intentionally deferred; it can be added as a
// third pmKey dimension in Step 4 if per-tenant cost visibility is needed.
type BreakdownEntry struct {
	Provider string
	Model    string
	Aggregate
}

// Snapshot is the result of a Query call.
type Snapshot struct {
	Window     time.Duration
	Totals     Aggregate
	Breakdowns []BreakdownEntry
}

// windowAccum collects raw counters and latency samples across multiple buckets
// before computing the final Aggregate (percentiles require all samples).
type windowAccum struct {
	requestCount     int
	promptTokens     int
	completionTokens int
	totalTokens      int
	cacheHits        int
	cacheMisses      int
	errorCount       int
	costUSD          float64
	reqLat           []float64
	provLat          []float64
}

func (a *windowAccum) add(b *bucket) {
	a.requestCount += b.requestCount
	a.promptTokens += b.promptTokens
	a.completionTokens += b.completionTokens
	a.totalTokens += b.totalTokens
	a.cacheHits += b.cacheHits
	a.cacheMisses += b.cacheMisses
	a.errorCount += b.errorCount
	a.costUSD += b.costUSD
	a.reqLat = append(a.reqLat, b.reqLatencies...)
	a.provLat = append(a.provLat, b.provLatencies...)
}

func (a *windowAccum) toAggregate() Aggregate {
	return Aggregate{
		RequestCount:       a.requestCount,
		PromptTokens:       a.promptTokens,
		CompletionTokens:   a.completionTokens,
		TotalTokens:        a.totalTokens,
		CacheHits:          a.cacheHits,
		CacheMisses:        a.cacheMisses,
		ErrorCount:         a.errorCount,
		CostUSD:            a.costUSD,
		RequestLatencyP50:  percentile(a.reqLat, 0.50),
		RequestLatencyP95:  percentile(a.reqLat, 0.95),
		ProviderLatencyP50: percentile(a.provLat, 0.50),
		ProviderLatencyP95: percentile(a.provLat, 0.95),
	}
}

// Query returns aggregated metrics over the given window and evicts buckets
// older than 1 hour. Passing window > 1h returns the same result as window = 1h.
func (s *Store) Query(window time.Duration) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().UTC()
	evictBefore := now.Add(-bucketRetention).Truncate(time.Minute).Unix()
	windowStart := now.Add(-window).Truncate(time.Minute).Unix()

	for k := range s.buckets {
		if k < evictBefore {
			delete(s.buckets, k)
		}
	}

	total := &windowAccum{}
	pmAccums := make(map[pmKey]*windowAccum)

	for k, bs := range s.buckets {
		if k < windowStart {
			continue
		}
		total.add(bs.total)
		for pm, b := range bs.byPM {
			a := pmAccums[pm]
			if a == nil {
				a = &windowAccum{}
				pmAccums[pm] = a
			}
			a.add(b)
		}
	}

	snap := Snapshot{Window: window, Totals: total.toAggregate()}
	for pm, a := range pmAccums {
		snap.Breakdowns = append(snap.Breakdowns, BreakdownEntry{
			Provider:  pm.Provider,
			Model:     pm.Model,
			Aggregate: a.toAggregate(),
		})
	}
	return snap
}

// percentile returns the p-th percentile (0 < p <= 1) of samples.
// Samples are copied and sorted; the original slice is not modified.
// Returns 0 for an empty slice.
func percentile(samples []float64, p float64) float64 {
	n := len(samples)
	if n == 0 {
		return 0
	}
	sorted := make([]float64, n)
	copy(sorted, samples)
	sort.Float64s(sorted)
	idx := int(math.Ceil(p*float64(n))) - 1
	if idx < 0 {
		idx = 0
	}
	return sorted[idx]
}
