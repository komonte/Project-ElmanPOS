package transport

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
)

type ResourceHandler[T any] struct {
	service *Service[T]
	label   string
}

func NewResourceHandler[T any](s *Service[T], label string) *ResourceHandler[T] {
	return &ResourceHandler[T]{
		service: s,
		label:   label,
	}
}

func (h *ResourceHandler[T]) HandleCollection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		var entities []*T
		var err error

		query := strings.TrimSpace(r.URL.Query().Get("name"))
		if query != "" {
			entities, err = h.service.SearchByName(ctx, query)
			if err != nil {
				errorJSON(w, http.StatusInternalServerError,
					fmt.Sprintf("failed to search %ss", h.label))
				return
			}
		} else {
			entities, err = h.service.GetAll(ctx)
			if err != nil {
				errorJSON(w, http.StatusInternalServerError,
					fmt.Sprintf("failed to retrieve %ss", h.label))
				return
			}
		}

		if err := writeJSON(w, http.StatusOK, entities); err != nil {
			errorJSON(w, http.StatusInternalServerError, "failed to encode response")
		}

	case http.MethodPost:
		var entity T

		if err := readJSON(r, &entity); err != nil {
			errorJSON(w, http.StatusBadRequest, "invalid request body")
			return
		}

		created, err := h.service.Create(ctx, &entity)
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

	default:
		errorJSON(w, http.StatusMethodNotAllowed, "method not available")
	}
}

func (h *ResourceHandler[T]) HandleEntity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	prefix := "/" + h.label + "/"
	idStr := strings.TrimPrefix(r.URL.Path, prefix)
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		errorJSON(w, http.StatusBadRequest,
			fmt.Sprintf("invalid %s id", h.label))
		return
	}

	switch r.Method {
	case http.MethodGet:
		entity, err := h.service.GetByID(ctx, id)
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

	case http.MethodPut:
		var entity T

		if err := readJSON(r, &entity); err != nil {
			errorJSON(w, http.StatusBadRequest, "invalid request body")
			return
		}

		updated, err := h.service.Update(ctx, id, &entity)
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

	case http.MethodDelete:
		err := h.service.Delete(ctx, id)
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

	default:
		errorJSON(w, http.StatusMethodNotAllowed, "method not available")
	}
}