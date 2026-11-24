package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// ProductCodeStore guarda los códigos de barra de los productos (02 4.5).
// No es una tabla maestra (no tiene is_active ni lista de admin), así que no
// cumple Store[T]: los códigos se manejan siempre desde su producto (F-02).
//
// Los códigos tienen que llegar normalizados (model.ProductCode.Validate):
// uno con otro formato lo frena el CHECK de la DB, y eso es un bug (500).
type ProductCodeStore struct {
	db DBTX
}

// NewProductCodeStore recibe un DBTX: el *sql.DB, o una transacción cuando
// el código se guarda junto con su producto.
func NewProductCodeStore(db DBTX) *ProductCodeStore {
	return &ProductCodeStore{db: db}
}

// productCodeSelect trae también el sku y el nombre del producto, para poder
// decir "ese código ya es de COCA COLA 2L".
const productCodeSelect = `
	SELECT pc.id, pc.product_id, pc.code_type, pc.code, pc.created_at,
	       p.sku, p.name
	FROM product_codes pc
	JOIN products p ON p.id = pc.product_id`

func scanProductCode(row rowScanner) (*model.ProductCode, error) {
	c := new(model.ProductCode)
	err := row.Scan(&c.ID, &c.ProductID, &c.CodeType, &c.Code, &c.CreatedAt,
		&c.ProductSKU, &c.ProductName)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// ListByProduct devuelve los códigos de un producto, del más viejo al más
// nuevo (vacía, no nil, si no tiene). Un producto que no existe da lista vacía.
func (s *ProductCodeStore) ListByProduct(ctx context.Context, productID int64) ([]*model.ProductCode, error) {
	rows, err := s.db.QueryContext(ctx, productCodeSelect+`
		WHERE pc.product_id = $1
		ORDER BY pc.id;`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	codes := make([]*model.ProductCode, 0)
	for rows.Next() {
		c, err := scanProductCode(rows)
		if err != nil {
			return nil, err
		}
		codes = append(codes, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return codes, nil
}

// FindByCode busca un código exacto (ya normalizado). Es la búsqueda del
// escáner: usa el índice del UNIQUE(code). ErrNotFound si no existe.
func (s *ProductCodeStore) FindByCode(ctx context.Context, code string) (*model.ProductCode, error) {
	c, err := scanProductCode(s.db.QueryRowContext(ctx, productCodeSelect+`
		WHERE pc.code = $1;`, code))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return c, nil
}

// Add guarda un código para un producto y lo devuelve con su id y fecha.
//   - ErrDuplicateCode: el código ya es de un producto (de este o de otro).
//   - ErrInvalidReference: el producto no existe.
//
// Ojo dentro de una transacción: si falla, Postgres la deja abortada y ya no
// acepta otras consultas (ni FindByCode para ver de quién es el código).
func (s *ProductCodeStore) Add(ctx context.Context, c *model.ProductCode) (*model.ProductCode, error) {
	if c == nil {
		return nil, model.ErrNilProductCode
	}
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO product_codes (product_id, code_type, code) VALUES ($1, $2, $3) RETURNING id;`,
		c.ProductID, c.CodeType, c.Code).Scan(&id)
	if err != nil {
		switch {
		case isUniqueViolation(err):
			return nil, store.ErrDuplicateCode
		case isForeignKeyViolation(err):
			return nil, store.ErrInvalidReference
		default:
			return nil, err
		}
	}

	// Se relee para devolver también la fecha y el producto.
	return scanProductCode(s.db.QueryRowContext(ctx, productCodeSelect+`
		WHERE pc.id = $1;`, id))
}

// Delete borra un código, solo si es de ese producto: así una URL con el
// producto equivocado no borra el código de otro. ErrNotFound si no hay
// ninguno así. Nada referencia a product_codes: se borra de verdad.
func (s *ProductCodeStore) Delete(ctx context.Context, productID, codeID int64) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM product_codes WHERE id = $1 AND product_id = $2;`, codeID, productID)
	if err != nil {
		return err
	}
	return requireOneRow(res)
}
