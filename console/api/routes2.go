package consoleapi

import (
	"net/http"

	"tollgate/console/rbac"
)

func (s *Server) extraRoutes() map[string]http.Handler {
	req := s.Sessions.Require
	return map[string]http.Handler{
		"GET " + p + "/gate":                         req(rbac.SpendReadOwn, s.gate),
		"GET " + p + "/playground/models":            req(rbac.PlaygroundUse, s.playgroundModels),
		"POST " + p + "/playground/chat/completions": req(rbac.PlaygroundUse, s.playgroundChat),
		"GET " + p + "/playground/sessions":          req(rbac.PlaygroundUse, s.listPlaygroundSessions),
		"POST " + p + "/playground/sessions":         req(rbac.PlaygroundUse, s.createPlaygroundSession),
		"GET " + p + "/playground/sessions/{id}":     req(rbac.PlaygroundUse, s.getPlaygroundSession),
		"PUT " + p + "/playground/sessions/{id}":     req(rbac.PlaygroundUse, s.updatePlaygroundSession),
		"DELETE " + p + "/playground/sessions/{id}":  req(rbac.PlaygroundUse, s.deletePlaygroundSession),
		"GET " + p + "/credentials":                  req(rbac.ModelsWrite, s.listCredentials),
		"POST " + p + "/credentials":                 req(rbac.ModelsWrite, s.createCredential),
		"DELETE " + p + "/credentials/{id}":          req(rbac.ModelsWrite, s.deleteCredential),
		"POST " + p + "/credentials/test":            req(rbac.ModelsWrite, s.testCredential),
		"PUT " + p + "/credentials/{id}":             req(rbac.ModelsWrite, s.updateCredential),
		"GET " + p + "/models":                       req(rbac.ModelsRead, s.listModels),
		"POST " + p + "/models":                      req(rbac.ModelsWrite, s.createModel),
		"PUT " + p + "/models/{id}":                  req(rbac.ModelsWrite, s.updateModel),
		"DELETE " + p + "/models/{id}":               req(rbac.ModelsWrite, s.deleteModel),
		"GET " + p + "/spend":                        req(rbac.SpendReadOwn, s.spend),
		"GET " + p + "/spend/requests":               req(rbac.SpendReadOwn, s.spendRequests),
		"GET " + p + "/audit":                        req(rbac.AuditRead, s.auditLog),
		"GET " + p + "/settings":                     req(rbac.SettingsRead, s.getSettings),
		"PUT " + p + "/settings":                     req(rbac.SettingsWrite, s.putSettings),
		"POST " + p + "/settings/webhook/test":       req(rbac.SettingsWrite, s.testWebhook),
		// Admit-path instrumentation (#33). Admin-only via metrics:read; see
		// console/api/metrics.go for the read and core/api/metrics.go for why
		// the gateway port also serves it unauthenticated.
		"GET " + p + "/metrics": req(rbac.MetricsRead, s.metrics),
	}
}
