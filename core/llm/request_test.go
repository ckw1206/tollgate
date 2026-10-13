package llm

import "testing"

func TestParseChatRequest_ContentShapes(t *testing.T) {
	body := []byte(`{
		"model": "claude",
		"stream": true,
		"stream_options": {"include_usage": true},
		"max_completion_tokens": 256,
		"temperature": 0.2,
		"stop": "END",
		"messages": [
			{"role": "system", "content": "be brief"},
			{"role": "user", "content": [
				{"type": "text", "text": "what is this"},
				{"type": "image_url", "image_url": {"url": "data:image/png;base64,AAAA"}}
			]}
		]
	}`)
	r, err := ParseChatRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if r.Model != "claude" || !r.Stream || !r.IncludeUsage || r.HasTools {
		t.Fatalf("flags: %+v", r)
	}
	if *r.MaxTokens != 256 || *r.Temperature != 0.2 || len(r.Stop) != 1 || r.Stop[0] != "END" {
		t.Fatalf("params: %+v", r)
	}
	if len(r.Messages) != 2 || r.Messages[0].Parts[0].Text != "be brief" {
		t.Fatalf("system: %+v", r.Messages)
	}
	p := r.Messages[1].Parts
	if len(p) != 2 || p[1].Type != "image_url" || p[1].ImageURL != "data:image/png;base64,AAAA" {
		t.Fatalf("parts: %+v", p)
	}
}

func TestParseChatRequest_DetectsTools(t *testing.T) {
	for _, body := range []string{
		`{"model":"m","messages":[{"role":"user","content":"x"}],"tools":[{"type":"function"}]}`,
		`{"model":"m","messages":[{"role":"tool","content":"x","tool_call_id":"1"}]}`,
		`{"model":"m","messages":[{"role":"assistant","content":null,"tool_calls":[{}]}]}`,
	} {
		r, err := ParseChatRequest([]byte(body))
		if err != nil || !r.HasTools {
			t.Fatalf("%s: HasTools=%v err=%v", body, r != nil && r.HasTools, err)
		}
	}
}

func TestParseChatRequest_Rejects(t *testing.T) {
	for _, body := range []string{`not json`, `{"messages":[]}`, `{"model":"m"}`, `{"model":"m","messages":[{"role":"user","content":5}]}`} {
		if _, err := ParseChatRequest([]byte(body)); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}
