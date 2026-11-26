package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
)

// fakeSetter anota con qué lo llamaron y devuelve err.
type fakeSetter struct {
	calls              int
	username, password string
	err                error
}

func (f *fakeSetter) SetPassword(_ context.Context, username, password string) (*model.User, error) {
	f.calls++
	f.username, f.password = username, password
	if f.err != nil {
		return nil, f.err
	}
	return &model.User{Username: model.NormalizeUsername(username), Name: "Admin"}, nil
}

// answers responde en orden y anota qué se preguntó.
func answers(asked *[]string, replies ...string) promptFunc {
	return func(prompt string) (string, error) {
		*asked = append(*asked, prompt)
		if len(replies) == 0 {
			return "", errors.New("no more input")
		}
		r := replies[0]
		replies = replies[1:]
		return r, nil
	}
}

func TestPasswd(t *testing.T) {
	ctx := context.Background()

	t.Run("ok", func(t *testing.T) {
		var asked []string
		var out bytes.Buffer
		s := &fakeSetter{}
		if err := passwd(ctx, s, []string{"Admin"}, answers(&asked, "clave del almacén", "clave del almacén"), &out); err != nil {
			t.Fatal(err)
		}
		if s.calls != 1 || s.username != "Admin" || s.password != "clave del almacén" {
			t.Errorf("setter: got %+v", s)
		}
		if len(asked) != 2 || !strings.Contains(asked[0], "Admin") || !strings.Contains(asked[0], "mínimo 8") {
			t.Errorf("prompts: got %q", asked)
		}
		if !strings.Contains(out.String(), "Listo: admin (Admin)") || !strings.Contains(out.String(), "sesiones abiertas se cerraron") {
			t.Errorf("output: got %q", out.String())
		}
	})

	t.Run("mismatch changes nothing", func(t *testing.T) {
		var asked []string
		s := &fakeSetter{}
		err := passwd(ctx, s, []string{"admin"}, answers(&asked, "clave del almacén", "clave del almacen"), &bytes.Buffer{})
		if !errors.Is(err, errPasswordMismatch) || s.calls != 0 {
			t.Errorf("got %v, %d calls", err, s.calls)
		}
	})

	t.Run("short is told before asking again", func(t *testing.T) {
		var asked []string
		s := &fakeSetter{}
		err := passwd(ctx, s, []string{"admin"}, answers(&asked, "corta", "corta"), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "al menos 8 caracteres") || len(asked) != 1 || s.calls != 0 {
			t.Errorf("got %v, asked %d, %d calls", err, len(asked), s.calls)
		}
	})

	t.Run("too long", func(t *testing.T) {
		var asked []string
		long := strings.Repeat("ñ", 40)
		err := passwd(ctx, &fakeSetter{}, []string{"admin"}, answers(&asked, long, long), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "demasiado larga") {
			t.Errorf("got %v", err)
		}
	})

	t.Run("unknown user", func(t *testing.T) {
		var asked []string
		s := &fakeSetter{err: service.ErrNotFound}
		err := passwd(ctx, s, []string{"nadie"}, answers(&asked, "clave larga", "clave larga"), &bytes.Buffer{})
		if err == nil || err.Error() != `no existe el usuario "nadie"` {
			t.Errorf("got %v", err)
		}
	})

	t.Run("other errors pass through", func(t *testing.T) {
		var asked []string
		boom := errors.New("db down")
		err := passwd(ctx, &fakeSetter{err: boom}, []string{"admin"}, answers(&asked, "clave larga", "clave larga"), &bytes.Buffer{})
		if !errors.Is(err, boom) {
			t.Errorf("got %v", err)
		}
	})

	t.Run("bad arguments", func(t *testing.T) {
		for _, args := range [][]string{nil, {""}, {"  "}, {"a", "b"}} {
			var asked []string
			s := &fakeSetter{}
			if err := passwd(ctx, s, args, answers(&asked), &bytes.Buffer{}); !errors.Is(err, errUsage) || len(asked) != 0 {
				t.Errorf("%q: got %v", args, err)
			}
		}
	})

	t.Run("input ends", func(t *testing.T) {
		var asked []string
		if err := passwd(ctx, &fakeSetter{}, []string{"admin"}, answers(&asked), &bytes.Buffer{}); err == nil {
			t.Error("want an error")
		}
	})
}

func TestLinePrompt(t *testing.T) {
	var out bytes.Buffer
	ask := linePrompt(bufio.NewReader(strings.NewReader("clave uno\r\n con espacios \nsin salto")), &out)
	for _, want := range []string{"clave uno", " con espacios ", "sin salto"} {
		got, err := ask("> ")
		if err != nil || got != want {
			t.Errorf("got %q, %v, want %q", got, err, want)
		}
	}
	if _, err := ask("> "); err == nil {
		t.Error("end of input: want an error")
	}
	if out.String() != strings.Repeat("> \n", 4) {
		t.Errorf("prompts: got %q", out.String())
	}
}
