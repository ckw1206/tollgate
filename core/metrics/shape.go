package metrics

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Shape validation for the /metrics document.
//
// This exists because the obvious leak test is VACUOUS. Asserting that the
// response does not contain a test's own fixture key or user ID only proves the
// handler does not echo the strings the test happened to pick; a real leak —
// a new series keyed by scope ID, or an extra field carrying a token — sails
// straight through it. (Demonstrated: adding a `leaked_scope` key holding a
// hardcoded UUID to Report() left both earlier tests green.)
//
// The guarantee that actually matters is structural, and it is checkable: the
// document may contain ONLY the keys and shapes enumerated below, and no scalar
// anywhere may look like an identifier, hash, or token. ValidateShape encodes
// that as an allow-list walk, so a new field is a test failure until someone
// deliberately adds it here — which is the review conversation where the
// question "should this be published?" gets asked.
//
// It is NOT part of serving the document: nothing in Report or the handlers
// calls it. It is exported for the two endpoint test suites (core/api and
// console/api), which cannot reach unexported helpers across packages.

// topLevelKeys is the complete set of keys Report may return at the top level.
var topLevelKeys = map[string]bool{
	"NOTE":              true,
	"uptime_seconds":    true,
	"bucket_edges_ms":   true,
	"ops":               true,
	"admit_calls_total": true,
	"auth":              true,
}

// opKeys is the complete set of keys each ops[<op>] entry may carry.
var opKeys = map[string]bool{
	"count":        true,
	"sum_ms":       true,
	"avg_ms":       true,
	"p50_ms":       true,
	"p90_ms":       true,
	"p99_ms":       true,
	"p99_is_floor": true,
	"buckets":      true,
}

// authKeys is the complete set of keys the auth block may carry.
var authKeys = map[string]bool{
	"queries_total":  true,
	"qps_60s":        true,
	"window_seconds": true,
	"trigger_p99_ms": true,
	"trigger_qps":    true,
	"trigger":        true,
	"per_op_qps_60s": true,
}

// maxPlausibleNumeric is the ceiling above which a numeric field in this
// document is treated as suspicious rather than as a counter. Numeric fields are
// deliberately NOT matched against identifierLike — matching digit runs would
// false-positive on legitimate bucket counts — so this is the backstop that
// stops a leak encoded as a number from passing unexamined. It is set loose
// (~31.7 years of milliseconds, and far more requests than one process can
// serve) so it can never fire on real traffic; it exists to catch the
// deliberate case, not to validate counters.
const maxPlausibleNumeric = 1e12

// identifierLike matches anything that could be a secret or a tenant
// identifier: UUIDs (with or without dashes), hex strings long enough to be a
// hash, tg- prefixed API keys, and JWT-shaped triples. The point is not to be
// exhaustive about token formats — it is to make it impossible for a new field
// to smuggle one in without a test failure.
var identifierLike = regexp.MustCompile(
	`(?i)` +
		`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}` + // uuid
		`|[0-9a-f]{32,}` + // bare hex hash
		`|tg-[A-Za-z0-9_-]{4,}` + // raw API key
		`|[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`, // jwt-shaped
)

// knownOps reports whether name is one of the closed Op values.
func knownOps() map[string]bool {
	out := make(map[string]bool, len(ops))
	for _, o := range ops {
		out[string(o)] = true
	}
	return out
}

// asNumber accepts a numeric scalar in either form the validator sees: a
// JSON-decoded document (everything is float64) or a Go-native map straight
// from Report() (int64, int, float64). Returning ok=false for a non-number is
// the point — a string where a count belongs is a shape violation.
func asNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	default:
		return 0, false
	}
}

// asObject is asMap with a different name for readability at call sites.
func asObject(v any) (map[string]any, bool) { return asMap(v) }

// asMap accepts the map forms the validator sees: JSON-decoded
// map[string]any, or a Go-native map with a concrete value type (Report emits
// map[string]float64 for per_op_qps_60s, for instance). The values are
// normalised into map[string]any so the rest of the walk is uniform.
func asMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[string]float64:
		out := make(map[string]any, len(m))
		for k, e := range m {
			out[k] = e
		}
		return out, true
	case map[string]int64:
		out := make(map[string]any, len(m))
		for k, e := range m {
			out[k] = e
		}
		return out, true
	case map[string]int:
		out := make(map[string]any, len(m))
		for k, e := range m {
			out[k] = e
		}
		return out, true
	default:
		return nil, false
	}
}

// asSlice accepts the slice forms the validator sees, normalising to []any.
func asSlice(v any) ([]any, bool) {
	switch s := v.(type) {
	case []any:
		return s, true
	case []float64:
		out := make([]any, len(s))
		for i, e := range s {
			out[i] = e
		}
		return out, true
	case []int64:
		out := make([]any, len(s))
		for i, e := range s {
			out[i] = e
		}
		return out, true
	default:
		return nil, false
	}
}

// ValidateShape walks a decoded Report document and returns every structural
// violation it finds. An empty result means the document matches the allow-list
// exactly and no scalar looks like an identifier, hash, or token.
//
// It checks, in order:
//  1. every object key is on the allow-list for its position;
//  2. every required key is present (so a silently dropped field is caught too);
//  3. numeric fields really are numbers and non-negative;
//  4. bucket arrays are the right length;
//  5. no string anywhere matches identifierLike.
func ValidateShape(doc map[string]any) []string {
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	// 1 + 2: top level.
	for k := range doc {
		if !topLevelKeys[k] {
			add("unexpected top-level key %q", k)
		}
	}
	for k := range topLevelKeys {
		if _, ok := doc[k]; !ok {
			add("missing top-level key %q", k)
		}
	}

	// 3: scalars we expect to be numeric and sane.
	for _, k := range []string{"uptime_seconds", "admit_calls_total"} {
		if v, ok := doc[k]; ok {
			num, isNum := asNumber(v)
			if !isNum {
				add("%s is %T, want a number", k, v)
			} else if num < 0 {
				add("%s is negative (%v)", k, num)
			} else if num > maxPlausibleNumeric {
				add("%s = %v is implausibly large (ceiling %v)", k, num, maxPlausibleNumeric)
			}
		}
	}

	// 4: bucket edges are a known, bounded layout.
	edges, ok := asSlice(doc["bucket_edges_ms"])
	if !ok {
		add("bucket_edges_ms is %T, want an array", doc["bucket_edges_ms"])
	} else if len(edges) > 64 {
		add("bucket_edges_ms has %d entries; the layout must stay bounded", len(edges))
	}

	// ops: keyed ONLY by known Op values.
	opsRaw, ok := asObject(doc["ops"])
	if !ok {
		add("ops is %T, want an object", doc["ops"])
	} else {
		known := knownOps()
		// COMPLETENESS, not just membership. Checking only the series present
		// means a refactor that drops a series passes silently — including
		// dropping auth_lookup, the one series this whole change exists to
		// publish. Every known op must appear.
		for name := range known {
			if _, present := opsRaw[name]; !present {
				add("ops is missing the required series %q; every known Op must be reported", name)
			}
		}
		for name, raw := range opsRaw {
			if !known[name] {
				add("ops has unknown series %q; the Op set is closed", name)
				continue
			}
			entry, ok := asObject(raw)
			if !ok {
				add("ops[%s] is %T, want an object", name, raw)
				continue
			}
			for k := range entry {
				if !opKeys[k] {
					add("ops[%s] has unexpected key %q", name, k)
				}
			}
			for k := range opKeys {
				if _, ok := entry[k]; !ok {
					add("ops[%s] is missing key %q", name, k)
				}
			}
			for _, k := range []string{"count", "sum_ms", "avg_ms", "p50_ms", "p90_ms", "p99_ms"} {
				v, ok := entry[k]
				if !ok {
					continue
				}
				num, isNum := asNumber(v)
				if !isNum {
					add("ops[%s].%s is %T, want a number", name, k, v)
					continue
				}
				if num < 0 {
					add("ops[%s].%s is negative (%v)", name, k, num)
				}
				// Range sanity. Numeric fields are DELIBERATELY exempt from the
				// identifier regex (a counter is not a string, and matching
				// digits would false-positive on every bucket count), which
				// leaves a leak-encoded-as-a-number unexamined. A cheap ceiling
				// closes the obvious version of that: these are counts and
				// millisecond sums from one process, so a value in the 10^12+
				// range is either a smuggled identifier (a 13-digit epoch-ms or
				// a truncated UUID's decimal form) or a broken counter, and both
				// deserve a look. The bound is deliberately loose — ~31 years of
				// milliseconds — so it cannot fire on real traffic.
				if num > maxPlausibleNumeric {
					add("ops[%s].%s = %v is implausibly large for a percentage-free counter or ms sum (ceiling %v); "+
						"numeric fields are exempt from the identifier check, so this is the backstop",
						name, k, num, maxPlausibleNumeric)
				}
			}
			buckets, ok := asSlice(entry["buckets"])
			if !ok {
				add("ops[%s].buckets is %T, want an array", name, entry["buckets"])
			} else if len(edges) > 0 && len(buckets) != len(edges)+1 {
				add("ops[%s].buckets has %d entries, want len(bucket_edges_ms)+1 = %d",
					name, len(buckets), len(edges)+1)
			}
			if pf, ok := entry["p99_is_floor"]; ok {
				if _, isBool := pf.(bool); !isBool {
					add("ops[%s].p99_is_floor is %T, want a bool", name, pf)
				}
			}
		}
	}

	// auth: fixed keys, and per_op_qps_60s keyed only by known Op values.
	authRaw, ok := asObject(doc["auth"])
	if !ok {
		add("auth is %T, want an object", doc["auth"])
	} else {
		for k := range authRaw {
			if !authKeys[k] {
				add("auth has unexpected key %q", k)
			}
		}
		for k := range authKeys {
			if _, ok := authRaw[k]; !ok {
				add("auth is missing key %q", k)
			}
		}
		if per, ok := asObject(authRaw["per_op_qps_60s"]); ok {
			known := knownOps()
			for name := range per {
				if !known[name] {
					add("auth.per_op_qps_60s has unknown series %q", name)
				}
			}
			// Completeness here too: a missing auth_lookup in the rate block
			// would leave the "100 queries/second sustained" trigger without its
			// number, and a membership-only check would not notice.
			for name := range known {
				if _, present := per[name]; !present {
					add("auth.per_op_qps_60s is missing the required series %q", name)
				}
			}
		} else {
			add("auth.per_op_qps_60s is %T, want an object", authRaw["per_op_qps_60s"])
		}
	}

	// 5: no scalar anywhere may look like an identifier, hash or token. Walk the
	// whole document, including the free-text NOTE and trigger strings, so a
	// secret cannot be smuggled in through prose either.
	for _, path := range leafStrings(doc, "") {
		if m := identifierLike.FindString(path.value); m != "" {
			add("%s contains an identifier-like value %q", path.path, m)
		}
	}
	sort.Strings(errs)
	return errs
}

type leaf struct{ path, value string }

// leafStrings walks v and returns every string scalar with its JSON-ish path.
// Map keys are checked too: a key is data as much as a value is.
//
// SCOPE / LIMITATION: the walk recurses only through map[string]any and []any,
// i.e. a JSON-DECODED document. That is the only shape this function is meant
// for — over HTTP, Report() is marshalled and decoded before validation, so
// every object is map[string]any. Go-native composites (map[string]string,
// []map[string]any, a struct) are NOT traversed and their contents would go
// unchecked, so a future caller validating a hand-built map (rather than a
// decoded one) must not assume this covers it. ValidateShape's key/shape checks
// use the tolerant asMap/asSlice/asNumber helpers and are unaffected; only this
// deep string walk has the narrower view.
func leafStrings(v any, path string) []leaf {
	var out []leaf
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			child := path + "." + k
			out = append(out, leaf{child + " (key)", k})
			out = append(out, leafStrings(t[k], child)...)
		}
	case []any:
		for i, e := range t {
			out = append(out, leafStrings(e, fmt.Sprintf("%s[%d]", path, i))...)
		}
	case string:
		out = append(out, leaf{path, t})
	}
	return out
}

// ValidateShapeSummary is a convenience for tests: it joins the violations into
// one message, or "" when the document is clean.
func ValidateShapeSummary(doc map[string]any) string {
	errs := ValidateShape(doc)
	if len(errs) == 0 {
		return ""
	}
	return strings.Join(errs, "\n  ")
}
