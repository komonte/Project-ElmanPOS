// Package transport provides HTTP handlers, routing, and JSON request/response helpers.
package transport

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

type BrandService interface {
	CreateBrand(brand *model.Brand) (*model.Brand, error)
	GetAllBrands() ([]*model.Brand, error)
	GetBrandByID(id int64) (*model.Brand, error)
	SearchByName(query string) ([]*model.Brand, error)
	UpdateBrand(id int64, brand *model.Brand) (*model.Brand, error)
	DeleteBrand(id int64) error
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
	switch r.Method {
	case http.MethodGet:
        var (
            brands []*model.Brand
            err    error
        )

        query := strings.TrimSpace(r.URL.Query().Get("name"))

        if query != "" {
            brands, err = h.service.SearchByName(query)
            if err != nil {
                errorJSON(w, http.StatusInternalServerError, "failed to search brands")
                return
            }
        } else {
            brands, err = h.service.GetAllBrands()
            if err != nil {
                errorJSON(w, http.StatusInternalServerError, "failed to retrieve brands")
                return
            }
        }

        // 3. Respondemos con 200 OK y el listado resultante
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

		created, err := h.service.CreateBrand(&brand)
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
	idStr := strings.TrimPrefix(r.URL.Path, "/brand/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid brand id")
		return
	}

	switch r.Method {
	case http.MethodGet:
		brand, err := h.service.GetBrandByID(id)
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

		updated, err := h.service.UpdateBrand(id, &brand)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				errorJSON(w, http.StatusNotFound, "brand not found")
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
		if err := h.service.DeleteBrand(id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
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
