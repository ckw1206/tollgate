package llm

// Estimating the cost and token footprint of a request before it runs (#23, #28).
//
// The estimate exists only to reserve against a cap while a request is in
// flight. It is replaced by exact usage in finish(), so it should approximate
// what the request can plausibly consume. It is explicitly NOT an upper bound:
// neither the input heuristic nor the default output is worst-case (see
// EstimateInputTokens and defaultOutputTokens). Admission is best-effort — a
// request can overrun its reservation, bounded by the model's context window,
// and the overshoot is realized rather than refused because finish() records
// the real cost. Spend accounting itself is always exact.

// defaultOutputTokens is used when a request sets no max_tokens: the client has
// made no commitment about output length, so the reservation assumes a typical
// completion rather than the worst case.
//
// This is deliberately NOT the model's context_window. Reserving the whole
// context as output costs ~$1.28 on a 128k model at typical prices, which
// denies almost every request against a realistic cap — an over-reservation
// that rejects legitimate traffic is its own outage. The context window is a
// hard ceiling on what a model CAN emit, not a statement about what a request
// WILL emit.
//
// The cost of that choice is real and worth stating plainly: a request that
// omits max_tokens and then generates a long completion can exceed its
// reservation by a large factor — 32x for a 32k-token completion against this
// default. That overrun is not refused; it is recorded at its true cost. So for
// requests without max_tokens the cap is an admission heuristic, not a hard
// ceiling. Callers who need a hard guarantee set max_tokens, which is exactly
// the signal the reservation respects.
const defaultOutputTokens = 1024

// EstimateOutputTokens is the output bound reserved for a request: the client's
// max_tokens when set, else defaultOutputTokens.
//
// A client that sets max_tokens gets an exact reservation. That is the
// intended way to make the cap bite precisely.
func EstimateOutputTokens(req *ChatRequest, m *Model) int64 {
	if req != nil && req.MaxTokens != nil && *req.MaxTokens > 0 {
		return int64(*req.MaxTokens)
	}
	// A model whose whole context is smaller than the default cannot emit the
	// default; clamping keeps the estimate physically achievable.
	if m != nil && m.ContextWindow > 0 && m.ContextWindow < defaultOutputTokens {
		return m.ContextWindow
	}
	return defaultOutputTokens
}

// EstimateInputTokens approximates the prompt size without a tokenizer, which
// the gateway does not have (providers tokenize upstream, per provider).
//
// ~4 bytes per token is the standard English heuristic. It is deliberately NOT
// an upper bound: CJK and other non-Latin text tokenizes at roughly 1-2 bytes
// per token, so this undercounts such prompts by 2-4x. Inflating it to cover
// that would overcount every English request instead, denying traffic that
// fits. The reservation is therefore advisory for input size, and a request
// that omits max_tokens can exceed its reservation on both terms (see
// defaultOutputTokens). Admission is best-effort against a cap, not a hard
// guarantee; the committed spend in spend_nanos is always exact.
func EstimateInputTokens(req *ChatRequest) int64 {
	if req == nil {
		return 0
	}
	var bytes int64
	for _, msg := range req.Messages {
		for _, part := range msg.Parts {
			bytes += int64(len(part.Text))
		}
		// Per-message framing overhead (role, separators) that carries no text.
		bytes += 4
	}
	return bytes/4 + 1
}

// Estimate is the reservation to take for a chat request: cost in nano-USD
// (integers throughout, per CLAUDE.md) and total tokens for TPM.
func Estimate(req *ChatRequest, m *Model) (costNanos, tokens int64) {
	if m == nil {
		return 0, 0
	}
	in := EstimateInputTokens(req)
	out := EstimateOutputTokens(req, m)
	u := Usage{Input: in, Output: out}
	return Cost(u, m.Prices), u.Tokens()
}

// EstimateEmbedding is the reservation for an embeddings request: embeddings
// take no max_tokens and return no completion, so only the input is reserved.
// Reserving an output bound here would refuse requests that can never cost it.
func EstimateEmbedding(bodyLen int, m *Model) (costNanos, tokens int64) {
	if m == nil {
		return 0, 0
	}
	u := Usage{Input: int64(bodyLen)/4 + 1}
	return Cost(u, m.Prices), u.Tokens()
}
