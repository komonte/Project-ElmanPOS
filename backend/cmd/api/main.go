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
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/costs"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/createproduct"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/prices"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/productcodes"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/purchases"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/stockcounts"
)

//go:embed static/css/* static/js/* static/fonts/*
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

	// Web UI
	tmplEngine := web.NewTemplateEngine()
	webH := web.NewWebHandler(tmplEngine, brandSvc, taxSvc, supplierSvc, categorySvc, productSvc, createProduct, productCodes, priceSvc, costSvc, purchaseSvc, stockCountSvc)

	// Configurar rutas
	mux := http.NewServeMux()

	// API routes
	transport.RegisterAPIRoutes(mux, brandH, taxH, supplierH, categoryH, productH, productCodesH, pricesH, costsH, purchasesH)

	// Web UI routes
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		slog.Error("failed to create static file system", "error", err)
		os.Exit(1)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	mux.HandleFunc("GET /{$}", webH.Index)
	mux.HandleFunc("GET /brands", webH.BrandsList)
	mux.HandleFunc("GET /brands/new", webH.BrandsNewForm)
	mux.HandleFunc("POST /brands/new", webH.BrandsCreate)
	mux.HandleFunc("GET /brands/{id}/edit", webH.BrandsEditForm)
	mux.HandleFunc("POST /brands/{id}/edit", webH.BrandsUpdate)
	mux.HandleFunc("DELETE /brands/{id}", webH.BrandsDelete)
	mux.HandleFunc("PATCH /brands/{id}/deactivate", webH.BrandsDeactivate)
	mux.HandleFunc("PATCH /brands/{id}/reactivate", webH.BrandsReactivate)
	mux.HandleFunc("GET /taxes", webH.TaxesList)
	mux.HandleFunc("GET /taxes/new", webH.TaxesNewForm)
	mux.HandleFunc("POST /taxes/new", webH.TaxesCreate)
	mux.HandleFunc("GET /taxes/{id}/edit", webH.TaxesEditForm)
	mux.HandleFunc("POST /taxes/{id}/edit", webH.TaxesUpdate)
	mux.HandleFunc("DELETE /taxes/{id}", webH.TaxesDelete)
	mux.HandleFunc("PATCH /taxes/{id}/deactivate", webH.TaxesDeactivate)
	mux.HandleFunc("PATCH /taxes/{id}/reactivate", webH.TaxesReactivate)
	mux.HandleFunc("GET /suppliers", webH.SuppliersList)
	mux.HandleFunc("GET /suppliers/new", webH.SuppliersNewForm)
	mux.HandleFunc("POST /suppliers/new", webH.SuppliersCreate)
	mux.HandleFunc("GET /suppliers/{id}/edit", webH.SuppliersEditForm)
	mux.HandleFunc("POST /suppliers/{id}/edit", webH.SuppliersUpdate)
	mux.HandleFunc("DELETE /suppliers/{id}", webH.SuppliersDelete)
	mux.HandleFunc("PATCH /suppliers/{id}/deactivate", webH.SuppliersDeactivate)
	mux.HandleFunc("PATCH /suppliers/{id}/reactivate", webH.SuppliersReactivate)

	mux.HandleFunc("GET /categories", webH.CategoriesList)
	mux.HandleFunc("GET /categories/new", webH.CategoriesNewForm)
	mux.HandleFunc("POST /categories/new", webH.CategoriesCreate)
	mux.HandleFunc("GET /categories/{id}/edit", webH.CategoriesEditForm)
	mux.HandleFunc("POST /categories/{id}/edit", webH.CategoriesUpdate)
	mux.HandleFunc("DELETE /categories/{id}", webH.CategoriesDelete)
	mux.HandleFunc("PATCH /categories/{id}/deactivate", webH.CategoriesDeactivate)
	mux.HandleFunc("PATCH /categories/{id}/reactivate", webH.CategoriesReactivate)

	mux.HandleFunc("GET /products", webH.ProductsList)
	mux.HandleFunc("GET /products/new", webH.ProductsNewForm)
	mux.HandleFunc("GET /products/brand-options", webH.ProductsBrandOptions)
	mux.HandleFunc("GET /products/barcode-check", webH.ProductsBarcodeCheck)
	mux.HandleFunc("POST /products/new", webH.ProductsCreate)
	mux.HandleFunc("GET /products/{id}/edit", webH.ProductsEditForm)
	mux.HandleFunc("POST /products/{id}/edit", webH.ProductsUpdate)
	mux.HandleFunc("DELETE /products/{id}", webH.ProductsDelete)
	mux.HandleFunc("PATCH /products/{id}/deactivate", webH.ProductsDeactivate)
	mux.HandleFunc("PATCH /products/{id}/reactivate", webH.ProductsReactivate)
	mux.HandleFunc("POST /products/{id}/codes", webH.ProductCodesAdd)
	mux.HandleFunc("DELETE /products/{id}/codes/{codeID}", webH.ProductCodesRemove)
	mux.HandleFunc("POST /products/{id}/prices", webH.ProductPricesSet)
	mux.HandleFunc("POST /products/{id}/costs", webH.ProductCostsSet)
	mux.HandleFunc("GET /counts", webH.CountsPage)
	mux.HandleFunc("POST /counts", webH.CountsOpen)
	mux.HandleFunc("POST /counts/{id}/entries", webH.CountsAddEntry)
	mux.HandleFunc("POST /counts/{id}/entries/{entryID}/delete", webH.CountsRemoveEntry)
	mux.HandleFunc("POST /counts/{id}/discard", webH.CountsDiscard)
	mux.HandleFunc("GET /counts/{id}/review", webH.CountsReview)
	mux.HandleFunc("POST /counts/{id}/items/{productID}/details", webH.CountsSetDetails)
	mux.HandleFunc("POST /counts/{id}/confirm", webH.CountsConfirm)
	mux.HandleFunc("GET /purchases", webH.PurchasesPage)
	mux.HandleFunc("POST /purchases", webH.PurchasesReceive)
	mux.HandleFunc("POST /purchases/prices/{id}", webH.PurchasesUsePrice)
	// El buscador de producto de un renglón (compras y conteos): opciones,
	// alta rápida y agregar código.
	mux.HandleFunc("GET /picker/product-options", webH.PickerProductOptions)
	mux.HandleFunc("GET /picker/quick-product", webH.PickerQuickProductForm)
	mux.HandleFunc("POST /picker/quick-product", webH.PickerQuickProductCreate)
	mux.HandleFunc("GET /picker/assign-code", webH.PickerAssignCodeForm)
	mux.HandleFunc("GET /picker/assign-code/options", webH.PickerAssignCodeOptions)
	mux.HandleFunc("POST /picker/assign-code", webH.PickerAssignCode)
	mux.HandleFunc("POST /settings/language", webH.SetLanguage)

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