package main

import (
	"context"
	"fmt"
	"time"
)

const deleteEventsTopic = "public.media.delete.events"
const deleteRejectedTopic = "public.media.delete.rejected"

// DeleteHandler handles DeleteIntent events by verifying file ownership,
// removing the object from MinIO, soft-deleting the DB record, and emitting
// a FileDeleted or FileDeleteRejected event.
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

	// Remove from object storage before updating the DB — if MinIO fails, the
	// file still exists and the user can retry. If DB fails after MinIO, the
	// file is gone but we can re-soft-delete (idempotent).
	if err := h.remover.RemoveObject(ctx, h.bucket, filePath); err != nil {
		return h.producer.Produce(ctx, deleteRejectedTopic, map[string]any{
			"user_id":       userID,
			"file_path":     filePath,
			"request_id":    requestID,
			"reason":        "internal_error",
			"rejected_time": time.Now().UTC().Format(time.RFC3339),
		})
	}

	// Soft-delete the database record.
	if err := h.deleter.SoftDelete(ctx, userID, filePath); err != nil {
		return fmt.Errorf("soft delete: %w", err)
	}

	return h.producer.Produce(ctx, deleteEventsTopic, map[string]any{
		"user_id":     userID,
		"file_path":   filePath,
		"file_name":   metadata.FileName,
		"media_type":  metadata.MediaType,
		"file_size":   metadata.FileSize,
		"delete_time": time.Now().UTC().Format(time.RFC3339),
	})
}
