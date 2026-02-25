package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// authenticateRequest extracts and validates a JWT Bearer token from the
// Authorization header. On success it returns the user ID from the "sub" claim.
// On failure it writes an HTTP 401 response and returns an empty string.
func authenticateRequest(w http.ResponseWriter, r *http.Request) (userID string, ok bool) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return "", false
	}

	token, err := jwt.Parse(strings.TrimPrefix(authHeader, "Bearer "), jwtKeyFunc)
	if err != nil || !token.Valid {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return "", false
	}

	claims, claimsOK := token.Claims.(jwt.MapClaims)
	if !claimsOK {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return "", false
	}

	sub, subOK := claims["sub"].(string)
	if !subOK || sub == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return "", false
	}

	return sub, true
}

// FileMetadata holds metadata about a file in object storage.
type FileMetadata struct {
	FilePath  string
	FileName  string
	MediaType string
	FileSize  int64
	UserID    string
}

// FileStore abstracts database queries for file ownership and soft-delete operations.
type FileStore interface {
	VerifyOwnership(ctx context.Context, filePath, userID string) (bool, error)
	SoftDelete(ctx context.Context, filePath, userID string) (bool, error)
	GetFileMetadata(ctx context.Context, filePath, userID string) (*FileMetadata, error)
}

// FullURLSigner extends URLSigner with presigned GET URL generation for downloads.
type FullURLSigner interface {
	URLSigner
	GeneratePresignedGET(bucket, key string, expiry time.Duration) (string, error)
}

// EventProducer is a function that produces an event to a Kafka topic.
type EventProducer func(ctx context.Context, topic string, payload map[string]any) error
