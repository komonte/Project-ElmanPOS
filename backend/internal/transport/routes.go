package transport

import (
	"net/http"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

// Register mounts the CRUD endpoints of h under base (e.g. "/api/brands").
// Methods are part of the patterns, so ServeMux answers 405 (with an Allow
// header) for unsupported methods and r.PathValue("id") holds the id.
func (h *ResourceHandler[T]) Register(mux *http.ServeMux, base string) {
	mux.HandleFunc("GET "+base, h.List)
	mux.HandleFunc("POST "+base, h.Create)
	mux.HandleFunc("GET "+base+"/{id}", h.Get)
	mux.HandleFunc("PUT "+base+"/{id}", h.Update)
	mux.HandleFunc("DELETE "+base+"/{id}", h.Delete)
}

// RegisterAPIRoutes mounts the JSON API on mux. main.go and the router tests
// both call it, so tests exercise the exact same routing as production.
func RegisterAPIRoutes(mux *http.ServeMux, brands *ResourceHandler[model.Brand], taxes *ResourceHandler[model.Tax]) {
	brands.Register(mux, "/api/brands")
	taxes.Register(mux, "/api/taxes")
}
