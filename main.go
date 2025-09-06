package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/cors"
)

// SSE Client management
type SSEClient struct {
	DeviceID string
	Channel  chan []byte
	Closed   bool
}

// Global SSE clients store
var (
	sseClients      = make(map[string]*SSEClient)
	sseClientsMutex = sync.RWMutex{}
)

// SSE Event types
type SSEEvent struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// Broadcast SSE message to specific device
func broadcastToDevice(deviceID string, event SSEEvent) {
	sseClientsMutex.RLock()
	client, exists := sseClients[deviceID]
	sseClientsMutex.RUnlock()

	if exists && !client.Closed {
		eventData, _ := json.Marshal(event)
		select {
		case client.Channel <- eventData:
		default:
			// Channel is full, skip
		}
	}
}

// Response structures
type Response struct {
	Message string      `json:"message"`
	Status  string      `json:"status"`
	Time    string      `json:"time"`
	Data    interface{} `json:"data,omitempty"`
}

// User and Device structures
type User struct {
	ID           string    `json:"id"`
	DeviceID     string    `json:"device_id"`
	Credits      int       `json:"credits"`
	FreePhotos   int       `json:"free_photos"`
	TotalPhotos  int       `json:"total_photos"`
	CreatedAt    time.Time `json:"created_at"`
	LastActiveAt time.Time `json:"last_active_at"`
}

// Payment structures
type PaymentRequest struct {
	DeviceID      string `json:"device_id"`
	Amount        int    `json:"amount"`         // in MMK
	Credits       int    `json:"credits"`        // number of photos
	PaymentMethod string `json:"payment_method"` // "wavepay" or "kbzpay"
	TransactionID string `json:"transaction_id"` // user provided
	Screenshot    string `json:"screenshot"`     // base64 image proof
}

type PaymentTransaction struct {
	ID            string     `json:"id"`
	DeviceID      string     `json:"device_id"`
	Amount        int        `json:"amount"`
	Credits       int        `json:"credits"`
	PaymentMethod string     `json:"payment_method"`
	TransactionID string     `json:"transaction_id"`
	Screenshot    string     `json:"screenshot"`
	Status        string     `json:"status"` // "pending", "approved", "rejected"
	CreatedAt     time.Time  `json:"created_at"`
	ProcessedAt   *time.Time `json:"processed_at,omitempty"`
	ProcessedBy   string     `json:"processed_by,omitempty"`
}

// Image processing structures
type ImageData struct {
	ID        string    `json:"id"`
	DeviceID  string    `json:"device_id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Labels    []string  `json:"labels"`
	CreatedAt time.Time `json:"created_at"`
}

// Payment account info
type PaymentAccount struct {
	ID            string `json:"id"`
	Method        string `json:"method"`         // Provider name like "kbzpay", "wavepay"
	AccountName   string `json:"account_name"`   // Username
	AccountNumber string `json:"account_number"` // Account number
	QRCode        string `json:"qr_code,omitempty"`
	IsActive      bool   `json:"is_active"`
	CreatedAt     string `json:"created_at"`
}

// Admin request structures
type AdminAddPaymentAccountRequest struct {
	Method        string `json:"method"`
	AccountName   string `json:"account_name"`
	AccountNumber string `json:"account_number"`
	QRCode        string `json:"qr_code,omitempty"`
}

// System settings structures
type SystemSettings struct {
	ID              string `json:"id"`
	CreditsPerPhoto int    `json:"credits_per_photo"`
	CreditPriceMMK  int    `json:"credit_price_mmk"`
	FreePhotosLimit int    `json:"free_photos_limit"`
	MinimumPurchase int    `json:"minimum_purchase"`
	UpdatedAt       string `json:"updated_at"`
}

type AdminUpdateSettingsRequest struct {
	CreditsPerPhoto int `json:"credits_per_photo"`
	CreditPriceMMK  int `json:"credit_price_mmk"`
	FreePhotosLimit int `json:"free_photos_limit"`
	MinimumPurchase int `json:"minimum_purchase"`
}

// Credit Package structures
type CreditPackage struct {
	ID        string `json:"id"`
	Name      string `json:"name"`       // e.g., "Starter", "Standard", "Premium"
	Credits   int    `json:"credits"`    // Number of credits
	PriceMMK  int    `json:"price_mmk"`  // Total price in MMK
	IsActive  bool   `json:"is_active"`  // Whether package is available
	IsDefault bool   `json:"is_default"` // Whether this is a default package
	CreatedAt string `json:"created_at"`
}

type AdminCreditPackageRequest struct {
	Name      string `json:"name"`
	Credits   int    `json:"credits"`
	PriceMMK  int    `json:"price_mmk"`
	IsActive  bool   `json:"is_active"`
	IsDefault bool   `json:"is_default"`
}

// In-memory storage with SQLite database persistence
var db *sql.DB
var users = make(map[string]*User)
var transactions = make(map[string]*PaymentTransaction)
var images = make(map[string]*ImageData)
var paymentAccounts = make(map[string]*PaymentAccount)
var creditPackages = make(map[string]*CreditPackage)
var systemSettings *SystemSettings
var usersMutex = sync.RWMutex{}
var paymentAccountsMutex = sync.RWMutex{}
var creditPackagesMutex = sync.RWMutex{}
var settingsMutex = sync.RWMutex{}

// Database file path
const DB_FILE = "imagelabel.db"

// Constants
const (
	CREDIT_PRICE_MMK  = 100 // 100 MMK per credit
	CREDITS_PER_PHOTO = 10  // 10 credits needed per photo
	FREE_PHOTOS_LIMIT = 30
)

// Initialize SQLite database
func initDatabase() error {
	var err error
	db, err = sql.Open("sqlite3", DB_FILE)
	if err != nil {
		return fmt.Errorf("failed to open database: %v", err)
	}

	// Test connection
	if err = db.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %v", err)
	}

	// Create tables
	if err = createTables(); err != nil {
		return fmt.Errorf("failed to create tables: %v", err)
	}

	log.Printf("✅ Database initialized: %s", DB_FILE)
	return nil
}

// Create database tables
func createTables() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			device_id TEXT PRIMARY KEY,
			id TEXT NOT NULL,
			credits INTEGER NOT NULL DEFAULT 0,
			free_photos INTEGER NOT NULL DEFAULT 30,
			total_photos INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			last_active_at DATETIME NOT NULL
		)`,

		`CREATE TABLE IF NOT EXISTS transactions (
			id TEXT PRIMARY KEY,
			device_id TEXT NOT NULL,
			amount INTEGER NOT NULL,
			credits INTEGER NOT NULL,
			payment_method TEXT NOT NULL,
			transaction_id TEXT NOT NULL,
			screenshot TEXT,
			status TEXT NOT NULL DEFAULT 'pending',
			created_at DATETIME NOT NULL,
			processed_at DATETIME,
			processed_by TEXT
		)`,

		`CREATE TABLE IF NOT EXISTS images (
			id TEXT PRIMARY KEY,
			device_id TEXT NOT NULL,
			name TEXT NOT NULL,
			url TEXT NOT NULL,
			labels TEXT,
			created_at DATETIME NOT NULL
		)`,

		`CREATE TABLE IF NOT EXISTS payment_accounts (
			id TEXT PRIMARY KEY,
			method TEXT NOT NULL,
			account_name TEXT NOT NULL,
			account_number TEXT NOT NULL,
			qr_code TEXT,
			is_active BOOLEAN NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL
		)`,

		`CREATE TABLE IF NOT EXISTS system_settings (
			id TEXT PRIMARY KEY DEFAULT 'default',
			credits_per_photo INTEGER NOT NULL DEFAULT 10,
			credit_price_mmk INTEGER NOT NULL DEFAULT 100,
			free_photos_limit INTEGER NOT NULL DEFAULT 30,
			minimum_purchase INTEGER NOT NULL DEFAULT 10,
			updated_at DATETIME NOT NULL
		)`,

		`CREATE TABLE IF NOT EXISTS credit_packages (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			credits INTEGER NOT NULL,
			price_mmk INTEGER NOT NULL,
			is_active BOOLEAN NOT NULL DEFAULT 1,
			is_default BOOLEAN NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL
		)`,
	}

	for _, query := range queries {
		_, err := db.Exec(query)
		if err != nil {
			return fmt.Errorf("failed to create table: %v", err)
		}
	}

	return nil
}

// Load data from database into memory
func loadDataFromDatabase() error {
	usersMutex.Lock()
	defer usersMutex.Unlock()

	// Load users
	rows, err := db.Query("SELECT device_id, id, credits, free_photos, total_photos, created_at, last_active_at FROM users")
	if err != nil {
		return fmt.Errorf("failed to load users: %v", err)
	}
	defer rows.Close()

	userCount := 0
	for rows.Next() {
		var user User
		var createdAt, lastActiveAt string
		err := rows.Scan(&user.DeviceID, &user.ID, &user.Credits, &user.FreePhotos, &user.TotalPhotos, &createdAt, &lastActiveAt)
		if err != nil {
			log.Printf("Error scanning user: %v", err)
			continue
		}

		// Parse timestamps
		user.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		user.LastActiveAt, _ = time.Parse(time.RFC3339, lastActiveAt)

		users[user.DeviceID] = &user
		userCount++
	}
	log.Printf("Loaded %d users from database", userCount)

	// Load transactions
	rows, err = db.Query("SELECT id, device_id, amount, credits, payment_method, transaction_id, screenshot, status, created_at, processed_at, processed_by FROM transactions")
	if err != nil {
		return fmt.Errorf("failed to load transactions: %v", err)
	}
	defer rows.Close()

	transactionCount := 0
	for rows.Next() {
		var tx PaymentTransaction
		var createdAt, processedAt, processedBy sql.NullString
		err := rows.Scan(&tx.ID, &tx.DeviceID, &tx.Amount, &tx.Credits, &tx.PaymentMethod, &tx.TransactionID, &tx.Screenshot, &tx.Status, &createdAt, &processedAt, &processedBy)
		if err != nil {
			log.Printf("Error scanning transaction: %v", err)
			continue
		}

		// Parse timestamps
		tx.CreatedAt, _ = time.Parse(time.RFC3339, createdAt.String)
		if processedAt.Valid {
			parsedTime, _ := time.Parse(time.RFC3339, processedAt.String)
			tx.ProcessedAt = &parsedTime
		}
		if processedBy.Valid {
			tx.ProcessedBy = processedBy.String
		}

		transactions[tx.ID] = &tx
		transactionCount++
	}
	log.Printf("Loaded %d transactions from database", transactionCount)

	// Load images
	rows, err = db.Query("SELECT id, device_id, name, url, labels, created_at FROM images")
	if err != nil {
		return fmt.Errorf("failed to load images: %v", err)
	}
	defer rows.Close()

	imageCount := 0
	for rows.Next() {
		var image ImageData
		var labelsJSON, createdAt string
		err := rows.Scan(&image.ID, &image.DeviceID, &image.Name, &image.URL, &labelsJSON, &createdAt)
		if err != nil {
			log.Printf("Error scanning image: %v", err)
			continue
		}

		// Parse labels JSON
		json.Unmarshal([]byte(labelsJSON), &image.Labels)

		// Parse timestamp
		image.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)

		images[image.ID] = &image
		imageCount++
	}
	log.Printf("Loaded %d images from database", imageCount)

	// Load payment accounts
	paymentAccountsMutex.Lock()
	rows, err = db.Query("SELECT id, method, account_name, account_number, qr_code, is_active, created_at FROM payment_accounts")
	if err != nil {
		paymentAccountsMutex.Unlock()
		return fmt.Errorf("failed to load payment accounts: %v", err)
	}
	defer rows.Close()

	accountCount := 0
	for rows.Next() {
		var account PaymentAccount
		var qrCode sql.NullString
		err := rows.Scan(&account.ID, &account.Method, &account.AccountName, &account.AccountNumber, &qrCode, &account.IsActive, &account.CreatedAt)
		if err != nil {
			log.Printf("Error scanning payment account: %v", err)
			continue
		}

		if qrCode.Valid {
			account.QRCode = qrCode.String
		}

		paymentAccounts[account.ID] = &account
		accountCount++
	}
	paymentAccountsMutex.Unlock()
	log.Printf("Loaded %d payment accounts from database", accountCount)

	// Load system settings
	settingsMutex.Lock()
	row := db.QueryRow("SELECT id, credits_per_photo, credit_price_mmk, free_photos_limit, minimum_purchase, updated_at FROM system_settings WHERE id = 'default'")

	var settings SystemSettings
	err = row.Scan(&settings.ID, &settings.CreditsPerPhoto, &settings.CreditPriceMMK, &settings.FreePhotosLimit, &settings.MinimumPurchase, &settings.UpdatedAt)
	if err == sql.ErrNoRows {
		// Create default settings if they don't exist
		settings = SystemSettings{
			ID:              "default",
			CreditsPerPhoto: CREDITS_PER_PHOTO,
			CreditPriceMMK:  CREDIT_PRICE_MMK,
			FreePhotosLimit: FREE_PHOTOS_LIMIT,
			MinimumPurchase: 10,
			UpdatedAt:       time.Now().Format(time.RFC3339),
		}
		_, err = db.Exec("INSERT INTO system_settings (id, credits_per_photo, credit_price_mmk, free_photos_limit, minimum_purchase, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
			settings.ID, settings.CreditsPerPhoto, settings.CreditPriceMMK, settings.FreePhotosLimit, settings.MinimumPurchase, settings.UpdatedAt)
		if err != nil {
			settingsMutex.Unlock()
			return fmt.Errorf("failed to create default settings: %v", err)
		}
		log.Printf("Created default system settings")
	} else if err != nil {
		settingsMutex.Unlock()
		return fmt.Errorf("failed to load settings: %v", err)
	}

	systemSettings = &settings
	settingsMutex.Unlock()
	log.Printf("Loaded system settings: %d credits per photo, %d MMK per credit", settings.CreditsPerPhoto, settings.CreditPriceMMK)

	// Load credit packages
	if err := loadCreditPackagesFromDB(); err != nil {
		log.Printf("Warning: Failed to load credit packages: %v", err)
	} else {
		creditPackagesMutex.RLock()
		packageCount := len(creditPackages)
		creditPackagesMutex.RUnlock()
		log.Printf("Loaded %d credit packages from database", packageCount)
	}

	return nil
}

// Save data to database (replace saveDataToFiles)
func saveDataToDatabase() {
	// This function is now mostly handled by individual insert/update operations
	// We'll keep it for compatibility but it's not actively used
}

// Database helper functions
func saveUserToDB(user *User) error {
	query := `INSERT OR REPLACE INTO users (device_id, id, credits, free_photos, total_photos, created_at, last_active_at) 
			  VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := db.Exec(query, user.DeviceID, user.ID, user.Credits, user.FreePhotos, user.TotalPhotos,
		user.CreatedAt.Format(time.RFC3339), user.LastActiveAt.Format(time.RFC3339))
	return err
}

func saveTransactionToDB(tx *PaymentTransaction) error {
	var processedAt sql.NullString
	if tx.ProcessedAt != nil {
		processedAt.String = tx.ProcessedAt.Format(time.RFC3339)
		processedAt.Valid = true
	}

	query := `INSERT OR REPLACE INTO transactions (id, device_id, amount, credits, payment_method, transaction_id, screenshot, status, created_at, processed_at, processed_by) 
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := db.Exec(query, tx.ID, tx.DeviceID, tx.Amount, tx.Credits, tx.PaymentMethod, tx.TransactionID,
		tx.Screenshot, tx.Status, tx.CreatedAt.Format(time.RFC3339), processedAt, tx.ProcessedBy)
	return err
}

func saveImageToDB(image *ImageData) error {
	labelsJSON, _ := json.Marshal(image.Labels)
	query := `INSERT OR REPLACE INTO images (id, device_id, name, url, labels, created_at) 
			  VALUES (?, ?, ?, ?, ?, ?)`
	_, err := db.Exec(query, image.ID, image.DeviceID, image.Name, image.URL, string(labelsJSON),
		image.CreatedAt.Format(time.RFC3339))
	return err
}

func savePaymentAccountToDB(account *PaymentAccount) error {
	query := `INSERT OR REPLACE INTO payment_accounts (id, method, account_name, account_number, qr_code, is_active, created_at) 
			  VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := db.Exec(query, account.ID, account.Method, account.AccountName, account.AccountNumber,
		account.QRCode, account.IsActive, account.CreatedAt)
	return err
}

func deletePaymentAccountFromDB(accountID string) error {
	query := `DELETE FROM payment_accounts WHERE id = ?`
	_, err := db.Exec(query, accountID)
	return err
}

func saveSettingsToDB(settings *SystemSettings) error {
	query := `UPDATE system_settings SET credits_per_photo = ?, credit_price_mmk = ?, free_photos_limit = ?, minimum_purchase = ?, updated_at = ? WHERE id = ?`
	_, err := db.Exec(query, settings.CreditsPerPhoto, settings.CreditPriceMMK, settings.FreePhotosLimit, settings.MinimumPurchase, settings.UpdatedAt, settings.ID)
	return err
}

// Helper functions to get current settings values
func getCurrentCreditsPerPhoto() int {
	settingsMutex.RLock()
	defer settingsMutex.RUnlock()
	if systemSettings != nil {
		return systemSettings.CreditsPerPhoto
	}
	return CREDITS_PER_PHOTO // fallback to constant
}

func getCurrentCreditPrice() int {
	settingsMutex.RLock()
	defer settingsMutex.RUnlock()
	if systemSettings != nil {
		return systemSettings.CreditPriceMMK
	}
	return CREDIT_PRICE_MMK // fallback to constant
}

func getCurrentFreePhotosLimit() int {
	settingsMutex.RLock()
	defer settingsMutex.RUnlock()
	if systemSettings != nil {
		return systemSettings.FreePhotosLimit
	}
	return FREE_PHOTOS_LIMIT // fallback to constant
}

func getCurrentMinimumPurchase() int {
	settingsMutex.RLock()
	defer settingsMutex.RUnlock()
	if systemSettings != nil {
		return systemSettings.MinimumPurchase
	}
	return 10 // fallback default
}

// Credit Package Database Functions
func saveCreditPackageToDB(pkg *CreditPackage) error {
	query := `INSERT OR REPLACE INTO credit_packages (id, name, credits, price_mmk, is_active, is_default, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := db.Exec(query, pkg.ID, pkg.Name, pkg.Credits, pkg.PriceMMK, pkg.IsActive, pkg.IsDefault, pkg.CreatedAt)
	return err
}

func deleteCreditPackageFromDB(packageID string) error {
	query := `DELETE FROM credit_packages WHERE id = ?`
	_, err := db.Exec(query, packageID)
	return err
}

func loadCreditPackagesFromDB() error {
	query := `SELECT id, name, credits, price_mmk, is_active, is_default, created_at FROM credit_packages`
	rows, err := db.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	creditPackagesMutex.Lock()
	defer creditPackagesMutex.Unlock()

	for rows.Next() {
		pkg := &CreditPackage{}
		err := rows.Scan(&pkg.ID, &pkg.Name, &pkg.Credits, &pkg.PriceMMK, &pkg.IsActive, &pkg.IsDefault, &pkg.CreatedAt)
		if err != nil {
			log.Printf("Error scanning credit package: %v", err)
			continue
		}
		creditPackages[pkg.ID] = pkg
	}

	return rows.Err()
}

// Serve admin dashboard
func serveAdminHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "admin_pro.html")
}

// Serve professional admin dashboard
func serveAdminProHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "admin_pro.html")
}

// Serve landing page
func serveLandingPageHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "index.html")
}

func main() {
	// Initialize database
	log.Println("🔄 Initializing SQLite database...")
	if err := initDatabase(); err != nil {
		log.Fatalf("Database initialization failed: %v", err)
	}
	defer db.Close()

	// Load existing data from database
	log.Println("🔄 Loading data from database...")
	if err := loadDataFromDatabase(); err != nil {
		log.Printf("Warning: Failed to load data from database: %v", err)
	}

	r := mux.NewRouter()

	// Serve admin dashboard
	r.HandleFunc("/admin.html", serveAdminHandler).Methods("GET")
	r.HandleFunc("/admin", serveAdminHandler).Methods("GET")
	r.HandleFunc("/admin-pro", serveAdminProHandler).Methods("GET")
	r.HandleFunc("/admin-pro.html", serveAdminProHandler).Methods("GET")

	// API routes
	api := r.PathPrefix("/api").Subrouter()

	// Health check endpoint
	api.HandleFunc("/health", healthHandler).Methods("GET")

	// User/Device management
	api.HandleFunc("/user/register", registerDeviceHandler).Methods("POST")
	api.HandleFunc("/user/{device_id}/status", getUserStatusHandler).Methods("GET")

	// Payment system
	api.HandleFunc("/payment/accounts", getPaymentAccountsHandler).Methods("GET")
	api.HandleFunc("/payment/request", submitPaymentHandler).Methods("POST")
	api.HandleFunc("/payment/history/{device_id}", getPaymentHistoryHandler).Methods("GET")

	// Credit packages (public endpoint for mobile)
	api.HandleFunc("/credit-packages", getCreditPackagesHandler).Methods("GET")

	// Admin endpoints
	admin := api.PathPrefix("/admin").Subrouter()
	admin.HandleFunc("/transactions", getAllTransactionsHandler).Methods("GET")
	admin.HandleFunc("/transactions/{id}/approve", approveTransactionHandler).Methods("POST")
	admin.HandleFunc("/transactions/{id}/reject", rejectTransactionHandler).Methods("POST")

	// Admin payment account management
	admin.HandleFunc("/payment/accounts", adminGetPaymentAccountsHandler).Methods("GET")
	admin.HandleFunc("/payment/accounts", adminAddPaymentAccountHandler).Methods("POST")
	admin.HandleFunc("/payment/accounts/{id}", adminUpdatePaymentAccountHandler).Methods("PUT")
	admin.HandleFunc("/payment/accounts/{id}", adminDeletePaymentAccountHandler).Methods("DELETE")
	admin.HandleFunc("/payment/accounts/{id}/toggle", adminTogglePaymentAccountHandler).Methods("POST")

	// Admin system settings management
	admin.HandleFunc("/settings", adminGetSettingsHandler).Methods("GET")
	admin.HandleFunc("/settings", adminUpdateSettingsHandler).Methods("PUT")

	// Admin credit packages management
	admin.HandleFunc("/credit-packages", adminGetCreditPackagesHandler).Methods("GET")
	admin.HandleFunc("/credit-packages", adminAddCreditPackageHandler).Methods("POST")
	admin.HandleFunc("/credit-packages/{id}", adminUpdateCreditPackageHandler).Methods("PUT")
	admin.HandleFunc("/credit-packages/{id}", adminDeleteCreditPackageHandler).Methods("DELETE")
	admin.HandleFunc("/credit-packages/{id}/toggle", adminToggleCreditPackageHandler).Methods("POST")

	admin.HandleFunc("/users", getAllUsersHandler).Methods("GET")

	// Image processing endpoints
	api.HandleFunc("/images", getImagesHandler).Methods("GET")
	api.HandleFunc("/images/upload", uploadImageHandler).Methods("POST")
	api.HandleFunc("/images/{id}/labels", addLabelHandler).Methods("POST")

	// SSE endpoint for real-time updates
	api.HandleFunc("/stream/{device_id}", sseHandler).Methods("GET")

	// Serve static files (APK, images, etc.)
	r.PathPrefix("/").Handler(http.StripPrefix("/", http.FileServer(http.Dir("./"))))

	// Serve landing page at root
	r.HandleFunc("/", serveLandingPageHandler).Methods("GET")

	// CORS middleware
	c := cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"*"},
	})

	handler := c.Handler(r)

	fmt.Println("🚀 Go subscription server starting on port 8080...")
	fmt.Println("🌐 Professional Admin Dashboard: http://localhost:8080/admin.html")
	fmt.Println("📋 Available endpoints:")
	fmt.Println("   === Admin Dashboard ===")
	fmt.Println("   GET  /admin.html (Professional UI)")
	fmt.Println("   GET  /admin (Professional UI)")
	fmt.Println("   GET  /admin-pro (Professional UI)")
	fmt.Println("   GET  /admin-pro.html (Professional UI)")
	fmt.Println("   === User Management ===")
	fmt.Println("   POST /api/user/register")
	fmt.Println("   GET  /api/user/{device_id}/status")
	fmt.Println("   === Payment System ===")
	fmt.Println("   GET  /api/payment/accounts")
	fmt.Println("   POST /api/payment/request")
	fmt.Println("   GET  /api/payment/history/{device_id}")
	fmt.Println("   GET  /api/credit-packages")
	fmt.Println("   === Admin Panel ===")
	fmt.Println("   GET  /api/admin/transactions")
	fmt.Println("   POST /api/admin/transactions/{id}/approve")
	fmt.Println("   POST /api/admin/transactions/{id}/reject")
	fmt.Println("   GET  /api/admin/users")
	fmt.Println("   === Admin Payment Management ===")
	fmt.Println("   GET  /api/admin/payment/accounts")
	fmt.Println("   POST /api/admin/payment/accounts")
	fmt.Println("   PUT  /api/admin/payment/accounts/{id}")
	fmt.Println("   DELETE /api/admin/payment/accounts/{id}")
	fmt.Println("   POST /api/admin/payment/accounts/{id}/toggle")
	fmt.Println("   === Image Processing ===")
	fmt.Println("   GET  /api/images")
	fmt.Println("   POST /api/images/upload")
	fmt.Println("   POST /api/images/{id}/labels")

	log.Fatal(http.ListenAndServe(":8080", handler))
}

// Utility functions
func generateID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func getUserByDeviceID(deviceID string) *User {
	usersMutex.RLock()
	defer usersMutex.RUnlock()
	return users[deviceID]
}

func createUser(deviceID string) *User {
	usersMutex.Lock()
	defer usersMutex.Unlock()

	freePhotosLimit := getCurrentFreePhotosLimit()
	user := &User{
		ID:           generateID(),
		DeviceID:     deviceID,
		Credits:      0,
		FreePhotos:   freePhotosLimit,
		TotalPhotos:  0,
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
	}
	users[deviceID] = user

	// Save to database immediately
	if err := saveUserToDB(user); err != nil {
		log.Printf("Error saving user to database: %v", err)
	}

	log.Printf("Created new user: %s with %d free photos", deviceID, freePhotosLimit)
	return user
}

// Health check handler
func healthHandler(w http.ResponseWriter, r *http.Request) {
	response := Response{
		Message: "Subscription server is running",
		Status:  "healthy",
		Time:    time.Now().Format(time.RFC3339),
		Data: map[string]interface{}{
			"total_users":        len(users),
			"total_transactions": len(transactions),
			"total_images":       len(images),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// User Management Handlers
func registerDeviceHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID string `json:"device_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.DeviceID == "" {
		http.Error(w, "Device ID is required", http.StatusBadRequest)
		return
	}

	user := getUserByDeviceID(req.DeviceID)
	var message string

	if user == nil {
		user = createUser(req.DeviceID)
		message = "Device registered successfully! Welcome to ImageLabel App! 🎉"
		log.Printf("✅ New user registered: %s with %d free photos", req.DeviceID, user.FreePhotos)
	} else {
		usersMutex.Lock()
		user.LastActiveAt = time.Now()
		usersMutex.Unlock()

		// Save updated data to database
		if err := saveUserToDB(user); err != nil {
			log.Printf("Error updating user in database: %v", err)
		}
		message = "Welcome Back Sir! 👋"
		log.Printf("🔄 Existing user logged in: %s (Free: %d, Credits: %d)", req.DeviceID, user.FreePhotos, user.Credits)
	}

	response := Response{
		Message: message,
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data:    user,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func getUserStatusHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	deviceID := vars["device_id"]

	user := getUserByDeviceID(deviceID)
	if user == nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	user.LastActiveAt = time.Now()

	response := Response{
		Message: "User status retrieved",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data: map[string]interface{}{
			"user":             user,
			"can_process_free": user.FreePhotos > 0,
			"needs_payment":    user.FreePhotos == 0 && user.Credits == 0,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Payment System Handlers
func getPaymentAccountsHandler(w http.ResponseWriter, r *http.Request) {
	paymentAccountsMutex.RLock()
	defer paymentAccountsMutex.RUnlock()

	// Get only active payment accounts for public access
	var activeAccounts []PaymentAccount
	for _, account := range paymentAccounts {
		if account.IsActive {
			activeAccounts = append(activeAccounts, *account)
		}
	}

	// If no accounts configured, return empty list
	if len(activeAccounts) == 0 {
		activeAccounts = []PaymentAccount{}
	}

	// Get active credit packages
	creditPackagesMutex.RLock()
	var activeCreditPackages []CreditPackage
	for _, pkg := range creditPackages {
		if pkg.IsActive {
			activeCreditPackages = append(activeCreditPackages, *pkg)
		}
	}
	creditPackagesMutex.RUnlock()

	response := Response{
		Message: "Payment accounts retrieved",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data: map[string]interface{}{
			"accounts":          activeAccounts,
			"credit_packages":   activeCreditPackages,
			"credit_price":      getCurrentCreditPrice(),
			"credits_per_photo": getCurrentCreditsPerPhoto(),
			"photo_price":       getCurrentCreditPrice() * getCurrentCreditsPerPhoto(),
			"minimum_purchase":  getCurrentMinimumPurchase(),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func submitPaymentHandler(w http.ResponseWriter, r *http.Request) {
	var req PaymentRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validation
	if req.DeviceID == "" || req.Amount <= 0 || req.Credits <= 0 || req.PaymentMethod == "" || req.TransactionID == "" {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	// Check if payment method exists in our accounts
	validMethod := false
	paymentAccountsMutex.RLock()
	for _, account := range paymentAccounts {
		if account.Method == req.PaymentMethod {
			validMethod = true
			break
		}
	}
	paymentAccountsMutex.RUnlock()

	if !validMethod {
		http.Error(w, "Invalid payment method", http.StatusBadRequest)
		return
	}

	// Verify amount calculation
	expectedAmount := req.Credits * getCurrentCreditPrice()
	if req.Amount != expectedAmount {
		http.Error(w, fmt.Sprintf("Invalid amount. Expected %d MMK for %d credits", expectedAmount, req.Credits), http.StatusBadRequest)
		return
	}

	// Create transaction
	transaction := &PaymentTransaction{
		ID:            generateID(),
		DeviceID:      req.DeviceID,
		Amount:        req.Amount,
		Credits:       req.Credits,
		PaymentMethod: req.PaymentMethod,
		TransactionID: req.TransactionID,
		Screenshot:    req.Screenshot,
		Status:        "pending",
		CreatedAt:     time.Now(),
	}

	transactions[transaction.ID] = transaction

	// Save to database
	if err := saveTransactionToDB(transaction); err != nil {
		log.Printf("Error saving transaction to database: %v", err)
	}

	response := Response{
		Message: "Payment request submitted successfully",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data: map[string]interface{}{
			"transaction_id": transaction.ID,
			"status":         transaction.Status,
			"message":        "Your payment is being processed. Credits will be added after admin approval.",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func getPaymentHistoryHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	deviceID := vars["device_id"]

	var userTransactions []*PaymentTransaction
	for _, tx := range transactions {
		if tx.DeviceID == deviceID {
			userTransactions = append(userTransactions, tx)
		}
	}

	response := Response{
		Message: "Payment history retrieved",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data:    userTransactions,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Admin Handlers
func getAllTransactionsHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("🔍 getAllTransactionsHandler called - processing %d transactions", len(transactions))

	var allTransactions []*PaymentTransaction
	for _, tx := range transactions {
		allTransactions = append(allTransactions, tx)
	}

	log.Printf("✅ Prepared %d transactions for response", len(allTransactions))

	response := Response{
		Message: "All transactions retrieved",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data:    allTransactions,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("❌ Error encoding response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	log.Printf("📤 Successfully sent transaction response")
}

func approveTransactionHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	txID := vars["id"]

	tx := transactions[txID]
	if tx == nil {
		http.Error(w, "Transaction not found", http.StatusNotFound)
		return
	}

	if tx.Status != "pending" {
		http.Error(w, "Transaction already processed", http.StatusBadRequest)
		return
	}

	// Approve transaction
	tx.Status = "approved"
	now := time.Now()
	tx.ProcessedAt = &now
	tx.ProcessedBy = "admin" // In real app, get from auth token

	// Add credits to user
	user := getUserByDeviceID(tx.DeviceID)
	if user != nil {
		usersMutex.Lock()
		user.Credits += tx.Credits
		usersMutex.Unlock()

		// Save changes to database
		if err := saveUserToDB(user); err != nil {
			log.Printf("Error updating user in database: %v", err)
		}
		if err := saveTransactionToDB(tx); err != nil {
			log.Printf("Error updating transaction in database: %v", err)
		}

		log.Printf("Credits added for %s: +%d credits, total: %d", tx.DeviceID, tx.Credits, user.Credits)

		// Broadcast SSE update for credit addition
		creditEvent := SSEEvent{
			Type: "credit_added",
			Data: map[string]interface{}{
				"free_photos":    user.FreePhotos,
				"credits":        user.Credits,
				"total_photos":   user.TotalPhotos,
				"credits_added":  tx.Credits,
				"transaction_id": tx.ID,
			},
		}
		broadcastToDevice(tx.DeviceID, creditEvent)
	}

	response := Response{
		Message: fmt.Sprintf("Transaction approved. %d credits added to device %s", tx.Credits, tx.DeviceID),
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data:    tx,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func rejectTransactionHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	txID := vars["id"]

	tx := transactions[txID]
	if tx == nil {
		http.Error(w, "Transaction not found", http.StatusNotFound)
		return
	}

	if tx.Status != "pending" {
		http.Error(w, "Transaction already processed", http.StatusBadRequest)
		return
	}

	// Reject transaction
	tx.Status = "rejected"
	now := time.Now()
	tx.ProcessedAt = &now
	tx.ProcessedBy = "admin" // In real app, get from auth token

	response := Response{
		Message: fmt.Sprintf("Transaction rejected for device %s", tx.DeviceID),
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data:    tx,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func getAllUsersHandler(w http.ResponseWriter, r *http.Request) {
	var allUsers []*User
	for _, user := range users {
		allUsers = append(allUsers, user)
	}

	response := Response{
		Message: "All users retrieved",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data:    allUsers,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Image Processing Handlers (Enhanced with credit system)
func getImagesHandler(w http.ResponseWriter, r *http.Request) {
	deviceID := r.URL.Query().Get("device_id")

	var userImages []*ImageData
	if deviceID != "" {
		for _, img := range images {
			if img.DeviceID == deviceID {
				userImages = append(userImages, img)
			}
		}
	} else {
		for _, img := range images {
			userImages = append(userImages, img)
		}
	}

	response := Response{
		Message: "Images retrieved successfully",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data:    userImages,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func uploadImageHandler(w http.ResponseWriter, r *http.Request) {
	deviceID := r.FormValue("device_id")
	if deviceID == "" {
		http.Error(w, "Device ID is required", http.StatusBadRequest)
		return
	}

	// Get image count for batch processing (default to 1 if not provided)
	imageCountStr := r.FormValue("image_count")
	imageCount := 1
	if imageCountStr != "" {
		if count, err := strconv.Atoi(imageCountStr); err == nil && count > 0 {
			imageCount = count
		}
	}

	user := getUserByDeviceID(deviceID)
	if user == nil {
		http.Error(w, "Device not registered", http.StatusNotFound)
		return
	}

	// Check if user can process images (considering batch count)
	canProcess := false
	creditsPerPhoto := getCurrentCreditsPerPhoto()
	totalCreditsNeeded := creditsPerPhoto * imageCount

	if user.FreePhotos >= imageCount {
		canProcess = true
	} else if user.Credits >= totalCreditsNeeded {
		canProcess = true
	} else if user.FreePhotos > 0 && (user.FreePhotos+user.Credits) >= totalCreditsNeeded {
		// Mixed: some free photos + some credits
		canProcess = true
	}

	if !canProcess {
		response := Response{
			Message: fmt.Sprintf("Not enough credits for %d images. Need %d credits but have %d free photos and %d credits.",
				imageCount, totalCreditsNeeded, user.FreePhotos, user.Credits),
			Status: "error",
			Time:   time.Now().Format(time.RFC3339),
			Data: map[string]interface{}{
				"free_photos":          user.FreePhotos,
				"credits":              user.Credits,
				"images_requested":     imageCount,
				"credits_per_photo":    creditsPerPhoto,
				"total_credits_needed": totalCreditsNeeded,
				"need_payment":         true,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		json.NewEncoder(w).Encode(response)
		return
	}

	// Parse multipart form
	err := r.ParseMultipartForm(10 << 20) // 10 MB limit
	if err != nil {
		http.Error(w, "Unable to parse form", http.StatusBadRequest)
		return
	}

	// Create image record
	image := &ImageData{
		ID:        generateID(),
		DeviceID:  deviceID,
		Name:      fmt.Sprintf("image_%s.jpg", generateID()),
		URL:       fmt.Sprintf("/uploads/%s", generateID()),
		Labels:    []string{},
		CreatedAt: time.Now(),
	}

	images[image.ID] = image

	// Deduct credits for batch processing
	usersMutex.Lock()
	freePhotosUsed := 0
	creditsUsed := 0

	if user.FreePhotos >= imageCount {
		// Use only free photos
		user.FreePhotos -= imageCount
		freePhotosUsed = imageCount
	} else {
		// Mixed or credits only
		freePhotosUsed = user.FreePhotos
		creditsNeeded := (imageCount - user.FreePhotos) * creditsPerPhoto
		user.FreePhotos = 0
		user.Credits -= creditsNeeded
		creditsUsed = creditsNeeded
	}

	user.TotalPhotos += imageCount
	user.LastActiveAt = time.Now()
	usersMutex.Unlock()

	// Save changes to database
	if err := saveImageToDB(image); err != nil {
		log.Printf("Error saving image to database: %v", err)
	}
	if err := saveUserToDB(user); err != nil {
		log.Printf("Error updating user in database: %v", err)
	}

	// Log the batch processing
	if creditsUsed > 0 && freePhotosUsed > 0 {
		log.Printf("Batch processing for %s: %d images processed using %d free photos + %d credits, %d free photos and %d credits remaining",
			deviceID, imageCount, freePhotosUsed, creditsUsed, user.FreePhotos, user.Credits)
	} else if creditsUsed > 0 {
		log.Printf("Batch processing for %s: %d images processed using %d credits, %d credits remaining",
			deviceID, imageCount, creditsUsed, user.Credits)
	} else {
		log.Printf("Batch processing for %s: %d images processed using %d free photos, %d free photos remaining",
			deviceID, imageCount, freePhotosUsed, user.FreePhotos)
	}

	// Broadcast SSE update for credit changes
	creditEvent := SSEEvent{
		Type: "credit_update",
		Data: map[string]interface{}{
			"free_photos":       user.FreePhotos,
			"credits":           user.Credits,
			"total_photos":      user.TotalPhotos,
			"images_processed":  imageCount,
			"credits_per_photo": creditsPerPhoto,
			"free_photos_used":  freePhotosUsed,
			"credits_used":      creditsUsed,
		},
	}
	broadcastToDevice(deviceID, creditEvent)

	response := Response{
		Message: fmt.Sprintf("Batch processing successful: %d images processed", imageCount),
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data: map[string]interface{}{
			"image":             image,
			"images_processed":  imageCount,
			"used_credit":       creditsUsed > 0, // Boolean for Android app compatibility
			"free_photos_used":  freePhotosUsed,
			"credits_used":      creditsUsed,
			"credits_per_photo": creditsPerPhoto,
			"remaining": map[string]interface{}{
				"free_photos": user.FreePhotos,
				"credits":     user.Credits,
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func addLabelHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	imageID := vars["id"]

	image := images[imageID]
	if image == nil {
		http.Error(w, "Image not found", http.StatusNotFound)
		return
	}

	var req struct {
		Label string `json:"label"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Label != "" {
		image.Labels = append(image.Labels, req.Label)
	}

	response := Response{
		Message: fmt.Sprintf("Label added to image %s", imageID),
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data:    image,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// SSE Handler for real-time updates
func sseHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	deviceID := vars["device_id"]

	if deviceID == "" {
		http.Error(w, "Device ID is required", http.StatusBadRequest)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Cache-Control")

	// Create SSE client
	client := &SSEClient{
		DeviceID: deviceID,
		Channel:  make(chan []byte, 100),
		Closed:   false,
	}

	// Register client
	sseClientsMutex.Lock()
	sseClients[deviceID] = client
	sseClientsMutex.Unlock()

	// Send initial user status
	user := getUserByDeviceID(deviceID)
	if user != nil {
		initialEvent := SSEEvent{
			Type: "user_status",
			Data: map[string]interface{}{
				"free_photos":  user.FreePhotos,
				"credits":      user.Credits,
				"total_photos": user.TotalPhotos,
			},
		}
		eventData, _ := json.Marshal(initialEvent)
		fmt.Fprintf(w, "data: %s\n\n", eventData)
		w.(http.Flusher).Flush()
	}

	// Handle connection
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			// Client disconnected
			client.Closed = true
			sseClientsMutex.Lock()
			delete(sseClients, deviceID)
			sseClientsMutex.Unlock()
			return
		case data := <-client.Channel:
			// Send event to client
			fmt.Fprintf(w, "data: %s\n\n", data)
			w.(http.Flusher).Flush()
		case <-time.After(30 * time.Second):
			// Send keepalive ping
			fmt.Fprintf(w, ": keepalive\n\n")
			w.(http.Flusher).Flush()
		}
	}
}

// ============================================
// ADMIN PAYMENT ACCOUNT MANAGEMENT HANDLERS
// ============================================

// Admin: Get all payment accounts (including inactive)
func adminGetPaymentAccountsHandler(w http.ResponseWriter, r *http.Request) {
	paymentAccountsMutex.RLock()
	defer paymentAccountsMutex.RUnlock()

	log.Printf("🔍 adminGetPaymentAccountsHandler called - processing %d accounts", len(paymentAccounts))

	// Convert map to slice for easy JSON response
	var accounts []PaymentAccount
	for id, account := range paymentAccounts {
		log.Printf("Account %s: Method=%s, Name=%s, Number=%s", id, account.Method, account.AccountName, account.AccountNumber)
		accounts = append(accounts, *account)
	}

	response := Response{
		Message: "Admin payment accounts retrieved",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data: map[string]interface{}{
			"accounts": accounts,
			"total":    len(accounts),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Admin: Add new payment account
func adminAddPaymentAccountHandler(w http.ResponseWriter, r *http.Request) {
	var req AdminAddPaymentAccountRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validation
	if req.Method == "" || req.AccountName == "" || req.AccountNumber == "" {
		http.Error(w, "Method, AccountName, and AccountNumber are required", http.StatusBadRequest)
		return
	}

	// Generate unique ID
	accountID := fmt.Sprintf("acc_%d", time.Now().UnixNano())

	// Create new payment account
	newAccount := &PaymentAccount{
		ID:            accountID,
		Method:        req.Method,
		AccountName:   req.AccountName,
		AccountNumber: req.AccountNumber,
		QRCode:        req.QRCode,
		IsActive:      true, // Default to active
		CreatedAt:     time.Now().Format(time.RFC3339),
	}

	// Add to storage
	paymentAccountsMutex.Lock()
	paymentAccounts[accountID] = newAccount
	paymentAccountsMutex.Unlock()

	// Save to database
	if err := savePaymentAccountToDB(newAccount); err != nil {
		log.Printf("Error saving payment account to database: %v", err)
	}

	log.Printf("✅ Admin added payment account: %s (%s - %s)", req.Method, req.AccountName, req.AccountNumber)

	response := Response{
		Message: "Payment account added successfully",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data: map[string]interface{}{
			"account": newAccount,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Admin: Update payment account
func adminUpdatePaymentAccountHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	accountID := vars["id"]

	var req AdminAddPaymentAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	paymentAccountsMutex.Lock()
	defer paymentAccountsMutex.Unlock()

	account, exists := paymentAccounts[accountID]
	if !exists {
		http.Error(w, "Payment account not found", http.StatusNotFound)
		return
	}

	// Update fields
	if req.Method != "" {
		account.Method = req.Method
	}
	if req.AccountName != "" {
		account.AccountName = req.AccountName
	}
	if req.AccountNumber != "" {
		account.AccountNumber = req.AccountNumber
	}
	if req.QRCode != "" {
		account.QRCode = req.QRCode
	}

	// Save to database
	if err := savePaymentAccountToDB(account); err != nil {
		log.Printf("Error updating payment account in database: %v", err)
	}

	log.Printf("🔄 Admin updated payment account: %s", accountID)

	response := Response{
		Message: "Payment account updated successfully",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data: map[string]interface{}{
			"account": account,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Admin: Delete payment account
func adminDeletePaymentAccountHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	accountID := vars["id"]

	paymentAccountsMutex.Lock()
	defer paymentAccountsMutex.Unlock()

	_, exists := paymentAccounts[accountID]
	if !exists {
		http.Error(w, "Payment account not found", http.StatusNotFound)
		return
	}

	delete(paymentAccounts, accountID)

	// Save to database
	if err := deletePaymentAccountFromDB(accountID); err != nil {
		log.Printf("Error deleting payment account from database: %v", err)
	}

	log.Printf("🗑️ Admin deleted payment account: %s", accountID)

	response := Response{
		Message: "Payment account deleted successfully",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data: map[string]interface{}{
			"deleted_id": accountID,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Admin: Toggle payment account active status
func adminTogglePaymentAccountHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	accountID := vars["id"]

	paymentAccountsMutex.Lock()
	defer paymentAccountsMutex.Unlock()

	account, exists := paymentAccounts[accountID]
	if !exists {
		http.Error(w, "Payment account not found", http.StatusNotFound)
		return
	}

	// Toggle active status
	account.IsActive = !account.IsActive

	// Save to database
	if err := savePaymentAccountToDB(account); err != nil {
		log.Printf("Error updating payment account in database: %v", err)
	}

	status := "activated"
	if !account.IsActive {
		status = "deactivated"
	}

	log.Printf("🔄 Admin %s payment account: %s", status, accountID)

	response := Response{
		Message: fmt.Sprintf("Payment account %s successfully", status),
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data: map[string]interface{}{
			"account": account,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// ============================================
// ADMIN SYSTEM SETTINGS MANAGEMENT HANDLERS
// ============================================

// Admin: Get system settings
func adminGetSettingsHandler(w http.ResponseWriter, r *http.Request) {
	settingsMutex.RLock()
	defer settingsMutex.RUnlock()

	response := Response{
		Message: "System settings retrieved",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data: map[string]interface{}{
			"settings": systemSettings,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Admin: Update system settings
func adminUpdateSettingsHandler(w http.ResponseWriter, r *http.Request) {
	var req AdminUpdateSettingsRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validation
	if req.CreditsPerPhoto <= 0 || req.CreditPriceMMK <= 0 || req.FreePhotosLimit < 0 || req.MinimumPurchase <= 0 {
		http.Error(w, "All values must be positive numbers (free photos limit can be 0)", http.StatusBadRequest)
		return
	}

	settingsMutex.Lock()
	defer settingsMutex.Unlock()

	// Update settings
	systemSettings.CreditsPerPhoto = req.CreditsPerPhoto
	systemSettings.CreditPriceMMK = req.CreditPriceMMK
	systemSettings.FreePhotosLimit = req.FreePhotosLimit
	systemSettings.MinimumPurchase = req.MinimumPurchase
	systemSettings.UpdatedAt = time.Now().Format(time.RFC3339)

	// Save to database
	if err := saveSettingsToDB(systemSettings); err != nil {
		log.Printf("Error saving settings to database: %v", err)
		http.Error(w, "Failed to save settings", http.StatusInternalServerError)
		return
	}

	log.Printf("🔧 Admin updated system settings: %d credits per photo, %d MMK per credit, %d free photos",
		req.CreditsPerPhoto, req.CreditPriceMMK, req.FreePhotosLimit)

	response := Response{
		Message: "System settings updated successfully",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data: map[string]interface{}{
			"settings":        systemSettings,
			"photo_price_mmk": systemSettings.CreditPriceMMK * systemSettings.CreditsPerPhoto,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Admin Credit Package Management Handlers
// Public credit packages handler for mobile app
func getCreditPackagesHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("🔍 getCreditPackagesHandler called (public)")
	creditPackagesMutex.RLock()
	defer creditPackagesMutex.RUnlock()

	var packages []*CreditPackage
	for _, pkg := range creditPackages {
		// Only return active packages for public API
		if pkg.IsActive {
			packages = append(packages, pkg)
		}
	}

	// Ensure we return an empty array instead of null
	if packages == nil {
		packages = []*CreditPackage{}
	}

	log.Printf("✅ Returning %d active credit packages for public API", len(packages))

	response := Response{
		Message: "Credit packages retrieved",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data:    packages,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func adminGetCreditPackagesHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("🔍 adminGetCreditPackagesHandler called")
	creditPackagesMutex.RLock()
	defer creditPackagesMutex.RUnlock()

	var packages []*CreditPackage
	for _, pkg := range creditPackages {
		packages = append(packages, pkg)
	}

	// Ensure we return an empty array instead of null
	if packages == nil {
		packages = []*CreditPackage{}
	}

	log.Printf("✅ Returning %d credit packages", len(packages))

	response := Response{
		Message: "Credit packages retrieved",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data:    packages,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func adminAddCreditPackageHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("🔍 adminAddCreditPackageHandler called")

	var req AdminCreditPackageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("❌ Error decoding request body: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	log.Printf("✅ Received package request: %+v", req)

	// Validation
	if req.Name == "" || req.Credits <= 0 || req.PriceMMK <= 0 {
		http.Error(w, "Name, credits and price are required and must be positive", http.StatusBadRequest)
		return
	}

	packageID := generateID()
	newPackage := &CreditPackage{
		ID:        packageID,
		Name:      req.Name,
		Credits:   req.Credits,
		PriceMMK:  req.PriceMMK,
		IsActive:  req.IsActive,
		IsDefault: req.IsDefault,
		CreatedAt: time.Now().Format(time.RFC3339),
	}

	creditPackagesMutex.Lock()
	creditPackages[packageID] = newPackage
	creditPackagesMutex.Unlock()

	// Save to database
	if err := saveCreditPackageToDB(newPackage); err != nil {
		log.Printf("Error saving credit package to database: %v", err)
	}

	response := Response{
		Message: "Credit package added successfully",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data:    newPackage,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func adminUpdateCreditPackageHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	packageID := vars["id"]

	var req AdminCreditPackageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	creditPackagesMutex.Lock()
	defer creditPackagesMutex.Unlock()

	pkg := creditPackages[packageID]
	if pkg == nil {
		http.Error(w, "Credit package not found", http.StatusNotFound)
		return
	}

	// Update fields
	if req.Name != "" {
		pkg.Name = req.Name
	}
	if req.Credits > 0 {
		pkg.Credits = req.Credits
	}
	if req.PriceMMK > 0 {
		pkg.PriceMMK = req.PriceMMK
	}
	pkg.IsActive = req.IsActive
	pkg.IsDefault = req.IsDefault

	// Save to database
	if err := saveCreditPackageToDB(pkg); err != nil {
		log.Printf("Error saving credit package to database: %v", err)
	}

	response := Response{
		Message: "Credit package updated successfully",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data:    pkg,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func adminDeleteCreditPackageHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	packageID := vars["id"]

	creditPackagesMutex.Lock()
	defer creditPackagesMutex.Unlock()

	if creditPackages[packageID] == nil {
		http.Error(w, "Credit package not found", http.StatusNotFound)
		return
	}

	delete(creditPackages, packageID)

	// Delete from database
	if err := deleteCreditPackageFromDB(packageID); err != nil {
		log.Printf("Error deleting credit package from database: %v", err)
	}

	response := Response{
		Message: "Credit package deleted successfully",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func adminToggleCreditPackageHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	packageID := vars["id"]

	creditPackagesMutex.Lock()
	defer creditPackagesMutex.Unlock()

	pkg := creditPackages[packageID]
	if pkg == nil {
		http.Error(w, "Credit package not found", http.StatusNotFound)
		return
	}

	pkg.IsActive = !pkg.IsActive

	// Save to database
	if err := saveCreditPackageToDB(pkg); err != nil {
		log.Printf("Error saving credit package to database: %v", err)
	}

	response := Response{
		Message: fmt.Sprintf("Credit package %s", map[bool]string{true: "activated", false: "deactivated"}[pkg.IsActive]),
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data:    pkg,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
