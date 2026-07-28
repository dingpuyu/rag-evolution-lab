package cost

import "sync"

type Estimate struct {
	InputUSD  float64 `json:"input_usd"`
	OutputUSD float64 `json:"output_usd"`
	TotalUSD  float64 `json:"total_usd"`
}

// Calculator keeps pricing outside provider adapters. Rates are USD per one
// million tokens and can be changed without changing trace contracts.
type Calculator struct {
	mu               sync.RWMutex
	InputPerMillion  float64
	OutputPerMillion float64
}

func (calculator *Calculator) Estimate(inputTokens, outputTokens int) Estimate {
	if calculator == nil {
		return Estimate{}
	}
	calculator.mu.RLock()
	inputRate, outputRate := calculator.InputPerMillion, calculator.OutputPerMillion
	calculator.mu.RUnlock()
	input := float64(inputTokens) / 1_000_000 * inputRate
	output := float64(outputTokens) / 1_000_000 * outputRate
	return Estimate{InputUSD: input, OutputUSD: output, TotalUSD: input + output}
}
