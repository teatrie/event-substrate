package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// CreditChecker abstracts the database query for user credit balances.
type CreditChecker interface {
	GetBalance(ctx context.Context, userID string) (int, error)
}

// URLSigner abstracts presigned URL generation for object storage.
type URLSigner interface {
	GeneratePresignedPUT(bucket, key string, expiry time.Duration) (string, error)
}

// UploadConfig holds configuration for the upload handler.
type UploadConfig struct {
	StorageBucketName string
	StorageEndpoint   string
	StorageAccessKey  string
	StorageSecretKey  string
	StorageURLExpiry  int
	SupabaseDBURL     string
}

// UploadRequest represents the incoming JSON body for upload URL requests.
type UploadRequest struct {
	FileName  string `json:"file_name"`
	MediaType string `json:"media_type"`
	FileSize  int64  `json:"file_size"`
}

// UploadResponse represents the JSON response for a successful upload URL request.
type UploadResponse struct {
	UploadURL string `json:"upload_url"`
	FilePath  string `json:"file_path"`
	ExpiresIn int    `json:"expires_in"`
}

// UploadHandler handles presigned upload URL generation with credit checking.
type UploadHandler struct {
	Credits CreditChecker
	Signer  URLSigner
	Config  *UploadConfig
}

var allowedMediaTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
	"video/mp4":  true,
	"video/webm": true,
	"audio/mpeg": true,
	"audio/wav":  true,
	"audio/ogg":  true,
}

// writeInsufficientCredits writes a structured 402 JSON error response.
func writeInsufficientCredits(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPaymentRequired)
	json.NewEncoder(w).Encode(map[string]string{"error": "insufficient_credits"})
}

// ServeHTTP handles the POST /api/v1/media/upload-url endpoint.
func (h *UploadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Authenticate via JWT Bearer token
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	token, err := jwt.Parse(strings.TrimPrefix(authHeader, "Bearer "), jwtKeyFunc)
	if err != nil || !token.Valid {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	userID, ok := claims["sub"].(string)
	if !ok || userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse and validate request body
	var req UploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	if req.FileName == "" || req.MediaType == "" || req.FileSize <= 0 {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	if !allowedMediaTypes[req.MediaType] {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Verify user has available credits
	balance, err := h.Credits.GetBalance(r.Context(), userID)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			writeInsufficientCredits(w)
			return
		}
		log.Printf("Upload handler: credit check failed for user %s: %v", userID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if balance <= 0 {
		writeInsufficientCredits(w)
		return
	}

	// Generate presigned upload URL
	key := fmt.Sprintf("uploads/%s/%s/%s", userID, uuid.New().String(), req.FileName)
	expiry := time.Duration(h.Config.StorageURLExpiry) * time.Second
	uploadURL, err := h.Signer.GeneratePresignedPUT(h.Config.StorageBucketName, key, expiry)
	if err != nil {
		log.Printf("Upload handler: presigned URL generation failed: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(UploadResponse{
		UploadURL: uploadURL,
		FilePath:  key,
		ExpiresIn: h.Config.StorageURLExpiry,
	})
}
