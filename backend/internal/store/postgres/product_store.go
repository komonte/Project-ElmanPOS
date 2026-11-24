package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// Falla al compilar si ProductStore deja de cumplir la interfaz Store.
var _ Store[model.Product] = (*ProductStore)(nil)

// ProductStore guarda el catálogo de productos (02 4.4). Solo la tabla
// products: los códigos de barra y los precios tienen su propio store.
type ProductStore struct {
	db DBTX
}

// NewProductStore recibe un DBTX: en la app es el *sql.DB, en los tests una
// transacción que se deshace al terminar.
func NewProductStore(db DBTX) *ProductStore {
	return &ProductStore{db: db}
}

// productSelect es el SELECT de todas las lecturas. Trae además los nombres de
// la marca y el proveedor (LEFT JOIN: son opcionales) y el de la categoría
// (JOIN: es obligatoria). Una description NULL vuelve como "".
const productSelect = `
	SELECT p.id, p.sku, p.name, COALESCE(p.description, ''),
	       p.brand_id, p.category_id, p.measure_unit, p.sale_type,
	       p.controls_batches, p.reorder_point, p.max_stock, p.margin_percent,
	       p.preferred_supplier_id, p.is_consignment, p.is_active,
	       p.created_at, p.updated_at,
	       COALESCE(b.name, ''), c.name, COALESCE(s.name, '')
	FROM products p
	LEFT JOIN brands b ON b.id = p.brand_id
	JOIN categories c ON c.id = p.category_id
	LEFT JOIN suppliers s ON s.id = p.preferred_supplier_id`

// scanProduct lee una fila de productSelect. Los punteros (brand_id,
// max_stock, preferred_supplier_id) quedan en nil si la columna es NULL.
func scanProduct(row rowScanner) (*model.Product, error) {
	p := new(model.Product)
	err := row.Scan(&p.ID, &p.SKU, &p.Name, &p.Description,
		&p.BrandID, &p.CategoryID, &p.MeasureUnit, &p.SaleType,
		&p.ControlsBatches, &p.ReorderPoint, &p.MaxStock, &p.MarginPercent,
		&p.PreferredSupplierID, &p.IsConsignment, &p.IsActive,
		&p.CreatedAt, &p.UpdatedAt,
		&p.BrandName, &p.CategoryName, &p.SupplierName)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// queryProducts corre una consulta que devuelve varias filas de productSelect
// y las junta en una lista (vacía, no nil, si no hay ninguna).
func (s *ProductStore) queryProducts(ctx context.Context, q string, args ...any) ([]*model.Product, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	products := make([]*model.Product, 0)
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		products = append(products, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return products, nil
}

func (s *ProductStore) Create(ctx context.Context, product *model.Product) (*model.Product, error) {
	if product == nil {
		return nil, errors.New("product cannot be nil")
	}

	// is_active y las fechas las pone la DB. Los punteros nil se guardan
	// como NULL, y una description vacía también (NULLIF).
	q := `INSERT INTO products (sku, name, description, brand_id, category_id,
	                            measure_unit, sale_type, controls_batches,
	                            reorder_point, max_stock, margin_percent,
	                            preferred_supplier_id, is_consignment)
	      VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	      RETURNING id;`

	var id int64
	err := s.db.QueryRowContext(ctx, q,
		product.SKU, product.Name, product.Description, product.BrandID, product.CategoryID,
		product.MeasureUnit, product.SaleType, product.ControlsBatches,
		product.ReorderPoint, product.MaxStock, product.MarginPercent,
		product.PreferredSupplierID, product.IsConsignment,
	).Scan(&id)
	if err != nil {
		return nil, productWriteError(err)
	}

	// Se relee para devolver también los nombres de marca, categoría y proveedor.
	return s.GetByID(ctx, id)
}

// Las listas ordenan por nombre y después por sku: el nombre puede repetirse.

func (s *ProductStore) GetAll(ctx context.Context) ([]*model.Product, error) {
	return s.queryProducts(ctx, productSelect+`
		WHERE p.is_active = TRUE
		ORDER BY p.name, p.sku;`)
}

func (s *ProductStore) GetAllIncludingInactive(ctx context.Context) ([]*model.Product, error) {
	return s.queryProducts(ctx, productSelect+`
		ORDER BY p.is_active DESC, p.name, p.sku;`)
}

func (s *ProductStore) GetByID(ctx context.Context, id int64) (*model.Product, error) {
	row := s.db.QueryRowContext(ctx, productSelect+`
		WHERE p.id = $1;`, id)

	p, err := scanProduct(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return p, nil
}

// SearchByName busca por nombre o por sku: en el almacén se busca de las
// dos formas ("oreo" o "OREO-200").
func (s *ProductStore) SearchByName(ctx context.Context, query string) ([]*model.Product, error) {
	return s.queryProducts(ctx, productSelect+`
		WHERE p.is_active = TRUE
		  AND (p.name ILIKE '%' || $1 || '%' OR p.sku ILIKE '%' || $1 || '%')
		ORDER BY p.name, p.sku
		LIMIT 20;`, query)
}

func (s *ProductStore) SearchAllByName(ctx context.Context, query string) ([]*model.Product, error) {
	return s.queryProducts(ctx, productSelect+`
		WHERE p.name ILIKE '%' || $1 || '%' OR p.sku ILIKE '%' || $1 || '%'
		ORDER BY p.is_active DESC, p.name, p.sku
		LIMIT 20;`, query)
}

// Update cambia todos los campos salvo is_active: activar y desactivar pasan
// solo por SetActive. Así un PUT por la API sin "is_active" no desactiva.
func (s *ProductStore) Update(ctx context.Context, id int64, product *model.Product) (*model.Product, error) {
	if product == nil {
		return nil, errors.New("product cannot be nil")
	}

	q := `UPDATE products
	      SET sku = $1, name = $2, description = NULLIF($3, ''), brand_id = $4,
	          category_id = $5, measure_unit = $6, sale_type = $7,
	          controls_batches = $8, reorder_point = $9, max_stock = $10,
	          margin_percent = $11, preferred_supplier_id = $12, is_consignment = $13
	      WHERE id = $14;`

	res, err := s.db.ExecContext(ctx, q,
		product.SKU, product.Name, product.Description, product.BrandID, product.CategoryID,
		product.MeasureUnit, product.SaleType, product.ControlsBatches,
		product.ReorderPoint, product.MaxStock, product.MarginPercent,
		product.PreferredSupplierID, product.IsConsignment, id,
	)
	if err != nil {
		return nil, productWriteError(err)
	}
	if err := requireOneRow(res); err != nil {
		return nil, err
	}

	return s.GetByID(ctx, id)
}

// Delete borra de verdad, solo si nada lo referencia (códigos, precios,
// movimientos, ventas...). Si no, ErrInUse: hay que desactivarlo.
func (s *ProductStore) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM products WHERE id = $1;`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return store.ErrInUse
		}
		return err
	}
	return requireOneRow(res)
}

func (s *ProductStore) SetActive(ctx context.Context, id int64, active bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE products SET is_active = $1 WHERE id = $2;`, active, id)
	if err != nil {
		return err
	}
	return requireOneRow(res)
}

// productWriteError traduce los errores de la DB al crear o editar un producto.
// Un CHECK que falla (ej. max_stock <= reorder_point) no se traduce: Validate
// ya lo frena antes, así que si llega acá es un bug y conviene que sea un 500.
func productWriteError(err error) error {
	switch {
	case isUniqueViolation(err):
		return store.ErrDuplicateSKU // sku es la única columna UNIQUE
	case isForeignKeyViolation(err):
		return store.ErrInvalidReference // marca, categoría o proveedor que no existe
	default:
		return err
	}
}
