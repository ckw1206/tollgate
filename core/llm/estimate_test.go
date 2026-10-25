package llm

import "testing"

func TestEstimateOutputTokens_PrefersMaxTokens(t *testing.T) {
	m := &Model{ContextWindow: 128000}
	mt := 500
	got := EstimateOutputTokens(&ChatRequest{MaxTokens: &mt}, m)
	if got != 500 {
		t.Fatalf("max_tokens should win, got %d", got)
	}
}

func TestEstimateOutputTokens_DefaultsWithoutMaxTokens(t *testing.T) {
	// The context window must NOT be used as an output estimate: at typical
	// prices that reserves ~$1.28 on a 128k model, denying nearly every
	// request. See defaultOutputTokens.
	m := &Model{ContextWindow: 128000}
	if got := EstimateOutputTokens(&ChatRequest{}, m); got != defaultOutputTokens {
		t.Fatalf("expected default %d, got %d", defaultOutputTokens, got)
	}
}

func TestEstimateOutputTokens_ClampsToTinyContext(t *testing.T) {
	// A model that cannot physically emit the default must not reserve it.
	m := &Model{ContextWindow: 64}
	if got := EstimateOutputTokens(&ChatRequest{}, m); got != 64 {
		t.Fatalf("expected clamp to context_window 64, got %d", got)
	}
}

func TestEstimateOutputTokens_ZeroMaxTokensFallsBack(t *testing.T) {
	zero := 0
	if got := EstimateOutputTokens(&ChatRequest{MaxTokens: &zero}, &Model{}); got != defaultOutputTokens {
		t.Fatalf("explicit 0 is not a bound; expected %d, got %d", defaultOutputTokens, got)
	}
}

func TestEstimateInputTokens_GrowsWithContent(t *testing.T) {
	small := &ChatRequest{Messages: []Message{{Parts: []Part{{Text: "hi"}}}}}
	large := &ChatRequest{Messages: []Message{{Parts: []Part{{Text: string(make([]byte, 4000))}}}}}
	if EstimateInputTokens(large) <= EstimateInputTokens(small) {
		t.Fatalf("larger prompt must estimate more: small=%d large=%d",
			EstimateInputTokens(small), EstimateInputTokens(large))
	}
}

func TestEstimate_IntegersOnlyAndPositive(t *testing.T) {
	m := &Model{Prices: Prices{InputMicros: 2_500_000, OutputMicros: 10_000_000}}
	req := &ChatRequest{Messages: []Message{{Parts: []Part{{Text: "hello"}}}}}
	cost, tokens := Estimate(req, m)
	if cost <= 0 || tokens <= 0 {
		t.Fatalf("expected positive estimate, got cost=%d tokens=%d", cost, tokens)
	}
	// Money is an exact integer count of nanos. Asserting that a value already
	// typed int64 "is an int64" can never fail, so instead pin that the estimate
	// matches the integer cost formula with no rounding loss:
	// cost = (in*inMicros + out*outMicros) / 1000.
	in := EstimateInputTokens(req)
	out := EstimateOutputTokens(req, m)
	if want := (in*2_500_000 + out*10_000_000) / 1000; cost != want {
		t.Fatalf("estimate must follow the integer cost formula: got %d, want %d", cost, want)
	}
	if tokens != in+out {
		t.Fatalf("tokens must be input+output: got %d, want %d", tokens, in+out)
	}
}

func TestEstimate_NilModelIsZero(t *testing.T) {
	if c, tok := Estimate(&ChatRequest{}, nil); c != 0 || tok != 0 {
		t.Fatalf("nil model must estimate zero, got %d/%d", c, tok)
	}
}

func TestEstimateEmbedding_NoOutputReserved(t *testing.T) {
	m := &Model{Prices: Prices{InputMicros: 2_500_000, OutputMicros: 10_000_000}, ContextWindow: 128000}
	cost, tokens := EstimateEmbedding(4000, m)
	if cost <= 0 || tokens <= 0 {
		t.Fatalf("expected positive embedding estimate, got %d/%d", cost, tokens)
	}
	// Output price is 4x input; reserving a completion would cost far more.
	// ~1000 input tokens at 2.5M micros = 2.5M nanos.
	if cost > 10_000_000 {
		t.Fatalf("embedding reserved against an output it cannot produce: %d nanos", cost)
	}
}
