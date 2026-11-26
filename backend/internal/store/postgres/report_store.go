package postgres

import (
	"context"
	"time"

	"github.com/lib/pq"
	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

// ReportStore lee los reportes de ventas y caja (R-06, R-07). Solo lee y
// suma en la base: los reportes se calculan, nunca se guardan.
//
// Los tramos (horas, días, meses) llegan armados: buckets son sus
// comienzos, en orden, y end es donde termina el último. La base solo dice
// en qué tramo cae cada venta con width_bucket (1 = el primero), así el SQL
// no sabe de zonas horarias.
type ReportStore struct {
	db DBTX
}

func NewReportStore(db DBTX) *ReportStore {
	return &ReportStore{db: db}
}

// thresholds pasa los comienzos de los tramos a un array de timestamptz.
func thresholds(buckets []time.Time) pq.StringArray {
	out := make(pq.StringArray, len(buckets))
	for i, b := range buckets {
		out[i] = b.UTC().Format(time.RFC3339Nano)
	}
	return out
}

// SalesByBucket devuelve lo vendido (ventas COMPLETED) en cada tramo que
// tuvo ventas, en orden. Las unidades y los kilos salen de los renglones,
// según cómo se vende cada producto.
func (s *ReportStore) SalesByBucket(ctx context.Context, buckets []time.Time, end time.Time) ([]model.SalesTally, error) {
	if len(buckets) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT width_bucket(s.created_at, $3::timestamptz[]) - 1 AS bucket,
		       count(*), SUM(s.total), COALESCE(SUM(q.units), 0), COALESCE(SUM(q.kg), 0)
		FROM sales s
		LEFT JOIN LATERAL (
			SELECT SUM(si.quantity) FILTER (WHERE p.sale_type <> 'WEIGHT') AS units,
			       SUM(si.quantity) FILTER (WHERE p.sale_type = 'WEIGHT') AS kg
			FROM sale_items si JOIN products p ON p.id = si.product_id
			WHERE si.sale_id = s.id
		) q ON TRUE
		WHERE s.status = 'COMPLETED' AND s.created_at >= $1 AND s.created_at < $2
		GROUP BY bucket ORDER BY bucket;`, buckets[0], end, thresholds(buckets))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SalesTally
	for rows.Next() {
		var t model.SalesTally
		if err := rows.Scan(&t.Bucket, &t.Count, &t.Total, &t.Units, &t.Kg); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// PaymentsByBucket devuelve los pagos de las ventas COMPLETED por tramo y
// medio, en orden de tramo. En efectivo, el pago guardado es lo aplicado a
// la venta (sin el vuelto), así que los pagos de un tramo suman su total.
func (s *ReportStore) PaymentsByBucket(ctx context.Context, buckets []time.Time, end time.Time) ([]model.PaymentTally, error) {
	if len(buckets) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT width_bucket(s.created_at, $3::timestamptz[]) - 1 AS bucket, p.method, count(*), SUM(p.amount)
		FROM payments p
		JOIN sales s ON s.id = p.sale_id
		WHERE s.status = 'COMPLETED' AND s.created_at >= $1 AND s.created_at < $2
		GROUP BY bucket, p.method ORDER BY bucket, p.method;`, buckets[0], end, thresholds(buckets))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.PaymentTally
	for rows.Next() {
		var t model.PaymentTally
		if err := rows.Scan(&t.Bucket, &t.Method, &t.Count, &t.Amount); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Voided dice cuántas ventas del período (por cuándo se hicieron) están
// anuladas y por cuánto.
func (s *ReportStore) Voided(ctx context.Context, start, end time.Time) (int, decimal.Decimal, error) {
	var n int
	var total decimal.Decimal
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*), COALESCE(SUM(total), 0) FROM sales
		WHERE status = 'VOIDED' AND created_at >= $1 AND created_at < $2;`, start, end).Scan(&n, &total)
	return n, total, err
}

// ClosedSessions devuelve las sesiones cerradas que se abrieron en el
// período (la jornada es el día en que se abrió), en orden, con sus ventas,
// sus anuladas, sus pagos por medio, lo que se contó al cerrar y el margen.
//
// El margen es sin IVA: el total de cada renglón (cantidad × precio,
// redondeado a centavos como en la venta) sin su IVA, menos cantidad ×
// unit_cost (el costo exacto de lo que salió, por lote o el promedio). Los
// renglones sin costo (unit_cost 0) no entran: van a NoCost, con IVA.
func (s *ReportStore) ClosedSessions(ctx context.Context, start, end time.Time) ([]*model.SessionTally, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+sessionColumns+`,
		       COALESCE(st.sales, 0), COALESCE(st.total, 0), COALESCE(st.voided, 0), COALESCE(st.voided_total, 0),
		       COALESCE(mg.net, 0), COALESCE(mg.cost, 0), COALESCE(mg.no_cost, 0)
		FROM `+sessionFrom+`
		LEFT JOIN LATERAL (
			SELECT count(*) FILTER (WHERE status = 'COMPLETED') AS sales,
			       SUM(total) FILTER (WHERE status = 'COMPLETED') AS total,
			       count(*) FILTER (WHERE status = 'VOIDED') AS voided,
			       SUM(total) FILTER (WHERE status = 'VOIDED') AS voided_total
			FROM sales WHERE cashier_session_id = cs.id
		) st ON TRUE
		LEFT JOIN LATERAL (
			SELECT round(SUM(round(si.quantity * si.unit_price, 2) * 100 / (100 + si.tax_rate))
			             FILTER (WHERE si.unit_cost > 0), 2) AS net,
			       round(SUM(si.quantity * si.unit_cost) FILTER (WHERE si.unit_cost > 0), 2) AS cost,
			       SUM(round(si.quantity * si.unit_price, 2)) FILTER (WHERE si.unit_cost <= 0) AS no_cost
			FROM sale_items si JOIN sales s ON s.id = si.sale_id
			WHERE s.cashier_session_id = cs.id AND s.status = 'COMPLETED'
		) mg ON TRUE
		WHERE cs.status = 'CLOSED' AND cs.opened_at >= $1 AND cs.opened_at < $2
		ORDER BY cs.opened_at, cs.id;`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.SessionTally
	byID := map[int64]*model.SessionTally{}
	var ids []int64
	for rows.Next() {
		t := &model.SessionTally{Session: new(model.CashierSession), Paid: map[string]decimal.Decimal{}}
		cs := t.Session
		if err := rows.Scan(&cs.ID, &cs.CashRegisterID, &cs.OpenedBy, &cs.ClosedBy, &cs.OpeningAmount, &cs.ClosingAmount,
			&cs.Status, &cs.OpenedAt, &cs.ClosedAt, &cs.RegisterName, &cs.OpenedByName, &cs.ClosedByName,
			&t.Sales, &t.Total, &t.Voided, &t.VoidedTotal, &t.Margin.Net, &t.Margin.Cost, &t.Margin.NoCost); err != nil {
			return nil, err
		}
		out = append(out, t)
		byID[cs.ID] = t
		ids = append(ids, cs.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return out, nil
	}
	if err := s.sessionPayments(ctx, ids, byID); err != nil {
		return nil, err
	}
	if err := s.sessionAudits(ctx, ids, byID); err != nil {
		return nil, err
	}
	return out, nil
}

// sessionPayments llena Paid: los pagos de las ventas COMPLETED por medio.
func (s *ReportStore) sessionPayments(ctx context.Context, ids []int64, byID map[int64]*model.SessionTally) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.cashier_session_id, p.method, SUM(p.amount)
		FROM payments p JOIN sales s ON s.id = p.sale_id
		WHERE s.status = 'COMPLETED' AND s.cashier_session_id = ANY($1)
		GROUP BY s.cashier_session_id, p.method;`, pq.Array(ids))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var method string
		var sum decimal.Decimal
		if err := rows.Scan(&id, &method, &sum); err != nil {
			return err
		}
		byID[id].Paid[method] = sum
	}
	return rows.Err()
}

// sessionAudits llena Declared: lo que se contó al cerrar cada sesión.
func (s *ReportStore) sessionAudits(ctx context.Context, ids []int64, byID map[int64]*model.SessionTally) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, cashier_session_id, method, declared_amount, created_at
		FROM cash_audits WHERE cashier_session_id = ANY($1) ORDER BY id;`, pq.Array(ids))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		a := new(model.CashAudit)
		if err := rows.Scan(&a.ID, &a.CashierSessionID, &a.Method, &a.DeclaredAmount, &a.CreatedAt); err != nil {
			return err
		}
		t := byID[a.CashierSessionID]
		t.Declared = append(t.Declared, a)
	}
	return rows.Err()
}

// OpenSessions dice cuántas sesiones abiertas en el período siguen abiertas:
// no están en el reporte de caja hasta que se cierren (cierre ciego).
func (s *ReportStore) OpenSessions(ctx context.Context, start, end time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM cashier_sessions
		WHERE status = 'OPEN' AND opened_at >= $1 AND opened_at < $2;`, start, end).Scan(&n)
	return n, err
}
