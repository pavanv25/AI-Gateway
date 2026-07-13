package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	defaultMaxTokens = 1000
	windowMS         = 60_000 // sliding window width in milliseconds
	ttlSeconds       = 120    // sorted set TTL — 2× the window, as a GC backstop
)

// ErrLimitExceeded is returned by Reserve when the rolling window is full.
var ErrLimitExceeded = errors.New("rate limit exceeded")

// Config holds the single global token-per-minute ceiling.
type Config struct {
	TPMLimit int
}

// Limiter enforces a sliding-window token-per-minute cap per API key using a
// Redis sorted set. It is goroutine-safe; all mutable state lives in Redis.
type Limiter struct {
	rdb     *redis.Client
	cfg     Config
	reserve *redis.Script
	commit  *redis.Script
	peek    *redis.Script
}

// New creates a Limiter. rdb must be an already-configured client;
// cfg.TPMLimit must be > 0.
func New(rdb *redis.Client, cfg Config) *Limiter {
	return &Limiter{
		rdb:     rdb,
		cfg:     cfg,
		reserve: redis.NewScript(reserveLua),
		commit:  redis.NewScript(commitLua),
		peek:    redis.NewScript(peekLua),
	}
}

// Reserve atomically checks capacity and adds a reservation to the window.
// Returns a reservation token (the sorted-set member string) that must be
// passed to Commit once actual usage is known.
// If maxTokens <= 0, defaultMaxTokens (1000) is used.
func (l *Limiter) Reserve(ctx context.Context, apiKey string, maxTokens int) (string, error) {
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	nowMs    := time.Now().UnixMilli()
	winStart := nowMs - windowMS
	id       := strconv.FormatInt(time.Now().UnixNano(), 36)
	member   := id + ":" + strconv.Itoa(maxTokens)

	_, err := l.reserve.Run(ctx, l.rdb,
		[]string{"rl:" + apiKey},
		winStart, nowMs, maxTokens, l.cfg.TPMLimit, member, ttlSeconds,
	).Result()
	if err != nil {
		if strings.Contains(err.Error(), "LIMIT_EXCEEDED") {
			return "", ErrLimitExceeded
		}
		return "", fmt.Errorf("reserve: %w", err)
	}
	return member, nil
}

// Commit replaces the reservation with the actual token count, correcting the
// window usage. Negative actualTokens are clamped to 0.
func (l *Limiter) Commit(ctx context.Context, apiKey, reservationToken string, actualTokens int) error {
	if actualTokens < 0 {
		actualTokens = 0
	}
	_, err := l.commit.Run(ctx, l.rdb,
		[]string{"rl:" + apiKey},
		reservationToken, time.Now().UnixMilli(), actualTokens, ttlSeconds,
	).Result()
	if err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Usage returns the current token usage in the sliding window for apiKey,
// alongside the configured limit. It prunes expired entries as a side
// effect but never adds a reservation — safe to poll repeatedly.
func (l *Limiter) Usage(ctx context.Context, apiKey string) (used, limit int, err error) {
	nowMs := time.Now().UnixMilli()
	winStart := nowMs - windowMS

	res, err := l.peek.Run(ctx, l.rdb,
		[]string{"rl:" + apiKey},
		winStart,
	).Int64()
	if err != nil {
		return 0, l.cfg.TPMLimit, fmt.Errorf("usage: %w", err)
	}
	return int(res), l.cfg.TPMLimit, nil
}

// reserveLua atomically prunes expired entries, checks the token sum, and
// adds a new reservation — all in a single Redis round trip.
const reserveLua = `
local key       = KEYS[1]
local win_start = tonumber(ARGV[1])
local now_ms    = tonumber(ARGV[2])
local tokens    = tonumber(ARGV[3])
local limit     = tonumber(ARGV[4])
local member    = ARGV[5]
local ttl       = tonumber(ARGV[6])

redis.call('ZREMRANGEBYSCORE', key, '-inf', win_start)

local entries = redis.call('ZRANGE', key, 0, -1)
local used = 0
for _, m in ipairs(entries) do
    local colon = string.find(m, ':', 1, true)
    if colon then
        local t = tonumber(string.sub(m, colon + 1))
        if t then used = used + t end
    end
end

if used + tokens > limit then
    return redis.error_reply('LIMIT_EXCEEDED')
end

redis.call('ZADD', key, now_ms, member)
redis.call('EXPIRE', key, ttl)
return used + tokens
`

// commitLua removes the reservation member and replaces it with the actual
// token count under the same ID.
const commitLua = `
local key     = KEYS[1]
local old_mbr = ARGV[1]
local now_ms  = tonumber(ARGV[2])
local actual  = tonumber(ARGV[3])
local ttl     = tonumber(ARGV[4])

redis.call('ZREM', key, old_mbr)

local colon = string.find(old_mbr, ':', 1, true)
local id     = string.sub(old_mbr, 1, colon - 1)
local new_mbr = id .. ':' .. tostring(actual)

redis.call('ZADD', key, now_ms, new_mbr)
redis.call('EXPIRE', key, ttl)
return actual
`

// peekLua prunes expired entries and sums the remaining token reservations
// without adding a new one — a read-only usage check.
const peekLua = `
local key       = KEYS[1]
local win_start = tonumber(ARGV[1])

redis.call('ZREMRANGEBYSCORE', key, '-inf', win_start)

local entries = redis.call('ZRANGE', key, 0, -1)
local used = 0
for _, m in ipairs(entries) do
    local colon = string.find(m, ':', 1, true)
    if colon then
        local t = tonumber(string.sub(m, colon + 1))
        if t then used = used + t end
    end
end

return used
`
