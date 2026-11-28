package postgres

import (
	"context"

	"github.com/lib/pq"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// ProductComponentStore guarda la receta de los combos (02 4.6, F-05): qué
// componentes lleva cada uno y cuánto. Como los códigos de barra, se maneja
// siempre desde su combo; las reglas (qué producto puede ser componente)
// las revisa el caso de uso.
type ProductComponentStore struct {
	db DBTX
}

func NewProductComponentStore(db DBTX) *ProductComponentStore {
	return &ProductComponentStore{db: db}
}

// productComponentSelect trae también lo que la pantalla y la venta
// necesitan del componente.
const productComponentSelect = `
	SELECT pc.id, pc.pack_id, pc.component_id, pc.quantity, pc.created_at,
	       p.sku, p.name, p.sale_type, p.is_active
	FROM product_components pc
	JOIN products p ON p.id = pc.component_id`

func scanProductComponent(row rowScanner) (*model.ProductComponent, error) {
	c := new(model.ProductComponent)
	err := row.Scan(&c.ID, &c.PackID, &c.ComponentID, &c.Quantity, &c.CreatedAt,
		&c.ComponentSKU, &c.ComponentName, &c.ComponentSaleType, &c.ComponentActive)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// ListByPack devuelve los componentes de un combo, en orden de id del
// componente (vacía, no nil, si no tiene). Ese orden es también el de la
// venta: así dos ventas traban el stock en el mismo orden.
func (s *ProductComponentStore) ListByPack(ctx context.Context, packID int64) ([]*model.ProductComponent, error) {
	byPack, err := s.ListByPacks(ctx, []int64{packID})
	if err != nil {
		return nil, err
	}
	if c := byPack[packID]; c != nil {
		return c, nil
	}
	return make([]*model.ProductComponent, 0), nil
}

// ListByPacks devuelve los componentes de varios combos, por combo. Los que
// no tienen componentes no están en el mapa.
func (s *ProductComponentStore) ListByPacks(ctx context.Context, packIDs []int64) (map[int64][]*model.ProductComponent, error) {
	out := map[int64][]*model.ProductComponent{}
	if len(packIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, productComponentSelect+`
		WHERE pc.pack_id = ANY($1)
		ORDER BY pc.pack_id, pc.component_id;`, pq.Array(packIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanProductComponent(rows)
		if err != nil {
			return nil, err
		}
		out[c.PackID] = append(out[c.PackID], c)
	}
	return out, rows.Err()
}

// Set pone un componente en el combo con esa cantidad: si ya estaba, le
// cambia la cantidad. Devuelve la fila como quedó.
//   - store.ErrInvalidReference: el combo o el componente no existen.
func (s *ProductComponentStore) Set(ctx context.Context, c *model.ProductComponent) (*model.ProductComponent, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO product_components (pack_id, component_id, quantity)
		VALUES ($1, $2, $3)
		ON CONFLICT (pack_id, component_id) DO UPDATE SET quantity = EXCLUDED.quantity
		RETURNING id;`, c.PackID, c.ComponentID, c.Quantity).Scan(&id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return nil, store.ErrInvalidReference
		}
		return nil, err
	}
	return scanProductComponent(s.db.QueryRowContext(ctx, productComponentSelect+`
		WHERE pc.id = $1;`, id))
}

// Delete quita un componente de su combo (solo desde ese combo).
// store.ErrNotFound si ese combo no tiene ese renglón.
func (s *ProductComponentStore) Delete(ctx context.Context, packID, id int64) error {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM product_components WHERE id = $1 AND pack_id = $2;`, id, packID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}
