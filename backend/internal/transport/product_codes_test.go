package transport_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// fakeCodes es un store de códigos en memoria para productcodes.Service.
// Arranca con el producto 1 (OREO, ver newTestMux) dueño del código 10.
// Cada test arma su propio mux, así no se pisan.
type fakeCodes struct {
	codes  map[int64]*model.ProductCode
	nextID int64
}

func newFakeCodes() *fakeCodes {
	return &fakeCodes{
		codes: map[int64]*model.ProductCode{
			10: {ID: 10, ProductID: 1, CodeType: model.CodeTypeEAN13, Code: "7790070991232",
				ProductSKU: "OREO-200", ProductName: "GALLETITAS OREO 200G"},
		},
		nextID: 11,
	}
}

func (f *fakeCodes) ListByProduct(_ context.Context, productID int64) ([]*model.ProductCode, error) {
	list := make([]*model.ProductCode, 0)
	for _, c := range f.codes {
		if c.ProductID == productID {
			list = append(list, c)
		}
	}
	return list, nil
}

func (f *fakeCodes) FindByCode(_ context.Context, code string) (*model.ProductCode, error) {
	for _, c := range f.codes {
		if c.Code == code {
			return c, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeCodes) Add(_ context.Context, c *model.ProductCode) (*model.ProductCode, error) {
	if c.ProductID != 1 {
		return nil, store.ErrInvalidReference
	}
	added := *c
	added.ID = f.nextID
	f.nextID++
	f.codes[added.ID] = &added
	return &added, nil
}

func (f *fakeCodes) Delete(_ context.Context, productID, codeID int64) error {
	c, ok := f.codes[codeID]
	if !ok || c.ProductID != productID {
		return store.ErrNotFound
	}
	delete(f.codes, codeID)
	return nil
}

func TestProductCodesAdd(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		path     string
		body     string
		want     int
		wantBody string // texto que tiene que aparecer en la respuesta
	}{
		{"normalized and typed by the server", "/api/products/1/codes", `{"code": " 40170725 "}`,
			http.StatusCreated, `"code_type":"EAN8","code":"0000040170725"`},
		{"taken code names its owner", "/api/products/1/codes", `{"code": "7790070991232"}`,
			http.StatusConflict, "already belongs to product OREO-200 GALLETITAS OREO 200G"},
		{"invalid code", "/api/products/1/codes", `{"code": "77-90"}`,
			http.StatusBadRequest, "barcode can only contain digits"},
		{"empty code", "/api/products/1/codes", `{"code": ""}`,
			http.StatusBadRequest, "barcode is required"},
		{"type is not accepted from the client", "/api/products/1/codes", `{"code": "123", "code_type": "EAN13"}`,
			http.StatusBadRequest, "invalid request body"},
		{"missing product", "/api/products/999/codes", `{"code": "9770401"}`,
			http.StatusNotFound, "product not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			newTestMux().ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("status: got %d, want %d. Body: %s", rec.Code, tc.want, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("body %q does not contain %q", rec.Body.String(), tc.wantBody)
			}
		})
	}
}

func TestProductCodesList(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/products/1/codes", nil)
	rec := httptest.NewRecorder()
	newTestMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"code":"7790070991232"`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}
