package consoleapi

import (
	"encoding/json"
	"net/http"
	"time"

	"tollgate/console/auth"
	"tollgate/core/api"
	"tollgate/core/db/repo"
)

// playgroundModel is what the console needs from a model: identity plus the
// prices, so client-side cost math mirrors llm.Cost (streams carry no
// X-Tollgate-Cost). No extra/api_base/credential_id — those are redacted for
// anyone without models:write, and the playground needs none of them.
type playgroundModel struct {
	Name             string `json:"name"`
	Provider         string `json:"provider"`
	InputMicros      int64  `json:"input_micros"`
	OutputMicros     int64  `json:"output_micros"`
	CacheReadMicros  *int64 `json:"cache_read_micros"`
	CacheWriteMicros *int64 `json:"cache_write_micros"`
}

// playgroundModels: the enabled models the caller may run — the owner's group
// (empty = all) union extra grants — sorted by name, like ListModels.
func (s *Server) playgroundModels(w http.ResponseWriter, r *http.Request) {
	me := auth.UserFrom(r.Context())
	g, err := s.Store.GetGroup(r.Context(), me.GroupID)
	if err != nil {
		storeErr(w, err)
		return
	}
	ms, err := s.Store.ListModels(r.Context())
	if err != nil {
		storeErr(w, err)
		return
	}
	out := []playgroundModel{}
	for _, m := range ms {
		if !m.Enabled || repo.Access(g.Models, me.ExtraModels, m.Name) == "" {
			continue
		}
		out = append(out, playgroundModel{Name: m.Name, Provider: m.Provider,
			InputMicros: m.PriceInputMicros, OutputMicros: m.PriceOutputMicros,
			CacheReadMicros: m.PriceCacheReadMicros, CacheWriteMicros: m.PriceCacheWriteMicros})
	}
	api.WriteJSON(w, 200, out)
}

// playgroundChat runs the gateway chat path as the signed-in user: a key-less
// principal, so limits and model access come from the user's own group only
// and the spend row bills the group (source "playground", no key).
func (s *Server) playgroundChat(w http.ResponseWriter, r *http.Request) {
	me := auth.UserFrom(r.Context())
	g, err := s.Store.GetGroup(r.Context(), me.GroupID)
	if err != nil {
		storeErr(w, err)
		return
	}
	s.Gateway.ChatAs(w, r, &repo.AuthInfo{
		Key:         repo.APIKey{UserID: me.ID, GroupID: me.GroupID},
		Group:       *g,
		UserEmail:   me.Email,
		GroupModels: g.Models,
		ExtraModels: me.ExtraModels,
	})
}

// sessionView is the list row: enough to render and re-open a conversation
// without loading its data.
type sessionView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updated_at"`
}

type sessionInput struct {
	Name string          `json:"name"`
	Data json.RawMessage `json:"data"`
}

// validSessionInput mirrors the column constraints so bad bodies are 400s:
// name is 1-120 characters, and data is a JSON object (the console's own
// state) or absent.
func validSessionInput(w http.ResponseWriter, in sessionInput) bool {
	if len(in.Name) == 0 || len(in.Name) > 120 {
		badRequest(w, "name must be 1-120 characters")
		return false
	}
	if len(in.Data) > 0 && in.Data[0] != '{' {
		badRequest(w, "data must be a JSON object")
		return false
	}
	return true
}

// sessionID validates the path id before it reaches the repo, whose queries
// compare it straight against a uuid column: a non-UUID must 404, never 500.
func sessionID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !isUUID(id) {
		notFound(w)
		return "", false
	}
	return id, true
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return false
			}
		}
	}
	return true
}

func (s *Server) listPlaygroundSessions(w http.ResponseWriter, r *http.Request) {
	me := auth.UserFrom(r.Context())
	ps, err := s.Store.ListPlaygroundSessions(r.Context(), me.ID)
	if err != nil {
		storeErr(w, err)
		return
	}
	out := make([]sessionView, 0, len(ps))
	for _, p := range ps {
		out = append(out, sessionView{ID: p.ID, Name: p.Name, UpdatedAt: p.UpdatedAt})
	}
	api.WriteJSON(w, 200, out)
}

func (s *Server) getPlaygroundSession(w http.ResponseWriter, r *http.Request) {
	id, ok := sessionID(w, r)
	if !ok {
		return
	}
	p, err := s.Store.GetPlaygroundSession(r.Context(), auth.UserFrom(r.Context()).ID, id)
	if err != nil {
		storeErr(w, err)
		return
	}
	api.WriteJSON(w, 200, p)
}

func (s *Server) createPlaygroundSession(w http.ResponseWriter, r *http.Request) {
	var in sessionInput
	if !decode(w, r, &in) || !validSessionInput(w, in) {
		return
	}
	p, err := s.Store.CreatePlaygroundSession(r.Context(), auth.UserFrom(r.Context()).ID, in.Name, in.Data)
	if err != nil {
		storeErr(w, err)
		return
	}
	api.WriteJSON(w, 201, p)
}

func (s *Server) updatePlaygroundSession(w http.ResponseWriter, r *http.Request) {
	id, ok := sessionID(w, r)
	if !ok {
		return
	}
	var in sessionInput
	if !decode(w, r, &in) || !validSessionInput(w, in) {
		return
	}
	p, err := s.Store.UpdatePlaygroundSession(r.Context(), auth.UserFrom(r.Context()).ID, id, in.Name, in.Data)
	if err != nil {
		storeErr(w, err)
		return
	}
	api.WriteJSON(w, 200, p)
}

func (s *Server) deletePlaygroundSession(w http.ResponseWriter, r *http.Request) {
	id, ok := sessionID(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeletePlaygroundSession(r.Context(), auth.UserFrom(r.Context()).ID, id); err != nil {
		storeErr(w, err)
		return
	}
	w.WriteHeader(204)
}
