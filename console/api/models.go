package consoleapi

import (
	"errors"
	"log/slog"
	"net/http"

	"tollgate/core/api"
	"tollgate/core/db/repo"
)

func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	ms, err := s.Store.ListModels(r.Context())
	if err != nil {
		storeErr(w, err)
		return
	}
	if ms == nil {
		ms = []repo.Model{}
	}
	api.WriteJSON(w, 200, ms)
}

func validModel(w http.ResponseWriter, in repo.ModelInput) bool {
	switch {
	case in.Name == "" || in.UpstreamModel == "":
		badRequest(w, "name and upstream_model are required")
	case in.Provider != "openai" && in.Provider != "anthropic" && in.Provider != "google":
		badRequest(w, "provider must be openai, anthropic, or google")
	case in.PriceInputMicros < 0 || in.PriceOutputMicros < 0:
		badRequest(w, "prices must not be negative")
	case in.Provider != "openai" && in.CredentialID == nil:
		badRequest(w, in.Provider+" models need a credential")
	default:
		return true
	}
	return false
}

func (s *Server) reload(r *http.Request) {
	if err := s.Reload(r.Context()); err != nil {
		slog.Error("model registry reload failed", "err", err)
	}
}

func (s *Server) createModel(w http.ResponseWriter, r *http.Request) {
	var in repo.ModelInput
	if !decode(w, r, &in) || !validModel(w, in) {
		return
	}
	m, err := s.Store.CreateModel(r.Context(), in)
	if err != nil {
		storeErr(w, err)
		return
	}
	s.audit(r, "model.create", "models", m.ID, nil, m)
	s.reload(r)
	api.WriteJSON(w, 201, m)
}

func (s *Server) updateModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in repo.ModelInput
	if !decode(w, r, &in) || !validModel(w, in) {
		return
	}
	before, err := s.Store.GetModel(r.Context(), id)
	if err != nil {
		storeErr(w, err)
		return
	}
	after, err := s.Store.UpdateModel(r.Context(), id, in)
	if err != nil {
		storeErr(w, err)
		return
	}
	s.audit(r, "model.update", "models", id, before, after)
	s.reload(r)
	api.WriteJSON(w, 200, after)
}

func (s *Server) deleteModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.Store.DeleteModel(r.Context(), id)
	if errors.Is(err, repo.ErrConflict) {
		api.WriteError(w, 409, "invalid_request_error", "model_in_use", "model has spend history; disable it instead")
		return
	}
	if err != nil {
		storeErr(w, err)
		return
	}
	s.audit(r, "model.delete", "models", id, nil, nil)
	s.reload(r)
	w.WriteHeader(204)
}
