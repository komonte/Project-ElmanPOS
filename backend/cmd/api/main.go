package main

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport/web"
)

//go:embed static/css/* static/js/*
var staticFS embed.FS

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func main() {

	// conectar Postgres

	if err := godotenv.Load(); err != nil {
		slog.Warn("no .env file found, using system environment variables")
	}

	dbHost := getEnv("DB_HOST", "localhost")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "")
	dbPass := getEnv("DB_PASSWORD", "")
	dbName := getEnv("DB_NAME", "")
	sslMode := getEnv("DB_SSLMODE", "disable")

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		dbUser, dbPass, dbHost, dbPort, dbName, sslMode)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		slog.Error("failed to open database conection pool", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		slog.Error("cannot reach postgres database", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to PostgreSQL successfully")

	// Inyectar dependencias

	brandStore := postgres.NewBrandStore(db)
	brandSvc := service.NewService[model.Brand](brandStore, func(b *model.Brand) error { return b.Validate() })
	brandH := transport.NewResourceHandler[model.Brand](brandSvc, "brand")

	taxStore := postgres.NewTaxStore(db)
	taxSvc := service.NewService[model.Tax](taxStore, func(t *model.Tax) error { return t.Validate() })
	taxH := transport.NewResourceHandler[model.Tax](taxSvc, "tax")

	// Web UI
	tmplEngine := web.NewTemplateEngine()
	webH := web.NewWebHandler(tmplEngine, brandSvc, taxSvc)

	// Configurar rutas
	mux := http.NewServeMux()

	// API routes (sin cambios)
	mux.HandleFunc("/api/brands", brandH.HandleCollection)
	mux.HandleFunc("/api/brand/", brandH.HandleEntity)
	mux.HandleFunc("/api/taxes", taxH.HandleCollection)
	mux.HandleFunc("/api/tax/", taxH.HandleEntity)

	// Web UI routes
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		slog.Error("failed to create static file system", "error", err)
		os.Exit(1)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	mux.HandleFunc("/", webH.Index)
	mux.HandleFunc("/brands", webH.BrandsList)
	mux.HandleFunc("/brands/new", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			webH.BrandsCreate(w, r)
		} else {
			webH.BrandsNewForm(w, r)
		}
	})
	mux.HandleFunc("/brands/", webH.BrandsList)
	mux.HandleFunc("/taxes", webH.TaxesList)
	mux.HandleFunc("/taxes/new", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			webH.TaxesCreate(w, r)
		} else {
			webH.TaxesNewForm(w, r)
		}
	})
	mux.HandleFunc("/taxes/", webH.TaxesList)
	mux.HandleFunc("/settings/language", webH.SetLanguage)

	// Middleware chain
	handler := transport.CORSMiddleware(mux)
	handler = transport.LocaleMiddleware(handler)

	// empezar y escuchar al servidor

	serverPort := getEnv("PORT", "8080")
	server := &http.Server{
		Addr:         ":" + serverPort,
		Handler:      handler,
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	slog.Info("starting server", "port", serverPort)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server failed: %v", err)
	}
}