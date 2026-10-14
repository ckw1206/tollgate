package consoleapi

import (
	"net/http"

	"tollgate/console/rbac"
)

func (s *Server) extraRoutes() map[string]http.Handler {
	req := s.Sessions.Require
	return map[string]http.Handler{
		"GET " + p + "/credentials":         req(rbac.ModelsWrite, s.listCredentials),
		"POST " + p + "/credentials":        req(rbac.ModelsWrite, s.createCredential),
		"DELETE " + p + "/credentials/{id}": req(rbac.ModelsWrite, s.deleteCredential),
		"POST " + p + "/credentials/test":   req(rbac.ModelsWrite, s.testCredential),
		"PUT " + p + "/credentials/{id}":    req(rbac.ModelsWrite, s.updateCredential),
		"GET " + p + "/models":              req(rbac.ModelsRead, s.listModels),
		"POST " + p + "/models":             req(rbac.ModelsWrite, s.createModel),
		"PUT " + p + "/models/{id}":         req(rbac.ModelsWrite, s.updateModel),
		"DELETE " + p + "/models/{id}":      req(rbac.ModelsWrite, s.deleteModel),
		"GET " + p + "/spend":               req(rbac.SpendReadOwn, s.spend),
		"GET " + p + "/spend/requests":      req(rbac.SpendReadOwn, s.spendRequests),
		"GET " + p + "/audit":               req(rbac.AuditRead, s.auditLog),
		"GET " + p + "/settings":            req(rbac.SettingsRead, s.getSettings),
		"PUT " + p + "/settings":            req(rbac.SettingsWrite, s.putSettings),
	}
}
