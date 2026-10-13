package llm

import "testing"

func TestCost(t *testing.T) {
	p := Prices{InputMicros: 2_500_000, OutputMicros: 10_000_000} // $2.50 / $10 per 1M
	// 1000 in, 500 out = $0.0025 + $0.005 = $0.0075 = 7_500_000 nanos
	if got := Cost(Usage{Input: 1000, Output: 500}, p); got != 7_500_000 {
		t.Fatalf("got %d", got)
	}
	// cache prices default to input price
	if got := Cost(Usage{CacheRead: 1000}, p); got != 2_500_000 {
		t.Fatalf("cache default: %d", got)
	}
	cr := int64(250_000)
	p.CacheReadMicros = &cr
	if got := Cost(Usage{CacheRead: 1000}, p); got != 250_000 {
		t.Fatalf("cache read price: %d", got)
	}
	// $600/M output must not overflow
	if got := Cost(Usage{Output: 1_000_000}, Prices{OutputMicros: 600_000_000}); got != 600_000_000_000 {
		t.Fatalf("big: %d", got)
	}
}

func TestFormatUSD(t *testing.T) {
	if s := FormatUSD(7_500_000); s != "0.0075" {
		t.Fatalf("got %s", s)
	}
}
