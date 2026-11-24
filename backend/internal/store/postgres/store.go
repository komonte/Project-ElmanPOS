package postgres

import "context"

// Store es lo que toda tabla maestra (D-04) tiene que implementar para
// usarse con service.Service[T]. Incluye la baja lógica vía is_active.
type Store[T any] interface {
	Create(ctx context.Context, entity *T) (*T, error)
	GetAll(ctx context.Context) ([]*T, error)
	GetAllIncludingInactive(ctx context.Context) ([]*T, error)
	GetByID(ctx context.Context, id int64) (*T, error)
	SearchByName(ctx context.Context, query string) ([]*T, error)
	SearchAllByName(ctx context.Context, query string) ([]*T, error)
	Update(ctx context.Context, id int64, entity *T) (*T, error)
	Delete(ctx context.Context, id int64) error
	SetActive(ctx context.Context, id int64, active bool) error

	// ListAll es la lista de admin: activos e inactivos, filtrados por query
	// (vacío = todos), solo los primeros limit, y cuántos pasan el filtro en
	// total. Así una tabla de miles de filas no viaja entera para mostrar 50.
	ListAll(ctx context.Context, query string, limit int) ([]*T, int, error)
}
