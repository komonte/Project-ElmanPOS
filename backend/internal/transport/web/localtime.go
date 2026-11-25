package web

import (
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// defaultStoreTimezone es la zona del almacén si no se configura otra con la
// variable STORE_TIMEZONE.
const defaultStoreTimezone = "America/Argentina/Buenos_Aires"

// storeLocation es la zona horaria en que se muestran las fechas. Se lee una
// vez, la primera vez que se dibuja una fecha (main ya cargó el .env).
var storeLocation = sync.OnceValue(func() *time.Location {
	name := os.Getenv("STORE_TIMEZONE")
	if name == "" {
		name = defaultStoreTimezone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		// Mejor una hora corrida que una pantalla rota: se avisa en el log y
		// se usa la de Argentina (UTC-3, sin horario de verano).
		slog.Error("invalid STORE_TIMEZONE, using UTC-3", "timezone", name, "error", err)
		return time.FixedZone("UTC-3", -3*60*60)
	}
	return loc
})

// formatTime muestra una fecha de la DB ("2026-11-25T18:31:38.586087Z", en
// UTC) en la hora del almacén: "2026-11-25 15:31:38". La DB guarda UTC y está
// bien: la conversión es solo para mostrar.
func formatTime(t string) string {
	if t == "" {
		return "-"
	}
	parsed, err := time.Parse(time.RFC3339Nano, t)
	if err != nil {
		// Un formato inesperado se muestra como viene, sin la "T".
		return strings.Replace(t, "T", " ", 1)
	}
	return parsed.In(storeLocation()).Format(time.DateTime)
}
