package api

import "net/http"

// NewRouter builds the API routes and applies common middleware.
func NewRouter(h *Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/set", h.Set)
	mux.HandleFunc("/get", h.Get)
	mux.HandleFunc("/delete", h.Delete)

	var root http.Handler = mux
	root = panicRecoveryMiddleware(root, h.Logger)
	root = requestIDMiddleware(root)
	return root
}
