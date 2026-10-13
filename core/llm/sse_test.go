package llm

import (
	"strings"
	"testing"
)

func TestReadSSE(t *testing.T) {
	in := "event: message_start\ndata: {\"a\":1}\n\n: comment\ndata: line1\ndata: line2\n\ndata: [DONE]\n\n"
	var got []string
	err := ReadSSE(strings.NewReader(in), func(ev, data string) error {
		got = append(got, ev+"|"+data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`message_start|{"a":1}`, "|line1\nline2", "|[DONE]"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %q", got)
	}
}
