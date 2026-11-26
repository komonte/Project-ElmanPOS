package model

import "testing"

// TestCan es la matriz de F-13: si cambia, este test cambia con ella.
func TestCan(t *testing.T) {
	want := map[string][]Permission{
		RoleAdmin:      {PermSell, PermCash, PermCashSetup, PermVoid, PermStock, PermCatalog, PermPrices, PermAPI},
		RoleCashier:    {PermSell, PermCash},
		RoleStockClerk: {PermStock, PermCatalog},
		"":             {},
		"OWNER":        {},
	}
	for role, perms := range want {
		allowed := map[Permission]bool{}
		for _, p := range perms {
			allowed[p] = true
		}
		for _, p := range Permissions {
			if got := Can(role, p); got != allowed[p] {
				t.Errorf("Can(%q, %q) = %v, want %v", role, p, got, allowed[p])
			}
		}
	}
}

func TestUserCan(t *testing.T) {
	var nobody *User
	if nobody.Can(PermSell) {
		t.Error("nil user can sell")
	}
	inactive := &User{Role: RoleAdmin}
	if inactive.Can(PermSell) {
		t.Error("inactive admin can sell")
	}
	cashier := &User{Role: RoleCashier, IsActive: true}
	if !cashier.Can(PermSell) || cashier.Can(PermVoid) {
		t.Error("cashier: sells, does not void")
	}
}
