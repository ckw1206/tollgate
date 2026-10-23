package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"tollgate/core/llm"
)

// probeServer answers the model-list GET with the given status and body.
func probeServer(status int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
}

func probeModel(base, upstream string) *llm.Model {
	return &llm.Model{Provider: "openai", APIBase: base, UpstreamModel: upstream}
}

func TestProbeLimits(t *testing.T) {
	tests := []struct {
		name       string
		upstream   string
		status     int
		body       string
		wantLen    int64
		wantStatus int
	}{
		{
			name: "matching id reports the limit", upstream: "DeepSeek-V4.1-Flash", status: 200,
			body:       `{"object":"list","data":[{"id":"other","max_model_len":4096},{"id":"DeepSeek-V4.1-Flash","max_model_len":32768}]}`,
			wantLen:    32768,
			wantStatus: 200,
		},
		{
			name: "id mismatch returns zero", upstream: "not-listed", status: 200,
			body:       `{"data":[{"id":"DeepSeek-V4.1-Flash","max_model_len":32768}]}`,
			wantLen:    0,
			wantStatus: 200,
		},
		{
			name: "case-insensitive fallback matches", upstream: "deepseek-v4.1-flash", status: 200,
			body:       `{"data":[{"id":"DeepSeek-V4.1-Flash","max_model_len":32768}]}`,
			wantLen:    32768,
			wantStatus: 200,
		},
		{
			name: "exact match wins over an earlier case-insensitive one", upstream: "M", status: 200,
			body:       `{"data":[{"id":"m","max_model_len":4096},{"id":"M","max_model_len":32768}]}`,
			wantLen:    32768,
			wantStatus: 200,
		},
		{
			name: "field absent returns zero", upstream: "m", status: 200,
			body:       `{"data":[{"id":"m","object":"model"}]}`,
			wantLen:    0,
			wantStatus: 200,
		},
		{
			name: "field null returns zero", upstream: "m", status: 200,
			body:    `{"data":[{"id":"m","max_model_len":null}]}`,
			wantLen: 0, wantStatus: 200,
		},
		{
			name: "float limit is truncated to int64", upstream: "m", status: 200,
			body:       `{"data":[{"id":"m","max_model_len":32768.0}]}`,
			wantLen:    32768,
			wantStatus: 200,
		},
		{
			name: "fractional limit is not a token count", upstream: "m", status: 200,
			body:       `{"data":[{"id":"m","max_model_len":0.5}]}`,
			wantLen:    0,
			wantStatus: 200,
		},
		{
			name: "non-numeric limit returns zero", upstream: "m", status: 200,
			body:       `{"data":[{"id":"m","max_model_len":"32768"}]}`,
			wantLen:    0,
			wantStatus: 200,
		},
		{
			// float64->int64 for an out-of-range literal is implementation-defined (on amd64
			// 1e100 became MaxInt64), which would hand the caller a limit so large it disables
			// its check. An impossible context length must read as "no limit" instead.
			name: "absurdly large limit is ignored", upstream: "m", status: 200,
			body:       `{"data":[{"id":"m","max_model_len":1e100}]}`,
			wantLen:    0,
			wantStatus: 200,
		},
		{
			name: "limit just under the bound is kept", upstream: "m", status: 200,
			body:       `{"data":[{"id":"m","max_model_len":1000000}]}`,
			wantLen:    1000000,
			wantStatus: 200,
		},
		{
			name: "empty data returns zero", upstream: "m", status: 200,
			body:       `{"data":[]}`,
			wantLen:    0,
			wantStatus: 200,
		},
		{
			name: "entry that is not an object is skipped", upstream: "m", status: 200,
			body:       `{"data":["nope",{"id":"m","max_model_len":8192}]}`,
			wantLen:    8192,
			wantStatus: 200,
		},
		{
			name: "gemini models shape", upstream: "gemini-2.0-flash", status: 200,
			body:       `{"models":[{"id":"gemini-2.0-flash","max_model_len":1048576}]}`,
			wantLen:    1048576,
			wantStatus: 200,
		},
		{
			name: "vertex publisherModels shape", upstream: "gemini-1.5-pro", status: 200,
			body:       `{"publisherModels":[{"id":"gemini-1.5-pro","max_model_len":2097152}]}`,
			wantLen:    2097152,
			wantStatus: 200,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := probeServer(tc.status, tc.body)
			defer srv.Close()

			status, gotLen, err := ProbeLimits(context.Background(), srv.Client(), probeModel(srv.URL, tc.upstream))
			if err != nil {
				t.Fatalf("ProbeLimits: unexpected error %v", err)
			}
			if status != tc.wantStatus {
				t.Errorf("status = %d, want %d", status, tc.wantStatus)
			}
			if gotLen != tc.wantLen {
				t.Errorf("maxModelLen = %d, want %d", gotLen, tc.wantLen)
			}
		})
	}
}

func TestProbeLimits_Errors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantMsg    string
	}{
		{name: "non-JSON body", status: 200, body: `<html>not json</html>`,
			wantStatus: 200, wantMsg: "upstream returned non-JSON body"},
		{name: "unauthorized", status: 401,
			body:       `{"error":{"message":"invalid api key"}}`,
			wantStatus: 401, wantMsg: "invalid api key"},
		{name: "server error", status: 500, body: `upstream exploded`,
			wantStatus: 500, wantMsg: "upstream exploded"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := probeServer(tc.status, tc.body)
			defer srv.Close()

			status, gotLen, err := ProbeLimits(context.Background(), srv.Client(), probeModel(srv.URL, "m"))
			var ue *llm.UpstreamError
			if !errors.As(err, &ue) {
				t.Fatalf("err = %v (%T), want *llm.UpstreamError", err, err)
			}
			if status != tc.wantStatus {
				t.Errorf("status = %d, want %d", status, tc.wantStatus)
			}
			if ue.Status != tc.wantStatus {
				t.Errorf("UpstreamError.Status = %d, want %d", ue.Status, tc.wantStatus)
			}
			if ue.Message != tc.wantMsg {
				t.Errorf("UpstreamError.Message = %q, want %q", ue.Message, tc.wantMsg)
			}
			if gotLen != 0 {
				t.Errorf("maxModelLen = %d, want 0 on error", gotLen)
			}
		})
	}
}

// An unreachable upstream is reported as status 0 with an *llm.UpstreamError, matching Probe.
func TestProbeLimits_Unreachable(t *testing.T) {
	srv := probeServer(200, `{"data":[]}`)
	url := srv.URL
	srv.Close() // nothing is listening on url any more

	status, gotLen, err := ProbeLimits(context.Background(), http.DefaultClient, probeModel(url, "m"))
	var ue *llm.UpstreamError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v (%T), want *llm.UpstreamError", err, err)
	}
	if status != 0 {
		t.Errorf("status = %d, want 0", status)
	}
	if gotLen != 0 {
		t.Errorf("maxModelLen = %d, want 0", gotLen)
	}
}

// ProbeLimits must read the same endpoint, and use m.UpstreamModel rather than m.Name.
func TestProbeLimits_UsesUpstreamModelNotName(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.Write([]byte(`{"data":[{"id":"upstream-id","max_model_len":123}]}`))
	}))
	defer srv.Close()

	m := probeModel(srv.URL+"/v1", "upstream-id")
	m.Name = "display-name"
	m.Secret = []byte("sk-test-secret")

	_, gotLen, err := ProbeLimits(context.Background(), srv.Client(), m)
	if err != nil {
		t.Fatalf("ProbeLimits: %v", err)
	}
	if gotLen != 123 {
		t.Errorf("maxModelLen = %d, want 123", gotLen)
	}
	if gotPath != "/v1/models" {
		t.Errorf("path = %q, want %q", gotPath, "/v1/models")
	}
	if gotAuth != "Bearer sk-test-secret" {
		t.Errorf("Authorization = %q, want the bearer credential", gotAuth)
	}
}

// Existing Probe behavior is unchanged: same count semantics, same errors.
func TestProbe_UnchangedBehavior(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantCount  int
		wantErr    string
	}{
		{name: "counts all three shapes", status: 200,
			body:       `{"data":[{"id":"a"},{"id":"b"}],"models":[{"id":"c"}],"publisherModels":[{"id":"d"}]}`,
			wantStatus: 200, wantCount: 4},
		{name: "counts entries without max_model_len", status: 200,
			body:       `{"data":[{"id":"a"},{"id":"b"}]}`,
			wantStatus: 200, wantCount: 2},
		{name: "non-JSON body", status: 200, body: `nope`,
			wantStatus: 200, wantErr: "upstream returned non-JSON body"},
		{name: "not found", status: 404, body: `{"error":{"message":"no such route"}}`,
			wantStatus: 404, wantErr: "no such route"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := probeServer(tc.status, tc.body)
			defer srv.Close()

			status, count, err := Probe(context.Background(), srv.Client(), probeModel(srv.URL, "a"))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Probe: unexpected error %v", err)
				}
			} else {
				var ue *llm.UpstreamError
				if !errors.As(err, &ue) || ue.Message != tc.wantErr {
					t.Fatalf("err = %v, want *llm.UpstreamError with %q", err, tc.wantErr)
				}
			}
			if status != tc.wantStatus || count != tc.wantCount {
				t.Errorf("Probe = (%d, %d), want (%d, %d)", status, count, tc.wantStatus, tc.wantCount)
			}
		})
	}
}

// Guards the SGLang shape that motivated ProbeLimits: a large context_window reported per model.
func TestProbeLimits_SGLangShape(t *testing.T) {
	body := `{"object":"list","data":[
		{"id":"/data/models/DeepSeek-V4.1-Flash","object":"model","created":1,"owned_by":"sglang","max_model_len":32768}
	]}`
	var list probeList
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := maxModelLenFor(list, "/data/models/DeepSeek-V4.1-Flash"); got != 32768 {
		t.Errorf("maxModelLenFor = %d, want 32768", got)
	}
	if got := maxModelLenFor(list, "some-other-model"); got != 0 {
		t.Errorf("maxModelLenFor(mismatch) = %d, want 0", got)
	}
}
