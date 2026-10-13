package llm

import "testing"

func TestValidateAPIBase(t *testing.T) {
	for s, ok := range map[string]bool{
		"":                                true,
		"https://api.example.com/v1":      true,
		"http://localhost:8000/v1":        true,
		"http://LOCALHOST:8000":           true,
		"http://127.0.0.1:11434/v1":       true,
		"http://10.1.2.3/v1":              true,
		"http://172.31.255.1/v1":          true,
		"http://192.168.1.50:4000":        true,
		"http://[::1]:8000/v1":            true,
		"http://172.32.0.1/v1":            false, // outside 172.16/12
		"http://api.example.com/v1":       false,
		"http://[fd00::1]/v1":             false, // IPv6 ULA isn't in the allowed list
		"https://api.example.com/v1/":     false,
		"https://u:p@api.example.com/v1":  false,
		"https://api.example.com/v1?k=1":  false,
		"https://api.example.com/v1#frag": false,
		"ftp://api.example.com":           false,
		"api.example.com/v1":              false,
	} {
		if err := ValidateAPIBase(s); (err == nil) != ok {
			t.Errorf("%q: err=%v, want ok=%v", s, err, ok)
		}
	}
}
