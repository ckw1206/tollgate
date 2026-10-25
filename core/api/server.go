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
}

const maxBody = 20 << 20

// Upstream calls get their own deadline and survive client disconnects, so
// streams can be drained for exact usage.
const upstreamTimeout = 10 * time.Minute

func (s *Server) Register(mux *http.ServeMux) {
	mux.Handle("POST /v1/chat/completions", s.auth(http.HandlerFunc(s.chat)))
	mux.Handle("POST /v1/embeddings", s.auth(http.HandlerFunc(s.embeddings)))
	mux.Handle("GET /v1/models", s.auth(http.HandlerFunc(s.models)))
	mux.HandleFunc("GET /health", s.health)
	// Admit-path instrumentation (#33). Unauthenticated and internal-ish: see
	// metricsHandler for why, and for what the document deliberately excludes.
	mux.HandleFunc("GET /metrics", s.metricsHandler)
}
