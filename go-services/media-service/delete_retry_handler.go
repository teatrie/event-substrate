package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

const deleteDeadLetterTopic = "internal.media.delete.dead-letter"

// DeleteRetryHandler consumes delete.retry events, attempts RemoveObject,
// and either succeeds (no events), retries (retry_count < 3), or dead-letters (>= 3).
type DeleteRetryHandler struct {
	remover  ObjectRemover
	producer EventProducer
	bucket   string
}

// NewDeleteRetryHandler constructs a DeleteRetryHandler.
func NewDeleteRetryHandler(remover ObjectRemover, producer EventProducer, bucket string) *DeleteRetryHandler {
	return &DeleteRetryHandler{remover: remover, producer: producer, bucket: bucket}
}

// Handle processes a FileDeleteRetry payload.
func (h *DeleteRetryHandler) Handle(ctx context.Context, payload map[string]any) error {
	filePath, _ := payload["file_path"].(string)

	// Parse retry_count handling int, float64, and json.Number
	var retryCount int
	switch v := payload["retry_count"].(type) {
	case json.Number:
		n, _ := v.Int64()
		retryCount = int(n)
	case float64:
		retryCount = int(v)
	case int:
		retryCount = v
	}

	if err := h.remover.RemoveObject(ctx, h.bucket, filePath); err != nil {
		if retryCount >= 3 {
			// Dead-letter
			deadLetterEvent := map[string]any{
				"user_id":          payload["user_id"],
				"file_path":        filePath,
				"file_name":        payload["file_name"],
				"media_type":       payload["media_type"],
				"file_size":        payload["file_size"],
				"request_id":       payload["request_id"],
				"retry_count":      int32(retryCount),
				"failure_reason":   err.Error(),
				"dead_letter_time": time.Now().UTC().Format(time.RFC3339),
			}
			if produceErr := h.producer.Produce(ctx, deleteDeadLetterTopic, deadLetterEvent); produceErr != nil {
				return fmt.Errorf("dead-letter produce failed: %w", produceErr)
			}
			log.Printf("DeleteRetryHandler: dead-lettered %s after %d retries", filePath, retryCount)
			return nil
		}

		// Retry
		retryEvent := map[string]any{
			"user_id":     payload["user_id"],
			"file_path":   filePath,
			"file_name":   payload["file_name"],
			"media_type":  payload["media_type"],
			"file_size":   payload["file_size"],
			"request_id":  payload["request_id"],
			"retry_count": int32(retryCount + 1),
			"failed_at":   time.Now().UTC().Format(time.RFC3339),
		}
		if produceErr := h.producer.Produce(ctx, deleteRetryTopic, retryEvent); produceErr != nil {
			return fmt.Errorf("retry produce failed: %w", produceErr)
		}
		log.Printf("DeleteRetryHandler: re-queued %s with retry_count=%d", filePath, retryCount+1)
		return nil
	}

	log.Printf("DeleteRetryHandler: successfully removed %s", filePath)
	return nil
}
