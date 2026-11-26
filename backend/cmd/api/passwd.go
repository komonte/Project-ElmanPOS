package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/auth"
)

// Comandos de administración (F-13). Por ahora uno: poner la contraseña de
// un usuario. Hace falta para el primer ingreso (los usuarios del seed
// tienen un hash inventado) y para cuando alguien se la olvida, hasta que
// haya una página de usuarios. Se corre en la PC del almacén, con acceso a
// la base: quien puede correrlo ya tiene todo.
//
//	elmanpos passwd <usuario>     (o: make passwd U=<usuario>)

const usage = "uso: elmanpos passwd <usuario>"

var (
	errUsage            = errors.New(usage)
	errPasswordMismatch = errors.New("las contraseñas no coinciden: no se cambió nada")
)

// runCommand corre un comando y devuelve el código de salida.
func runCommand(db *sql.DB, args []string) int {
	switch args[0] {
	case "passwd":
		svc := auth.New(db, postgres.NewTxRunner(db))
		ask := newPrompt(os.Stdin, os.Stderr)
		if err := passwd(context.Background(), svc, args[1:], ask, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	default:
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
}

// passwordSetter es lo que passwd usa de auth.Service.
type passwordSetter interface {
	SetPassword(ctx context.Context, username, password string) (*model.User, error)
}

// promptFunc muestra el texto y lee una contraseña.
type promptFunc func(prompt string) (string, error)

// passwd pide la contraseña dos veces y la guarda. Si no coinciden no cambia
// nada. Los errores salen en palabras simples.
func passwd(ctx context.Context, svc passwordSetter, args []string, ask promptFunc, out io.Writer) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return errUsage
	}
	username := args[0]
	first, err := ask(fmt.Sprintf("Contraseña nueva para %s (mínimo %d caracteres): ", username, model.PasswordMinLen))
	if err != nil {
		return err
	}
	// Antes de pedirla de nuevo: si es corta, que lo sepa ya.
	if err := model.ValidatePassword(first); err != nil {
		return passwordError(err)
	}
	second, err := ask("Repetila: ")
	if err != nil {
		return err
	}
	if first != second {
		return errPasswordMismatch
	}
	u, err := svc.SetPassword(ctx, username, first)
	switch {
	case errors.Is(err, service.ErrNotFound):
		return fmt.Errorf("no existe el usuario %q", username)
	case errors.Is(err, service.ErrValidation):
		return passwordError(err)
	case err != nil:
		return err
	}
	fmt.Fprintf(out, "Listo: %s (%s) tiene contraseña nueva. Sus sesiones abiertas se cerraron.\n", u.Username, u.Name)
	return nil
}

func passwordError(err error) error {
	switch {
	case errors.Is(err, model.ErrPasswordTooShort):
		return fmt.Errorf("la contraseña tiene que tener al menos %d caracteres", model.PasswordMinLen)
	case errors.Is(err, model.ErrPasswordTooLong):
		return fmt.Errorf("la contraseña es demasiado larga (hasta %d letras sin acento)", model.PasswordMaxBytes)
	}
	return err
}

// newPrompt lee contraseñas de in. En una terminal no se ven al escribir;
// si no es una terminal (un script con un pipe), lee una línea por vez.
func newPrompt(in *os.File, out io.Writer) promptFunc {
	if term.IsTerminal(int(in.Fd())) {
		return func(prompt string) (string, error) {
			fmt.Fprint(out, prompt)
			b, err := term.ReadPassword(int(in.Fd()))
			fmt.Fprintln(out)
			return string(b), err
		}
	}
	return linePrompt(bufio.NewReader(in), out)
}

// linePrompt lee una línea por contraseña (sin el salto de línea final).
func linePrompt(r *bufio.Reader, out io.Writer) promptFunc {
	return func(prompt string) (string, error) {
		fmt.Fprint(out, prompt)
		line, err := r.ReadString('\n')
		fmt.Fprintln(out) // lo escrito no se ve: el salto lo pone el programa
		if err != nil && !(errors.Is(err, io.EOF) && line != "") {
			return "", fmt.Errorf("no se pudo leer la contraseña: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
}
