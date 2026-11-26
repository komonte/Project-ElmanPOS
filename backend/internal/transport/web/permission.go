package web

import (
	"net/http"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
)

// Permisos en las pantallas (F-13, PR D). El server ya rechaza lo que el
// rol no puede (transport.Guard, cmd/api/routes.go); acá solo se esconden
// los links y botones que darían 403, para no ofrecer lo que no se puede.

// canMap son los permisos del usuario, para los templates: (can .Can "void").
func canMap(u *model.User) map[string]bool {
	m := make(map[string]bool, len(model.Permissions))
	for _, p := range model.Permissions {
		if u.Can(p) {
			m[string(p)] = true
		}
	}
	return m
}

// requestCan son los permisos del usuario del pedido.
func requestCan(r *http.Request) map[string]bool {
	return canMap(transport.CurrentUser(r.Context()))
}

// setCan pone los permisos del usuario del pedido en data["Can"], si no
// vienen ya.
func setCan(r *http.Request, data map[string]any) {
	if _, ok := data["Can"]; !ok {
		data["Can"] = requestCan(r)
	}
}

// can dice si el usuario del pedido tiene el permiso.
func can(r *http.Request, p model.Permission) bool {
	return transport.CurrentUser(r.Context()).Can(p)
}

// Forbidden dibuja la página "no tenés permiso" (403), con el menú del rol
// para seguir desde ahí. Es transport.Guard.Page: no es una ruta.
func (h *WebHandler) Forbidden(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	h.render(w, r, "forbidden.gohtml", map[string]any{"TitleKey": "forbidden.title",
		"Home": homeFor(transport.CurrentUser(r.Context()))})
}

// homeFor es a dónde va alguien que entra sin una página pedida: el cajero
// (que solo vende y maneja la caja) directo a vender; los demás, al inicio.
func homeFor(u *model.User) string {
	if u.Can(model.PermSell) && !u.Can(model.PermCatalog) && !u.Can(model.PermStock) {
		return "/pos"
	}
	return "/"
}
