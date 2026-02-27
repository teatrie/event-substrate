package main

import (
	"context"
)

// ExpiredCleanupHandler handles FileUploadExpired events by removing the
// partially-uploaded object from MinIO. This is best-effort: errors are logged
// but not returned, as the 24h MinIO lifecycle policy is the backstop.
type ExpiredCleanupHandler struct {
	remover ObjectRemover
	bucket  string
}

// NewExpiredCleanupHandler constructs an ExpiredCleanupHandler.
func NewExpiredCleanupHandler(remover ObjectRemover, bucket string) *ExpiredCleanupHandler {
	return &ExpiredCleanupHandler{
		remover: remover,
		bucket:  bucket,
	}
}

// Handle processes a FileUploadExpired payload. Silently skips if file_path is
// missing, empty, or not a string. Logs but does not return errors from MinIO.
func (h *ExpiredCleanupHandler) Handle(ctx context.Context, payload map[string]any) error {
	filePathVal, ok := payload["file_path"]
	if !ok {
		return nil
	}
	filePath, ok := filePathVal.(string)
	if !ok || filePath == "" {
		return nil
	}

	if err := h.remover.RemoveObject(ctx, h.bucket, filePath); err != nil {
		log.Warn().Err(err).Str("file_path", filePath).Str("bucket", h.bucket).Msg("ExpiredCleanupHandler: failed to remove object (MinIO lifecycle policy is backstop)")
	}
	return nil
}
