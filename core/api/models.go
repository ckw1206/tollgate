package api

import "net/http"

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	info := authFrom(r.Context())
	data := []map[string]any{}
	for _, m := range s.Registry.List() {
		if modelAllowed(info, m.Name) {
			data = append(data, map[string]any{"id": m.Name, "object": "model", "created": m.Created, "owned_by": m.Provider})
		}
	}
	WriteJSON(w, 200, map[string]any{"object": "list", "data": data})
}
