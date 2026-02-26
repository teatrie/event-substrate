package main

import (
	"context"
	"time"
)

// DownloadURLSigner generates presigned GET URLs for object storage.
type DownloadURLSigner interface {
	PresignedGetURL(ctx context.Context, bucket, objectKey string, expiry time.Duration) (string, error)
}

// FileMetadata holds metadata about a stored file, retrieved alongside ownership verification.
type FileMetadata struct {
	FileName  string
	MediaType string
	FileSize  int64
}

// FileMetadataLookup retrieves file metadata while verifying ownership.
// Returns nil metadata (and nil error) when the file is not found or not owned by userID.
// Returns a non-nil error only for internal failures (DB connectivity, etc).
type FileMetadataLookup interface {
	GetFileMetadata(ctx context.Context, userID, filePath string) (*FileMetadata, error)
}

// ObjectRemover deletes an object from object storage.
// Used by both DeleteHandler and ExpiredCleanupHandler.
type ObjectRemover interface {
	RemoveObject(ctx context.Context, bucket, objectKey string) error
}

// FileDeleter soft-deletes a file record in the database.
type FileDeleter interface {
	SoftDelete(ctx context.Context, userID, filePath string) error
}
