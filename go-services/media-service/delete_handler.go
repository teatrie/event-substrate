package main

import (
	"context"
	"fmt"
	"time"
)

const deleteEventsTopic = "public.media.delete.events"
const deleteRejectedTopic = "public.media.delete.rejected"
const deleteRetryTopic = "internal.media.delete.retry"

// DeleteHandler handles DeleteIntent events by verifying file ownership,
// soft-deleting the DB record, emitting FileDeleted, and then removing the
// object from MinIO (best-effort, with retry on failure).
type DeleteHandler struct {
	lookup   FileMetadataLookup
	remover  ObjectRemover
	deleter  FileDeleter
	producer EventProducer
	bucket   string
}

// NewDeleteHandler constructs a DeleteHandler.
func NewDeleteHandler(lookup FileMetadataLookup, remover ObjectRemover, deleter FileDeleter, producer EventProducer, bucket string) *DeleteHandler {
	return &DeleteHandler{
		lookup:   lookup,
		remover:  remover,
		deleter:  deleter,
		producer: producer,
		bucket:   bucket,
	}
}

// Handle processes a DeleteIntent payload and emits the appropriate event.
func (h *DeleteHandler) Handle(ctx context.Context, payload map[string]any) error {
	userID, err := requireString(payload, "user_id")
	if err != nil {
		return err
	}
	filePath, err := requireString(payload, "file_path")
	if err != nil {
		return err
	}
	requestID, err := requireString(payload, "request_id")
	if err != nil {
		return err
	}

	// Verify ownership and retrieve metadata needed for the FileDeleted event.
	metadata, err := h.lookup.GetFileMetadata(ctx, userID, filePath)
	if err != nil {
		return fmt.Errorf("metadata lookup: %w", err)
	}

	if metadata == nil {
		return h.producer.Produce(ctx, deleteRejectedTopic, map[string]any{
			"user_id":       userID,
			"file_path":     filePath,
			"request_id":    requestID,
			"reason":        "not_found",
			"rejected_time": time.Now().UTC().Format(time.RFC3339),
		})
	}

	// Soft-delete the database record first. If this fails, emit a rejection
	// event and do NOT touch MinIO (file still exists, user can retry).
	if err := h.deleter.SoftDelete(ctx, userID, filePath); err != nil {
		return h.producer.Produce(ctx, deleteRejectedTopic, map[string]any{
			"user_id":       userID,
			"file_path":     filePath,
			"request_id":    requestID,
			"reason":        "internal_error",
			"rejected_time": time.Now().UTC().Format(time.RFC3339),
		})
	}

	// Emit FileDeleted — the user is now notified of successful deletion.
	if err := h.producer.Produce(ctx, deleteEventsTopic, map[string]any{
		"user_id":     userID,
		"file_path":   filePath,
		"file_name":   metadata.FileName,
		"media_type":  metadata.MediaType,
		"file_size":   metadata.FileSize,
		"delete_time": time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return err
	}

	// Remove from object storage — best-effort. If MinIO fails, emit a retry
	// event for background cleanup. The user has already been notified.
	if err := h.remover.RemoveObject(ctx, h.bucket, filePath); err != nil {
		return h.producer.Produce(ctx, deleteRetryTopic, map[string]any{
			"user_id":     userID,
			"file_path":   filePath,
			"file_name":   metadata.FileName,
			"media_type":  metadata.MediaType,
			"file_size":   metadata.FileSize,
			"request_id":  requestID,
			"retry_count": int32(0),
			"failed_at":   time.Now().UTC().Format(time.RFC3339),
		})
	}

	return nil
}
