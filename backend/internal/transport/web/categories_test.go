package web

import (
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

// Árbol de prueba:
//
//	Almacen (1)
//	├── Galletitas (2)
//	│   └── Dulces (3)
//	└── Varios (4)
//	Limpieza (5, inactiva)
//	└── Varios (6, inactiva)
func testCategoryTree() []*model.Category {
	return []*model.Category{
		{ID: 1, Name: "Almacen", TaxID: 10, IsActive: true},
		{ID: 2, Name: "Galletitas", TaxID: 10, IsActive: true, ParentID: new(int64(1))},
		{ID: 3, Name: "Dulces", TaxID: 10, IsActive: true, ParentID: new(int64(2))},
		{ID: 4, Name: "Varios", TaxID: 10, IsActive: true, ParentID: new(int64(1))},
		{ID: 5, Name: "Limpieza", TaxID: 20, IsActive: false},
		{ID: 6, Name: "Varios", TaxID: 20, IsActive: false, ParentID: new(int64(5))},
	}
}

func TestCategoryPaths(t *testing.T) {
	paths := categoryPaths(testCategoryTree())
	want := map[int64]string{
		1: "Almacen",
		2: "Almacen › Galletitas",
		3: "Almacen › Galletitas › Dulces",
		4: "Almacen › Varios",
		5: "Limpieza",
		6: "Limpieza › Varios",
	}
	for id, w := range want {
		if paths[id] != w {
			t.Errorf("path %d: got %q, want %q", id, paths[id], w)
		}
	}
}

func TestCategoryRowsSortedByPath(t *testing.T) {
	all := testCategoryTree()
	rows := categoryRows(all, categoryPaths(all))

	wantIDs := []int64{1, 2, 3, 4, 5, 6}
	for i, row := range rows {
		if row.ID != wantIDs[i] {
			t.Fatalf("row %d: got id %d, want %d", i, row.ID, wantIDs[i])
		}
	}
	if rows[2].ParentPath != "Almacen › Galletitas" || rows[0].ParentPath != "" {
		t.Fatalf("parent paths: got %q and %q", rows[2].ParentPath, rows[0].ParentPath)
	}
}

func TestParentOptions(t *testing.T) {
	all := testCategoryTree()
	paths := categoryPaths(all)

	ids := func(opts []categoryOption) map[int64]bool {
		m := make(map[int64]bool)
		for _, o := range opts {
			m[o.ID] = true
		}
		return m
	}

	tests := []struct {
		name     string
		category *model.Category
		want     []int64
	}{
		// Nueva: solo las activas.
		{"new", &model.Category{}, []int64{1, 2, 3, 4}},
		// Galletitas no puede ir dentro de sí misma ni de Dulces (su hija).
		{"edit excludes itself and descendants", all[1], []int64{1, 4}},
		// La raíz Almacen no puede ir dentro de nada suyo.
		{"edit root", all[0], []int64{}},
		// Varios inactiva conserva a su padre inactivo como opción.
		{"keeps inactive current parent", all[5], []int64{1, 2, 3, 4, 5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ids(parentOptions(all, paths, tt.category))
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for _, id := range tt.want {
				if !got[id] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestTaxOptionsKeepsInactiveCurrent(t *testing.T) {
	taxes := []*model.Tax{
		{ID: 10, Name: "IVA 21%", IsActive: true},
		{ID: 20, Name: "IVA viejo", IsActive: false},
	}

	if got := taxOptions(taxes, &model.Category{}); len(got) != 1 {
		t.Fatalf("new category: got %d options, want 1", len(got))
	}
	got := taxOptions(taxes, &model.Category{TaxID: 20})
	if len(got) != 2 || !got[1].Selected || !got[1].Inactive {
		t.Fatalf("category with inactive tax: got %+v", got)
	}
}
