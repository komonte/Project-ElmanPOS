// Package auth es el caso de uso F-13: entrar con usuario y contraseña,
// saber quién es cada pedido, salir, y poner contraseñas.
//
// Decisiones (2026-11-26):
//   - Sesión en la base (user_sessions de la 0019), no JWT: salir o
//     desactivar a alguien corta en el momento. El navegador guarda un token
//     al azar (32 bytes); la base, solo su SHA-256.
//   - Una sesión dura 12 horas desde que se entra: una jornada.
//   - "Usuario o contraseña incorrectos" sirve para todo: usuario que no
//     existe, inactivo o contraseña mal. Así no se puede averiguar qué
//     usuarios hay. Con un usuario que no existe igual se compara contra un
//     hash, para tardar lo mismo.
//   - 5 fallos seguidos de un usuario lo traban 5 minutos, aunque después
//     ponga la contraseña bien. Se lleva en memoria: reiniciar el server lo
//     olvida, y alcanza para un almacén.
//   - Cada login y cada cambio de contraseña quedan en audit_log. Los
//     intentos fallidos van al log del server, no a la base.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

const (
	SessionDuration = 12 * time.Hour
	MaxFailures     = 5
	LockoutDuration = 5 * time.Minute
	tokenBytes      = 32
	// maxTracked limita cuántos usuarios distintos se recuerdan con fallos:
	// alguien que prueba miles de nombres no llena la memoria.
	maxTracked = 10000
)

var (
	// ErrInvalidCredentials: usuario que no existe, inactivo o contraseña
	// mal. A propósito no dice cuál.
	ErrInvalidCredentials = errors.New("invalid username or password")
	// ErrTooManyAttempts: el usuario está trabado por muchos fallos.
	ErrTooManyAttempts = errors.New("too many failed attempts: wait a few minutes")
	// ErrNotAuthenticated: sin token, o la sesión no existe, venció o es de
	// un usuario inactivo. Hay que volver a entrar.
	ErrNotAuthenticated = errors.New("not logged in")
)

// Login es un ingreso correcto: el token va a la cookie y no se guarda en
// ningún otro lado.
type Login struct {
	Token     string
	User      *model.User
	ExpiresAt time.Time
}

type Service struct {
	db postgres.DBTX     // lecturas
	tx postgres.TxRunner // login, logout, contraseñas
	// now es el reloj (los tests lo adelantan para probar la traba).
	now func() time.Time

	mu       sync.Mutex
	failures map[string]*failures
}

// failures son los fallos seguidos de un usuario.
type failures struct {
	count       int
	lockedUntil time.Time
}

func New(db postgres.DBTX, tx postgres.TxRunner) *Service {
	return &Service{db: db, tx: tx, now: time.Now, failures: make(map[string]*failures)}
}

// HashToken es la huella con que el token se guarda y se busca.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// dummyHash es un hash bcrypt de una contraseña al azar, del mismo costo
// que los de verdad: comparar contra él tarda lo mismo que contra un
// usuario que existe. Se calcula una vez, en el primer login.
var dummyHash = sync.OnceValue(func() string {
	b := make([]byte, 16)
	rand.Read(b)
	h, err := model.HashPassword(base64.RawStdEncoding.EncodeToString(b))
	if err != nil {
		panic(err) // 22 caracteres: siempre es válida
	}
	return h
})

// Login entra con usuario y contraseña y abre una sesión de 12 horas.
//   - ErrInvalidCredentials: no existe, está inactivo o la contraseña no es.
//   - ErrTooManyAttempts: 5 fallos seguidos lo trabaron por 5 minutos.
func (s *Service) Login(ctx context.Context, username, password string) (*Login, error) {
	username = model.NormalizeUsername(username)
	if s.locked(username) {
		slog.Warn("login blocked: too many failed attempts", "username", username)
		return nil, ErrTooManyAttempts
	}

	u, err := postgres.NewUserStore(s.db).ByUsername(ctx, username)
	switch {
	case errors.Is(err, store.ErrNotFound):
		model.CheckPassword(dummyHash(), password) // tardar lo mismo
		s.fail(username)
		// Sin el nombre: si alguien escribió la contraseña en el campo del
		// usuario, no queda en el log.
		slog.Warn("login failed: unknown username")
		return nil, ErrInvalidCredentials
	case err != nil:
		return nil, err
	}
	if !model.CheckPassword(u.PasswordHash, password) || !u.IsActive {
		if s.fail(username) {
			slog.Warn("login failed: user locked for a while", "username", username, "minutes", LockoutDuration.Minutes())
		} else {
			slog.Warn("login failed", "username", username, "active", u.IsActive)
		}
		return nil, ErrInvalidCredentials
	}

	token, err := newToken()
	if err != nil {
		return nil, err
	}
	expires := s.now().Add(SessionDuration)
	err = s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		sessions := postgres.NewUserSessionStore(tx)
		if _, err := sessions.DeleteExpired(ctx); err != nil {
			return err
		}
		sess, err := sessions.Create(ctx, u.ID, HashToken(token), expires)
		if err != nil {
			return err
		}
		_, err = postgres.NewAuditLogStore(tx).Create(ctx, postgres.AuditEntry{
			UserID: &u.ID, Table: "users", Action: "LOGIN", RecordID: u.ID,
			NewData: map[string]any{"session_id": sess.ID, "expires_at": expires},
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	s.succeed(username)
	return &Login{Token: token, User: u, ExpiresAt: expires}, nil
}

// Authenticate devuelve la sesión del token con su usuario.
// ErrNotAuthenticated si no hay token o la sesión no vale.
func (s *Service) Authenticate(ctx context.Context, token string) (*model.UserSession, error) {
	if token == "" {
		return nil, ErrNotAuthenticated
	}
	sess, err := postgres.NewUserSessionStore(s.db).Valid(ctx, HashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotAuthenticated
	}
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// Logout cierra la sesión del token. Sin token, o con una que ya no vale,
// no hace nada.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return postgres.NewUserSessionStore(s.db).Delete(ctx, HashToken(token))
}

// SetPassword pone una contraseña nueva (el comando elmanpos passwd) y
// cierra todas las sesiones del usuario: si alguien sabía la anterior, deja
// de entrar en el momento. También le saca la traba por fallos.
//   - service.ErrValidation: la contraseña es corta o larga.
//   - service.ErrNotFound: el usuario no existe.
func (s *Service) SetPassword(ctx context.Context, username, password string) (*model.User, error) {
	username = model.NormalizeUsername(username)
	hash, err := model.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}
	var u *model.User
	err = s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		users := postgres.NewUserStore(tx)
		var err error
		if u, err = users.ByUsername(ctx, username); err != nil {
			return err
		}
		if err := users.SetPassword(ctx, u.ID, hash); err != nil {
			return err
		}
		closed, err := postgres.NewUserSessionStore(tx).DeleteForUser(ctx, u.ID)
		if err != nil {
			return err
		}
		// Sin user_id: lo hace el comando, no alguien que entró.
		_, err = postgres.NewAuditLogStore(tx).Create(ctx, postgres.AuditEntry{
			Table: "users", Action: "PASSWORD", RecordID: u.ID,
			NewData: map[string]any{"username": u.Username, "sessions_closed": closed},
		})
		return err
	})
	if errors.Is(err, store.ErrNotFound) {
		return nil, service.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.succeed(username)
	return u, nil
}

func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// locked dice si el usuario está trabado ahora.
func (s *Service) locked(username string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.failures[username]
	return f != nil && s.now().Before(f.lockedUntil)
}

// fail anota un fallo; al quinto seguido traba al usuario y devuelve true.
func (s *Service) fail(username string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.failures[username]
	if f == nil {
		if len(s.failures) >= maxTracked {
			s.forgetUnlocked()
		}
		f = new(failures)
		s.failures[username] = f
	}
	f.count++
	if f.count < MaxFailures {
		return false
	}
	f.count = 0
	f.lockedUntil = s.now().Add(LockoutDuration)
	return true
}

// succeed borra los fallos del usuario.
func (s *Service) succeed(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failures, username)
}

// forgetUnlocked olvida a los que no están trabados (con s.mu tomado).
func (s *Service) forgetUnlocked() {
	now := s.now()
	for name, f := range s.failures {
		if !now.Before(f.lockedUntil) {
			delete(s.failures, name)
		}
	}
}
