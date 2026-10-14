package consoleapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"tollgate/console/auth"
	"tollgate/console/rbac"
	"tollgate/core/api"
	"tollgate/core/db/repo"
	"tollgate/core/llm"
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
	// extra.headers can carry upstream auth, and api_base/credential_id are
	// deployment details: only people who can edit models see them.
	if !rbac.Has(auth.UserFrom(r.Context()).Role, rbac.ModelsWrite) {
		for i := range ms {
			ms[i].Extra, ms[i].APIBase, ms[i].CredentialID = nil, "", nil
		}
	}
	api.WriteJSON(w, 200, ms)
}

// validModel also drops api_base from a model with a credential: the credential's endpoint
// wins, and the console's Enable/Disable toggle re-sends whatever the model had stored.
func validModel(w http.ResponseWriter, in *repo.ModelInput) bool {
	if in.CredentialID != nil {
		in.APIBase = ""
	}
	switch {
	case in.Name == "" || in.UpstreamModel == "":
		badRequest(w, "name and upstream_model are required")
	case in.Provider != "openai" && in.Provider != "anthropic" && in.Provider != "google":
		badRequest(w, "provider must be openai, anthropic, or google")
	case in.PriceInputMicros < 0 || in.PriceOutputMicros < 0:
		badRequest(w, "prices must not be negative")
	case in.Provider != "openai" && in.CredentialID == nil:
		badRequest(w, in.Provider+" models need a credential")
	case in.CredentialID == nil && in.APIBase == "":
		badRequest(w, "api_base is required without a credential")
	case llm.ValidateAPIBase(in.APIBase) != nil:
		badRequest(w, llm.ValidateAPIBase(in.APIBase).Error())
	case len(in.Extra) > 0 && json.Unmarshal(in.Extra, new(llm.Extra)) != nil:
		// The registry skips models whose extra doesn't decode; reject it here instead.
		badRequest(w, "extra must be an object: vertex_project, vertex_location, headers (string map)")
	default:
		return true
	}
	return false
}

// credentialFits rejects a credential made for another provider: its endpoint and
// secret would be sent to the wrong API.
func (s *Server) credentialFits(w http.ResponseWriter, r *http.Request, in repo.ModelInput) bool {
	if in.CredentialID == nil {
		return true
	}
	c, err := s.Store.GetCredential(r.Context(), *in.CredentialID)
	switch {
	case errors.Is(err, repo.ErrNotFound):
		badRequest(w, "credential not found")
	case err != nil:
		storeErr(w, err)
	case c.Provider != in.Provider:
		badRequest(w, "credential "+c.Name+" is for "+c.Provider+", not "+in.Provider)
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
	if !decode(w, r, &in) || !validModel(w, &in) || !s.credentialFits(w, r, in) {
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
	if !decode(w, r, &in) || !validModel(w, &in) || !s.credentialFits(w, r, in) {
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
