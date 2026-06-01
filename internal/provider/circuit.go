package provider

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/pavanv25/ai-gateway/pkg/models"
)

// State is the circuit breaker's 3-state machine value.
type State int

const (
	StateClosed   State = iota // normal operation, failures tracked
	StateOpen                  // short-circuits all calls with ErrCircuitOpen
	StateHalfOpen              // one probe call allowed; outcome resets state
)

// Config holds circuit breaker parameters.
type Config struct {
	// FailureThreshold is the number of consecutive tripping failures (5xx, 429,
	// network errors) required to open the circuit. 0 disables the breaker.
	FailureThreshold int
	// CooldownDuration is how long the circuit stays Open before transitioning
	// to HalfOpen to allow a single probe call.
	CooldownDuration time.Duration
}

// CircuitBreaker wraps a Provider and implements the same interface.
// It tracks consecutive failures per provider in memory and short-circuits
// calls when the circuit is open. Safe for concurrent use.
type CircuitBreaker struct {
	inner Provider
	cfg   Config

	mu            sync.Mutex
	state         State
	failures      int
	openedAt      time.Time
	probeInFlight bool
}

// New wraps p with a circuit breaker governed by cfg.
// If cfg.FailureThreshold == 0, the breaker is disabled and never opens.
func New(p Provider, cfg Config) *CircuitBreaker {
	return &CircuitBreaker{inner: p, cfg: cfg}
}

func (cb *CircuitBreaker) Name() string { return cb.inner.Name() }

func (cb *CircuitBreaker) Chat(ctx context.Context, req *models.ChatRequest) (*models.ChatResponse, error) {
	if err := cb.beforeCall(); err != nil {
		return nil, err
	}
	resp, err := cb.inner.Chat(ctx, req)
	if err != nil {
		cb.onFailure(err)
		return nil, err
	}
	cb.onSuccess()
	return resp, nil
}

func (cb *CircuitBreaker) ChatStream(ctx context.Context, req *models.ChatRequest) (<-chan models.StreamEvent, error) {
	if err := cb.beforeCall(); err != nil {
		return nil, err
	}
	ch, err := cb.inner.ChatStream(ctx, req)
	if err != nil {
		cb.onFailure(err)
		return nil, err
	}
	cb.onSuccess()
	return ch, nil
}

// beforeCall checks the current state and either allows the call or returns
// ErrCircuitOpen. For HalfOpen, it ensures only one probe proceeds at a time.
func (cb *CircuitBreaker) beforeCall() error {
	if cb.cfg.FailureThreshold == 0 {
		return nil
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return nil
	case StateOpen:
		if time.Since(cb.openedAt) >= cb.cfg.CooldownDuration {
			cb.state = StateHalfOpen
			cb.probeInFlight = true
			return nil
		}
		return ErrCircuitOpen
	case StateHalfOpen:
		if cb.probeInFlight {
			return ErrCircuitOpen
		}
		cb.probeInFlight = true
		return nil
	}
	return nil
}

func (cb *CircuitBreaker) onSuccess() {
	if cb.cfg.FailureThreshold == 0 {
		return
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures = 0
	cb.probeInFlight = false
	cb.state = StateClosed
}

func (cb *CircuitBreaker) onFailure(err error) {
	if cb.cfg.FailureThreshold == 0 || !isBreakerTrippingError(err) {
		return
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state == StateHalfOpen {
		cb.state = StateOpen
		cb.openedAt = time.Now()
		cb.probeInFlight = false
		return
	}
	cb.failures++
	if cb.failures >= cb.cfg.FailureThreshold {
		cb.state = StateOpen
		cb.openedAt = time.Now()
	}
}

// isBreakerTrippingError reports whether err should count toward opening the
// circuit. Context cancellation is excluded; 429, 5xx, and network errors count.
func isBreakerTrippingError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.StatusCode == 429 || pe.StatusCode >= 500
	}
	return true
}
