package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// Falla al compilar si CategoryStore deja de cumplir la interfaz Store.
var _ Store[model.Category] = (*CategoryStore)(nil)

// CategoryStore guarda el árbol de categorías (02 4.2). El CRUD es el mismo
// que el de cualquier tabla maestra; lo propio del árbol (ciclos, padre
// inactivo, hijas activas) se revisa en checkParent y SetActive.
type CategoryStore struct {
	db DBTX
}

// NewCategoryStore recibe un DBTX: en la app es el *sql.DB, en los tests una
// transacción que se deshace al terminar.
func NewCategoryStore(db DBTX) *CategoryStore {
	return &CategoryStore{db: db}
}

// categorySelect es el SELECT de todas las lecturas. Trae además el nombre del
// padre (LEFT JOIN: las raíces no tienen) y el del impuesto (JOIN: es
// obligatorio). Cada método le agrega su WHERE / ORDER BY.
const categorySelect = `
	SELECT c.id, c.parent_id, c.name, c.tax_id, c.is_active,
	       c.created_at, c.updated_at,
	       COALESCE(p.name, ''), t.name
	FROM categories c
	LEFT JOIN categories p ON p.id = c.parent_id
	JOIN taxes t ON t.id = c.tax_id`

// rowScanner lo cumplen *sql.Row y *sql.Rows, así scanCategory sirve para
// leer una fila (GetByID) o muchas (listas).
type rowScanner interface {
	Scan(dest ...any) error
}

// scanCategory lee una fila de categorySelect. parent_id se escanea directo
// en el *int64: database/sql pone nil si la columna es NULL.
func scanCategory(row rowScanner) (*model.Category, error) {
	c := new(model.Category)
	err := row.Scan(&c.ID, &c.ParentID, &c.Name, &c.TaxID, &c.IsActive,
		&c.CreatedAt, &c.UpdatedAt, &c.ParentName, &c.TaxName)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// queryCategories corre una consulta que devuelve varias filas de
// categorySelect y las junta en una lista (vacía, no nil, si no hay ninguna).
func (s *CategoryStore) queryCategories(ctx context.Context, q string, args ...any) ([]*model.Category, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	categories := make([]*model.Category, 0)
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		categories = append(categories, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return categories, nil
}

func (s *CategoryStore) Create(ctx context.Context, category *model.Category) (*model.Category, error) {
	if category == nil {
		return nil, errors.New("category cannot be nil")
	}
	if err := s.checkParent(ctx, 0, category.ParentID); err != nil {
		return nil, err
	}

	// is_active y las fechas las pone la DB. Un ParentID nil se guarda como NULL.
	q := `INSERT INTO categories (parent_id, name, tax_id)
	      VALUES ($1, $2, $3)
	      RETURNING id;`

	var id int64
	err := s.db.QueryRowContext(ctx, q, category.ParentID, category.Name, category.TaxID).Scan(&id)
	if err != nil {
		return nil, categoryWriteError(err)
	}

	// Se relee para devolver también ParentName y TaxName.
	return s.GetByID(ctx, id)
}

func (s *CategoryStore) GetAll(ctx context.Context) ([]*model.Category, error) {
	return s.queryCategories(ctx, categorySelect+`
		WHERE c.is_active = TRUE
		ORDER BY c.name;`)
}

func (s *CategoryStore) GetAllIncludingInactive(ctx context.Context) ([]*model.Category, error) {
	return s.queryCategories(ctx, categorySelect+`
		ORDER BY c.is_active DESC, c.name;`)
}

func (s *CategoryStore) GetByID(ctx context.Context, id int64) (*model.Category, error) {
	row := s.db.QueryRowContext(ctx, categorySelect+`
		WHERE c.id = $1;`, id)

	c, err := scanCategory(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return c, nil
}

func (s *CategoryStore) SearchByName(ctx context.Context, query string) ([]*model.Category, error) {
	return s.queryCategories(ctx, categorySelect+`
		WHERE c.is_active = TRUE
		  AND c.name ILIKE '%' || $1 || '%'
		ORDER BY c.name
		LIMIT 20;`, query)
}

func (s *CategoryStore) SearchAllByName(ctx context.Context, query string) ([]*model.Category, error) {
	return s.queryCategories(ctx, categorySelect+`
		WHERE c.name ILIKE '%' || $1 || '%'
		ORDER BY c.is_active DESC, c.name
		LIMIT 20;`, query)
}

// Update cambia padre, nombre e impuesto. No toca is_active: activar y
// desactivar pasan solo por SetActive, que es donde se revisan las reglas
// del árbol (hijas activas, padre inactivo).
func (s *CategoryStore) Update(ctx context.Context, id int64, category *model.Category) (*model.Category, error) {
	if category == nil {
		return nil, errors.New("category cannot be nil")
	}
	if err := s.checkParent(ctx, id, category.ParentID); err != nil {
		return nil, err
	}

	q := `UPDATE categories
	      SET parent_id = $1, name = $2, tax_id = $3
	      WHERE id = $4;`

	res, err := s.db.ExecContext(ctx, q, category.ParentID, category.Name, category.TaxID, id)
	if err != nil {
		return nil, categoryWriteError(err)
	}
	if err := requireOneRow(res); err != nil {
		return nil, err
	}

	return s.GetByID(ctx, id)
}

func (s *CategoryStore) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM categories WHERE id = $1;`, id)
	if err != nil {
		// La referencian sus subcategorías (parent_id) y los productos.
		if isForeignKeyViolation(err) {
			return store.ErrInUse
		}
		return err
	}
	return requireOneRow(res)
}

// SetActive activa o desactiva una categoría cuidando que nunca quede una
// subcategoría activa colgando de un padre inactivo:
//   - no se desactiva una categoría con subcategorías activas;
//   - no se reactiva una subcategoría si su padre está inactivo.
func (s *CategoryStore) SetActive(ctx context.Context, id int64, active bool) error {
	if active {
		// Si la categoría no existe, el SELECT no da filas y EXISTS es FALSE:
		// sigue de largo y el UPDATE de abajo devuelve ErrNotFound.
		var parentInactive bool
		q := `SELECT EXISTS (
		          SELECT 1 FROM categories c
		          JOIN categories p ON p.id = c.parent_id
		          WHERE c.id = $1 AND p.is_active = FALSE
		      );`
		if err := s.db.QueryRowContext(ctx, q, id).Scan(&parentInactive); err != nil {
			return err
		}
		if parentInactive {
			return store.ErrCategoryParentInactive
		}
	} else {
		var hasActiveChildren bool
		q := `SELECT EXISTS (
		          SELECT 1 FROM categories
		          WHERE parent_id = $1 AND is_active = TRUE
		      );`
		if err := s.db.QueryRowContext(ctx, q, id).Scan(&hasActiveChildren); err != nil {
			return err
		}
		if hasActiveChildren {
			return store.ErrCategoryHasActiveChildren
		}
	}

	res, err := s.db.ExecContext(ctx, `UPDATE categories SET is_active = $1 WHERE id = $2;`, active, id)
	if err != nil {
		return err
	}
	return requireOneRow(res)
}

// checkParent revisa el padre elegido antes de guardar. id es la categoría
// que se edita (0 al crear, porque todavía no existe).
//   - Sin padre (raíz): no hay nada que revisar.
//   - El padre tiene que existir y estar activo.
//   - Al editar, el padre no puede ser la misma categoría ni una de sus
//     subcategorías: eso armaría un ciclo (A dentro de B, B dentro de A).
//
// Se revisa en Go y no con la FK porque la FK solo dice "no existe", sin
// distinguir los otros casos. Si otro usuario cambia el árbol entre este
// chequeo y el UPDATE podría pasar algo raro; con un solo admin no es un
// riesgo real, y el CHECK de la DB igual frena el caso de ser su propio padre.
func (s *CategoryStore) checkParent(ctx context.Context, id int64, parentID *int64) error {
	if parentID == nil {
		return nil
	}

	var parentActive bool
	err := s.db.QueryRowContext(ctx, `SELECT is_active FROM categories WHERE id = $1;`, *parentID).Scan(&parentActive)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.ErrInvalidReference
		}
		return err
	}
	if !parentActive {
		return store.ErrCategoryParentInactive
	}

	if id == 0 {
		return nil // al crear, la categoría nueva no puede tener hijas
	}

	// Sube desde el padre nuevo hasta la raíz (sus "ancestros"). Si en ese
	// camino aparece la categoría que se edita, el padre nuevo es ella misma
	// o una de sus subcategorías. Subir es más barato que bajar: un camino
	// hasta la raíz es corto, mientras que las subcategorías pueden ser muchas.
	// UNION (y no UNION ALL) descarta filas repetidas: si por algún error ya
	// hubiera un ciclo en la DB, la consulta termina en vez de girar para siempre.
	q := `WITH RECURSIVE ancestors AS (
	          SELECT id, parent_id FROM categories WHERE id = $1
	          UNION
	          SELECT c.id, c.parent_id
	          FROM categories c
	          JOIN ancestors a ON c.id = a.parent_id
	      )
	      SELECT EXISTS (SELECT 1 FROM ancestors WHERE id = $2);`

	var cycle bool
	if err := s.db.QueryRowContext(ctx, q, *parentID, id).Scan(&cycle); err != nil {
		return err
	}
	if cycle {
		return store.ErrCategoryCycle
	}
	return nil
}

// categoryWriteError traduce los errores de la DB al crear o editar una categoría.
func categoryWriteError(err error) error {
	switch {
	case isUniqueViolation(err):
		return store.ErrDuplicateName // mismo nombre entre hermanas
	case isForeignKeyViolation(err):
		return store.ErrInvalidReference // tax_id (o parent_id) que no existe
	case isCheckViolation(err):
		return store.ErrCategoryCycle // parent_id = id
	default:
		return err
	}
}

// requireOneRow devuelve ErrNotFound si un UPDATE o DELETE por id no tocó
// ninguna fila (el id no existe).
func requireOneRow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}
