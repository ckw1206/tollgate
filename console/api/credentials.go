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

// credentialForm is the credential dialog: POST /credentials and /credentials/test.
type credentialForm struct {
	repo.CredentialInput
	Secret string `json:"secret"`
	ID     string `json:"id"` // test only: a saved credential whose provider, and secret if none is given, to use
}

// validCredential checks a credential form. keepSecret (an edit that leaves the secret
// alone) skips the secret checks.
func validCredential(w http.ResponseWriter, in *credentialForm, keepSecret bool) bool {
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
	case !keepSecret && in.Secret == "" && in.Provider != "openai": // OpenAI-compatible servers may need no key
		badRequest(w, "secret is required")
	case !keepSecret && vertex && !json.Valid([]byte(in.Secret)):
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
	if !validCredential(w, &in, false) {
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

// credentialPatch is PUT /credentials/{id}. Absent fields keep their value, and so does
// an absent or empty secret. kind and provider can't change.
type credentialPatch struct {
	Name           *string `json:"name"`
	Kind           *string `json:"kind"`
	Provider       *string `json:"provider"`
	APIBase        *string `json:"api_base"`
	VertexProject  *string `json:"vertex_project"`
	VertexLocation *string `json:"vertex_location"`
	Secret         string  `json:"secret"`
}

func (s *Server) updateCredential(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var p credentialPatch
	if !decode(w, r, &p) {
		return
	}
	old, err := s.Store.GetCredential(r.Context(), id)
	if err != nil {
		storeErr(w, err)
		return
	}
	if (p.Provider != nil && *p.Provider != old.Provider) || (p.Kind != nil && *p.Kind != old.Kind) {
		badRequest(w, "provider can't be changed; add a new credential instead")
		return
	}
	in := credentialForm{CredentialInput: old.CredentialInput, Secret: p.Secret}
	for dst, v := range map[*string]*string{&in.Name: p.Name, &in.APIBase: p.APIBase, &in.VertexProject: p.VertexProject, &in.VertexLocation: p.VertexLocation} {
		if v != nil {
			*dst = *v
		}
	}
	keep := p.Secret == ""
	if in.Name == "" {
		badRequest(w, "name is required")
		return
	}
	if !validCredential(w, &in, keep) {
		return
	}
	// The name is the secret's associated data, so a rename re-seals the kept secret too.
	secret := []byte(in.Secret)
	if keep {
		if secret, err = s.Vault.Open(old.Name, old.Nonce, old.Ciphertext); err != nil {
			badRequest(w, "the saved secret can't be decrypted (wrong ENCRYPTION_KEY?); enter a new one")
			return
		}
	}
	nonce, ct, err := s.Vault.Seal(in.Name, secret)
	if err != nil {
		storeErr(w, err)
		return
	}
	c, err := s.Store.UpdateCredential(r.Context(), id, in.CredentialInput, nonce, ct, !keep)
	if err != nil {
		storeErr(w, err) // ErrConflict → 409 on a duplicate name
		return
	}
	if err := s.Reload(r.Context()); err != nil { // models pick up the new endpoint and secret
		slog.Error("credential updated; registry reload failed", "err", err)
	}
	changed := []string{}
	for _, f := range []struct {
		name     string
		was, now string
	}{{"name", old.Name, c.Name}, {"api_base", old.APIBase, c.APIBase},
		{"vertex_project", old.VertexProject, c.VertexProject}, {"vertex_location", old.VertexLocation, c.VertexLocation}} {
		if f.was != f.now {
			changed = append(changed, f.name)
		}
	}
	if !keep {
		changed = append(changed, "secret")
	}
	// Field names only: endpoints and project IDs can be sensitive too.
	s.audit(r, "credential.update", "provider_credentials", id, nil, map[string]any{"changed": changed})
	api.WriteJSON(w, 200, c)
}

// credentialTestTimeout bounds Test connection; a var so tests can shorten it.
var credentialTestTimeout = 10 * time.Second

// probeClient doesn't follow redirects: a test reaches only the URL that was validated.
var probeClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// testCredential lists the upstream's models with an unsaved credential form. Nothing is
// stored; the secret, typed or saved, is scrubbed from any upstream or transport error text.
func (s *Server) testCredential(w http.ResponseWriter, r *http.Request) {
	var in credentialForm
	if !decode(w, r, &in) {
		return
	}
	if in.ID != "" { // editing: the saved provider, and the saved secret unless a new one was entered
		old, err := s.Store.GetCredential(r.Context(), in.ID)
		if err != nil {
			storeErr(w, err)
			return
		}
		in.Provider, in.Kind = old.Provider, old.Kind
		if in.Secret == "" {
			pt, err := s.Vault.Open(old.Name, old.Nonce, old.Ciphertext)
			if err != nil {
				badRequest(w, "the saved secret can't be decrypted (wrong ENCRYPTION_KEY?); enter a new one")
				return
			}
			in.Secret = string(pt)
		}
	}
	if !validCredential(w, &in, false) {
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
