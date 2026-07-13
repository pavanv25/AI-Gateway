export interface Aggregate {
  RequestCount: number
  PromptTokens: number
  CompletionTokens: number
  TotalTokens: number
  CacheHits: number
  CacheMisses: number
  ErrorCount: number
  CostUSD: number
  RequestLatencyP50: number
  RequestLatencyP95: number
  ProviderLatencyP50: number
  ProviderLatencyP95: number
}

export interface BreakdownEntry extends Aggregate {
  Provider: string
  Model: string
}

export interface KeyBreakdownEntry extends Aggregate {
  APIKeyHash: string // truncate for display; "other" is a rollup of keys beyond the top N
}

export interface RateLimitStatus {
  Used: number
  Limit: number
}

export interface CircuitStatus {
  Provider: string
  State: string // "closed" | "open" | "half_open" | "n/a" (breakers disabled)
}

export interface Snapshot {
  Window: number // nanoseconds — treat as opaque; use queryWindow state for display
  Totals: Aggregate
  Breakdowns: BreakdownEntry[] | null // Go nil slice → JSON null when no events
  KeyBreakdowns: KeyBreakdownEntry[] | null
  RateLimit: RateLimitStatus
  CircuitBreakers: CircuitStatus[] | null
}

export interface MetricEvent {
  Timestamp: string // ISO 8601
  Provider: string
  Model: string
  APIKeyHash: string
  PromptTokens: number
  CompletionTokens: number
  TotalTokens: number
  CacheHit: boolean
  Stream: boolean
  RequestLatencyMs: number
  ProviderLatencyMs: number
  CacheLatencyMs: number
  CostUSD: number
  ErrorType: string
  FallbackAttempts: number
}

export interface RateBucket {
  minute: string // HH:MM
  requests: number
  errors: number
}
