package consoleapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"tollgate/core/api"
	"tollgate/core/db/repo"
	"tollgate/core/llm"
	"tollgate/core/llm/providers"
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

// credentialForm is the Add credential dialog: POST /credentials and /credentials/test.
type credentialForm struct {
	repo.CredentialInput
	Secret string `json:"secret"`
}

func validCredential(w http.ResponseWriter, in *credentialForm) bool {
	if in.Kind == "" {
		in.Kind = "api_key"
	}
	vertex := in.Kind == "gcp_service_account"
	if vertex && in.Provider == "" {
		in.Provider = "google"
	}
	baseErr := llm.ValidateAPIBase(in.APIBase)
	switch {
	case in.Kind != "api_key" && !vertex:
		badRequest(w, "kind must be api_key or gcp_service_account")
	case in.Provider != "openai" && in.Provider != "anthropic" && in.Provider != "google":
		badRequest(w, "provider must be openai, anthropic, or google")
	case vertex && in.Provider != "google":
		badRequest(w, "service accounts are for google (Vertex AI) only")
	case in.Secret == "" && in.Provider != "openai": // OpenAI-compatible servers may need no key
		badRequest(w, "secret is required")
	case vertex && !json.Valid([]byte(in.Secret)):
		badRequest(w, "service account secret must be the JSON key file contents")
	case vertex && in.VertexProject == "":
		badRequest(w, "vertex_project is required for a service account")
	case baseErr != nil:
		badRequest(w, baseErr.Error())
	default:
		if !vertex {
			in.VertexProject, in.VertexLocation = "", ""
		} else if in.VertexLocation == "" {
			in.VertexLocation = "us-central1"
		}
		return true
	}
	return false
}

func (s *Server) createCredential(w http.ResponseWriter, r *http.Request) {
	var in credentialForm
	if !decode(w, r, &in) {
		return
	}
	if in.Name == "" {
		badRequest(w, "name is required")
		return
	}
	if !validCredential(w, &in) {
		return
	}
	nonce, ct, err := s.Vault.Seal(in.Name, []byte(in.Secret))
	if err != nil {
		storeErr(w, err)
		return
	}
	c, err := s.Store.CreateCredential(r.Context(), in.CredentialInput, nonce, ct)
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

// credentialTestTimeout bounds Test connection; a var so tests can shorten it.
var credentialTestTimeout = 10 * time.Second

// probeClient doesn't follow redirects: a test reaches only the URL that was validated.
var probeClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// testCredential lists the upstream's models with an unsaved credential form. Nothing is
// stored; the secret is scrubbed from any upstream or transport error text.
func (s *Server) testCredential(w http.ResponseWriter, r *http.Request) {
	var in credentialForm
	if !decode(w, r, &in) || !validCredential(w, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), credentialTestTimeout)
	defer cancel()
	m := &llm.Model{Provider: in.Provider, APIBase: in.APIBase, CredentialKind: in.Kind, Secret: []byte(in.Secret),
		Extra: llm.Extra{VertexProject: in.VertexProject, VertexLocation: in.VertexLocation}}
	start := time.Now()
	status, n, err := providers.Probe(ctx, probeClient, m)
	out := map[string]any{"ok": err == nil, "status": status, "model_count": n, "latency_ms": time.Since(start).Milliseconds(), "message": ""}
	if err != nil {
		msg := err.Error()
		var ue *llm.UpstreamError
		if errors.As(err, &ue) {
			msg = ue.Message
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			msg = fmt.Sprintf("timed out after %s", credentialTestTimeout)
		}
		out["message"] = redactSecret(msg, in.Secret)
	}
	api.WriteJSON(w, 200, out)
}

// redactSecret removes the secret, and a service account's private key, from s.
func redactSecret(s, secret string) string {
	secrets := []string{secret}
	var sa struct {
		PrivateKey   string `json:"private_key"`
		PrivateKeyID string `json:"private_key_id"`
	}
	if json.Unmarshal([]byte(secret), &sa) == nil {
		secrets = append(secrets, sa.PrivateKey, sa.PrivateKeyID, strings.ReplaceAll(sa.PrivateKey, "\n", `\n`))
	}
	for _, v := range secrets {
		if len(v) >= 4 {
			s = strings.ReplaceAll(s, v, "[redacted]")
		}
	}
	return s
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
