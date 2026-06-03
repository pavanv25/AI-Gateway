package metrics

import (
	"log"
	"sync"
)

type modelPrice struct {
	PromptPerMillionTokens     float64
	CompletionPerMillionTokens float64
}

var pricingTable = map[string]modelPrice{
	"openai/gpt-4o":                       {2.50, 10.00},
	"openai/gpt-4o-mini":                  {0.15, 0.60},
	"anthropic/claude-sonnet-4-6":         {3.00, 15.00},
	"anthropic/claude-opus-4-7":           {15.00, 75.00},
	"anthropic/claude-haiku-4-5-20251001": {0.80, 4.00},
}

// unknownModelsLogged tracks (provider/model) pairs that have already triggered
// a pricing warning, so each pair logs at most once per process.
var unknownModelsLogged sync.Map

// EstimateCost returns the estimated USD cost for a single request.
// Prices are hardcoded per the table above (per-million-token rates).
// Returns 0 for the mock provider (no real cost) and for any model not in the
// table, logging a one-time warning in the latter case.
func EstimateCost(provider, model string, promptTokens, completionTokens int) float64 {
	if provider == "mock" || provider == "cache" {
		return 0
	}
	key := provider + "/" + model
	price, ok := pricingTable[key]
	if !ok {
		if _, alreadyLogged := unknownModelsLogged.LoadOrStore(key, struct{}{}); !alreadyLogged {
			log.Printf("metrics: no pricing data for %q — CostUSD will be 0", key)
		}
		return 0
	}
	return (float64(promptTokens)*price.PromptPerMillionTokens +
		float64(completionTokens)*price.CompletionPerMillionTokens) / 1_000_000
}
