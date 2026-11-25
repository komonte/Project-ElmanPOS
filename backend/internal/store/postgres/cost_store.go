package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// CostStore guarda la historia de costos (02 4.7, F-04). Igual que los
// precios, las filas no se editan ni se borran: un costo nuevo agrega una
// fila, así los reportes de ventas viejas usan el costo de ese momento
// (BR-03).
type CostStore struct {
	db DBTX
}

// NewCostStore recibe un DBTX: el *sql.DB, o una transacción cuando el costo
// se guarda junto con su auditoría (o con la compra, en F-06).
func NewCostStore(db DBTX) *CostStore {
	return &CostStore{db: db}
}

const costSelect = `
	SELECT id, product_id, unit_cost, avg_cost, valid_from, created_at
	FROM product_costs`

func scanCost(row rowScanner) (*model.Cost, error) {
	c := new(model.Cost)
	err := row.Scan(&c.ID, &c.ProductID, &c.UnitCost, &c.AvgCost, &c.ValidFrom, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Create guarda un costo que rige desde ya y lo devuelve con su id y fechas.
// AvgCost tiene que venir calculado (model.AverageCost): el store no hace
// cuentas. El ValidFrom que venga se ignora.
//   - ErrDuplicate: el producto ya tiene un costo con ese mismo valid_from.
//   - ErrInvalidReference: el producto no existe.
//
// valid_from es clock_timestamp(), por lo mismo que en PriceStore.Create: una
// compra con dos renglones del mismo producto guarda dos costos en la misma
// transacción, y con now() chocarían.
func (s *CostStore) Create(ctx context.Context, c *model.Cost) (*model.Cost, error) {
	if c == nil {
		return nil, model.ErrNilCost
	}
	got, err := scanCost(s.db.QueryRowContext(ctx, `
		INSERT INTO product_costs (product_id, unit_cost, avg_cost, valid_from)
		VALUES ($1, $2, $3, clock_timestamp())
		RETURNING id, product_id, unit_cost, avg_cost, valid_from, created_at;`,
		c.ProductID, c.UnitCost, c.AvgCost))
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

// Current devuelve el costo vigente de un producto: el de valid_from más
// reciente. ErrNotFound si todavía no tiene costo (o si no existe).
func (s *CostStore) Current(ctx context.Context, productID int64) (*model.Cost, error) {
	c, err := scanCost(s.db.QueryRowContext(ctx, costSelect+`
		WHERE product_id = $1
		ORDER BY valid_from DESC
		LIMIT 1;`, productID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return c, nil
}

// History devuelve los costos de un producto, del más nuevo al más viejo
// (vacía, no nil, si no tiene). Las dos consultas usan el índice
// (product_id, valid_from DESC).
func (s *CostStore) History(ctx context.Context, productID int64) ([]*model.Cost, error) {
	rows, err := s.db.QueryContext(ctx, costSelect+`
		WHERE product_id = $1
		ORDER BY valid_from DESC;`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	costs := make([]*model.Cost, 0)
	for rows.Next() {
		c, err := scanCost(rows)
		if err != nil {
			return nil, err
		}
		costs = append(costs, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return costs, nil
}
