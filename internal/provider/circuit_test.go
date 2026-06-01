package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/pavanv25/ai-gateway/pkg/models"
)

// controlled is a test stub that returns a sequence of errors (or nil for success).
// Once the sequence is exhausted, the last element is repeated.
type controlled struct {
	results []error
	mu      sync.Mutex
	calls   int
}

func (p *controlled) Name() string { return "controlled" }

func (p *controlled) Chat(_ context.Context, _ *models.ChatRequest) (*models.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	err := p.next()
	if err != nil {
		return nil, err
	}
	return &models.ChatResponse{}, nil
}

func (p *controlled) ChatStream(_ context.Context, _ *models.ChatRequest) (<-chan models.StreamEvent, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	err := p.next()
	if err != nil {
		return nil, err
	}
	ch := make(chan models.StreamEvent, 1)
	ch <- models.StreamEvent{Done: true}
	close(ch)
	return ch, nil
}

func (p *controlled) next() error {
	idx := p.calls
	if idx >= len(p.results) {
		idx = len(p.results) - 1
	}
	p.calls++
	return p.results[idx]
}

func (p *controlled) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func serverErr(code int) error {
	return &ProviderError{StatusCode: code, Cause: fmt.Errorf("http %d", code)}
}

func callChat(cb *CircuitBreaker) error {
	_, err := cb.Chat(context.Background(), &models.ChatRequest{})
	return err
}

// --- State machine: Closed → Open ---

func TestCircuit_ClosedToOpen(t *testing.T) {
	inner := &controlled{results: []error{serverErr(500)}}
	cb := New(inner, Config{FailureThreshold: 3, CooldownDuration: time.Minute})

	// First two failures: still Closed
	for i := 0; i < 2; i++ {
		err := callChat(cb)
		if errors.Is(err, ErrCircuitOpen) {
			t.Fatalf("call %d: got ErrCircuitOpen before threshold", i+1)
		}
	}
	// Third failure: circuit opens
	_ = callChat(cb)
	if err := callChat(cb); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen after threshold, got %v", err)
	}
}

func TestCircuit_429Counts(t *testing.T) {
	inner := &controlled{results: []error{serverErr(429)}}
	cb := New(inner, Config{FailureThreshold: 2, CooldownDuration: time.Minute})

	_ = callChat(cb)
	_ = callChat(cb)
	if err := callChat(cb); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen after 429 threshold, got %v", err)
	}
}

func TestCircuit_ContextCancelDoesNotCount(t *testing.T) {
	inner := &controlled{results: []error{context.Canceled}}
	cb := New(inner, Config{FailureThreshold: 2, CooldownDuration: time.Minute})

	for i := 0; i < 5; i++ {
		_ = callChat(cb)
	}
	// Circuit must still be Closed — context cancel doesn't trip it
	inner.results = []error{nil}
	if err := callChat(cb); err != nil {
		t.Fatalf("expected success after context cancels, got %v", err)
	}
}

// --- Open state ---

func TestCircuit_OpenReturnsSentinel(t *testing.T) {
	inner := &controlled{results: []error{serverErr(500)}}
	cb := New(inner, Config{FailureThreshold: 1, CooldownDuration: time.Minute})

	_ = callChat(cb) // trips
	err := callChat(cb)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuit_OpenSkipsInnerProvider(t *testing.T) {
	inner := &controlled{results: []error{serverErr(500)}}
	cb := New(inner, Config{FailureThreshold: 1, CooldownDuration: time.Minute})

	_ = callChat(cb) // trips; inner.calls == 1
	_ = callChat(cb) // open; inner.calls should stay 1
	_ = callChat(cb)

	if n := inner.callCount(); n != 1 {
		t.Fatalf("inner called %d times while open, want 1", n)
	}
}

// --- Open → HalfOpen → Closed ---

func TestCircuit_OpenToHalfOpenAfterCooldown(t *testing.T) {
	inner := &controlled{results: []error{serverErr(500), nil}}
	cb := New(inner, Config{FailureThreshold: 1, CooldownDuration: time.Millisecond})

	_ = callChat(cb) // trips
	time.Sleep(5 * time.Millisecond)

	err := callChat(cb) // probe
	if errors.Is(err, ErrCircuitOpen) {
		t.Fatal("expected probe to reach inner provider after cooldown")
	}
}

func TestCircuit_HalfOpenSuccessClosesCircuit(t *testing.T) {
	inner := &controlled{results: []error{serverErr(500), nil, nil}}
	cb := New(inner, Config{FailureThreshold: 1, CooldownDuration: time.Millisecond})

	_ = callChat(cb) // trips
	time.Sleep(5 * time.Millisecond)
	_ = callChat(cb) // probe succeeds → Closed

	if err := callChat(cb); err != nil {
		t.Fatalf("expected success after circuit closed, got %v", err)
	}
}

func TestCircuit_HalfOpenFailureReopens(t *testing.T) {
	inner := &controlled{results: []error{serverErr(500)}}
	cb := New(inner, Config{FailureThreshold: 1, CooldownDuration: time.Millisecond})

	_ = callChat(cb) // trips
	time.Sleep(5 * time.Millisecond)
	_ = callChat(cb) // probe fails → reopens

	if err := callChat(cb); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen after failed probe, got %v", err)
	}
}

// --- HalfOpen concurrency: only one probe ---

func TestCircuit_HalfOpenOnlyOneProbe(t *testing.T) {
	// Use a channel to hold the probe call inside the inner provider
	// until the second goroutine has already attempted its call.
	gate := make(chan struct{})
	probeCount := 0
	var pmu sync.Mutex

	slow := &slowProvider{gate: gate, onCall: func() {
		pmu.Lock()
		probeCount++
		pmu.Unlock()
	}}

	cb := New(slow, Config{FailureThreshold: 1, CooldownDuration: time.Millisecond})

	// Trip the circuit with one failure.
	slow.failNext = true
	_ = callChat(cb)
	time.Sleep(5 * time.Millisecond)

	var wg sync.WaitGroup
	errs := make([]error, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = callChat(cb)
		}(i)
	}

	// Let both goroutines reach beforeCall, then release the gate.
	time.Sleep(5 * time.Millisecond)
	close(gate)
	wg.Wait()

	// Exactly one goroutine must have reached the inner provider.
	pmu.Lock()
	n := probeCount
	pmu.Unlock()
	if n != 1 {
		t.Fatalf("expected exactly 1 probe, got %d", n)
	}

	// Exactly one goroutine must have gotten ErrCircuitOpen.
	openCount := 0
	for _, err := range errs {
		if errors.Is(err, ErrCircuitOpen) {
			openCount++
		}
	}
	if openCount != 1 {
		t.Fatalf("expected exactly 1 ErrCircuitOpen among 2 concurrent calls, got %d", openCount)
	}
}

// slowProvider blocks Chat until gate is closed, then returns success.
// If failNext is true, the first call returns a 500 instead.
type slowProvider struct {
	gate     chan struct{}
	onCall   func()
	failNext bool
	mu       sync.Mutex
}

func (p *slowProvider) Name() string { return "slow" }

func (p *slowProvider) Chat(_ context.Context, _ *models.ChatRequest) (*models.ChatResponse, error) {
	p.mu.Lock()
	fail := p.failNext
	p.failNext = false
	p.mu.Unlock()

	if fail {
		return nil, serverErr(500)
	}
	if p.onCall != nil {
		p.onCall()
	}
	<-p.gate
	return &models.ChatResponse{}, nil
}

func (p *slowProvider) ChatStream(_ context.Context, _ *models.ChatRequest) (<-chan models.StreamEvent, error) {
	return nil, errors.New("not used")
}

// --- Disabled (threshold == 0) ---

func TestCircuit_ThresholdZeroDisabled(t *testing.T) {
	inner := &controlled{results: []error{serverErr(500)}}
	cb := New(inner, Config{FailureThreshold: 0, CooldownDuration: time.Minute})

	for i := 0; i < 10; i++ {
		err := callChat(cb)
		if errors.Is(err, ErrCircuitOpen) {
			t.Fatalf("call %d: got ErrCircuitOpen with threshold=0", i+1)
		}
	}
}

// --- IsRetriable ---

func TestIsRetriable_ErrCircuitOpen(t *testing.T) {
	if !IsRetriable(ErrCircuitOpen) {
		t.Fatal("IsRetriable(ErrCircuitOpen) should return true")
	}
}
