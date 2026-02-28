package main

import (
	"context"
	"database/sql"
	"fmt"
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

func (m *MinioURLSigner) GeneratePresignedGET(bucket, key string, expiry time.Duration) (string, error) {
	presignedURL, err := m.Client.PresignedGetObject(context.Background(), bucket, key, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("presigned GET URL generation failed: %w", err)
	}
	return presignedURL.String(), nil
}

// PostgresFileStore queries media_files table in Supabase for ownership and soft-delete.
type PostgresFileStore struct {
	DB *sql.DB
}

func (p *PostgresFileStore) VerifyOwnership(ctx context.Context, filePath, userID string) (bool, error) {
	var count int
	err := p.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM media_files WHERE file_path = $1 AND user_id = $2 AND status = 'active'",
		filePath, userID,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (p *PostgresFileStore) SoftDelete(ctx context.Context, filePath, userID string) (bool, error) {
	result, err := p.DB.ExecContext(ctx, //nolint:gosec // parameterized query, not SQL injection
		"UPDATE media_files SET status = 'deleted' WHERE file_path = $1 AND user_id = $2 AND status = 'active'",
		filePath, userID,
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (p *PostgresFileStore) GetFileMetadata(ctx context.Context, filePath, userID string) (*FileMetadata, error) {
	var meta FileMetadata
	err := p.DB.QueryRowContext(ctx, //nolint:gosec // parameterized query, not SQL injection
		"SELECT file_path, file_name, media_type, file_size, user_id FROM media_files WHERE file_path = $1 AND user_id = $2 AND status = 'active'",
		filePath, userID,
	).Scan(&meta.FilePath, &meta.FileName, &meta.MediaType, &meta.FileSize, &meta.UserID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &meta, nil
}

func getEnvOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// MediaHandlers holds all media-related HTTP handlers sharing common dependencies.
type MediaHandlers struct {
	Upload   *UploadHandler
	Download *DownloadHandler
	Delete   *DeleteHandler
}

// initMediaHandlers creates and returns all media handlers sharing a single DB pool and MinIO client.
// Returns nil if required dependencies can't be reached (non-fatal — gateway still serves other routes).
func initMediaHandlers(produceEvent EventProducer) *MediaHandlers {
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

	// Connect to Postgres for credit balance and file metadata queries
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to open DB for media handlers")
		return nil
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		log.Warn().Err(err).Msg("DB ping failed for media handlers")
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
		log.Warn().Err(err).Msg("Failed to create MinIO client")
		return nil
	}

	config := &UploadConfig{
		StorageBucketName: storageBucket,
		StorageEndpoint:   storageEndpoint,
		StorageAccessKey:  storageAccessKey,
		StorageSecretKey:  storageSecretKey,
		StorageURLExpiry:  900,
		SupabaseDBURL:     dbURL,
	}

	signer := &MinioURLSigner{Client: minioClient}
	fileStore := &PostgresFileStore{DB: db}

	log.Info().
		Str("bucket", storageBucket).
		Str("endpoint", storageEndpoint).
		Str("public_endpoint", storagePublicEndpoint).
		Msg("Media handlers initialized")

	return &MediaHandlers{
		Upload: &UploadHandler{
			Credits: &PostgresCreditChecker{DB: db},
			Signer:  signer,
			Config:  config,
		},
		Download: &DownloadHandler{
			Files:        fileStore,
			Signer:       signer,
			Config:       config,
			ProduceEvent: produceEvent,
		},
		Delete: &DeleteHandler{
			Files:        fileStore,
			Config:       config,
			ProduceEvent: produceEvent,
		},
	}
}
