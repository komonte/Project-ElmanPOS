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
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sales"
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
		sales.ErrSaleOtherSession:       "Solo se pueden anular ventas de la caja abierta.",
		sales.ErrSaleAlreadyVoided:      "Esta venta ya estaba anulada.",
		model.ErrVoidReasonRequired:     "Escribí el motivo de la anulación.",
		model.ErrVoidReasonTooLong:      "hasta 300 caracteres",
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
		if _, ok := data["Can"]; !ok {
			data["Can"] = adminCan
		}
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

	t.Run("closed after a close", func(t *testing.T) {
		closedAt := "2026-11-25T23:30:00Z"
		html := render(t, map[string]any{"State": &sessions.State{Register: reg,
			LastClosed: &model.CashierSession{ID: 7, ClosedAt: &closedAt}}})
		for _, s := range []string{`href="/cash/sessions/7" id="cash-last-close"`, "Ver el último cierre",
			"Cerrada el 2026-11-25 20:30:00."} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
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
		if strings.Contains(html, "cash-last-close") {
			t.Error("never closed: no link to a last close")
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

	t.Run("open without sales", func(t *testing.T) {
		html := render(t, map[string]any{"State": openState, "CloseFields": fields})
		for _, s := range []string{`id="cash-sales"`, "Ventas de esta caja", `id="cash-sales-empty"`,
			"Todavía no hay ventas en esta caja."} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		if strings.Contains(html, "<table") {
			t.Error("no sales: no table")
		}
	})

	t.Run("open with sales", func(t *testing.T) {
		voidedAt := "2026-11-26T14:10:00Z"
		list := []*model.Sale{
			{ID: 31, SaleNumber: 12, Status: model.SaleVoided, Total: decimal.RequireFromString("1800"),
				CreatedAt: "2026-11-26T14:00:00Z", VoidedAt: &voidedAt, VoidReason: "Se cobró dos veces",
				Payments: []*model.Payment{{Method: model.PaymentCash, Amount: decimal.RequireFromString("1800")}}},
			{ID: 30, SaleNumber: 11, Status: model.SaleCompleted, Total: decimal.RequireFromString("5000"),
				CreatedAt: "2026-11-26T13:05:00Z",
				Payments: []*model.Payment{{Method: model.PaymentCash, Amount: decimal.RequireFromString("2000")},
					{Method: model.PaymentDebit, Amount: decimal.RequireFromString("3000")}}},
		}
		html := render(t, map[string]any{"State": openState, "CloseFields": fields, "Sales": list})
		for _, s := range []string{
			`<th scope="col" class="p-3">N.º</th>`, "Hora", "Medios", "Estado",
			// Cobrada: hora local, total y cada medio con su monto.
			`id="sale-30" data-status="COMPLETED"`, ">11</th>", ">10:05</td>", "$ 5.000,00",
			`Efectivo <span class="tabular">$ 2.000,00</span></span> · <span class="whitespace-nowrap">Débito <span class="tabular">$ 3.000,00</span>`,
			"Cobrada",
			// Anulada: tachada, con el motivo.
			`id="sale-31" data-status="VOIDED" class="border-b border-line align-top text-muted"`,
			"line-through", "Anulada", "Motivo: Se cobró dos veces",
			// El ticket se reimprime en otra pestaña.
			`href="/pos/sales/31/ticket" target="_blank" rel="noopener"`, `href="/pos/sales/30/ticket" target="_blank"`,
			`aria-label="Ticket de la venta N.º 11 (se abre en otra pestaña)"`,
		} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		// La última primero, y antes del cierre.
		if a, b, c := strings.Index(html, `id="sale-31"`), strings.Index(html, `id="sale-30"`),
			strings.Index(html, `id="cash-close"`); !(a < b && b < c) {
			t.Errorf("order: sale 12 at %d, sale 11 at %d, close at %d", a, b, c)
		}
		if strings.Contains(html, "cash-sales-empty") {
			t.Error("with sales: no empty notice")
		}
		// Solo la anulada va tachada: 4 celdas.
		if n := strings.Count(html, " line-through"); n != 4 {
			t.Errorf("struck cells: got %d, want 4", n)
		}
		// Anular: solo la cobrada, con el motivo plegado debajo de su fila.
		for _, s := range []string{
			`id="void-open-30" class="btn-secondary px-3 py-1" aria-controls="void-30"`, `aria-expanded="false" onclick="cashToggleVoid(this)"`,
			`aria-label="Anular la venta N.º 11"`,
			`<tr id="void-30" class="border-b border-line bg-danger-soft" hidden>`,
			`action="/cash/sales/30/void"`, `onsubmit="return cashConfirmVoid(this)"`,
			`data-ask="¿Anular la venta N.º 11 de $ 5.000,00?"`, `data-reason="Motivo"`,
			`name="reason" required maxlength="300"`, "¿Por qué se anula la venta N.º 11?", "Confirmar anulación",
			"function cashToggleVoid(btn)", "function cashConfirmVoid(form)",
		} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		for _, s := range []string{"void-open-31", `id="void-31"`, "/cash/sales/31/void", `id="cash-refund"`} {
			if strings.Contains(html, s) {
				t.Errorf("unexpected %q", s)
			}
		}
		if strings.Contains(html, `role="alert" class="p-4 bg-surface border-l-8 border-danger`) {
			t.Error("no void error yet")
		}

		// El cajero ve las ventas y el ticket, pero no anula (F-13).
		cashier := render(t, map[string]any{"State": openState, "CloseFields": fields, "Sales": list,
			"Can": canMap(cashierUser)})
		if !strings.Contains(cashier, `href="/pos/sales/30/ticket"`) || !strings.Contains(cashier, `action="/cash/close"`) {
			t.Error("cashier: no ticket or no close")
		}
		for _, s := range []string{"void-open-30", `id="void-30"`, "/cash/sales/30/void"} {
			if strings.Contains(cashier, s) {
				t.Errorf("cashier: unexpected %q", s)
			}
		}

		// Después de anular: el aviso de qué devolver.
		html = render(t, map[string]any{"State": openState, "CloseFields": fields, "Sales": list,
			"Refund": "Venta N.º 12 anulada. Devolvé $ 1.800,00 en efectivo."})
		if !strings.Contains(html, `id="cash-refund" role="status"`) ||
			!strings.Contains(html, "Venta N.º 12 anulada. Devolvé $ 1.800,00 en efectivo.") {
			t.Error("missing the refund notice")
		}

		// Anular falló: la fila vuelve abierta, con lo escrito y el mensaje.
		html = render(t, map[string]any{"State": openState, "CloseFields": fields, "Sales": list,
			"VoidError": "Escribí el motivo de la anulación.", "VoidSaleID": int64(30), "VoidReason": "  "})
		for _, s := range []string{
			`<tr id="void-30" class="border-b border-line bg-danger-soft">`, `aria-expanded="true"`,
			`role="alert" class="p-4 bg-surface border-l-8 border-danger`, "Escribí el motivo de la anulación.", `value="  "`, `aria-invalid="true" autofocus`,
		} {
			if !strings.Contains(html, s) {
				t.Errorf("void error: missing %q", s)
			}
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
			"Section": "cash", "TitleKey": "cash.summary_title", "Summary": sum, "Can": adminCan,
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

func TestRefundNotice(t *testing.T) {
	pay := func(method, amount string) *model.Payment {
		return &model.Payment{Method: method, Amount: decimal.RequireFromString(amount)}
	}
	tests := []struct {
		name, lang string
		payments   []*model.Payment
		want       string
	}{
		{"cash only", "es", []*model.Payment{pay(model.PaymentCash, "1800")},
			"Venta N.º 12 anulada. Devolvé $ 1.800,00 en efectivo."},
		{"two methods", "es", []*model.Payment{pay(model.PaymentCash, "2000"), pay(model.PaymentDebit, "3000")},
			"Venta N.º 12 anulada. Devolvé $ 2.000,00 en efectivo y $ 3.000,00 por débito."},
		{"in the order of the methods, the same method summed", "es",
			[]*model.Payment{pay(model.PaymentQR, "700"), pay(model.PaymentCredit, "1000"),
				pay(model.PaymentCash, "500"), pay(model.PaymentCredit, "250")},
			"Venta N.º 12 anulada. Devolvé $ 500,00 en efectivo, $ 1.250,00 por crédito y $ 700,00 por QR."},
		{"english", "en", []*model.Payment{pay(model.PaymentTransfer, "1500.5"), pay(model.PaymentCash, "100")},
			"Sale No. 12 voided. Give back $100.00 in cash and $1,500.50 by transfer."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := refundNotice(tt.lang, &model.Sale{SaleNumber: 12, Payments: tt.payments})
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVoidResult(t *testing.T) {
	h := &WebHandler{}
	r := httptest.NewRequest("GET", "/cash", nil)
	list := []*model.Sale{
		{ID: 31, SaleNumber: 12, Status: model.SaleVoided,
			Payments: []*model.Payment{{Method: model.PaymentCash, Amount: decimal.RequireFromString("1800")}}},
		{ID: 30, SaleNumber: 11, Status: model.SaleCompleted},
	}

	t.Run("voided sale of this session", func(t *testing.T) {
		data := map[string]any{"VoidedID": int64(31)}
		h.voidResult(r, data, list)
		if got, _ := data["Refund"].(string); !strings.Contains(got, "12") || !strings.Contains(got, "1,800.00") {
			t.Errorf("refund: got %q", got)
		}
	})
	for name, id := range map[string]int64{"not voided": 30, "not in this session": 99} {
		t.Run(name, func(t *testing.T) {
			data := map[string]any{"VoidedID": id}
			h.voidResult(r, data, list)
			if _, ok := data["Refund"]; ok {
				t.Errorf("unexpected refund: %v", data["Refund"])
			}
		})
	}

	t.Run("error in the row", func(t *testing.T) {
		data := map[string]any{"VoidError": "x", "VoidSaleID": int64(30)}
		h.voidResult(r, data, list)
		if data["VoidError"] != "x" || data["Error"] != nil {
			t.Errorf("got %v", data)
		}
	})
	// Una venta que no está (o ya anulada, sin formulario): el error arriba.
	for name, id := range map[string]int64{"sale not in the list": 99, "already voided": 31} {
		t.Run(name, func(t *testing.T) {
			data := map[string]any{"VoidError": "x", "VoidSaleID": id}
			h.voidResult(r, data, list)
			if data["Error"] != "x" || data["VoidError"] != "" {
				t.Errorf("got %v", data)
			}
		})
	}
}
