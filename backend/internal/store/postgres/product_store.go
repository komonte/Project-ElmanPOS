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
//
// El precio vigente (F-03) sale de un LATERAL: el último de prices, con el
// índice (product_id, valid_from DESC). Postgres lo calcula solo para las
// filas que devuelve: medido con el catálogo real (64.602 productos, 3
// precios cada uno), la lista de 50 pasa de 0,14 a 0,40 ms.
const productSelect = `
	SELECT p.id, p.sku, p.name, COALESCE(p.description, ''),
	       p.brand_id, p.category_id, p.measure_unit, p.sale_type,
	       p.controls_batches, p.reorder_point, p.max_stock, p.margin_percent,
	       p.preferred_supplier_id, p.is_consignment, p.is_active,
	       p.created_at, p.updated_at,
	       COALESCE(b.name, ''), c.name, COALESCE(s.name, ''),
	       cp.unit_price
	FROM products p
	LEFT JOIN brands b ON b.id = p.brand_id
	JOIN categories c ON c.id = p.category_id
	LEFT JOIN suppliers s ON s.id = p.preferred_supplier_id
	LEFT JOIN LATERAL (
	    SELECT pr.unit_price FROM prices pr
	    WHERE pr.product_id = p.id
	    ORDER BY pr.valid_from DESC
	    LIMIT 1
	) cp ON true`

// scanProduct lee una fila de productSelect. Los punteros (brand_id,
// max_stock, preferred_supplier_id, el precio vigente) quedan en nil si la
// columna es NULL.
func scanProduct(row rowScanner) (*model.Product, error) {
	p := new(model.Product)
	err := row.Scan(&p.ID, &p.SKU, &p.Name, &p.Description,
		&p.BrandID, &p.CategoryID, &p.MeasureUnit, &p.SaleType,
		&p.ControlsBatches, &p.ReorderPoint, &p.MaxStock, &p.MarginPercent,
		&p.PreferredSupplierID, &p.IsConsignment, &p.IsActive,
		&p.CreatedAt, &p.UpdatedAt,
		&p.BrandName, &p.CategoryName, &p.SupplierName,
		&p.CurrentPrice)
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
	if err := s.checkReferences(ctx, 0, product); err != nil {
		return nil, err
	}

	// is_active y las fechas las pone la DB. Los punteros nil se guardan
	// como NULL, y una description vacía también (NULLIF). Un sku vacío lo
	// genera la DB con next_product_sku() (migración 0010): COALESCE solo la
	// llama si hace falta, así un sku escrito a mano no gasta un número.
	q := `INSERT INTO products (sku, name, description, brand_id, category_id,
	                            measure_unit, sale_type, controls_batches,
	                            reorder_point, max_stock, margin_percent,
	                            preferred_supplier_id, is_consignment)
	      VALUES (COALESCE(NULLIF($1, ''), next_product_sku()), $2, NULLIF($3, ''), $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
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

// productMatch es el filtro de las búsquedas: lo escrito ($1) en el nombre o
// en el sku ("oreo" o "OREO-200"), o el código de barras exacto ($2, ya
// normalizado; vacío no encuentra nada). En el almacén se busca de las tres
// formas, y el escáner escribe el código en el mismo buscador.
//
// El código va como "p.id = (subconsulta)" y no con un EXISTS: como el código
// es UNIQUE, Postgres corre la subconsulta una sola vez (por índice) y
// compara un id. Medido con el catálogo real: igual de rápido que sin código;
// con EXISTS, entre 100 y 400 ms.
const productMatch = `(p.name ILIKE '%' || $1 || '%' OR p.sku ILIKE '%' || $1 || '%'
	OR p.id = (SELECT pc.product_id FROM product_codes pc WHERE pc.code = $2))`

// searchCode es lo buscado como código de barras, normalizado como se guarda
// ("9770301" -> "0000009770301"). "" si no puede ser un código (tiene letras,
// está vacío): con "" la subconsulta no encuentra nada.
func searchCode(query string) string {
	code, err := model.NormalizeBarcode(query)
	if err != nil {
		return ""
	}
	return code
}

// SearchByName busca entre los activos por nombre, sku o código de barras.
func (s *ProductStore) SearchByName(ctx context.Context, query string) ([]*model.Product, error) {
	return s.queryProducts(ctx, productSelect+`
		WHERE p.is_active = TRUE AND `+productMatch+`
		ORDER BY p.name, p.sku
		LIMIT 20;`, query, searchCode(query))
}

func (s *ProductStore) SearchAllByName(ctx context.Context, query string) ([]*model.Product, error) {
	return s.queryProducts(ctx, productSelect+`
		WHERE `+productMatch+`
		ORDER BY p.is_active DESC, p.name, p.sku
		LIMIT 20;`, query, searchCode(query))
}

// ListAll busca como SearchByName (vacío = todos). El orden coincide con el
// índice idx_products_admin_list, así Postgres frena en las limit primeras
// en vez de ordenar todo el catálogo.
func (s *ProductStore) ListAll(ctx context.Context, query string, limit int) ([]*model.Product, int, error) {
	const where = `WHERE ($1 = '' OR ` + productMatch + `)`
	return listPage(ctx, s.db,
		`SELECT count(*) FROM products p `+where+`;`,
		productSelect+` `+where+`
		 ORDER BY p.is_active DESC, p.name, p.sku LIMIT $3;`,
		scanProduct, limit, query, searchCode(query))
}

// Update cambia todos los campos salvo is_active: activar y desactivar pasan
// solo por SetActive. Así un PUT por la API sin "is_active" no desactiva.
// Un sku vacío deja el que tenía (la pantalla de edición no lo manda).
//
// measure_unit, sale_type y controls_batches no cambian si el producto ya
// tiene historial (F-01): movimientos de stock, ventas, compras o lotes.
// La regla va en el mismo UPDATE (en el WHERE las columnas tienen todavía
// los valores viejos), así no hay hueco entre revisar y guardar.
func (s *ProductStore) Update(ctx context.Context, id int64, product *model.Product) (*model.Product, error) {
	if product == nil {
		return nil, errors.New("product cannot be nil")
	}
	if err := s.checkReferences(ctx, id, product); err != nil {
		return nil, err
	}

	q := `UPDATE products
	      SET sku = COALESCE(NULLIF($1, ''), sku), name = $2, description = NULLIF($3, ''), brand_id = $4,
	          category_id = $5, measure_unit = $6, sale_type = $7,
	          controls_batches = $8, reorder_point = $9, max_stock = $10,
	          margin_percent = $11, preferred_supplier_id = $12, is_consignment = $13
	      WHERE id = $14
	        AND (
	              (measure_unit = $6 AND sale_type = $7 AND controls_batches = $8)
	              OR NOT (
	                   EXISTS (SELECT 1 FROM stock_movements WHERE product_id = $14)
	                OR EXISTS (SELECT 1 FROM sale_items      WHERE product_id = $14)
	                OR EXISTS (SELECT 1 FROM purchase_items  WHERE product_id = $14)
	                OR EXISTS (SELECT 1 FROM product_batches WHERE product_id = $14)
	              )
	            );`

	res, err := s.db.ExecContext(ctx, q,
		product.SKU, product.Name, product.Description, product.BrandID, product.CategoryID,
		product.MeasureUnit, product.SaleType, product.ControlsBatches,
		product.ReorderPoint, product.MaxStock, product.MarginPercent,
		product.PreferredSupplierID, product.IsConsignment, id,
	)
	if err != nil {
		return nil, productWriteError(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		// El WHERE no encontró la fila: o el id no existe, o la regla del
		// historial la frenó. Si existe, fue la regla.
		var exists bool
		err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM products WHERE id = $1);`, id).Scan(&exists)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, store.ErrProductHasHistory
		}
		return nil, store.ErrNotFound
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

// checkReferences frena elegir una marca, categoría o proveedor inactivo
// (BR-06). id es el producto que se edita (0 al crear, porque todavía no
// existe). Al editar, una referencia inactiva vale si es la que el producto
// ya tenía: así se puede corregir un producto viejo sin moverlo de categoría.
//
// Cada EXISTS da TRUE solo si la referencia existe, está inactiva y no es la
// actual. Una referencia vacía (NULL) o inexistente da FALSE: la inexistente
// la frena después la FK, con ErrInvalidReference.
//
// Igual que en categorías: si alguien desactiva algo entre este chequeo y el
// INSERT/UPDATE, se guardaría igual. Con un solo admin no es un riesgo real.
func (s *ProductStore) checkReferences(ctx context.Context, id int64, p *model.Product) error {
	q := `SELECT
	          EXISTS (SELECT 1 FROM brands b
	                  WHERE b.id = $1 AND NOT b.is_active
	                    AND NOT EXISTS (SELECT 1 FROM products p WHERE p.id = $4 AND p.brand_id = b.id)),
	          EXISTS (SELECT 1 FROM categories c
	                  WHERE c.id = $2 AND NOT c.is_active
	                    AND NOT EXISTS (SELECT 1 FROM products p WHERE p.id = $4 AND p.category_id = c.id)),
	          EXISTS (SELECT 1 FROM suppliers s
	                  WHERE s.id = $3 AND NOT s.is_active
	                    AND NOT EXISTS (SELECT 1 FROM products p WHERE p.id = $4 AND p.preferred_supplier_id = s.id));`

	var brandInactive, categoryInactive, supplierInactive bool
	err := s.db.QueryRowContext(ctx, q, p.BrandID, p.CategoryID, p.PreferredSupplierID, id).
		Scan(&brandInactive, &categoryInactive, &supplierInactive)
	if err != nil {
		return err
	}
	switch {
	case categoryInactive:
		return store.ErrProductCategoryInactive
	case brandInactive:
		return store.ErrProductBrandInactive
	case supplierInactive:
		return store.ErrProductSupplierInactive
	}
	return nil
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
