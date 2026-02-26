package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// RetryHandler consumes upload.retry events, performs a synchronous MoveObject,
// and re-produces upload.received with an incremented retry_count.
type RetryHandler struct {
	mover    ObjectMover
	producer EventProducer
	bucket   string
}

func NewRetryHandler(mover ObjectMover, producer EventProducer, bucket string) *RetryHandler {
	return &RetryHandler{mover: mover, producer: producer, bucket: bucket}
}

func (h *RetryHandler) Handle(ctx context.Context, payload map[string]any) error {
	filePath, _ := payload["file_path"].(string)
	permanentPath, _ := payload["permanent_path"].(string)

	log.Printf("Retry handler: moving %s → %s", filePath, permanentPath)

	if err := h.mover.MoveObject(ctx, h.bucket, filePath, permanentPath); err != nil {
		return fmt.Errorf("retry move failed: %w", err)
	}

	// Parse current retry_count and increment
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

	// Re-produce upload.received with incremented retry_count
	receivedEvent := map[string]any{
		"user_id":        payload["user_id"],
		"email":          payload["email"],
		"file_path":      filePath,
		"file_name":      payload["file_name"],
		"file_size":      payload["file_size"],
		"media_type":     payload["media_type"],
		"upload_time":    payload["upload_time"],
		"permanent_path": permanentPath,
		"retry_count":    int32(retryCount + 1),
	}

	if err := h.producer.Produce(ctx, uploadReceivedTopic, receivedEvent); err != nil {
		return fmt.Errorf("retry re-produce failed: %w", err)
	}

	log.Printf("Retry handler: re-produced upload.received with retry_count=%d", retryCount+1)
	return nil
}
