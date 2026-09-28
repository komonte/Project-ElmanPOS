// Package transport provides HTTP handlers, routing, and JSON request/response helpers.
package transport

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
)

type BrandService interface {
	CreateBrand(ctx context.Context , brand *model.Brand) (*model.Brand, error)
	GetAllBrands(ctx context.Context) ([]*model.Brand, error)
	GetBrandByID(ctx context.Context, id int64) (*model.Brand, error)
	SearchByName(ctx context.Context, query string) ([]*model.Brand, error)
	UpdateBrand(ctx context.Context, id int64, brand *model.Brand) (*model.Brand, error)
	DeleteBrand(ctx context.Context, id int64) error
}

type BrandHandler struct {
	service BrandService
}

func NewBrandHandler(s BrandService) *BrandHandler {
	return &BrandHandler{
		service: s,
	}
}

func (h *BrandHandler) HandleBrands(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
        var (
            brands []*model.Brand
            err    error
        )

        query := strings.TrimSpace(r.URL.Query().Get("name"))

        if query != "" {
            brands, err = h.service.SearchByName(ctx, query)
            if err != nil {
                errorJSON(w, http.StatusInternalServerError, "failed to search brands")
                return
            }
        } else {
            brands, err = h.service.GetAllBrands(ctx)
            if err != nil {
                errorJSON(w, http.StatusInternalServerError, "failed to retrieve brands")
                return
            }
        }

        if err := writeJSON(w, http.StatusOK, brands); err != nil {
            errorJSON(w, http.StatusInternalServerError, "failed to encode response")
            return
        }
	case http.MethodPost:
		var brand model.Brand

		if err := readJSON(r, &brand); err != nil {
			errorJSON(w, http.StatusBadRequest, "invalid request body")
			return
		}

		created, err := h.service.CreateBrand(ctx, &brand)
		if errors.Is(err, service.ErrBrandAlreadyExists) {
			errorJSON(w, http.StatusConflict, "brand already exists")
			return
		}
		if err != nil {
			errorJSON(w, http.StatusBadRequest, "failed to create brand")
			return
		}

		if err := writeJSON(w, http.StatusCreated, created); err != nil {
			errorJSON(w, http.StatusInternalServerError, "failed to encode response")
			return
		}

	default:
		errorJSON(w, http.StatusMethodNotAllowed, "method not available")
	}
}

func (h *BrandHandler) HandleBrandByID(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	idStr := strings.TrimPrefix(r.URL.Path, "/brand/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid brand id")
		return
	}

	switch r.Method {
	case http.MethodGet:
		brand, err := h.service.GetBrandByID(ctx, id)
		if err != nil {
			errorJSON(w, http.StatusNotFound, "brand not found")
			return
		}

		if err := writeJSON(w, http.StatusOK, brand); err != nil {
			errorJSON(w, http.StatusInternalServerError, "failed to encode response")
			return
		}
	case http.MethodPut:
		var brand model.Brand

		if err := readJSON(r, &brand); err != nil {
			errorJSON(w, http.StatusBadRequest, "invalid request body")
			return
		}

		updated, err := h.service.UpdateBrand(ctx, id, &brand)
		if err != nil {
			if errors.Is(err, service.ErrBrandNotFound) {
				errorJSON(w, http.StatusNotFound, "brand not found")
				return
			}
			if errors.Is(err, service.ErrBrandAlreadyExists) {
				errorJSON(w, http.StatusConflict, "brand name already exists")
				return
			}
			errorJSON(w, http.StatusInternalServerError, "failed to update brand")
			return
		}

		if err := writeJSON(w, http.StatusOK, updated); err != nil {
			errorJSON(w, http.StatusInternalServerError, "failed to encode response")
			return
		}

	case http.MethodDelete:
		if err := h.service.DeleteBrand(ctx, id); err != nil {
			if errors.Is(err, service.ErrBrandNotFound) {
				errorJSON(w, http.StatusNotFound, "brand not found")
				return
			}
			errorJSON(w, http.StatusInternalServerError, "failed to delete brand")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	default:
		errorJSON(w, http.StatusMethodNotAllowed, "method not available")
	}
}
