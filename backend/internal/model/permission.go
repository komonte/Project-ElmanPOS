package model

import "slices"

// Permisos por rol (F-13, PR D). Decisiones (2026-11-26):
//   - Las rutas, el menú y los botones preguntan por un permiso, no por un
//     rol: "¿puede anular?" y no "¿es admin?". Qué permisos tiene cada rol
//     está en una sola tabla (rolePermissions); cambiar la matriz es cambiar
//     esa tabla.
//   - Un rol desconocido no tiene ningún permiso.
//   - El repositor maneja el catálogo (da de alta lo que recibe), pero no
//     precios, costos ni impuestos.
//   - La receta de un combo, solo el admin (2026-11-28): una receta mal
//     cargada descuenta mal el stock en cada venta.

// Permission es algo que un usuario puede hacer.
type Permission string

const (
	PermSell      Permission = "sell"       // vender (F-07)
	PermCash      Permission = "cash"       // abrir y cerrar la caja, ver el resumen (F-11, F-12)
	PermCashSetup Permission = "cash_setup" // crear la caja del local
	PermVoid      Permission = "void"       // anular una venta (F-08)
	PermStock     Permission = "stock"      // compras, conteos y salidas (F-06, F-09, F-09b)
	PermCatalog   Permission = "catalog"    // productos, categorías, marcas, proveedores y códigos
	PermPrices    Permission = "prices"     // precios, costos e impuestos (F-03, F-04)
	PermAPI       Permission = "api"        // la API JSON (/api/)
	PermUsers     Permission = "users"      // crear, editar y desactivar usuarios, y resetear contraseñas
	PermReports   Permission = "reports"    // reportes de ventas, caja y margen (R-06, R-07)
	PermSettings  Permission = "settings"   // configuración del negocio (modo arranque)
	PermCombos    Permission = "combos"     // los componentes de un combo (F-05)
	PermCustomers Permission = "customers"  // editar y desactivar clientes (fiado)
	PermReturns   Permission = "returns"    // devoluciones (sacan plata de la caja)
)

// Permissions son todos los permisos.
var Permissions = []Permission{PermSell, PermCash, PermCashSetup, PermVoid, PermStock, PermCatalog, PermPrices, PermAPI, PermUsers, PermReports, PermSettings, PermCombos, PermCustomers, PermReturns}

// rolePermissions es la matriz de F-13.
var rolePermissions = map[string][]Permission{
	RoleAdmin:      Permissions,
	RoleCashier:    {PermSell, PermCash},
	RoleStockClerk: {PermStock, PermCatalog},
}

// Can dice si el rol tiene el permiso.
func Can(role string, p Permission) bool {
	return slices.Contains(rolePermissions[role], p)
}

// Can dice si el usuario tiene el permiso. Un usuario nil o inactivo no
// tiene ninguno.
func (u *User) Can(p Permission) bool {
	return u != nil && u.IsActive && Can(u.Role, p)
}
