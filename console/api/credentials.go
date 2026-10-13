package consoleapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"tollgate/core/api"
	"tollgate/core/db/repo"
)

func (s *Server) listCredentials(w http.ResponseWriter, r *http.Request) {
	cs, err := s.Store.ListCredentials(r.Context())
	if err != nil {
		storeErr(w, err)
		return
	}
	if cs == nil {
		cs = []repo.Credential{}
	}
	api.WriteJSON(w, 200, cs) // Ciphertext/Nonce are json:"-"
}

func (s *Server) createCredential(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name, Kind, Secret string }
	if !decode(w, r, &in) {
		return
	}
	switch {
	case in.Name == "" || in.Secret == "":
		badRequest(w, "name and secret are required")
		return
	case in.Kind != "api_key" && in.Kind != "gcp_service_account":
		badRequest(w, "kind must be api_key or gcp_service_account")
		return
	case in.Kind == "gcp_service_account" && !json.Valid([]byte(in.Secret)):
		badRequest(w, "service account secret must be the JSON key file contents")
		return
	}
	nonce, ct, err := s.Vault.Seal(in.Name, []byte(in.Secret))
	if err != nil {
		storeErr(w, err)
		return
	}
	c, err := s.Store.CreateCredential(r.Context(), in.Name, in.Kind, nonce, ct)
	if err != nil {
		storeErr(w, err)
		return
	}
	if err := s.Reload(r.Context()); err != nil {
		slog.Error("credential created; registry reload failed", "err", err)
	}
	s.audit(r, "credential.create", "provider_credentials", c.ID, nil, c)
	api.WriteJSON(w, 201, c)
}

func (s *Server) deleteCredential(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Store.DeleteCredential(r.Context(), id); err != nil {
		storeErr(w, err) // ErrConflict → 409 while models still use it
		return
	}
	if err := s.Reload(r.Context()); err != nil {
		slog.Error("credential deleted; registry reload failed", "err", err)
	}
	s.audit(r, "credential.delete", "provider_credentials", id, nil, nil)
	w.WriteHeader(204)
}
