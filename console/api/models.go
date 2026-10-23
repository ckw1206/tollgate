package consoleapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"

	"tollgate/console/auth"
	"tollgate/console/rbac"
	"tollgate/core/api"
	"tollgate/core/db/repo"
	"tollgate/core/llm"
	"tollgate/core/llm/providers"
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
	me := auth.UserFrom(r.Context())
	if rbac.Has(me.Role, rbac.ModelsWrite) {
		api.WriteJSON(w, 200, ms)
		return
	}
	// Everyone else sees only the enabled models they may call, and why.
	g, err := s.Store.GetGroup(r.Context(), me.GroupID)
	if err != nil {
		storeErr(w, err)
		return
	}
	type view struct {
		repo.Model
		Access string `json:"access"` // "group" or "granted"
	}
	out := []view{}
	for _, m := range ms {
		a := repo.Access(g.Models, me.ExtraModels, m.Name)
		if !m.Enabled || a == "" {
			continue
		}
		// extra.headers can carry upstream auth, and api_base/credential_id are
		// deployment details: only people who can edit models see them.
		m.Extra, m.APIBase, m.CredentialID = nil, "", nil
		out = append(out, view{m, a})
	}
	api.WriteJSON(w, 200, out)
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
	case in.ContextWindow != nil && *in.ContextWindow <= 0:
		badRequest(w, "context_window must be a positive token count")
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

// contextWindowTimeout bounds the upstream probe below; a var so tests can shorten it.
var contextWindowTimeout = credentialTestTimeout

// contextWindowFits asks the upstream how many tokens it really serves and rejects a
// declared context_window larger than that. It is the root-cause fix for the incident where
// a row declared 1000000 while the upstream served 32768: clients trusted the 1M, never
// compacted, and every oversized request came back HTTP 400.
//
// Fail OPEN, always. The probe runs on a model save that sets a context_window, and an
// upstream that is down, slow, or simply doesn't report a limit must never make the model
// unsaveable: gateway edits have to keep working during an outage. Only a positively
// reported limit that the declared value exceeds rejects the save.
//
// stored is the row as saved (nil when creating). A save that changes nothing about which
// upstream serves the model AND keeps the same context_window is let through WITHOUT probing:
// the console's Enable/Disable toggle re-sends the whole form (web/src/pages/Models.tsx), so
// guarding an unchanged value would make a row whose stored window is already too big
// impossible to disable — precisely when an operator most needs to switch it off. The guard
// exists to stop a NEW claim about the limit, not to freeze an existing one.
//
// The shortcut therefore requires the upstream identity to be unchanged too, not just the
// window: repointing upstream_model (or the credential/api_base) at a server with a smaller
// limit is a new claim, even when context_window is byte-identical. Keying on the window
// alone would let that rewrite the row back into the very incident this guards against.
//
// Known boundary: retargeting a CREDENTIAL'S api_base (updateCredential) re-points every model
// using it at once, and this guard cannot see that — the model's four compared fields are all
// unchanged and the row's own api_base is empty for credentialed models. That is a deliberate
// admin action, audited as credential.update, and not a model save, so it is out of scope here;
// revalidating the affected models belongs with the credential write. Rows left over the new
// limit keep the same escape hatch as any pre-existing over-limit row: edit or disable them.
//
// SECURITY: unlike the model api_base at create time (publicHostCheck in gate.go), this
// probe deliberately does NOT apply publicHostCheck. A self-hosted upstream on a private
// address is a supported deployment, and a private-host guard here would make such a model
// unsaveable. This is intentional — do not "fix" it by adding the guard. testCredential
// probes the same endpoints the same way, and the endpoint is already reachable through the
// gateway itself.
func (s *Server) contextWindowFits(w http.ResponseWriter, r *http.Request, in repo.ModelInput, stored *repo.Model) bool {
	if in.ContextWindow == nil {
		return true // nothing declared to contradict: no upstream call at all
	}
	if sameUpstream(stored, in) && stored.ContextWindow != nil && *stored.ContextWindow == *in.ContextWindow {
		return true // a no-op save: the toggle and other unchanged saves must always go through
	}
	m, ok := s.probeModel(r.Context(), in)
	if !ok {
		return true // the credential couldn't be resolved (deleted, or a wrong ENCRYPTION_KEY)
	}
	ctx, cancel := context.WithTimeout(r.Context(), contextWindowTimeout)
	defer cancel()
	_, max, err := providers.ProbeLimits(ctx, probeClient, m)
	if err != nil || max <= 0 {
		// Unreachable, timed out, upstream 4xx/5xx, non-JSON body, or no limit reported:
		// allow the save. Nothing is logged or returned here on purpose — the upstream error
		// can embed the endpoint, and a transport error can echo the request headers, so the
		// only safe thing to do with it is drop it. Silently succeeding leaks nothing.
		return true
	}
	if *in.ContextWindow > max {
		badRequest(w, fmt.Sprintf("context_window %d exceeds the upstream maximum of %d for %s",
			*in.ContextWindow, max, in.UpstreamModel))
		return false
	}
	return true
}

// sameUpstream reports whether a save still points at the same served model as stored: the
// same upstream_model, reached through the same provider, credential, api_base and extra. It
// gates the no-op shortcut in contextWindowFits, so any change here forces a fresh probe.
// stored is nil when creating.
//
// provider belongs in this comparison because probeRequest switches on it: openai and google
// hit different paths with different auth headers, and anthropic appends /v1 and sends
// x-api-key. Those endpoints can report different limits, so a provider change is a new claim
// about the limit even at an identical context_window.
//
// extra belongs here because extra.headers ride on every real chat request (each provider
// copies them in) even though probeRequest does not send them: a deployment that routes on a
// header serves a different model when that header changes, so the limit can differ while the
// probe would still see the old one. Comparing extra costs nothing and keeps the shortcut
// honest; vertex_project/vertex_location ride along with it.
func sameUpstream(stored *repo.Model, in repo.ModelInput) bool {
	if stored == nil {
		return false
	}
	return stored.Provider == in.Provider &&
		stored.UpstreamModel == in.UpstreamModel &&
		stored.APIBase == in.APIBase && // validModel has already cleared this for credentialed models
		sameCredential(stored.CredentialID, in.CredentialID) &&
		sameExtra(stored.Extra, in.Extra)
}

// sameExtra compares two optional extra JSON objects by value, not by bytes: Postgres hands
// back its own spacing ({"a": 1} vs {"a":1}), so a raw byte comparison would call every
// unchanged multi-key extra a change and force a probe on every save — breaking the
// Enable/Disable toggle this shortcut exists to protect. NULL and '{}' both mean "no extra".
func sameExtra(stored, in json.RawMessage) bool {
	var a, b any
	if !extraEmpty(stored) {
		if json.Unmarshal(stored, &a) != nil {
			return false
		}
	}
	if !extraEmpty(in) {
		if json.Unmarshal(in, &b) != nil {
			return false
		}
	}
	return reflect.DeepEqual(a, b)
}

// extraEmpty reports whether an extra column holds nothing meaningful.
func extraEmpty(raw json.RawMessage) bool {
	return len(raw) == 0 || string(bytes.TrimSpace(raw)) == "{}"
}

// sameCredential compares two optional credential ids.
func sameCredential(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// probeModel builds the runtime model for an unsaved form, exactly as llm.LoadModels builds
// one: the credential's api_base wins over the model's, its kind and secret come from the
// vault, and Vertex project/location fall back to the credential's. ok is false when the
// credential can't be read or decrypted — the caller fails open.
func (s *Server) probeModel(ctx context.Context, in repo.ModelInput) (*llm.Model, bool) {
	m := &llm.Model{Name: in.Name, Provider: in.Provider, UpstreamModel: in.UpstreamModel,
		APIBase: in.APIBase, ContextWindow: *in.ContextWindow}
	if len(in.Extra) > 0 {
		// validModel already rejected an undecodable extra; belt and braces.
		if json.Unmarshal(in.Extra, &m.Extra) != nil {
			return nil, false
		}
	}
	if in.CredentialID == nil {
		return m, true // no credential: the model's own api_base, no secret to send
	}
	c, err := s.Store.GetCredential(ctx, *in.CredentialID)
	if err != nil {
		// credentialFits reports a missing one properly; anything else (deleted mid-request,
		// store error) must not block the save here.
		return nil, false
	}
	secret, err := s.Vault.Open(c.Name, c.Nonce, c.Ciphertext)
	if err != nil {
		return nil, false // wrong ENCRYPTION_KEY? — let the save through, as load.go skips such models
	}
	m.Secret, m.CredentialKind = secret, c.Kind
	if c.APIBase != "" {
		m.APIBase = c.APIBase
	}
	if m.Extra.VertexProject == "" {
		m.Extra.VertexProject = c.VertexProject
	}
	if m.Extra.VertexLocation == "" {
		m.Extra.VertexLocation = c.VertexLocation
	}
	return m, true
}

func (s *Server) reload(r *http.Request) {
	if err := s.Reload(r.Context()); err != nil {
		slog.Error("model registry reload failed", "err", err)
	}
}

func (s *Server) createModel(w http.ResponseWriter, r *http.Request) {
	var in repo.ModelInput
	if !decode(w, r, &in) || !validModel(w, &in) || !s.credentialFits(w, r, in) || !s.contextWindowFits(w, r, in, nil) {
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
	// Read the row before the context-window guard: an unchanged value must pass without a
	// probe, so the Enable/Disable toggle keeps working on a row that is already over the limit.
	before, err := s.Store.GetModel(r.Context(), id)
	if err != nil {
		storeErr(w, err)
		return
	}
	if !s.contextWindowFits(w, r, in, before) {
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
