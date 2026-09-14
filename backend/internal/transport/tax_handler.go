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

type TaxService interface {
	GetTaxByID(ctx context.Context, id int64) (*model.Tax, error)
	GetAllTaxes(ctx context.Context) ([]*model.Tax, error)
	CreateTax(ctx context.Context, tax *model.Tax) (*model.Tax, error)
	UpdateTax(ctx context.Context, tax *model.Tax) (*model.Tax, error)
	DeleteTax(ctx context.Context, id int64) error
	SearchByName(ctx context.Context, query string) ([]*model.Tax, error)
}

type TaxHandler struct {
	service TaxService
}

func NewTaxHandler(s TaxService) *TaxHandler {
	return &TaxHandler{
		service: s,
	}
}

func (h *TaxHandler) HandleTaxes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		var (
			taxes []*model.Tax
			err   error
		)

		query := strings.TrimSpace(r.URL.Query().Get("name"))

		if query != "" {
			taxes, err = h.service.SearchByName(ctx, query)
			if err != nil {
				errorJSON(w, http.StatusInternalServerError, "failed to search taxes")
				return
			}
		} else {
			taxes, err = h.service.GetAllTaxes(ctx)
			if err != nil {
				errorJSON(w, http.StatusInternalServerError, "failed to retrieve taxes")
				return
			}
		}

		if err := writeJSON(w, http.StatusOK, taxes); err != nil {
			errorJSON(w, http.StatusInternalServerError, "failed to encode response")
			return
		}
	case http.MethodPost:
		var tax model.Tax

		if err := readJSON(r, &tax); err != nil {
			errorJSON(w, http.StatusBadRequest, "invalid request body")
			return
		}

		created, err := h.service.CreateTax(ctx, &tax)

		if errors.Is(err, service.ErrTaxAlreadyExists) {
			errorJSON(w, http.StatusConflict, "tax already exists")
			return
		}

		if err != nil {
			errorJSON(w, http.StatusBadRequest, "failed to create tax")
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

func (h *TaxHandler) HandleTaxByID(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	idStr := strings.TrimPrefix(r.URL.Path, "/tax/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid tax id")
		return
	}

	switch r.Method {
	case http.MethodGet:
		tax, err := h.service.GetTaxByID(ctx, id)
		if err != nil {
			errorJSON(w, http.StatusNotFound, "tax not found")
			return
		}

		if err := writeJSON(w, http.StatusOK, tax); err != nil {
			errorJSON(w, http.StatusInternalServerError, "failed to encode response")
			return
		}
	case http.MethodPut:
		var tax model.Tax

		if err := readJSON(r, &tax); err != nil {
			errorJSON(w, http.StatusBadRequest, "invalid request body")
			return
		}

		updated, err := h.service.UpdateTax(ctx, &tax)
		if err != nil {
			if errors.Is(err, service.ErrTaxNotFound) {
				errorJSON(w, http.StatusNotFound, "tax not found")
				return
			}
			if errors.Is(err, service.ErrTaxAlreadyExists) {
				errorJSON(w, http.StatusConflict, "tax name already exists")
				return
			}
			errorJSON(w, http.StatusInternalServerError, "failed to update tax")
			return
		}

		if err := writeJSON(w, http.StatusOK, updated); err != nil {
			errorJSON(w, http.StatusInternalServerError, "failed to encode response")
			return
		}

	case http.MethodDelete:
		if err := h.service.DeleteTax(ctx, id); err != nil {
			if errors.Is(err, service.ErrTaxNotFound) {
				errorJSON(w, http.StatusNotFound, "tax not found")
				return
			}
			errorJSON(w, http.StatusInternalServerError, "failed to delete tax")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	default:
		errorJSON(w, http.StatusMethodNotAllowed, "method not available")
	}
}
