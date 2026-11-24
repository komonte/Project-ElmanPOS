package web

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

// Pantalla de categorías. Igual que proveedores (lista, búsqueda, panel con
// formulario, desactivar / reactivar, borrar), más lo propio del árbol: cada
// fila muestra el camino de padres ("Almacen › Galletitas") y el formulario
// elige padre e IVA.

// categoryPathSep separa los niveles del camino: "Limpieza › Jabones".
const categoryPathSep = " › "

// categoryPaths arma el camino completo de cada categoría, de la raíz hacia
// abajo: paths[id de Jabones] = "Limpieza › Jabones". Necesita la lista
// completa (activas e inactivas) para poder subir hasta la raíz.
func categoryPaths(all []*model.Category) map[int64]string {
	byID := make(map[int64]*model.Category, len(all))
	for _, c := range all {
		byID[c.ID] = c
	}

	paths := make(map[int64]string, len(all))
	for _, c := range all {
		names := []string{c.Name}
		// Sube por los padres. seen corta el recorrido si hubiera un ciclo
		// (no debería: el store los rechaza).
		seen := map[int64]bool{c.ID: true}
		for p := c.ParentID; p != nil && !seen[*p]; {
			parent, ok := byID[*p]
			if !ok {
				break
			}
			seen[parent.ID] = true
			names = append(names, parent.Name)
			p = parent.ParentID
		}
		// names quedó de abajo hacia arriba: se da vuelta.
		for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
			names[i], names[j] = names[j], names[i]
		}
		paths[c.ID] = strings.Join(names, categoryPathSep)
	}
	return paths
}

// categoryRow es una fila de la tabla: la categoría más el camino de sus
// padres. Al embeber *model.Category, el template sigue usando .Name, .ID...
type categoryRow struct {
	*model.Category
	ParentPath string // "Almacen" para Galletitas; "" para una raíz
}

// categoryRows arma las filas ordenadas por camino, así cada subcategoría
// aparece justo debajo de su padre: Almacen, Almacen › Galletitas, Limpieza...
func categoryRows(list []*model.Category, paths map[int64]string) []categoryRow {
	rows := make([]categoryRow, 0, len(list))
	for _, c := range list {
		row := categoryRow{Category: c}
		if c.ParentID != nil {
			row.ParentPath = paths[*c.ParentID]
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return strings.ToLower(paths[rows[i].ID]) < strings.ToLower(paths[rows[j].ID])
	})
	return rows
}

// categoryOption es una opción de un <select> del formulario.
type categoryOption struct {
	ID       int64
	Label    string
	TaxID    int64 // solo en el select de padre: para precargar su IVA
	Selected bool
	Inactive bool
}

// parentOptions devuelve las categorías que se pueden elegir como padre de c:
//   - las activas;
//   - el padre actual aunque esté inactivo, para no perderlo al guardar;
//   - nunca c ni sus subcategorías (eso armaría un ciclo).
//
// Se muestran con el camino completo: "Varios" puede estar en dos lugares.
func parentOptions(all []*model.Category, paths map[int64]string, c *model.Category) []categoryOption {
	excluded := make(map[int64]bool)
	if c.ID != 0 {
		excluded[c.ID] = true
		// Marca las descendientes: repite hasta que no aparezcan nuevas
		// (cada vuelta baja un nivel más del árbol).
		for changed := true; changed; {
			changed = false
			for _, x := range all {
				if x.ParentID != nil && excluded[*x.ParentID] && !excluded[x.ID] {
					excluded[x.ID] = true
					changed = true
				}
			}
		}
	}

	opts := make([]categoryOption, 0, len(all))
	for _, x := range all {
		isCurrent := c.ParentID != nil && *c.ParentID == x.ID
		if excluded[x.ID] || (!x.IsActive && !isCurrent) {
			continue
		}
		opts = append(opts, categoryOption{
			ID: x.ID, Label: paths[x.ID], TaxID: x.TaxID,
			Selected: isCurrent, Inactive: !x.IsActive,
		})
	}
	sort.SliceStable(opts, func(i, j int) bool {
		return strings.ToLower(opts[i].Label) < strings.ToLower(opts[j].Label)
	})
	return opts
}

// taxOptions devuelve los impuestos activos más el actual de c aunque esté
// inactivo (igual que con el padre: si no, se perdería al guardar).
func taxOptions(taxes []*model.Tax, c *model.Category) []categoryOption {
	opts := make([]categoryOption, 0, len(taxes))
	for _, t := range taxes {
		isCurrent := t.ID == c.TaxID
		if !t.IsActive && !isCurrent {
			continue
		}
		opts = append(opts, categoryOption{ID: t.ID, Label: t.Name, Selected: isCurrent, Inactive: !t.IsActive})
	}
	return opts
}

// renderCategoryForm dibuja el panel con el formulario. Arma las opciones de
// los dos <select> cada vez, también al volver con un error.
func (h *WebHandler) renderCategoryForm(w http.ResponseWriter, r *http.Request, c *model.Category, errMsg string) {
	ctx := r.Context()
	data := map[string]any{"Category": c, "Error": errMsg}

	all, err := h.categorySvc.GetAllIncludingInactive(ctx)
	if err != nil {
		data["Error"] = h.errMsg(r, err)
		h.renderPartial(w, r, "category_form.gohtml", data)
		return
	}
	taxes, err := h.taxSvc.GetAllIncludingInactive(ctx)
	if err != nil {
		data["Error"] = h.errMsg(r, err)
		h.renderPartial(w, r, "category_form.gohtml", data)
		return
	}

	data["Parents"] = parentOptions(all, categoryPaths(all), c)
	data["Taxes"] = taxOptions(taxes, c)
	h.renderPartial(w, r, "category_form.gohtml", data)
}

// categoryFromForm copia los campos del formulario a c. Un padre vacío es una
// categoría raíz (nil). Un número mal escrito queda en 0, y Validate()
// devuelve el error que corresponde (falta el IVA, padre inválido).
func categoryFromForm(r *http.Request, c *model.Category) {
	c.Name = r.FormValue("name")
	c.TaxID, _ = strconv.ParseInt(r.FormValue("tax_id"), 10, 64)

	c.ParentID = nil
	if v := r.FormValue("parent_id"); v != "" {
		id, _ := strconv.ParseInt(v, 10, 64)
		c.ParentID = &id
	}
}

// categoriesTableData arma los datos de la tabla (ver adminList).
func (h *WebHandler) categoriesTableData(r *http.Request) (map[string]any, error) {
	list, total, err := adminList(r, h.categorySvc)
	if err != nil {
		return nil, err
	}
	// Para los caminos hacen falta todas, no solo las que pasan la búsqueda.
	all, err := h.categorySvc.GetAllIncludingInactive(r.Context())
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"Categories": categoryRows(list, categoryPaths(all)),
		"Shown":      len(list),
		"Total":      total,
	}, nil
}

// CategoriesList handles GET /categories
func (h *WebHandler) CategoriesList(w http.ResponseWriter, r *http.Request) {
	data, err := h.categoriesTableData(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") != "" {
		h.renderPartial(w, r, "category_table.gohtml", data)
		return
	}

	data["Section"] = "categories"
	data["TitleKey"] = "category.title"
	h.render(w, r, "categories.gohtml", data)
}

// CategoriesNewForm handles GET /categories/new
func (h *WebHandler) CategoriesNewForm(w http.ResponseWriter, r *http.Request) {
	h.renderCategoryForm(w, r, &model.Category{}, "")
}

// CategoriesCreate handles POST /categories/new
func (h *WebHandler) CategoriesCreate(w http.ResponseWriter, r *http.Request) {
	category := &model.Category{}
	if err := r.ParseForm(); err != nil {
		h.renderCategoryForm(w, r, category, h.errMsg(r, err))
		return
	}

	categoryFromForm(r, category)
	if _, err := h.categorySvc.Create(r.Context(), category); err != nil {
		h.renderCategoryForm(w, r, category, h.errMsg(r, err))
		return
	}

	w.Header().Set("HX-Redirect", "/categories")
	w.WriteHeader(http.StatusOK)
}

// CategoriesEditForm handles GET /categories/{id}/edit
func (h *WebHandler) CategoriesEditForm(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	category, err := h.categorySvc.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, h.errMsg(r, err), http.StatusNotFound)
		return
	}

	h.renderCategoryForm(w, r, category, "")
}

// CategoriesUpdate handles POST /categories/{id}/edit
func (h *WebHandler) CategoriesUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		h.renderCategoryForm(w, r, &model.Category{}, h.tr(r, "error.invalid_id"))
		return
	}

	// Se parte de la categoría guardada: el formulario no trae is_active.
	category, err := h.categorySvc.GetByID(ctx, id)
	if err != nil {
		h.renderCategoryForm(w, r, &model.Category{}, h.errMsg(r, err))
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderCategoryForm(w, r, category, h.errMsg(r, err))
		return
	}

	categoryFromForm(r, category)
	if _, err := h.categorySvc.Update(ctx, id, category); err != nil {
		h.renderCategoryForm(w, r, category, h.errMsg(r, err))
		return
	}

	w.Header().Set("HX-Redirect", "/categories")
	w.WriteHeader(http.StatusOK)
}

// CategoriesDelete handles DELETE /categories/{id}
func (h *WebHandler) CategoriesDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.categorySvc.Delete(r.Context(), id); err != nil {
		h.actionError(w, r, err)
		return
	}

	h.categoriesTable(w, r)
}

// CategoriesDeactivate handles PATCH /categories/{id}/deactivate
func (h *WebHandler) CategoriesDeactivate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.categorySvc.Deactivate(r.Context(), id); err != nil {
		h.actionError(w, r, err)
		return
	}

	h.categoriesTable(w, r)
}

// CategoriesReactivate handles PATCH /categories/{id}/reactivate
func (h *WebHandler) CategoriesReactivate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.categorySvc.Reactivate(r.Context(), id); err != nil {
		h.actionError(w, r, err)
		return
	}

	h.categoriesTable(w, r)
}

// categoriesTable redibuja la tabla después de una acción.
func (h *WebHandler) categoriesTable(w http.ResponseWriter, r *http.Request) {
	data, err := h.categoriesTableData(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderPartial(w, r, "category_table.gohtml", data)
}
