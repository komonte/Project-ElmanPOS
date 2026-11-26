package web

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sessions"
)

func TestParseFormMoney(t *testing.T) {
	tests := []struct {
		in   string
		want string // vacío: no se puede leer
	}{
		{"15000", "15000"},
		{"15.000", "15000"},
		{"1.500.000", "1500000"},
		{"$ 2.000", "2000"},
		{"1.500,50", "1500.5"},
		{"1,500.50", "1500.5"},
		{"12,5", "12.5"},
		{"12.5", "12.5"},
		{"12.50", "12.5"},
		{"0", "0"},
		{" 300 ", "300"},
		{"", ""},
		{"$", ""},
		{"diez", ""},
		{"1,2,3", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := parseFormMoney(tt.in)
			if tt.want == "" {
				if ok {
					t.Errorf("got %s, want an error", got)
				}
				return
			}
			if !ok || !got.Equal(decimal.RequireFromString(tt.want)) {
				t.Errorf("got %s (%v), want %s", got, ok, tt.want)
			}
		})
	}
}

func TestCashMessages(t *testing.T) {
	h := &WebHandler{}
	r := httptest.NewRequest("POST", "/cash/open", nil)
	for err, want := range map[error]string{
		model.ErrInvalidOpeningAmount:   "El efectivo inicial tiene que ser",
		sessions.ErrNoRegister:          "todavía no tiene caja",
		store.ErrSessionAlreadyOpen:     "La caja ya está abierta.",
		store.ErrSeveralActiveRegisters: "más de una caja activa",
	} {
		if got := h.errText(r, "es", err); !strings.Contains(got, want) {
			t.Errorf("%v: got %q, want %q", err, got, want)
		}
	}
}

func TestRenderCash(t *testing.T) {
	e := NewTemplateEngine()
	render := func(t *testing.T, data map[string]any) string {
		t.Helper()
		data["Section"], data["TitleKey"] = "cash", "cash.title"
		var buf bytes.Buffer
		if err := e.Render(&buf, "es", "cash.gohtml", data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	reg := &model.CashRegister{ID: 1, Name: "Caja 1"}

	t.Run("no register", func(t *testing.T) {
		html := render(t, map[string]any{"State": &sessions.State{}})
		for _, s := range []string{`href="/cash" aria-current="page"`, "todavía no tiene caja",
			`<form method="post" action="/cash/register">`, "Crear «Caja 1»"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		if strings.Contains(html, "opening_amount") {
			t.Error("cannot open without a register")
		}
	})

	t.Run("closed", func(t *testing.T) {
		html := render(t, map[string]any{"State": &sessions.State{Register: reg}, "Amount": "15.000",
			"Error": "El efectivo inicial tiene que ser un monto de 0 o más, con hasta 2 decimales."})
		for _, s := range []string{"Caja 1 está cerrada", `action="/cash/open"`, `name="opening_amount"`,
			`value="15.000"`, `role="alert"`, "Efectivo inicial (cambio)"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})

	t.Run("open", func(t *testing.T) {
		html := render(t, map[string]any{"State": &sessions.State{Register: reg, Session: &model.CashierSession{
			ID: 4, OpeningAmount: decimal.RequireFromString("15000"), OpenedAt: "2026-11-26T12:00:00Z"}}})
		for _, s := range []string{`id="cash-open"`, "Caja 1 está abierta", "Abierta desde 2026-11-26 09:00:00.",
			"$ 15.000,00"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		if strings.Contains(html, "opening_amount") {
			t.Error("an open register has no open form")
		}
	})

	t.Run("state error", func(t *testing.T) {
		html := render(t, map[string]any{"Error": "Hay más de una caja activa en el local."})
		if !strings.Contains(html, `role="alert"`) || strings.Contains(html, "<form") &&
			strings.Contains(html, "/cash/") {
			t.Errorf("error only: got %s", html)
		}
	})
}
