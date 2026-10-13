package api

import (
	"encoding/json"
	"net/http"
)

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// WriteError writes OpenAI's error shape. code is a string, per the OpenAI API.
func WriteError(w http.ResponseWriter, status int, typ, code, msg string) {
	WriteJSON(w, status, map[string]any{"error": map[string]any{"message": msg, "type": typ, "code": code}})
}
