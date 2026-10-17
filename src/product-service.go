package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

// Product mirrors components/schemas/Product in api.yaml.
type Product struct {
	ProductID    int32  `json:"product_id"`
	SKU          string `json:"sku"`
	Manufacturer string `json:"manufacturer"`
	CategoryID   int32  `json:"category_id"`
	Weight       int32  `json:"weight"`
	SomeOtherID  int32  `json:"some_other_id"`
}

// productInput is the POST body before validation. Pointers let us tell a
// missing field (400) apart from a valid zero value such as weight: 0.
type productInput struct {
	ProductID    *int32  `json:"product_id"`
	SKU          *string `json:"sku"`
	Manufacturer *string `json:"manufacturer"`
	CategoryID   *int32  `json:"category_id"`
	Weight       *int32  `json:"weight"`
	SomeOtherID  *int32  `json:"some_other_id"`
}

// ErrorResponse mirrors components/schemas/Error in api.yaml.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// ProductStore is an in-memory map guarded by a read/write mutex: many GETs
// can read at once, while a POST takes the lock exclusively.
type ProductStore struct {
	mu       sync.RWMutex
	products map[int32]Product
}

func NewProductStore() *ProductStore {
	return &ProductStore{products: make(map[int32]Product)}
}

// Seed creates products 1..n. The spec has no create endpoint (POST only
// updates existing products), so the store starts with known data.
func (s *ProductStore) Seed(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 1; i <= n; i++ {
		s.products[int32(i)] = Product{
			ProductID:    int32(i),
			SKU:          fmt.Sprintf("SKU-%06d", i),
			Manufacturer: "Acme Corporation",
			CategoryID:   int32(i%50 + 1),
			Weight:       int32(100 + i%5000),
			SomeOtherID:  int32(i%1000 + 1),
		}
	}
}

func (s *ProductStore) Get(id int32) (Product, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.products[id]
	return p, ok
}

// Update replaces an existing product's details and reports whether the
// product existed. The existence check and the write happen under one lock,
// so no other request can delete or change the product in between.
func (s *ProductStore) Update(p Product) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.products[p.ProductID]; !ok {
		return false
	}
	s.products[p.ProductID] = p
	return true
}

// parseProductID enforces the path parameter schema: int32, minimum 1.
func parseProductID(raw string) (int32, error) {
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("productId must be an integer between 1 and 2147483647, got %q", raw)
	}
	if n < 1 {
		return 0, fmt.Errorf("productId must be at least 1, got %d", n)
	}
	return int32(n), nil
}

// decodeProduct reads exactly one JSON object (max 1 MB) from the body and
// validates it against the Product schema.
func decodeProduct(c *gin.Context, pathID int32) (Product, error) {
	body := http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	dec := json.NewDecoder(body)
	var in productInput
	if err := dec.Decode(&in); err != nil {
		var tooBig *http.MaxBytesError
		switch {
		case errors.Is(err, io.EOF):
			return Product{}, errors.New("request body is required")
		case errors.As(err, &tooBig):
			return Product{}, errors.New("request body must be at most 1 MB")
		default:
			return Product{}, fmt.Errorf("request body is not valid JSON for a Product: %v", err)
		}
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Product{}, errors.New("request body must contain a single JSON object")
	}
	return validateProduct(in, pathID)
}

// validateProduct checks required fields, then each field's range or length.
func validateProduct(in productInput, pathID int32) (Product, error) {
	missing := []struct {
		ok   bool
		name string
	}{
		{in.ProductID != nil, "product_id"},
		{in.SKU != nil, "sku"},
		{in.Manufacturer != nil, "manufacturer"},
		{in.CategoryID != nil, "category_id"},
		{in.Weight != nil, "weight"},
		{in.SomeOtherID != nil, "some_other_id"},
	}
	for _, f := range missing {
		if !f.ok {
			return Product{}, fmt.Errorf("%s is required", f.name)
		}
	}
	p := Product{
		ProductID:    *in.ProductID,
		SKU:          *in.SKU,
		Manufacturer: *in.Manufacturer,
		CategoryID:   *in.CategoryID,
		Weight:       *in.Weight,
		SomeOtherID:  *in.SomeOtherID,
	}
	skuLen := utf8.RuneCountInString(p.SKU)
	mfrLen := utf8.RuneCountInString(p.Manufacturer)
	switch {
	case p.ProductID < 1:
		return Product{}, errors.New("product_id must be at least 1")
	case p.ProductID != pathID:
		return Product{}, fmt.Errorf("product_id in body (%d) must match productId in path (%d)", p.ProductID, pathID)
	case skuLen < 1 || skuLen > 100:
		return Product{}, fmt.Errorf("sku must be 1-100 characters, got %d", skuLen)
	case mfrLen < 1 || mfrLen > 200:
		return Product{}, fmt.Errorf("manufacturer must be 1-200 characters, got %d", mfrLen)
	case p.CategoryID < 1:
		return Product{}, errors.New("category_id must be at least 1")
	case p.Weight < 0:
		return Product{}, errors.New("weight must be at least 0")
	case p.SomeOtherID < 1:
		return Product{}, errors.New("some_other_id must be at least 1")
	}
	return p, nil
}

func writeError(c *gin.Context, status int, code, message, details string) {
	c.AbortWithStatusJSON(status, ErrorResponse{Error: code, Message: message, Details: details})
}

// recoverInternal turns any panic into the spec's 500 Error response. The
// panic value is logged on the server but never sent to the client.
func recoverInternal(c *gin.Context, recovered any) {
	log.Printf("panic recovered on %s %s: %v", c.Request.Method, c.Request.URL.Path, recovered)
	writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR",
		"An unexpected error occurred", "see server logs for details")
}

// faultInjection deliberately panics when a request carries
// X-Simulate-Error: 500, so the 500 path can be demonstrated. It is active
// only when the server starts with ENABLE_FAULT_INJECTION=true.
func faultInjection(enabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if enabled && c.GetHeader("X-Simulate-Error") == "500" {
			panic("simulated internal error requested via X-Simulate-Error header")
		}
		c.Next()
	}
}

type handler struct {
	store *ProductStore
}

// GET /products/{productId}: 200 Product, 404 Error, 500 Error.
// The spec lists no 400 here, so an ID that cannot exist is reported as 404.
func (h *handler) getProduct(c *gin.Context) {
	id, err := parseProductID(c.Param("productId"))
	if err != nil {
		writeError(c, http.StatusNotFound, "NOT_FOUND", "Product not found", err.Error())
		return
	}
	p, ok := h.store.Get(id)
	if !ok {
		writeError(c, http.StatusNotFound, "NOT_FOUND", "Product not found",
			fmt.Sprintf("no product with ID %d", id))
		return
	}
	c.JSON(http.StatusOK, p)
}

// POST /products/{productId}/details: 204, 400 Error, 404 Error, 500 Error.
// Validation runs first (400), then existence (404), then the update (204).
func (h *handler) addProductDetails(c *gin.Context) {
	id, err := parseProductID(c.Param("productId"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_INPUT", "Invalid product ID", err.Error())
		return
	}
	p, err := decodeProduct(c, id)
	if err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_INPUT", "Invalid input data", err.Error())
		return
	}
	if !h.store.Update(p) {
		writeError(c, http.StatusNotFound, "NOT_FOUND", "Product not found",
			fmt.Sprintf("no product with ID %d", id))
		return
	}
	c.Status(http.StatusNoContent)
}

func newRouter(store *ProductStore, faults bool) *gin.Engine {
	r := gin.New()
	r.Use(observability(newJSONLogger(), os.Getenv("LOG_REQUESTS") != "false"))
	r.Use(gin.CustomRecovery(recoverInternal))
	r.Use(faultInjection(faults))

	h := &handler{store: store}
	// The spec's servers end in /v1, so serve the routes with and without it.
	for _, prefix := range []string{"", "/v1"} {
		g := r.Group(prefix)
		g.GET("/products/:productId", h.getProduct)
		g.POST("/products/:productId/details", h.addProductDetails)
	}
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET(metricsPath, metricsHandler())
	return r
}

// envInt reads a non-negative integer environment variable, or returns def.
func envInt(name string, def int) int {
	v, err := strconv.Atoi(os.Getenv(name))
	if err != nil || v < 0 {
		return def
	}
	return v
}

func main() {
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	seed := envInt("SEED_PRODUCTS", 1000)
	faults := os.Getenv("ENABLE_FAULT_INJECTION") == "true"
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	store := NewProductStore()
	store.Seed(seed)

	log.Printf("product-service: %d products seeded, fault injection=%v, listening on :%s",
		seed, faults, port)
	if err := newRouter(store, faults).Run(":" + port); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}
