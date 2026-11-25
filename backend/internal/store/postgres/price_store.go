package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// PriceStore guarda la historia de precios de venta (02 4.7, F-03). Las filas
// no se editan ni se borran: un cambio de precio agrega una nueva, así las
// ventas viejas no cambian (BR-03).
type PriceStore struct {
	db DBTX
}

// NewPriceStore recibe un DBTX: el *sql.DB, o una transacción cuando el
// precio se guarda junto con su registro de auditoría.
func NewPriceStore(db DBTX) *PriceStore {
	return &PriceStore{db: db}
}

const priceSelect = `
	SELECT id, product_id, unit_price, valid_from, created_at
	FROM prices`

func scanPrice(row rowScanner) (*model.Price, error) {
	p := new(model.Price)
	err := row.Scan(&p.ID, &p.ProductID, &p.UnitPrice, &p.ValidFrom, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Create guarda un precio que rige desde ya y lo devuelve con su id y fechas.
// El ValidFrom que venga se ignora.
//   - ErrDuplicate: el producto ya tiene un precio con ese mismo valid_from.
//   - ErrInvalidReference: el producto no existe.
//
// valid_from es clock_timestamp() y no now(): now() es la hora en que empezó
// la transacción, así que dos precios en la misma transacción chocarían con
// el UNIQUE(product_id, valid_from). clock_timestamp() es la hora de verdad.
func (s *PriceStore) Create(ctx context.Context, p *model.Price) (*model.Price, error) {
	if p == nil {
		return nil, model.ErrNilPrice
	}
	got, err := scanPrice(s.db.QueryRowContext(ctx, `
		INSERT INTO prices (product_id, unit_price, valid_from)
		VALUES ($1, $2, clock_timestamp())
		RETURNING id, product_id, unit_price, valid_from, created_at;`,
		p.ProductID, p.UnitPrice))
	if err != nil {
		switch {
		case isUniqueViolation(err):
			return nil, store.ErrDuplicate
		case isForeignKeyViolation(err):
			return nil, store.ErrInvalidReference
		default:
			return nil, err
		}
	}
	return got, nil
}

// Current devuelve el precio vigente de un producto: el de valid_from más
// reciente. ErrNotFound si todavía no tiene precio (o si no existe).
//
// No filtra valid_from <= now() porque hoy no hay precios a futuro (Create
// siempre usa la hora actual). Si algún día se programan, hay que agregarlo.
func (s *PriceStore) Current(ctx context.Context, productID int64) (*model.Price, error) {
	p, err := scanPrice(s.db.QueryRowContext(ctx, priceSelect+`
		WHERE product_id = $1
		ORDER BY valid_from DESC
		LIMIT 1;`, productID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return p, nil
}

// History devuelve los precios de un producto, del más nuevo al más viejo
// (vacía, no nil, si no tiene). Las dos consultas usan el índice
// (product_id, valid_from DESC).
func (s *PriceStore) History(ctx context.Context, productID int64) ([]*model.Price, error) {
	rows, err := s.db.QueryContext(ctx, priceSelect+`
		WHERE product_id = $1
		ORDER BY valid_from DESC;`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	prices := make([]*model.Price, 0)
	for rows.Next() {
		p, err := scanPrice(rows)
		if err != nil {
			return nil, err
		}
		prices = append(prices, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return prices, nil
}
