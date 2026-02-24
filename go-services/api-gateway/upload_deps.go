package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/lib/pq"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// PostgresCreditChecker queries user_credit_balances view in Supabase.
type PostgresCreditChecker struct {
	DB *sql.DB
}

func (p *PostgresCreditChecker) GetBalance(ctx context.Context, userID string) (int, error) {
	var balance int
	err := p.DB.QueryRowContext(ctx,
		"SELECT balance FROM user_credit_balances WHERE user_id = $1", userID,
	).Scan(&balance)
	if err != nil {
		return 0, err
	}
	return balance, nil
}

// MinioURLSigner generates presigned PUT URLs for MinIO/S3-compatible storage.
type MinioURLSigner struct {
	Client *minio.Client
}

func (m *MinioURLSigner) GeneratePresignedPUT(bucket, key string, expiry time.Duration) (string, error) {
	presignedURL, err := m.Client.PresignedPutObject(context.Background(), bucket, key, expiry)
	if err != nil {
		return "", fmt.Errorf("presigned URL generation failed: %w", err)
	}
	return presignedURL.String(), nil
}

func getEnvOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// initUploadHandler creates and returns a fully wired UploadHandler.
// Returns nil if required dependencies can't be reached (non-fatal — gateway still serves other routes).
func initUploadHandler() *UploadHandler {
	dbURL := getEnvOrDefault("SUPABASE_DB_URL", "postgres://postgres:postgres@host.docker.internal:54322/postgres?sslmode=disable")
	storageEndpoint := getEnvOrDefault("MINIO_ENDPOINT", "host.docker.internal:9000")
	// MINIO_PUBLIC_ENDPOINT is the browser-reachable MinIO address used for presigned URLs.
	// S3v4 signatures include the Host, so the URL must be reachable by the browser.
	// Defaults to localhost:9000 (Docker port-forwarded) for local dev.
	storagePublicEndpoint := getEnvOrDefault("MINIO_PUBLIC_ENDPOINT", "localhost:9000")
	storageAccessKey := getEnvOrDefault("MINIO_ACCESS_KEY", "admin")
	storageSecretKey := getEnvOrDefault("MINIO_SECRET_KEY", "password")
	storageBucket := getEnvOrDefault("MINIO_BUCKET", "media-uploads")
	storageUseSSL := getEnvOrDefault("MINIO_USE_SSL", "false") == "true"

	// Connect to Postgres for credit balance queries
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Printf("WARNING: Failed to open DB for upload handler: %v", err)
		return nil
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		log.Printf("WARNING: DB ping failed for upload handler: %v", err)
		return nil
	}

	// Connect to MinIO for presigned URL generation.
	// Use the public endpoint so presigned URLs are reachable by browsers.
	// Region must be set to skip the getBucketLocation network call — the public
	// endpoint (localhost:9000) isn't reachable from inside the K8s pod, but
	// presigned URL generation is purely local when the region is known.
	minioClient, err := minio.New(storagePublicEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(storageAccessKey, storageSecretKey, ""),
		Secure: storageUseSSL,
		Region: "us-east-1",
	})
	if err != nil {
		log.Printf("WARNING: Failed to create MinIO client: %v", err)
		return nil
	}

	log.Printf("Upload handler initialized: bucket=%s endpoint=%s (public=%s)", storageBucket, storageEndpoint, storagePublicEndpoint)

	return &UploadHandler{
		Credits: &PostgresCreditChecker{DB: db},
		Signer:  &MinioURLSigner{Client: minioClient},
		Config: &UploadConfig{
			StorageBucketName: storageBucket,
			StorageEndpoint:   storageEndpoint,
			StorageAccessKey:  storageAccessKey,
			StorageSecretKey:  storageSecretKey,
			StorageURLExpiry:  900,
			SupabaseDBURL:     dbURL,
		},
	}
}
