package main

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport/web"
)

// Rutas y permisos (F-13, PR D). Cada ruta de la web dice qué permiso pide,
// en esta tabla y en ningún otro lado. registerRoutes no arranca (panic) si
// una ruta no lo dice: una ruta nueva sin permiso no puede quedar abierta
// por olvido. Los que no piden permiso lo dicen igual:
//   - public: sin haber entrado (también tienen que estar en
//     transport.publicPath, si no AuthMiddleware pide entrar igual);
//   - loggedIn: cualquiera que haya entrado.
// La API (/api/) entera pide model.PermAPI (mountAPI).

const (
	public   model.Permission = "public"
	loggedIn model.Permission = "logged_in"
)

type route struct {
	pattern string
	perm    model.Permission
	handler http.HandlerFunc
}

// webRoutes es la tabla de la web. h puede ser nil en los tests: solo se
// arman los method values, no se llaman.
func webRoutes(h *web.WebHandler) []route {
	const (
		sell      = model.PermSell
		cash      = model.PermCash
		cashSetup = model.PermCashSetup
		void      = model.PermVoid
		stock     = model.PermStock
		catalog   = model.PermCatalog
		prices    = model.PermPrices
		users     = model.PermUsers
		reports   = model.PermReports
	)
	return []route{
		{"GET /{$}", loggedIn, h.Index},
		{"POST /settings/language", public, h.SetLanguage},
		{"GET /login", public, h.LoginPage},
		{"POST /login", public, h.LoginSubmit},
		{"POST /logout", loggedIn, h.Logout},

		// Catálogo: el admin y el repositor.
		{"GET /brands", catalog, h.BrandsList},
		{"GET /brands/new", catalog, h.BrandsNewForm},
		{"POST /brands/new", catalog, h.BrandsCreate},
		{"GET /brands/{id}/edit", catalog, h.BrandsEditForm},
		{"POST /brands/{id}/edit", catalog, h.BrandsUpdate},
		{"DELETE /brands/{id}", catalog, h.BrandsDelete},
		{"PATCH /brands/{id}/deactivate", catalog, h.BrandsDeactivate},
		{"PATCH /brands/{id}/reactivate", catalog, h.BrandsReactivate},
		{"GET /suppliers", catalog, h.SuppliersList},
		{"GET /suppliers/new", catalog, h.SuppliersNewForm},
		{"POST /suppliers/new", catalog, h.SuppliersCreate},
		{"GET /suppliers/{id}/edit", catalog, h.SuppliersEditForm},
		{"POST /suppliers/{id}/edit", catalog, h.SuppliersUpdate},
		{"DELETE /suppliers/{id}", catalog, h.SuppliersDelete},
		{"PATCH /suppliers/{id}/deactivate", catalog, h.SuppliersDeactivate},
		{"PATCH /suppliers/{id}/reactivate", catalog, h.SuppliersReactivate},
		{"GET /categories", catalog, h.CategoriesList},
		{"GET /categories/new", catalog, h.CategoriesNewForm},
		{"POST /categories/new", catalog, h.CategoriesCreate},
		{"GET /categories/{id}/edit", catalog, h.CategoriesEditForm},
		{"POST /categories/{id}/edit", catalog, h.CategoriesUpdate},
		{"DELETE /categories/{id}", catalog, h.CategoriesDelete},
		{"PATCH /categories/{id}/deactivate", catalog, h.CategoriesDeactivate},
		{"PATCH /categories/{id}/reactivate", catalog, h.CategoriesReactivate},
		{"GET /products", catalog, h.ProductsList},
		{"GET /products/new", catalog, h.ProductsNewForm},
		{"GET /products/brand-options", catalog, h.ProductsBrandOptions},
		{"GET /products/barcode-check", catalog, h.ProductsBarcodeCheck},
		{"POST /products/new", catalog, h.ProductsCreate},
		{"GET /products/{id}/edit", catalog, h.ProductsEditForm},
		{"POST /products/{id}/edit", catalog, h.ProductsUpdate},
		{"DELETE /products/{id}", catalog, h.ProductsDelete},
		{"PATCH /products/{id}/deactivate", catalog, h.ProductsDeactivate},
		{"PATCH /products/{id}/reactivate", catalog, h.ProductsReactivate},
		{"POST /products/{id}/codes", catalog, h.ProductCodesAdd},
		{"DELETE /products/{id}/codes/{codeID}", catalog, h.ProductCodesRemove},

		// Precios, costos e impuestos: solo el admin.
		{"POST /products/{id}/prices", prices, h.ProductPricesSet},
		{"POST /products/{id}/costs", prices, h.ProductCostsSet},
		{"POST /purchases/prices/{id}", prices, h.PurchasesUsePrice},
		{"GET /taxes", prices, h.TaxesList},
		{"GET /taxes/new", prices, h.TaxesNewForm},
		{"POST /taxes/new", prices, h.TaxesCreate},
		{"GET /taxes/{id}/edit", prices, h.TaxesEditForm},
		{"POST /taxes/{id}/edit", prices, h.TaxesUpdate},
		{"DELETE /taxes/{id}", prices, h.TaxesDelete},
		{"PATCH /taxes/{id}/deactivate", prices, h.TaxesDeactivate},
		{"PATCH /taxes/{id}/reactivate", prices, h.TaxesReactivate},

		// Stock: compras, conteos, salidas y el buscador de sus renglones
		// (opciones, alta rápida y agregar código).
		{"GET /purchases", stock, h.PurchasesPage},
		{"POST /purchases", stock, h.PurchasesReceive},
		{"GET /counts", stock, h.CountsPage},
		{"POST /counts", stock, h.CountsOpen},
		{"POST /counts/{id}/entries", stock, h.CountsAddEntry},
		{"POST /counts/{id}/entries/{entryID}/delete", stock, h.CountsRemoveEntry},
		{"POST /counts/{id}/discard", stock, h.CountsDiscard},
		{"GET /counts/{id}/review", stock, h.CountsReview},
		{"GET /counts/{id}/import", stock, h.CountsImportPage},
		{"POST /counts/{id}/import", stock, h.CountsImportPreview},
		{"POST /counts/{id}/import/apply", stock, h.CountsImportApply},
		{"POST /counts/{id}/items/{productID}/details", stock, h.CountsSetDetails},
		{"POST /counts/{id}/confirm", stock, h.CountsConfirm},
		{"GET /adjustments", stock, h.AdjustmentsPage},
		{"POST /adjustments", stock, h.AdjustmentsRecord},
		{"GET /adjustments/product", stock, h.AdjustmentsProduct},
		{"GET /picker/product-options", stock, h.PickerProductOptions},
		{"GET /picker/quick-product", stock, h.PickerQuickProductForm},
		{"POST /picker/quick-product", stock, h.PickerQuickProductCreate},
		{"GET /picker/assign-code", stock, h.PickerAssignCodeForm},
		{"GET /picker/assign-code/options", stock, h.PickerAssignCodeOptions},
		{"POST /picker/assign-code", stock, h.PickerAssignCode},

		// Vender y la caja: el admin y el cajero. Anular y crear la caja,
		// solo el admin.
		{"GET /pos", sell, h.PosPage},
		{"GET /pos/search", sell, h.PosSearch},
		{"POST /pos/checkout", sell, h.PosCheckout},
		{"GET /pos/sales/{id}/ticket", sell, h.PosTicket},
		{"GET /cash", cash, h.CashPage},
		{"POST /cash/register", cashSetup, h.CashCreateRegister},
		{"POST /cash/open", cash, h.CashOpen},
		{"POST /cash/close", cash, h.CashClose},
		{"POST /cash/sales/{id}/void", void, h.CashVoidSale},
		{"GET /cash/sessions/{id}", cash, h.CashSession},

		// Usuarios y contraseñas: solo el admin.
		{"GET /users", users, h.UsersList},
		{"GET /users/new", users, h.UsersNewForm},
		{"POST /users/new", users, h.UsersCreate},
		{"GET /users/{id}/edit", users, h.UsersEditForm},
		{"POST /users/{id}/edit", users, h.UsersUpdate},
		{"GET /users/{id}/password", users, h.UsersPasswordForm},
		{"POST /users/{id}/password", users, h.UsersSetPassword},
		{"PATCH /users/{id}/deactivate", users, h.UsersDeactivate},
		{"PATCH /users/{id}/reactivate", users, h.UsersReactivate},

		// Reportes de ventas, caja y margen: solo el admin.
		{"GET /reports/sales", reports, h.ReportsSales},
		{"GET /reports/cash", reports, h.ReportsCash},
	}
}

// registerRoutes monta las rutas en mux, cada una detrás de su permiso.
func registerRoutes(mux *http.ServeMux, guard transport.Guard, routes []route) {
	for _, rt := range routes {
		switch {
		case rt.perm == public || rt.perm == loggedIn:
			mux.Handle(rt.pattern, rt.handler)
		case slices.Contains(model.Permissions, rt.perm):
			mux.Handle(rt.pattern, guard.Require(rt.perm, rt.handler))
		default:
			panic(fmt.Sprintf("route %q: unknown permission %q", rt.pattern, rt.perm))
		}
	}
}

// mountAPI monta la API JSON (api) bajo /api/, entera para model.PermAPI.
func mountAPI(mux *http.ServeMux, guard transport.Guard, api http.Handler) {
	mux.Handle("/api/", guard.Require(model.PermAPI, api))
}
