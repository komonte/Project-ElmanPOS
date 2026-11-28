package web

import (
	"net/http"

	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/settings"
)

// Configuración del negocio (Administración > Configuración): por ahora el
// modo arranque. Solo el admin (permiso settings, en la ruta).

// WithSettings le da al handler la configuración.
func (h *WebHandler) WithSettings(s *settings.Service) *WebHandler {
	h.settings = s
	return h
}

// SettingsPage handles GET /settings. ?saved=1 muestra "Guardado." después
// de un cambio.
func (h *WebHandler) SettingsPage(w http.ResponseWriter, r *http.Request) {
	h.renderSettings(w, r, r.URL.Query().Get("saved") == "1", "")
}

func (h *WebHandler) renderSettings(w http.ResponseWriter, r *http.Request, saved bool, errMsg string) {
	data := map[string]any{"Section": "settings", "TitleKey": "settings.title", "Saved": saved, "Error": errMsg}
	st, err := h.settings.Get(r.Context())
	if err != nil {
		http.Error(w, h.errMsg(r, err), http.StatusInternalServerError)
		return
	}
	data["Settings"] = st
	// Al apagarlo, avisa si quedó algo para contar (se puede contar igual).
	if items, err := h.reports.Unstocked(r.Context()); err == nil {
		data["UnstockedCount"] = len(items)
	}
	h.render(w, r, "settings.gohtml", data)
}

// SettingsStartupMode handles POST /settings/startup-mode: startup_mode=on
// lo prende; cualquier otra cosa (o nada) lo apaga. Vuelve a la página.
func (h *WebHandler) SettingsStartupMode(w http.ResponseWriter, r *http.Request) {
	on := r.FormValue("startup_mode") == "on"
	if err := h.settings.SetStartupMode(r.Context(), transport.UserID(r.Context()), on); err != nil {
		h.renderSettings(w, r, false, h.errMsg(r, err))
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}
