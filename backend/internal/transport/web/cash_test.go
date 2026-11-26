package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		store.ErrSessionNotOpen:         "La caja ya estaba cerrada.",
		model.ErrInvalidDeclaredAmount:  "escribí un monto de 0 o más",
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

	openState := &sessions.State{Register: reg, Session: &model.CashierSession{
		ID: 4, OpeningAmount: decimal.RequireFromString("15000"), OpenedAt: "2026-11-26T12:00:00Z"}}
	fields := []closeField{
		{Method: model.PaymentCash, Count: 5},
		{Method: model.PaymentDebit, Count: 1},
		{Method: model.PaymentCredit},
		{Method: model.PaymentTransfer, Count: 3},
		{Method: model.PaymentQR},
	}

	t.Run("open", func(t *testing.T) {
		html := render(t, map[string]any{"State": openState, "CloseFields": fields})
		for _, s := range []string{`id="cash-open"`, "Caja 1 está abierta", "Abierta desde 2026-11-26 09:00:00.",
			"$ 15.000,00",
			// Cerrar: plegado, con la sesión del formulario y un campo por medio.
			`<details id="cash-close"`, "Cerrar la caja", `action="/cash/close"`, `onsubmit="return cashConfirmClose(this)"`,
			`name="session_id" value="4"`,
			`name="declared_CASH"`, `name="declared_DEBIT_CARD"`, `name="declared_CREDIT_CARD"`,
			`name="declared_TRANSFER"`, `name="declared_QR"`, `onkeydown="cashNextField(event, this)"`,
			// Dónde mirar y cuántos cobros, nunca cuánto se espera.
			"Contá los billetes y monedas del cajón.", "Incluí el cambio con el que abriste:", `font-bold text-ink">$ 15.000,00</span>.`,
			"cierre de lote del posnet", "home banking", "Mercado Pago",
			"Hoy hubo 1 cobro con este medio.", "Hoy hubo 3 cobros con este medio.", "Hoy no hubo cobros con este medio.",
			`data-label="Transferencia"`, `data-empty="vacío (cuenta como $ 0)"`,
			"function cashConfirmClose(form)", "function cashNextField(e, input)",
		} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		// Plegado y sin aviso: todavía no se intentó cerrar.
		for _, s := range []string{"opening_amount", `rounded-md bg-surface" open>`, `role="alert" class="p-4 bg-danger-soft`} {
			if strings.Contains(html, s) {
				t.Errorf("unexpected %q", s)
			}
		}
		// Del efectivo no se cuentan cobros (se cuenta la plata).
		if strings.Count(html, "data-count=") != 4 {
			t.Errorf("payment counts: want 4 (all but cash), got %d", strings.Count(html, "data-count="))
		}
	})

	t.Run("close error", func(t *testing.T) {
		bad := append([]closeField(nil), fields...)
		bad[1].Typed, bad[1].Invalid = "-5", true
		bad[0].Typed = "20.000"
		html := render(t, map[string]any{"State": openState, "CloseFields": bad,
			"CloseError": "Débito: escribí un monto de 0 o más, con hasta 2 decimales."})
		for _, s := range []string{`rounded-md bg-surface" open>`, `role="alert"`, "Débito: escribí un monto",
			`value="20.000"`, `value="-5"`, `aria-invalid="true" autofocus`, "border-danger"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
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

func TestDeclaredFromForm(t *testing.T) {
	r := postForm(url.Values{"declared_CASH": {" 20.000 "}, "declared_DEBIT_CARD": {"3000"},
		"declared_QR": {""}, "declared_TRANSFER": {"1.500,50"}})
	declared, typed, bad := declaredFromForm(r)
	if bad != "" || len(declared) != 3 {
		t.Fatalf("got %v, bad %q", declared, bad)
	}
	want := map[string]string{model.PaymentCash: "20000", model.PaymentDebit: "3000", model.PaymentTransfer: "1500.5"}
	for _, a := range declared {
		if !a.DeclaredAmount.Equal(decimal.RequireFromString(want[a.Method])) {
			t.Errorf("%s: got %s, want %s", a.Method, a.DeclaredAmount, want[a.Method])
		}
	}
	if typed[model.PaymentCash] != "20.000" || typed[model.PaymentQR] != "" {
		t.Errorf("typed: got %v", typed)
	}

	for in, method := range map[string]string{"veinte": "DEBIT_CARD", "-5": "DEBIT_CARD", "1,005": "DEBIT_CARD"} {
		_, typed, bad := declaredFromForm(postForm(url.Values{"declared_CASH": {"100"}, "declared_DEBIT_CARD": {in}}))
		if bad != method || typed[model.PaymentDebit] != in {
			t.Errorf("%q: got bad %q, typed %v", in, bad, typed)
		}
	}
}

func TestCloseRows(t *testing.T) {
	sum, err := model.NewCloseSummary(decimal.RequireFromString("5000"),
		map[string]decimal.Decimal{model.PaymentCash: decimal.RequireFromString("2000"),
			model.PaymentQR: decimal.RequireFromString("700")},
		[]*model.CashAudit{{Method: model.PaymentCash, DeclaredAmount: decimal.RequireFromString("6950")},
			{Method: model.PaymentTransfer, DeclaredAmount: decimal.RequireFromString("100")}})
	if err != nil {
		t.Fatal(err)
	}
	rows := closeRows(sum)
	// Efectivo siempre; QR (esperado) y transferencia (declarada); débito y
	// crédito sin nada, no.
	if len(rows) != 3 || rows[0].Method != model.PaymentCash || rows[1].Method != model.PaymentTransfer ||
		rows[2].Method != model.PaymentQR {
		t.Fatalf("rows: got %+v", rows)
	}
	if !rows[0].Diff.Equal(decimal.RequireFromString("50")) || !rows[0].Short {
		t.Errorf("cash: got %+v", rows[0])
	}
	if !rows[1].Diff.Equal(decimal.RequireFromString("100")) || rows[1].Short {
		t.Errorf("transfer: got %+v", rows[1])
	}
}

func TestRenderCashSession(t *testing.T) {
	e := NewTemplateEngine()
	render := func(t *testing.T, sum *sessions.Summary) string {
		t.Helper()
		var buf bytes.Buffer
		if err := e.Render(&buf, "es", "cash_session.gohtml", map[string]any{
			"Section": "cash", "TitleKey": "cash.summary_title", "Summary": sum,
			"ClosedAt": "2026-11-26T23:30:00Z", "Rows": closeRows(sum.Close), "HasDifference": sum.Close.HasDifference(),
		}); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	sess := &model.CashierSession{ID: 4, RegisterName: "Caja 1", OpeningAmount: decimal.RequireFromString("5000"),
		OpenedAt: "2026-11-26T12:00:00Z"}

	diff, _ := model.NewCloseSummary(decimal.RequireFromString("5000"),
		map[string]decimal.Decimal{model.PaymentCash: decimal.RequireFromString("2000"),
			model.PaymentQR: decimal.RequireFromString("700")},
		[]*model.CashAudit{{Method: model.PaymentCash, DeclaredAmount: decimal.RequireFromString("6950")},
			{Method: model.PaymentTransfer, DeclaredAmount: decimal.RequireFromString("100")},
			{Method: model.PaymentQR, DeclaredAmount: decimal.RequireFromString("700")}})
	html := render(t, &sessions.Summary{Session: sess, Close: diff, Sales: 2, Voided: 1,
		Total: decimal.RequireFromString("5700")})
	for _, s := range []string{
		`href="/cash" aria-current="page"`, "Cierre de caja", "Caja 1", "abierta 2026-11-26 09:00:00, cerrada 2026-11-26 20:30:00",
		`id="cash-verdict"`, "Hay diferencias.", "transferencia que todavía no llegó",
		"Tendría que haber", "Contado", "Diferencia",
		`data-method="CASH"`, "con el cambio inicial de $ 5.000,00", "$ 7.000,00", "$ 6.950,00", "Faltan $ 50,00",
		`data-method="TRANSFER"`, "Sobran $ 100,00",
		`data-method="QR"`, "Justo",
		"2 ventas", "1 anulada", "Vendido", "$ 5.700,00", `href="/cash" class="btn-primary"`,
	} {
		if !strings.Contains(html, s) {
			t.Errorf("missing %q", s)
		}
	}
	if strings.Contains(html, `data-method="DEBIT_CARD"`) {
		t.Error("a method with nothing must not show")
	}

	even, _ := model.NewCloseSummary(decimal.RequireFromString("5000"), nil,
		[]*model.CashAudit{{Method: model.PaymentCash, DeclaredAmount: decimal.RequireFromString("5000")}})
	html = render(t, &sessions.Summary{Session: sess, Close: even, Sales: 1, Total: decimal.RequireFromString("0")})
	for _, s := range []string{"La caja cierra justo.", "1 venta ·", "0 anuladas"} {
		if !strings.Contains(html, s) {
			t.Errorf("even: missing %q", s)
		}
	}
	if strings.Contains(html, "Hay diferencias") {
		t.Error("even close shows differences")
	}
}

func TestCashSessionBadID(t *testing.T) {
	h := &WebHandler{engine: NewTemplateEngine(), sessions: sessions.New(nil, nil)}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/cash/sessions/abc", nil)
	r.SetPathValue("id", "abc")
	h.CashSession(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("bad id: got %d, want %d", w.Code, http.StatusNotFound)
	}
}
