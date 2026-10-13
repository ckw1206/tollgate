package api

import (
	"context"
	"net/http"
	"time"
)

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	out := map[string]string{"postgres": "ok", "redis": "ok"}
	status := 200
	if err := s.Store.Ping(ctx); err != nil {
		out["postgres"], status = "down", 503
	}
	if err := s.Cache.Ping(ctx); err != nil {
		out["redis"], status = "down", 503
	}
	WriteJSON(w, status, out)
}
