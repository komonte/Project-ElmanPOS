package main

import (
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"

	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
)

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

	// Inyectar nuestras dependencias

	brandStore := store.NewBrandStore(db)
	brandService := service.NewBrandService(brandStore)
	brandHandler := transport.NewBrandHandler(brandService)

	// Configurar rutas
	mux := http.NewServeMux()
	mux.HandleFunc("/brands", brandHandler.HandleBrands)
	mux.HandleFunc("/brand/", brandHandler.HandleBrandByID)

	handler := transport.CORSMiddleware(mux)

	// empezar y escuchar al servidor
	
	serverPort := getEnv("PORT", "8080")
	server := &http.Server{
		Addr:	":" + serverPort,
		Handler:	handler,
		IdleTimeout:	time.Minute,
		ReadTimeout: 10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	slog.Info("starting server", "port", serverPort)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server failed: %v", err)
	}
}
