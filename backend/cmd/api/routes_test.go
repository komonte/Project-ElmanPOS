package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport/web"
)

var roleUsers = map[string]*model.User{
	model.RoleAdmin:      {ID: 1, Username: "admin", Role: model.RoleAdmin, IsActive: true},
	model.RoleCashier:    {ID: 2, Username: "cajero", Role: model.RoleCashier, IsActive: true},
	model.RoleStockClerk: {ID: 3, Username: "stock", Role: model.RoleStockClerk, IsActive: true},
}

// stubMux arma las rutas de verdad (tabla, permisos y patrones) con
// handlers que responden 200, y pone al usuario como lo haría
// AuthMiddleware.
func stubMux(t *testing.T) func(u *model.User, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	routes := webRoutes(nil)
	for i := range routes {
		routes[i].handler = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
	}
	mux := http.NewServeMux()
	registerRoutes(mux, transport.Guard{}, routes)
	mountAPI(mux, transport.Guard{}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	return func(u *model.User, method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if u != nil {
			r = r.WithContext(transport.WithUser(r.Context(), u))
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
}

var wildcard = regexp.MustCompile(`\{[a-zA-Z]+\}`)

// samplePath convierte un patrón en un pedido: "POST /counts/{id}/confirm"
// es POST /counts/1/confirm.
func samplePath(pattern string) (method, path string) {
	method, path, _ = strings.Cut(pattern, " ")
	path = strings.ReplaceAll(path, "{$}", "")
	return method, wildcard.ReplaceAllString(path, "1")
}

// TestRoutesByRole prueba cada ruta con cada rol: pasa si el rol tiene el
// permiso de la ruta, si no 403.
func TestRoutesByRole(t *testing.T) {
	do := stubMux(t)
	for _, rt := range webRoutes(nil) {
		method, path := samplePath(rt.pattern)
		for role, u := range roleUsers {
			want := http.StatusForbidden
			if rt.perm == public || rt.perm == loggedIn || model.Can(role, rt.perm) {
				want = http.StatusOK
			}
			if w := do(u, method, path); w.Code != want {
				t.Errorf("%s %s as %s: got %d, want %d", method, path, role, w.Code, want)
			}
		}
	}
}

// TestRoutesMatrix es la matriz de F-13 escrita a mano, para que un permiso
// mal puesto en la tabla no pase sin que se note.
func TestRoutesMatrix(t *testing.T) {
	do := stubMux(t)
	const A, C, S = model.RoleAdmin, model.RoleCashier, model.RoleStockClerk
	tests := []struct {
		method, path string
		allowed      []string
	}{
		{"GET", "/", []string{A, C, S}},
		{"POST", "/logout", []string{A, C, S}},
		{"GET", "/pos", []string{A, C}},
		{"POST", "/pos/checkout", []string{A, C}},
		{"GET", "/cash", []string{A, C}},
		{"POST", "/cash/open", []string{A, C}},
		{"POST", "/cash/close", []string{A, C}},
		{"GET", "/cash/sessions/1", []string{A, C}},
		{"POST", "/cash/register", []string{A}},
		{"POST", "/cash/sales/1/void", []string{A}},
		{"POST", "/purchases", []string{A, S}},
		{"POST", "/counts/1/confirm", []string{A, S}},
		{"POST", "/adjustments", []string{A, S}},
		{"POST", "/picker/quick-product", []string{A, S}},
		{"POST", "/products/new", []string{A, S}},
		{"POST", "/products/1/codes", []string{A, S}},
		{"POST", "/products/1/prices", []string{A}},
		{"POST", "/products/1/costs", []string{A}},
		{"POST", "/purchases/prices/1", []string{A}},
		{"GET", "/taxes", []string{A}},
		{"GET", "/users", []string{A}},
		{"GET", "/reports/sales", []string{A}},
		{"GET", "/reports/cash", []string{A}},
		{"POST", "/users/new", []string{A}},
		{"POST", "/users/1/edit", []string{A}},
		{"POST", "/users/1/password", []string{A}},
		{"PATCH", "/users/1/deactivate", []string{A}},
		{"PATCH", "/users/1/reactivate", []string{A}},
		{"GET", "/api/brands", []string{A}},
		{"POST", "/api/products/1/prices", []string{A}},
	}
	for _, tt := range tests {
		for role, u := range roleUsers {
			want := http.StatusForbidden
			for _, a := range tt.allowed {
				if a == role {
					want = http.StatusOK
				}
			}
			if w := do(u, tt.method, tt.path); w.Code != want {
				t.Errorf("%s %s as %s: got %d, want %d", tt.method, tt.path, role, w.Code, want)
			}
		}
	}
}

func TestRoutesPublic(t *testing.T) {
	var got []string
	for _, rt := range webRoutes(nil) {
		if rt.perm == public {
			got = append(got, rt.pattern)
		}
	}
	// Tienen que ser los de transport.publicPath (más /static/, en main).
	want := []string{"POST /settings/language", "GET /login", "POST /login"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("public routes: got %v, want %v", got, want)
	}
	// Sin usuario, una ruta con permiso no pasa (si AuthMiddleware faltara).
	if w := stubMux(t)(nil, "GET", "/cash"); w.Code != http.StatusSeeOther {
		t.Errorf("no user: got %d", w.Code)
	}
}

// TestRoutesCoverHandlers: cada handler de WebHandler (func(w, r)) está en
// la tabla, así que tiene un permiso.
func TestRoutesCoverHandlers(t *testing.T) {
	inTable := map[string]bool{}
	for _, rt := range webRoutes(nil) {
		name := runtime.FuncForPC(reflect.ValueOf(rt.handler).Pointer()).Name()
		name = strings.TrimSuffix(name[strings.LastIndex(name, ".")+1:], "-fm")
		inTable[name] = true
	}
	handlerType := reflect.TypeOf(http.HandlerFunc(nil))
	typ := reflect.TypeOf(&web.WebHandler{})
	handlers := 0
	for i := range typ.NumMethod() {
		m := typ.Method(i)
		// Method.Type lleva el receptor primero: se compara sin él.
		if m.Type.NumIn() != 3 || m.Type.NumOut() != 0 ||
			m.Type.In(1) != handlerType.In(0) || m.Type.In(2) != handlerType.In(1) {
			continue
		}
		// Forbidden es la página 403 (transport.Guard.Page), no una ruta.
		if m.Name == "Forbidden" {
			continue
		}
		handlers++
		if !inTable[m.Name] {
			t.Errorf("WebHandler.%s is not in webRoutes: give it a permission", m.Name)
		}
	}
	// Que el test mire de verdad: cada handler de la tabla es uno de estos.
	if handlers != len(inTable) {
		t.Errorf("found %d handlers, the table has %d", handlers, len(inTable))
	}
}

func TestRegisterRoutesUnknownPermission(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a route without a known permission must not start")
		}
	}()
	registerRoutes(http.NewServeMux(), transport.Guard{}, []route{{"GET /x", "", http.NotFound}})
}
