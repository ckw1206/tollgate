package llm

import "strconv"

// Cost returns nano-USD. Prices are micro-USD per 1M tokens, so
// tokens * micros / 1e6 = micro-USD, and * 1000 = nano-USD → tokens * micros / 1000.
func Cost(u Usage, p Prices) int64 {
	cr, cw := p.InputMicros, p.InputMicros
	if p.CacheReadMicros != nil {
		cr = *p.CacheReadMicros
	}
	if p.CacheWriteMicros != nil {
		cw = *p.CacheWriteMicros
	}
	return (u.Input*p.InputMicros + u.Output*p.OutputMicros + u.CacheRead*cr + u.CacheWrite*cw) / 1000
}

func FormatUSD(nanos int64) string {
	return strconv.FormatFloat(float64(nanos)/1e9, 'f', -1, 64)
}
