package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// UserStore guarda los usuarios (02 4.23, F-13).
type UserStore struct {
	db DBTX
}

func NewUserStore(db DBTX) *UserStore {
	return &UserStore{db: db}
}

const userColumns = `u.id, u.username, u.name, u.password_hash, u.role, u.is_active, u.created_at, u.updated_at`

func scanUser(row interface{ Scan(...any) error }) (*model.User, error) {
	u := new(model.User)
	err := row.Scan(&u.ID, &u.Username, &u.Name, &u.PasswordHash, &u.Role, &u.IsActive, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// Create guarda un usuario ya validado y con el hash de su contraseña.
// ErrDuplicateUsername si el usuario existe (sin importar mayúsculas).
func (s *UserStore) Create(ctx context.Context, u *model.User) error {
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO users (username, name, password_hash, role, is_active)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, updated_at;`,
		u.Username, u.Name, u.PasswordHash, u.Role, u.IsActive).Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt)
	if isUniqueViolation(err) {
		return store.ErrDuplicateUsername
	}
	return err
}

// ByUsername busca un usuario, activo o no, sin importar mayúsculas (el
// login). ErrNotFound si no existe.
func (s *UserStore) ByUsername(ctx context.Context, username string) (*model.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `
		SELECT `+userColumns+` FROM users u WHERE lower(u.username) = lower($1);`, username))
}

// Get devuelve un usuario, activo o no. ErrNotFound si no existe.
func (s *UserStore) Get(ctx context.Context, id int64) (*model.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `
		SELECT `+userColumns+` FROM users u WHERE u.id = $1;`, id))
}

// SetPassword cambia el hash de la contraseña. ErrNotFound si el usuario no
// existe. Las sesiones abiertas siguen: quien llama decide si las cierra
// (UserSessionStore.DeleteForUser).
func (s *UserStore) SetPassword(ctx context.Context, id int64, hash string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1;`, id, hash)
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

// List devuelve todos los usuarios, activos o no: primero los activos, y
// cada grupo por nombre (la página de usuarios). Son pocos: sin búsqueda ni
// páginas.
func (s *UserStore) List(ctx context.Context) ([]*model.User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+userColumns+` FROM users u
		ORDER BY u.is_active DESC, lower(u.name), u.id;`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, u)
	}
	return list, rows.Err()
}

// Update cambia el nombre y el rol (ya validados). El usuario con que se
// entra no cambia. ErrNotFound si no existe.
func (s *UserStore) Update(ctx context.Context, id int64, name, role string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET name = $2, role = $3 WHERE id = $1;`, id, name, role)
	if err != nil {
		return err
	}
	return requireOneRow(res)
}

// SetActive activa o desactiva un usuario. ErrNotFound si no existe. Las
// sesiones abiertas no se tocan: quien llama las cierra al desactivar
// (UserSessionStore.DeleteForUser).
func (s *UserStore) SetActive(ctx context.Context, id int64, active bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET is_active = $2 WHERE id = $1;`, id, active)
	if err != nil {
		return err
	}
	return requireOneRow(res)
}

// LockActiveAdmins bloquea las filas de los administradores activos hasta
// el fin de la transacción y devuelve sus id. Se llama antes de desactivar
// a un admin o sacarle el rol, para no dejar el sistema sin ninguno: si dos
// admins se sacan el rol uno al otro a la vez, la segunda transacción espera
// a la primera y vuelve a leer, así que ya no cuenta al que dejó de ser
// admin. Solo sirve dentro de una transacción.
func (s *UserStore) LockActiveAdmins(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM users WHERE role = 'ADMIN' AND is_active
		ORDER BY id FOR UPDATE;`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// UserSessionStore guarda los logins abiertos (02 4.27, 0019). Recibe y
// busca la huella del token (SHA-256), nunca el token.
type UserSessionStore struct {
	db DBTX
}

func NewUserSessionStore(db DBTX) *UserSessionStore {
	return &UserSessionStore{db: db}
}

// Create abre una sesión del usuario hasta expiresAt.
func (s *UserSessionStore) Create(ctx context.Context, userID int64, tokenHash []byte, expiresAt time.Time) (*model.UserSession, error) {
	sess := &model.UserSession{UserID: userID}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO user_sessions (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
		RETURNING id, created_at, expires_at;`, userID, tokenHash, expiresAt).Scan(&sess.ID, &sess.CreatedAt, &sess.ExpiresAt)
	if isForeignKeyViolation(err) {
		return nil, store.ErrInvalidReference
	}
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// Valid devuelve la sesión de esa huella con su usuario, solo si no venció
// y el usuario está activo. Si no, ErrNotFound: para quien entra da igual
// por qué (tiene que volver a entrar).
func (s *UserSessionStore) Valid(ctx context.Context, tokenHash []byte) (*model.UserSession, error) {
	sess := new(model.UserSession)
	u := new(model.User)
	err := s.db.QueryRowContext(ctx, `
		SELECT us.id, us.user_id, us.created_at, us.expires_at, `+userColumns+`
		FROM user_sessions us
		JOIN users u ON u.id = us.user_id
		WHERE us.token_hash = $1 AND us.expires_at > now() AND u.is_active;`, tokenHash).Scan(
		&sess.ID, &sess.UserID, &sess.CreatedAt, &sess.ExpiresAt,
		&u.ID, &u.Username, &u.Name, &u.PasswordHash, &u.Role, &u.IsActive, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sess.User = u
	return sess, nil
}

// Delete cierra la sesión de esa huella (salir). Si no existe, no hace nada.
func (s *UserSessionStore) Delete(ctx context.Context, tokenHash []byte) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM user_sessions WHERE token_hash = $1;`, tokenHash)
	return err
}

// DeleteForUser cierra todas las sesiones de un usuario (al desactivarlo o
// cambiarle la contraseña) y dice cuántas eran.
func (s *UserSessionStore) DeleteForUser(ctx context.Context, userID int64) (int64, error) {
	return s.exec(ctx, `DELETE FROM user_sessions WHERE user_id = $1;`, userID)
}

// DeleteExpired borra las sesiones vencidas (ya no sirven) y dice cuántas
// eran. Se llama al entrar, así la tabla no crece.
func (s *UserSessionStore) DeleteExpired(ctx context.Context) (int64, error) {
	return s.exec(ctx, `DELETE FROM user_sessions WHERE expires_at <= now();`)
}

func (s *UserSessionStore) exec(ctx context.Context, q string, args ...any) (int64, error) {
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
