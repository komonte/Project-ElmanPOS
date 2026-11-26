package web

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/countcsv"
	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
)

// Importar un conteo desde una planilla (F-09): elegir el archivo y el
// sector por defecto, ver qué entra y qué no (vista previa) y cargar. La
// vista previa lleva el archivo de vuelta en un campo oculto (base64), así
// "Cargar" usa exactamente lo que se revisó sin guardarlo en el servidor.

// importMaxBytes es el tamaño máximo de la planilla.
const importMaxBytes = 5 << 20

// importPreviewRows es cuántas filas que entran muestra la vista previa (los
// problemas se muestran todos: son los que hay que resolver).
const importPreviewRows = 200

var errImportTooLarge = errors.New("the file is larger than 5 MB")

// importProblemRow es un problema ya traducido, para la tabla.
type importProblemRow struct {
	Line    int
	Code    string
	Message string
}

// CountsImportPage handles GET /counts/{id}/import: el formulario.
func (h *WebHandler) CountsImportPage(w http.ResponseWriter, r *http.Request) {
	countID, ok := h.countIDFromPath(w, r)
	if !ok {
		return
	}
	h.renderCountImport(w, r, countID, map[string]any{})
}

// CountsImportPreview handles POST /counts/{id}/import (multipart): lee la
// planilla y muestra la vista previa. No guarda nada.
func (h *WebHandler) CountsImportPreview(w http.ResponseWriter, r *http.Request) {
	countID, ok := h.countIDFromPath(w, r)
	if !ok {
		return
	}
	sector := strings.TrimSpace(r.FormValue("sector"))
	data := map[string]any{"Sector": sector}

	r.Body = http.MaxBytesReader(w, r.Body, importMaxBytes+1<<20) // el archivo y los otros campos
	file, _, err := r.FormFile("file")
	if err != nil {
		data["Error"] = h.importErrMsg(r, err)
		h.renderCountImport(w, r, countID, data)
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, importMaxBytes+1))
	if err == nil && len(raw) > importMaxBytes {
		err = errImportTooLarge
	}
	if err != nil {
		data["Error"] = h.importErrMsg(r, err)
		h.renderCountImport(w, r, countID, data)
		return
	}
	h.runImport(w, r, countID, raw, sector, false, data)
}

// CountsImportApply handles POST /counts/{id}/import/apply: carga lo que se
// vio en la vista previa (el archivo viene en el campo oculto) y vuelve al
// conteo avisando cuántas filas se cargaron.
func (h *WebHandler) CountsImportApply(w http.ResponseWriter, r *http.Request) {
	countID, ok := h.countIDFromPath(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2*importMaxBytes)
	sector := strings.TrimSpace(r.FormValue("sector"))
	data := map[string]any{"Sector": sector}
	raw, err := base64.StdEncoding.DecodeString(r.FormValue("file_data"))
	if err != nil {
		data["Error"] = h.importErrMsg(r, err)
		h.renderCountImport(w, r, countID, data)
		return
	}
	h.runImport(w, r, countID, raw, sector, true, data)
}

// runImport lee la planilla y la pasa al caso de uso. Con apply y sin error
// vuelve al conteo; si no, muestra la vista previa (o el error).
func (h *WebHandler) runImport(w http.ResponseWriter, r *http.Request, countID int64, raw []byte,
	sector string, apply bool, data map[string]any) {
	rows, problems, err := countcsv.Parse(bytes.NewReader(raw))
	if err != nil {
		data["Error"] = h.importErrMsg(r, err)
		h.renderCountImport(w, r, countID, data)
		return
	}
	res, err := h.stockCounts.Import(r.Context(), countID, rows, problems, sector, apply)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			http.Redirect(w, r, "/counts", http.StatusSeeOther)
			return
		}
		data["Error"] = h.importErrMsg(r, err)
		h.renderCountImport(w, r, countID, data)
		return
	}
	if res.Applied {
		http.Redirect(w, r, fmt.Sprintf("/counts?imported=%d", len(res.Lines)), http.StatusSeeOther)
		return
	}

	shown := res.Lines
	if len(shown) > importPreviewRows {
		shown = shown[:importPreviewRows]
	}
	problemRows := make([]importProblemRow, len(res.Problems))
	for i, p := range res.Problems {
		problemRows[i] = importProblemRow{Line: p.Line, Code: p.Code, Message: h.importErrMsg(r, p.Err)}
	}
	data["Preview"] = map[string]any{
		"Lines": shown, "LineCount": len(res.Lines), "Hidden": len(res.Lines) - len(shown),
		"Problems": problemRows, "FileData": base64.StdEncoding.EncodeToString(raw),
	}
	h.renderCountImport(w, r, countID, data)
}

// renderCountImport dibuja la página. El conteo tiene que existir y estar en
// borrador; si no, vuelve a la página de conteo.
func (h *WebHandler) renderCountImport(w http.ResponseWriter, r *http.Request, countID int64, data map[string]any) {
	c, err := h.stockCounts.Get(r.Context(), countID)
	if err != nil || c.Status != model.CountDraft {
		http.Redirect(w, r, "/counts", http.StatusSeeOther)
		return
	}
	sectors, err := h.stockCounts.Sectors(r.Context())
	if err != nil {
		http.Error(w, h.errMsg(r, err), http.StatusInternalServerError)
		return
	}
	data["Section"], data["TitleKey"] = "counts", "count.import_title"
	data["Count"], data["Sectors"] = c, sectors
	h.render(w, r, "count_import.gohtml", data)
}

// importErrMsg traduce los errores de la planilla. Los demás (producto
// inactivo, cantidad inválida...) son los de siempre (errMsg).
func (h *WebHandler) importErrMsg(r *http.Request, err error) string {
	var missing *countcsv.MissingColumnError
	var tooBig *http.MaxBytesError
	var corrupt base64.CorruptInputError
	switch {
	case errors.As(err, &corrupt):
		return h.tr(r, "error.import_bad_row")
	case errors.As(err, &missing):
		return fmt.Sprintf(h.tr(r, "error.import_missing_column"), missing.Column)
	case errors.Is(err, errImportTooLarge), errors.As(err, &tooBig):
		return h.tr(r, "error.import_too_large")
	case errors.Is(err, http.ErrMissingFile):
		return h.tr(r, "error.import_no_file")
	}
	return h.errMsg(r, err)
}

// countIDFromPath lee {id}; si está mal responde 400 y devuelve false.
func (h *WebHandler) countIDFromPath(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, h.tr(r, "error.invalid_id"), http.StatusBadRequest)
		return 0, false
	}
	return id, true
}
