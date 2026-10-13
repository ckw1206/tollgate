package vault

import (
	"bytes"
	"testing"
)

func key() []byte { return bytes.Repeat([]byte{7}, 32) }

func TestSealOpen_RoundTrip(t *testing.T) {
	v, err := New(key())
	if err != nil {
		t.Fatal(err)
	}
	nonce, ct, err := v.Seal("openai-prod", []byte("sk-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("sk-secret")) {
		t.Fatal("plaintext visible in ciphertext")
	}
	got, err := v.Open("openai-prod", nonce, ct)
	if err != nil || string(got) != "sk-secret" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestSeal_FreshNoncePerCall(t *testing.T) {
	v, _ := New(key())
	n1, c1, _ := v.Seal("a", []byte("x"))
	n2, c2, _ := v.Seal("a", []byte("x"))
	if bytes.Equal(n1, n2) || bytes.Equal(c1, c2) {
		t.Fatal("nonce reused")
	}
}

func TestOpen_WrongAADFails(t *testing.T) {
	v, _ := New(key())
	n, c, _ := v.Seal("openai-prod", []byte("sk"))
	if _, err := v.Open("anthropic-prod", n, c); err == nil {
		t.Fatal("ciphertext moved to another credential must not decrypt")
	}
}

func TestOpen_WrongKeyFails(t *testing.T) {
	v, _ := New(key())
	n, c, _ := v.Seal("a", []byte("sk"))
	other, _ := New(bytes.Repeat([]byte{8}, 32))
	if _, err := other.Open("a", n, c); err == nil {
		t.Fatal("expected failure")
	}
}

func TestNew_RejectsShortKey(t *testing.T) {
	if _, err := New(make([]byte, 16)); err == nil {
		t.Fatal("expected error")
	}
}
