package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// StoreStore lee los locales (tabla stores, 02 4.8). El nombre repite
// "store" porque el local del almacén se llama así en el modelo de datos.
type StoreStore struct {
	db DBTX
}

func NewStoreStore(db DBTX) *StoreStore {
	return &StoreStore{db: db}
}

// ActiveID devuelve el id del único local activo: ahí entra el stock de una
// compra (F-06), que no dice a qué local va.
//   - ErrNoActiveStore: no hay ninguno.
//   - ErrSeveralActiveStores: hay más de uno. No se elige uno al azar: el
//     stock quedaría en el local equivocado sin que nadie se entere. El día
//     que haya dos locales, la compra va a tener que decir cuál.
func (s *StoreStore) ActiveID(ctx context.Context) (int64, error) {
	// LIMIT 2: alcanza para saber si hay uno solo.
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM stores WHERE is_active ORDER BY id LIMIT 2;`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	switch len(ids) {
	case 0:
		return 0, store.ErrNoActiveStore
	case 1:
		return ids[0], nil
	default:
		return 0, store.ErrSeveralActiveStores
	}
}

// Lock traba la fila del local hasta que termine la transacción. Sirve para
// que dos pedidos que crean algo único del local (la primera caja) hagan
// fila en vez de crear dos. ErrNotFound si el local no existe.
func (s *StoreStore) Lock(ctx context.Context, id int64) error {
	var got int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM stores WHERE id = $1 FOR NO KEY UPDATE;`, id).Scan(&got)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	return err
}
