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
	// Las zonas horarias van dentro del binario: así STORE_TIMEZONE funciona
	// también en una máquina o contenedor sin /usr/share/zoneinfo.
	_ "time/tzdata"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport/web"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/adjustments"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/auth"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/costs"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/createproduct"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/prices"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/productcodes"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/purchases"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/reports"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sales"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sessions"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/stockcounts"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/users"
)

//go:embed static/css/* static/js/* static/fonts/*
var staticFS embed.FS

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

// openDB abre el pool de conexiones a Postgres con DB_* y prueba que llegue.
func openDB() (*sql.DB, error) {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		getEnv("DB_USER", ""), getEnv("DB_PASSWORD", ""), getEnv("DB_HOST", "localhost"),
		getEnv("DB_PORT", "5432"), getEnv("DB_NAME", ""), getEnv("DB_SSLMODE", "disable"))

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database connection pool: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func main() {

	// conectar Postgres

	if err := godotenv.Load(); err != nil {
		slog.Warn("no .env file found, using system environment variables")
	}

	db, err := openDB()
	if err != nil {
		slog.Error("cannot reach postgres database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Comandos de administración: elmanpos passwd <usuario>.
	if len(os.Args) > 1 {
		os.Exit(runCommand(db, os.Args[1:]))
	}

	slog.Info("connected to PostgreSQL successfully")

	// Inyectar dependencias

	brandStore := postgres.NewBrandStore(db)
	brandSvc := service.NewService[model.Brand](brandStore, func(b *model.Brand) error { return b.Validate() })
	brandH := transport.NewResourceHandler[model.Brand](brandSvc, "brand")

	taxStore := postgres.NewTaxStore(db)
	taxSvc := service.NewService[model.Tax](taxStore, func(t *model.Tax) error { return t.Validate() })
	taxH := transport.NewResourceHandler[model.Tax](taxSvc, "tax")

	supplierStore := postgres.NewSupplierStore(db)
	supplierSvc := service.NewService[model.Supplier](supplierStore, func(s *model.Supplier) error { return s.Validate() })
	supplierH := transport.NewResourceHandler[model.Supplier](supplierSvc, "supplier")

	categoryStore := postgres.NewCategoryStore(db)
	categorySvc := service.NewService[model.Category](categoryStore, func(c *model.Category) error { return c.Validate() })
	categoryH := transport.NewResourceHandler[model.Category](categorySvc, "category")

	productStore := postgres.NewProductStore(db)
	productSvc := service.NewService[model.Product](productStore, func(p *model.Product) error { return p.Validate() })
	productH := transport.NewResourceHandler[model.Product](productSvc, "product")

	// Casos de uso (D-02): códigos de barra (F-02).
	productCodeStore := postgres.NewProductCodeStore(db)
	productCodes := productcodes.New(productStore, productCodeStore)
	productCodesH := transport.NewProductCodesHandler(productCodes)
	// Alta de producto con código de barras opcional, en una transacción.
	createProduct := createproduct.New(postgres.NewTxRunner(db))
	// Precio de venta (F-03): precio y auditoría en una transacción.
	priceSvc := prices.New(db, postgres.NewTxRunner(db))
	pricesH := transport.NewPricesHandler(priceSvc)
	// Costo (F-04, carga manual): costo, promedio y auditoría en una transacción.
	costSvc := costs.New(db, postgres.NewTxRunner(db))
	costsH := transport.NewCostsHandler(costSvc)
	// Recepción de compras (F-06): compra, lotes, stock y costos en una transacción.
	purchaseSvc := purchases.New(db, postgres.NewTxRunner(db))
	purchasesH := transport.NewPurchasesHandler(purchaseSvc)
	stockCountSvc := stockcounts.New(db, postgres.NewTxRunner(db))
	adjustmentSvc := adjustments.New(db, postgres.NewTxRunner(db))
	// Caja (F-11): abrir con el efectivo inicial.
	sessionSvc := sessions.New(db, postgres.NewTxRunner(db))
	// Venta (F-07, F-10): venta, lotes, stock y pagos en una transacción.
	saleSvc := sales.New(db, postgres.NewTxRunner(db))

	// Login (F-13).
	authSvc := auth.New(db, postgres.NewTxRunner(db))

	// Cómo escucha el server: con HTTPS, la cookie de la sesión es Secure.
	cfg, err := loadServerConfig(os.Getenv)
	if err != nil {
		slog.Error("invalid server configuration", "error", err)
		os.Exit(1)
	}

	// Web UI
	tmplEngine := web.NewTemplateEngine()
	webH := web.NewWebHandler(tmplEngine, brandSvc, taxSvc, supplierSvc, categorySvc, productSvc, createProduct, productCodes, priceSvc, costSvc, purchaseSvc, stockCountSvc, adjustmentSvc, sessionSvc, saleSvc).
		WithAuth(authSvc, cfg.HTTPS()).
		WithUsers(users.New(db, postgres.NewTxRunner(db))).
		WithReports(reports.New(db))

	// Configurar rutas
	mux := http.NewServeMux()

	// API: entera para el admin (model.PermAPI).
	apiMux := http.NewServeMux()
	transport.RegisterAPIRoutes(apiMux, brandH, taxH, supplierH, categoryH, productH, productCodesH, pricesH, costsH, purchasesH)
	// Sin permiso, una página ve "no tenés permiso" con el menú de su rol.
	guard := transport.Guard{Page: webH.Forbidden}
	mountAPI(mux, guard, apiMux)

	// Web UI routes
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		slog.Error("failed to create static file system", "error", err)
		os.Exit(1)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
	// Cada ruta con su permiso (routes.go).
	registerRoutes(mux, guard, webRoutes(webH))

	// Middleware chain (de adentro hacia afuera): CORS; el login (todo pide
	// haber entrado, salvo /login, /static/ y el idioma); CSRF: un POST (o
	// PUT, DELETE...) que viene de otra página se rechaza con 403, con las
	// cabeceras Sec-Fetch-Site y Origin del navegador; el idioma.
	handler := transport.CORSMiddleware(mux)
	handler = transport.AuthMiddleware(authSvc, handler)
	handler = http.NewCrossOriginProtection().Handler(handler)
	handler = transport.LocaleMiddleware(handler)

	// empezar y escuchar al servidor

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		TLSConfig:         cfg.TLS,
		IdleTimeout:       time.Minute,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	slog.Info("starting server", "addr", cfg.Addr, "https", cfg.HTTPS())
	if cfg.HTTPS() {
		// El certificado ya está en TLSConfig (loadServerConfig lo cargó).
		err = server.ListenAndServeTLS("", "")
	} else {
		err = server.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		log.Fatalf("server failed: %v", err)
	}
}
