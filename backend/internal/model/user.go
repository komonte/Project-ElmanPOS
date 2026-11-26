package model

import (
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// Usuarios y contraseñas (F-13, 02 4.23). Decisiones (2026-11-26):
//   - El usuario se guarda en minúsculas y sin espacios: al entrar, "Admin"
//     y "admin" son la misma persona (uq_users_username_lower de la 0019).
//   - La contraseña: de 8 caracteres a 72 bytes. bcrypt solo mira los
//     primeros 72 bytes y descartaría el resto sin avisar: más larga se
//     rechaza. No se recorta: un espacio también es parte de la contraseña.
//   - bcrypt con costo 12 (unos 250 ms por intento): lento a propósito,
//     para que probar contraseñas de a miles no sirva.

const (
	UsernameMinLen = 3
	UsernameMaxLen = 60  // VARCHAR(60)
	UserNameMinLen = 2   // el nombre para mostrar
	UserNameMaxLen = 120 // VARCHAR(120)

	PasswordMinLen      = 8  // caracteres
	PasswordMaxBytes    = 72 // lo que bcrypt usa
	PasswordHashingCost = 12
)

// Roles (ENUM user_role). Qué puede hacer cada uno: la matriz de F-13.
const (
	RoleAdmin      = "ADMIN"
	RoleCashier    = "CASHIER"
	RoleStockClerk = "STOCK_CLERK"
)

// Roles son los roles, en el orden en que se ofrecen.
var Roles = []string{RoleAdmin, RoleCashier, RoleStockClerk}

var (
	ErrNilUser          = errors.New("user cannot be nil")
	ErrInvalidUsername  = errors.New("username must have 3 to 60 characters: lowercase letters, digits, '.', '-' or '_'")
	ErrEmptyUserName    = errors.New("user name is required")
	ErrUserNameTooShort = errors.New("user name must be at least 2 characters")
	ErrUserNameTooLong  = errors.New("user name cannot exceed 120 characters")
	ErrInvalidUserRole  = errors.New("user role must be ADMIN, CASHIER or STOCK_CLERK")
	ErrPasswordTooShort = errors.New("password must have at least 8 characters")
	ErrPasswordTooLong  = errors.New("password cannot exceed 72 bytes")
)

// User es una persona que usa el sistema.
type User struct {
	ID       int64  `json:"id" db:"id"`
	Username string `json:"username" db:"username"`
	Name     string `json:"name" db:"name"`
	// PasswordHash es el hash bcrypt. Nunca sale en un JSON.
	PasswordHash string `json:"-" db:"password_hash"`
	Role         string `json:"role" db:"role"`
	IsActive     bool   `json:"is_active" db:"is_active"`
	CreatedAt    string `json:"created_at" db:"created_at"`
	UpdatedAt    string `json:"updated_at" db:"updated_at"`
}

// NormalizeUsername deja el usuario como se guarda: sin espacios alrededor
// y en minúsculas. El login lo usa antes de buscar.
func NormalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// Validate normaliza y valida usuario, nombre y rol. La contraseña va
// aparte (HashPassword), porque al editar un usuario no siempre cambia.
func (u *User) Validate() error {
	if u == nil {
		return ErrNilUser
	}
	u.Username = NormalizeUsername(u.Username)
	if !validUsername(u.Username) {
		return ErrInvalidUsername
	}

	u.Name = strings.Join(strings.Fields(u.Name), " ")
	switch n := utf8.RuneCountInString(u.Name); {
	case n == 0:
		return ErrEmptyUserName
	case n < UserNameMinLen:
		return ErrUserNameTooShort
	case n > UserNameMaxLen:
		return ErrUserNameTooLong
	}

	if !slices.Contains(Roles, u.Role) {
		return ErrInvalidUserRole
	}
	return nil
}

// validUsername: de 3 a 60 caracteres, solo a-z, 0-9, '.', '-' y '_' (ya
// en minúsculas). Sin acentos ni espacios: se escribe rápido y sin dudas.
func validUsername(s string) bool {
	if len(s) < UsernameMinLen || len(s) > UsernameMaxLen {
		return false
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_'
		if !ok {
			return false
		}
	}
	return true
}

// ValidatePassword revisa el largo de una contraseña nueva: al menos 8
// caracteres y no más de 72 bytes.
func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < PasswordMinLen {
		return ErrPasswordTooShort
	}
	if len(password) > PasswordMaxBytes {
		return ErrPasswordTooLong
	}
	return nil
}

// HashPassword valida la contraseña y devuelve su hash bcrypt (con su sal:
// la misma contraseña da otro hash cada vez).
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), PasswordHashingCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// CheckPassword dice si la contraseña corresponde al hash. Un hash que no
// es bcrypt (los de mentira del seed) nunca corresponde.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// UserSession es un login abierto (02 4.27, F-13): quién y hasta cuándo.
// El token no está: solo su huella vive en la base, y el token solo en la
// cookie del navegador.
type UserSession struct {
	ID        int64  `json:"id" db:"id"`
	UserID    int64  `json:"user_id" db:"user_id"`
	CreatedAt string `json:"created_at" db:"created_at"`
	ExpiresAt string `json:"expires_at" db:"expires_at"`

	// User es el usuario de la sesión (lo llena UserSessionStore.Valid).
	User *User `json:"user,omitempty" db:"-"`
}
