package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/users"
)

// Página de usuarios (F-13, PR F): solo el admin (model.PermUsers). Las
// reglas (no a sí mismo, siempre un admin activo) están en usecase/users;
// acá solo se esconde lo que daría error, como el botón "Desactivar" en la
// fila propia.

// errPasswordMismatch: las dos contraseñas del formulario no coinciden. No
// llega al caso de uso: es del formulario.
var errPasswordMismatch = errors.New("the passwords do not match")

// WithUsers le da al handler la página de usuarios. Resetear la contraseña
// usa el auth de WithAuth.
func (h *WebHandler) WithUsers(u *users.Service) *WebHandler {
	h.users = u
	return h
}

// userRow es una fila de la tabla: el usuario y si es quien mira.
type userRow struct {
	*model.User
	Self bool
}

func userRows(list []*model.User, me int64) []userRow {
	rows := make([]userRow, len(list))
	for i, u := range list {
		rows[i] = userRow{User: u, Self: u.ID == me}
	}
	return rows
}

// UsersList handles GET /users
func (h *WebHandler) UsersList(w http.ResponseWriter, r *http.Request) {
	list, err := h.users.List(r.Context())
	if err != nil {
		http.Error(w, h.errMsg(r, err), http.StatusInternalServerError)
		return
	}
	h.render(w, r, "users.gohtml", map[string]any{
		"Users":    userRows(list, transport.UserID(r.Context())),
		"Section":  "users",
		"TitleKey": "user.title",
	})
}

func (h *WebHandler) usersTable(w http.ResponseWriter, r *http.Request) {
	list, err := h.users.List(r.Context())
	if err != nil {
		h.actionError(w, r, err)
		return
	}
	h.renderPartial(w, r, "user_table.gohtml", map[string]any{
		"Users": userRows(list, transport.UserID(r.Context())),
	})
}

// renderUserForm dibuja el alta o la edición. u es nil en un alta vacía.
func (h *WebHandler) renderUserForm(w http.ResponseWriter, r *http.Request, u *model.User, errMsg string) {
	self := u != nil && u.ID != 0 && u.ID == transport.UserID(r.Context())
	h.renderPartial(w, r, "user_form.gohtml", map[string]any{
		"User": u, "Self": self, "Roles": model.Roles, "Error": errMsg,
		"PasswordMinLen": model.PasswordMinLen,
	})
}

// UsersNewForm handles GET /users/new
func (h *WebHandler) UsersNewForm(w http.ResponseWriter, r *http.Request) {
	h.renderUserForm(w, r, nil, "")
}

// UsersCreate handles POST /users/new
func (h *WebHandler) UsersCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderUserForm(w, r, nil, h.errMsg(r, err))
		return
	}
	u := &model.User{Username: r.FormValue("username"), Name: r.FormValue("name"), Role: r.FormValue("role")}
	password := r.FormValue("password")
	if password != r.FormValue("password_again") {
		h.renderUserForm(w, r, u, h.errMsg(r, errPasswordMismatch))
		return
	}
	if _, err := h.users.Create(r.Context(), transport.UserID(r.Context()), u, password); err != nil {
		h.renderUserForm(w, r, u, h.errMsg(r, err))
		return
	}
	w.Header().Set("HX-Redirect", "/users")
	w.WriteHeader(http.StatusOK)
}

// userFromPath busca el usuario del {id}. Si no puede, ya respondió.
func (h *WebHandler) userFromPath(w http.ResponseWriter, r *http.Request) (*model.User, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, h.tr(r, "error.invalid_id"), http.StatusBadRequest)
		return nil, false
	}
	u, err := h.users.Get(r.Context(), id)
	if err != nil {
		h.actionError(w, r, err)
		return nil, false
	}
	return u, true
}

// UsersEditForm handles GET /users/{id}/edit
func (h *WebHandler) UsersEditForm(w http.ResponseWriter, r *http.Request) {
	u, ok := h.userFromPath(w, r)
	if !ok {
		return
	}
	h.renderUserForm(w, r, u, "")
}

// UsersUpdate handles POST /users/{id}/edit
func (h *WebHandler) UsersUpdate(w http.ResponseWriter, r *http.Request) {
	u, ok := h.userFromPath(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderUserForm(w, r, u, h.errMsg(r, err))
		return
	}
	ctx := r.Context()
	edited := *u
	edited.Name, edited.Role = r.FormValue("name"), r.FormValue("role")
	if _, err := h.users.Update(ctx, transport.UserID(ctx), u.ID, edited.Name, edited.Role); err != nil {
		h.renderUserForm(w, r, &edited, h.errMsg(r, err))
		return
	}
	w.Header().Set("HX-Redirect", "/users")
	w.WriteHeader(http.StatusOK)
}

// renderPasswordForm dibuja el formulario de contraseña nueva, o el aviso
// de que quedó (Done).
func (h *WebHandler) renderPasswordForm(w http.ResponseWriter, r *http.Request, u *model.User, errMsg string, done bool) {
	h.renderPartial(w, r, "user_password.gohtml", map[string]any{
		"User": u, "Self": u.ID == transport.UserID(r.Context()), "Error": errMsg, "Done": done,
		"PasswordMinLen": model.PasswordMinLen,
	})
}

// UsersPasswordForm handles GET /users/{id}/password
func (h *WebHandler) UsersPasswordForm(w http.ResponseWriter, r *http.Request) {
	u, ok := h.userFromPath(w, r)
	if !ok {
		return
	}
	h.renderPasswordForm(w, r, u, "", false)
}

// UsersSetPassword handles POST /users/{id}/password. Cierra todas las
// sesiones del usuario: si es el admin que mira, también la suya, y va al
// login.
func (h *WebHandler) UsersSetPassword(w http.ResponseWriter, r *http.Request) {
	u, ok := h.userFromPath(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderPasswordForm(w, r, u, h.errMsg(r, err), false)
		return
	}
	password := r.FormValue("password")
	if password != r.FormValue("password_again") {
		h.renderPasswordForm(w, r, u, h.errMsg(r, errPasswordMismatch), false)
		return
	}
	ctx := r.Context()
	if _, err := h.auth.SetPassword(ctx, transport.UserID(ctx), u.Username, password); err != nil {
		h.renderPasswordForm(w, r, u, h.errMsg(r, err), false)
		return
	}
	if u.ID == transport.UserID(ctx) {
		w.Header().Set("HX-Redirect", "/login")
		w.WriteHeader(http.StatusOK)
		return
	}
	h.renderPasswordForm(w, r, u, "", true)
}

// UsersDeactivate handles PATCH /users/{id}/deactivate
func (h *WebHandler) UsersDeactivate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, h.tr(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if err := h.users.Deactivate(ctx, transport.UserID(ctx), id); err != nil {
		h.actionError(w, r, err)
		return
	}
	h.usersTable(w, r)
}

// UsersReactivate handles PATCH /users/{id}/reactivate
func (h *WebHandler) UsersReactivate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, h.tr(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if err := h.users.Reactivate(ctx, transport.UserID(ctx), id); err != nil {
		h.actionError(w, r, err)
		return
	}
	h.usersTable(w, r)
}
