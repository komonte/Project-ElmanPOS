package model

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestUserValidate(t *testing.T) {
	valid := func() *User { return &User{Username: "cajero", Name: "Juan Pérez", Role: RoleCashier} }

	t.Run("nil", func(t *testing.T) {
		var u *User
		if err := u.Validate(); !errors.Is(err, ErrNilUser) {
			t.Errorf("got %v", err)
		}
	})

	t.Run("normalizes", func(t *testing.T) {
		u := &User{Username: "  Cajero.Tarde ", Name: "  Juan   Pérez ", Role: RoleCashier}
		if err := u.Validate(); err != nil {
			t.Fatal(err)
		}
		if u.Username != "cajero.tarde" || u.Name != "Juan Pérez" {
			t.Errorf("got %q / %q", u.Username, u.Name)
		}
	})

	tests := []struct {
		name string
		edit func(*User)
		want error
	}{
		{"valid", func(u *User) {}, nil},
		{"every role", func(u *User) { u.Role = RoleStockClerk }, nil},
		{"admin", func(u *User) { u.Role = RoleAdmin }, nil},
		{"username with digits, dot, dash, underscore", func(u *User) { u.Username = "caja_1.tarde-x" }, nil},
		{"username too short", func(u *User) { u.Username = "ab" }, ErrInvalidUsername},
		{"username too long", func(u *User) { u.Username = strings.Repeat("a", 61) }, ErrInvalidUsername},
		{"username 60 is fine", func(u *User) { u.Username = strings.Repeat("a", 60) }, nil},
		{"username with a space", func(u *User) { u.Username = "juan perez" }, ErrInvalidUsername},
		{"username with an accent", func(u *User) { u.Username = "josé" }, ErrInvalidUsername},
		{"username empty", func(u *User) { u.Username = "   " }, ErrInvalidUsername},
		{"name empty", func(u *User) { u.Name = "  " }, ErrEmptyUserName},
		{"name too short", func(u *User) { u.Name = "J" }, ErrUserNameTooShort},
		{"name too long", func(u *User) { u.Name = strings.Repeat("á", 121) }, ErrUserNameTooLong},
		{"role empty", func(u *User) { u.Role = "" }, ErrInvalidUserRole},
		{"role lowercase", func(u *User) { u.Role = "admin" }, ErrInvalidUserRole},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := valid()
			tt.edit(u)
			if err := u.Validate(); !errors.Is(err, tt.want) {
				t.Errorf("got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestNormalizeUsername(t *testing.T) {
	if got := NormalizeUsername("  ADMIN\t"); got != "admin" {
		t.Errorf("got %q", got)
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name, password string
		want           error
	}{
		{"8 characters", "12345678", nil},
		{"7 characters", "1234567", ErrPasswordTooShort},
		{"empty", "", ErrPasswordTooShort},
		{"8 characters with accents (more bytes, counts characters)", "ñandúñan", nil},
		{"spaces count", "    abcd", nil},
		{"72 bytes", strings.Repeat("a", 72), nil},
		{"73 bytes", strings.Repeat("a", 73), ErrPasswordTooLong},
		{"40 ñ are 80 bytes", strings.Repeat("ñ", 40), ErrPasswordTooLong},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidatePassword(tt.password); !errors.Is(err, tt.want) {
				t.Errorf("got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestHashPassword(t *testing.T) {
	hash, err := HashPassword("caja del almacén")
	if err != nil {
		t.Fatal(err)
	}
	if cost, err := bcrypt.Cost([]byte(hash)); err != nil || cost != PasswordHashingCost {
		t.Errorf("cost: got %d, %v", cost, err)
	}
	if len(hash) > 120 {
		t.Errorf("hash does not fit VARCHAR(120): %d", len(hash))
	}
	if !CheckPassword(hash, "caja del almacén") {
		t.Error("the right password must match")
	}
	for _, wrong := range []string{"caja del almacen", "Caja del almacén", "caja del almacén ", ""} {
		if CheckPassword(hash, wrong) {
			t.Errorf("%q must not match", wrong)
		}
	}

	// Con sal: la misma contraseña da otro hash.
	again, err := HashPassword("caja del almacén")
	if err != nil || again == hash {
		t.Errorf("same hash twice: %v", err)
	}

	if _, err := HashPassword("corta"); !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("short: got %v", err)
	}
	if _, err := HashPassword(strings.Repeat("a", 73)); !errors.Is(err, ErrPasswordTooLong) {
		t.Errorf("long: got %v", err)
	}
}

func TestCheckPasswordSeedPlaceholder(t *testing.T) {
	// Los usuarios del seed tienen un hash de mentira: nadie entra con ellos.
	if CheckPassword("$2a$10$placeholder-placeholder-placeholder", "placeholder") {
		t.Error("a placeholder hash must never match")
	}
	if CheckPassword("", "") {
		t.Error("an empty hash must never match")
	}
}

func TestUserJSONHidesHash(t *testing.T) {
	b, err := json.Marshal(&User{Username: "admin", PasswordHash: "$2a$12$secret"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "password") {
		t.Errorf("hash leaked: %s", b)
	}
}
