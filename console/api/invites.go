package consoleapi

import (
	"errors"
	"net/http"
	"time"

	"tollgate/console/auth"
	"tollgate/console/rbac"
	"tollgate/core/api"
	"tollgate/core/db/repo"
)

type inviteView struct {
	repo.Invite
	Status string `json:"status"`
}

func (s *Server) listInvites(w http.ResponseWriter, r *http.Request) {
	invs, err := s.Store.ListInvites(r.Context())
	if err != nil {
		storeErr(w, err)
		return
	}
	out := make([]inviteView, len(invs))
	for i := range invs {
		out[i] = inviteView{invs[i], invs[i].Status()}
	}
	api.WriteJSON(w, 200, out)
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role     string `json:"role"`
		GroupID  string `json:"group_id"`
		Email    string `json:"email"`
		TTLHours int    `json:"ttl_hours"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !rbac.ValidRole(in.Role) {
		badRequest(w, "role must be admin, member, or viewer")
		return
	}
	if _, err := s.Store.GetGroup(r.Context(), in.GroupID); err != nil {
		badRequest(w, "unknown group")
		return
	}
	if in.TTLHours <= 0 || in.TTLHours > 24*30 {
		in.TTLHours = 24 * 7
	}
	me := auth.UserFrom(r.Context())
	token, inv, err := s.Store.CreateInvite(r.Context(), me.ID, in.Role, in.GroupID, in.Email, time.Duration(in.TTLHours)*time.Hour)
	if err != nil {
		storeErr(w, err)
		return
	}
	s.audit(r, "invite.create", "invites", inv.ID, nil, inv)
	api.WriteJSON(w, 201, map[string]any{"invite": inviteView{*inv, inv.Status()}, "url": s.BaseURL + "/invite/" + token})
}

func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Store.RevokeInvite(r.Context(), id); err != nil {
		storeErr(w, err)
		return
	}
	s.audit(r, "invite.revoke", "invites", id, nil, nil)
	w.WriteHeader(204)
}

func (s *Server) inviteInfo(w http.ResponseWriter, r *http.Request) {
	inv, err := s.Store.GetInviteByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		notFound(w)
		return
	}
	g, err := s.Store.GetGroup(r.Context(), inv.GroupID)
	if err != nil {
		storeErr(w, err)
		return
	}
	api.WriteJSON(w, 200, map[string]any{"role": inv.Role, "group_name": g.Name, "email": inv.Email, "status": inv.Status()})
}

func (s *Server) inviteAccept(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Name, Password string }
	if !decode(w, r, &in) {
		return
	}
	if len(in.Password) < 12 {
		badRequest(w, "password must be at least 12 characters")
		return
	}
	if in.Email == "" || in.Name == "" {
		badRequest(w, "email and name are required")
		return
	}
	u, err := s.Store.AcceptInvite(r.Context(), r.PathValue("token"), in.Email, in.Name, in.Password)
	switch {
	case errors.Is(err, repo.ErrInviteUnusable):
		api.WriteError(w, 410, "invalid_request_error", "invite_unusable", err.Error())
		return
	case errors.Is(err, repo.ErrConflict):
		api.WriteError(w, 409, "invalid_request_error", "email_taken", "an account with that email already exists")
		return
	case err != nil:
		storeErr(w, err)
		return
	}
	if err := s.Sessions.Start(r.Context(), w, u.ID); err != nil {
		storeErr(w, err)
		return
	}
	e := repo.AuditEntry{ActorID: &u.ID, ActorEmail: u.Email, Action: "invite.accept", Resource: "users", ResourceID: u.ID, IP: auth.ClientIP(r, s.TrustedProxies)}
	s.Store.InsertAudit(r.Context(), e)
	api.WriteJSON(w, 201, u)
}
