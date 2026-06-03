package metrics

import (
	"testing"
)

func TestEstimateCost_KnownModel(t *testing.T) {
	// gpt-4o: $2.50/1M prompt, $10.00/1M completion
	// 1000 prompt + 500 completion = (1000*2.50 + 500*10.00) / 1_000_000
	//                              = (2500 + 5000) / 1_000_000 = 0.0075
	got := EstimateCost("openai", "gpt-4o", 1000, 500)
	want := 0.0075
	if got != want {
		t.Fatalf("EstimateCost(openai, gpt-4o, 1000, 500): want %f, got %f", want, got)
	}
}

func TestEstimateCost_PromptOnlyTokens(t *testing.T) {
	// claude-sonnet-4-6: $3.00/1M prompt, $15.00/1M completion
	// 1000 prompt + 0 completion = 1000 * 3.00 / 1_000_000 = 0.003
	got := EstimateCost("anthropic", "claude-sonnet-4-6", 1000, 0)
	want := 0.003
	if got != want {
		t.Fatalf("EstimateCost prompt-only: want %f, got %f", want, got)
	}
}

func TestEstimateCost_CompletionOnlyTokens(t *testing.T) {
	// claude-sonnet-4-6: $3.00/1M prompt, $15.00/1M completion
	// 0 prompt + 1000 completion = 1000 * 15.00 / 1_000_000 = 0.015
	got := EstimateCost("anthropic", "claude-sonnet-4-6", 0, 1000)
	want := 0.015
	if got != want {
		t.Fatalf("EstimateCost completion-only: want %f, got %f", want, got)
	}
}

func TestEstimateCost_ZeroTokens(t *testing.T) {
	got := EstimateCost("openai", "gpt-4o", 0, 0)
	if got != 0 {
		t.Fatalf("EstimateCost zero tokens: want 0, got %f", got)
	}
}

func TestEstimateCost_MockProviderIsZero(t *testing.T) {
	got := EstimateCost("mock", "any-model", 1000, 1000)
	if got != 0 {
		t.Fatalf("EstimateCost mock provider: want 0, got %f", got)
	}
}

func TestEstimateCost_CacheProviderIsZero(t *testing.T) {
	got := EstimateCost("cache", "gpt-4o", 1000, 500)
	if got != 0 {
		t.Fatalf("EstimateCost cache provider: want 0, got %f", got)
	}
}

func TestEstimateCost_UnknownModelReturnsZero(t *testing.T) {
	// Use a unique key so it does not collide with other tests.
	got := EstimateCost("openai", "unknown-model-test-zero", 1000, 500)
	if got != 0 {
		t.Fatalf("EstimateCost unknown model: want 0, got %f", got)
	}
}

func TestEstimateCost_UnknownModelWarnsOnce(t *testing.T) {
	// Use a unique key distinct from other test cases.
	model := "unknown-model-test-warn-once"
	EstimateCost("openai", model, 100, 100)
	EstimateCost("openai", model, 100, 100)

	key := "openai/" + model
	_, stored := unknownModelsLogged.Load(key)
	if !stored {
		t.Fatal("expected unknownModelsLogged to contain the key after first call")
	}
	// A second LoadOrStore on the same key must report alreadyLogged=true,
	// confirming the warning would not fire again.
	_, alreadyLogged := unknownModelsLogged.LoadOrStore(key, struct{}{})
	if !alreadyLogged {
		t.Fatal("expected second call to report key already logged")
	}
}

func TestEstimateCost_AllTableModels(t *testing.T) {
	// Smoke-test every entry in the pricing table to catch typos in keys.
	cases := []struct {
		provider, model string
	}{
		{"openai", "gpt-4o"},
		{"openai", "gpt-4o-mini"},
		{"anthropic", "claude-sonnet-4-6"},
		{"anthropic", "claude-opus-4-7"},
		{"anthropic", "claude-haiku-4-5-20251001"},
	}
	for _, c := range cases {
		got := EstimateCost(c.provider, c.model, 1000, 1000)
		if got <= 0 {
			t.Errorf("EstimateCost(%q, %q, 1000, 1000) = %f; want > 0", c.provider, c.model, got)
		}
	}
}
