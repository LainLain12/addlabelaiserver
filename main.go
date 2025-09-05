package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/cors"
)

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
	Method        string `json:"method"`
	AccountName   string `json:"account_name"`
	AccountNumber string `json:"account_number"`
	QRCode        string `json:"qr_code,omitempty"`
}

// In-memory storage (replace with database in production)
var users = make(map[string]*User)
var transactions = make(map[string]*PaymentTransaction)
var images = make(map[string]*ImageData)

// Constants
const (
	FREE_PHOTOS_LIMIT = 30
	CREDIT_PRICE_MMK  = 10 // 10 MMK per photo
)

// Serve admin dashboard
func serveAdminHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "admin.html")
}

func main() {
	r := mux.NewRouter()

	// Serve admin dashboard
	r.HandleFunc("/admin.html", serveAdminHandler).Methods("GET")
	r.HandleFunc("/admin", serveAdminHandler).Methods("GET")

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

	// Admin endpoints
	admin := api.PathPrefix("/admin").Subrouter()
	admin.HandleFunc("/transactions", getAllTransactionsHandler).Methods("GET")
	admin.HandleFunc("/transactions/{id}/approve", approveTransactionHandler).Methods("POST")
	admin.HandleFunc("/transactions/{id}/reject", rejectTransactionHandler).Methods("POST")
	admin.HandleFunc("/users", getAllUsersHandler).Methods("GET")

	// Image processing endpoints
	api.HandleFunc("/images", getImagesHandler).Methods("GET")
	api.HandleFunc("/images/upload", uploadImageHandler).Methods("POST")
	api.HandleFunc("/images/{id}/labels", addLabelHandler).Methods("POST")

	// CORS middleware
	c := cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"*"},
	})

	handler := c.Handler(r)

	fmt.Println("🚀 Go subscription server starting on port 8080...")
	fmt.Println("🌐 Admin Dashboard: http://localhost:8080/admin.html")
	fmt.Println("📋 Available endpoints:")
	fmt.Println("   === Admin Dashboard ===")
	fmt.Println("   GET  /admin.html")
	fmt.Println("   GET  /admin")
	fmt.Println("   === User Management ===")
	fmt.Println("   POST /api/user/register")
	fmt.Println("   GET  /api/user/{device_id}/status")
	fmt.Println("   === Payment System ===")
	fmt.Println("   GET  /api/payment/accounts")
	fmt.Println("   POST /api/payment/request")
	fmt.Println("   GET  /api/payment/history/{device_id}")
	fmt.Println("   === Admin Panel ===")
	fmt.Println("   GET  /api/admin/transactions")
	fmt.Println("   POST /api/admin/transactions/{id}/approve")
	fmt.Println("   POST /api/admin/transactions/{id}/reject")
	fmt.Println("   GET  /api/admin/users")
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
	return users[deviceID]
}

func createUser(deviceID string) *User {
	user := &User{
		ID:           generateID(),
		DeviceID:     deviceID,
		Credits:      0,
		FreePhotos:   FREE_PHOTOS_LIMIT,
		TotalPhotos:  0,
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
	}
	users[deviceID] = user
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
	if user == nil {
		user = createUser(req.DeviceID)
	} else {
		user.LastActiveAt = time.Now()
	}

	response := Response{
		Message: "Device registered successfully",
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
	accounts := []PaymentAccount{
		{
			Method:        "wavepay",
			AccountName:   "ImageLabel Service",
			AccountNumber: "09123456789",
			QRCode:        "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==",
		},
		{
			Method:        "kbzpay",
			AccountName:   "ImageLabel Service",
			AccountNumber: "09987654321",
			QRCode:        "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==",
		},
	}

	response := Response{
		Message: "Payment accounts retrieved",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data: map[string]interface{}{
			"accounts":         accounts,
			"credit_price":     CREDIT_PRICE_MMK,
			"minimum_purchase": 10, // minimum 10 photos
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

	if req.PaymentMethod != "wavepay" && req.PaymentMethod != "kbzpay" {
		http.Error(w, "Invalid payment method", http.StatusBadRequest)
		return
	}

	// Verify amount calculation
	expectedAmount := req.Credits * CREDIT_PRICE_MMK
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
	var allTransactions []*PaymentTransaction
	for _, tx := range transactions {
		allTransactions = append(allTransactions, tx)
	}

	response := Response{
		Message: "All transactions retrieved",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data:    allTransactions,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
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
		user.Credits += tx.Credits
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

	user := getUserByDeviceID(deviceID)
	if user == nil {
		http.Error(w, "Device not registered", http.StatusNotFound)
		return
	}

	// Check if user can process image
	canProcess := false
	useCredit := false

	if user.FreePhotos > 0 {
		canProcess = true
		useCredit = false
	} else if user.Credits > 0 {
		canProcess = true
		useCredit = true
	}

	if !canProcess {
		response := Response{
			Message: "No free photos or credits available",
			Status:  "error",
			Time:    time.Now().Format(time.RFC3339),
			Data: map[string]interface{}{
				"free_photos":  user.FreePhotos,
				"credits":      user.Credits,
				"need_payment": true,
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

	// Deduct free photo or credit
	if useCredit {
		user.Credits--
	} else {
		user.FreePhotos--
	}
	user.TotalPhotos++
	user.LastActiveAt = time.Now()

	response := Response{
		Message: "Image uploaded and processed successfully",
		Status:  "success",
		Time:    time.Now().Format(time.RFC3339),
		Data: map[string]interface{}{
			"image":       image,
			"used_credit": useCredit,
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
