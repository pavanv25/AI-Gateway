package metrics

import "time"

// MetricEvent captures all observable data for a single gateway request.
type MetricEvent struct {
	Timestamp         time.Time
	Provider          string
	Model             string
	APIKeyHash        string
	PromptTokens      int
	CompletionTokens  int
	TotalTokens       int
	CacheHit          bool
	Stream            bool
	RequestLatencyMs  float64
	ProviderLatencyMs float64 // 0 on cache hits
	CacheLatencyMs    float64 // 0 when cache is disabled
	CostUSD           float64 // populated in Step 3
	ErrorType         string  // empty on success
	FallbackAttempts  int     // entries tried before the successful (or final failed) attempt
}

// Collector receives MetricEvents emitted by the gateway handler.
type Collector interface {
	Record(MetricEvent)
}

// NoopCollector discards all events. Used as a default until a real store is wired in.
type NoopCollector struct{}

func (NoopCollector) Record(MetricEvent) {}
