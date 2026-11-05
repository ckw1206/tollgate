// Package api serves the OpenAI-compatible /v1 endpoints.
package api

import (
	"net/http"
	"time"

	"tollgate/core/cache"
	"tollgate/core/db/repo"
	"tollgate/core/llm"
)

type Server struct {
	Store    *repo.Store
	Cache    *cache.Cache
	Registry *llm.Registry
	Record   func(repo.SpendRecord) // spend.Recorder.Record in production
	Now      func() time.Time
	// Keepalive is how long a stream may stay silent before an SSE comment goes
	// out (see streamWriter.keepalive). 0 means defaultKeepalive.
	Keepalive time.Duration
}

// defaultKeepalive stays well under common proxy read timeouts (nginx 60 s by
// default, Cloudflare 100 s to the first byte), which a long prefill exceeds.
const defaultKeepalive = 20 * time.Second

func (s *Server) keepalive() time.Duration {
	if s.Keepalive > 0 {
		return s.Keepalive
	}
	return defaultKeepalive
}

const maxBody = 20 << 20

// Upstream calls get their own deadline. For priced models they survive client
// disconnects so streams can be drained for exact usage; $0 chat models are
// cancelled with the client (see ChatAs).
const upstreamTimeout = 10 * time.Minute

func (s *Server) Register(mux *http.ServeMux) {
	mux.Handle("POST /v1/chat/completions", s.auth(http.HandlerFunc(s.chat)))
	mux.Handle("POST /v1/embeddings", s.auth(http.HandlerFunc(s.embeddings)))
	mux.Handle("GET /v1/models", s.auth(http.HandlerFunc(s.models)))
	mux.HandleFunc("GET /health", s.health)
}
