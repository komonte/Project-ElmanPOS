package transport

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
)

type ResourceHandler[T any] struct {
	svc  *service.Service[T]
	label string
}

func NewResourceHandler[T any](s *service.Service[T], label string) *ResourceHandler[T] {
	return &ResourceHandler[T]{
		svc:   s,
		label: label,
	}
}

// List handles GET {base}, optionally filtered with ?name=.
func (h *ResourceHandler[T]) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var entities []*T
	var err error

	query := strings.TrimSpace(r.URL.Query().Get("name"))
	if query != "" {
		entities, err = h.svc.SearchByName(ctx, query)
		if err != nil {
			errorJSON(w, http.StatusInternalServerError,
				fmt.Sprintf("failed to search %ss", h.label))
			return
		}
	} else {
		entities, err = h.svc.GetAll(ctx)
		if err != nil {
			errorJSON(w, http.StatusInternalServerError,
				fmt.Sprintf("failed to retrieve %ss", h.label))
			return
		}
	}

	if err := writeJSON(w, http.StatusOK, entities); err != nil {
		errorJSON(w, http.StatusInternalServerError, "failed to encode response")
	}
}

// Create handles POST {base}.
func (h *ResourceHandler[T]) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var entity T

	if err := readJSON(r, &entity); err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}

	created, err := h.svc.Create(ctx, &entity)
	if errors.Is(err, service.ErrAlreadyExists) {
		errorJSON(w, http.StatusConflict,
			fmt.Sprintf("%s already exists", h.label))
		return
	}
	if err != nil {
		errorJSON(w, http.StatusBadRequest,
			fmt.Sprintf("invalid %s", h.label))
		return
	}

	if err := writeJSON(w, http.StatusCreated, created); err != nil {
		errorJSON(w, http.StatusInternalServerError, "failed to encode response")
	}
}

// parseID reads the {id} path wildcard. On failure it writes a 400 and returns false.
func (h *ResourceHandler[T]) parseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		errorJSON(w, http.StatusBadRequest,
			fmt.Sprintf("invalid %s id", h.label))
		return 0, false
	}
	return id, true
}

// Get handles GET {base}/{id}.
func (h *ResourceHandler[T]) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, ok := h.parseID(w, r)
	if !ok {
		return
	}

	entity, err := h.svc.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			errorJSON(w, http.StatusNotFound,
				fmt.Sprintf("%s not found", h.label))
			return
		}
		errorJSON(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := writeJSON(w, http.StatusOK, entity); err != nil {
		errorJSON(w, http.StatusInternalServerError, "failed to encode response")
	}
}

// Update handles PUT {base}/{id}.
func (h *ResourceHandler[T]) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, ok := h.parseID(w, r)
	if !ok {
		return
	}

	var entity T

	if err := readJSON(r, &entity); err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}

	updated, err := h.svc.Update(ctx, id, &entity)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			errorJSON(w, http.StatusNotFound,
				fmt.Sprintf("%s not found", h.label))
			return
		}
		if errors.Is(err, service.ErrAlreadyExists) {
			errorJSON(w, http.StatusConflict,
				fmt.Sprintf("%s already exists", h.label))
			return
		}
		errorJSON(w, http.StatusBadRequest,
			fmt.Sprintf("invalid %s", h.label))
		return
	}

	if err := writeJSON(w, http.StatusOK, updated); err != nil {
		errorJSON(w, http.StatusInternalServerError, "failed to encode response")
	}
}

// Delete handles DELETE {base}/{id}.
func (h *ResourceHandler[T]) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, ok := h.parseID(w, r)
	if !ok {
		return
	}

	err := h.svc.Delete(ctx, id)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			errorJSON(w, http.StatusNotFound,
				fmt.Sprintf("%s not found", h.label))
			return
		}
		if errors.Is(err, service.ErrInUse) {
			errorJSON(w, http.StatusConflict,
				fmt.Sprintf("%s is in use", h.label))
			return
		}
		errorJSON(w, http.StatusInternalServerError, "internal error")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
