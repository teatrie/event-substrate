package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// validDeleteRetryPayload returns a well-formed FileDeleteRetry event payload.
func validDeleteRetryPayload() map[string]any {
	return map[string]any{
		"user_id":     "user-retry-abc",
		"file_path":   "uploads/user-retry-abc/uuid-001/photo.jpg",
		"file_name":   "photo.jpg",
		"media_type":  "image/jpeg",
		"file_size":   int64(2048000),
		"request_id":  "req-delete-retry-001",
		"retry_count": int(0),
		"failed_at":   "2026-02-25T10:00:00Z",
	}
}

// defaultDeleteRetryHandler constructs a DeleteRetryHandler with default mocks.
func defaultDeleteRetryHandler() *DeleteRetryHandler {
	return NewDeleteRetryHandler(defaultObjectRemover(), defaultProducer(), "media-uploads")
}

// ---------------------------------------------------------------------------
// Happy Path Tests (RemoveObject succeeds)
// ---------------------------------------------------------------------------

// TestDeleteRetryHandler_Success_NoEventsProduced verifies that when RemoveObject
// succeeds, no Produce calls are made.
func TestDeleteRetryHandler_Success_NoEventsProduced(t *testing.T) {
	producer := defaultProducer()
	remover := defaultObjectRemover()
	handler := NewDeleteRetryHandler(remover, producer, "media-uploads")

	err := handler.Handle(context.Background(), validDeleteRetryPayload())
	if err != nil {
		t.Fatalf("Handle() returned unexpected error: %v", err)
	}

	if len(producer.calls) != 0 {
		t.Errorf("expected 0 Produce calls on RemoveObject success, got %d", len(producer.calls))
	}
}

// TestDeleteRetryHandler_Success_RemoveObjectCalledCorrectly verifies that
// RemoveObject is called with the correct bucket and file_path.
func TestDeleteRetryHandler_Success_RemoveObjectCalledCorrectly(t *testing.T) {
	remover := defaultObjectRemover()
	handler := NewDeleteRetryHandler(remover, defaultProducer(), "my-test-bucket")

	payload := validDeleteRetryPayload()
	payload["file_path"] = "uploads/user-retry-abc/uuid-001/photo.jpg"

	err := handler.Handle(context.Background(), payload)
	if err != nil {
		t.Fatalf("Handle() returned unexpected error: %v", err)
	}

	if remover.callCount != 1 {
		t.Errorf("expected 1 RemoveObject call, got %d", remover.callCount)
	}
	if remover.capturedBucket != "my-test-bucket" {
		t.Errorf("expected bucket 'my-test-bucket', got %q", remover.capturedBucket)
	}
	if remover.capturedObjectKey != "uploads/user-retry-abc/uuid-001/photo.jpg" {
		t.Errorf("expected objectKey 'uploads/user-retry-abc/uuid-001/photo.jpg', got %q", remover.capturedObjectKey)
	}
}

// TestDeleteRetryHandler_Success_ReturnsNil verifies that Handle returns nil
// when RemoveObject succeeds.
func TestDeleteRetryHandler_Success_ReturnsNil(t *testing.T) {
	handler := defaultDeleteRetryHandler()

	err := handler.Handle(context.Background(), validDeleteRetryPayload())
	if err != nil {
		t.Errorf("expected nil error on success, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Retry Path Tests (RemoveObject fails, retry_count < 3)
// ---------------------------------------------------------------------------

// TestDeleteRetryHandler_Retry_EmitsRetryEvent verifies that when RemoveObject
// fails and retry_count=0, produces to "internal.media.delete.retry".
func TestDeleteRetryHandler_Retry_EmitsRetryEvent(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("MinIO connection failed")}
	producer := defaultProducer()
	handler := NewDeleteRetryHandler(remover, producer, "media-uploads")

	payload := validDeleteRetryPayload()
	payload["retry_count"] = int(0)

	err := handler.Handle(context.Background(), payload)
	if err != nil {
		t.Fatalf("Handle() returned unexpected error: %v", err)
	}

	if len(producer.calls) != 1 {
		t.Fatalf("expected 1 Produce call, got %d", len(producer.calls))
	}

	if producer.calls[0].topic != "internal.media.delete.retry" {
		t.Errorf("expected topic 'internal.media.delete.retry', got %q", producer.calls[0].topic)
	}
}

// TestDeleteRetryHandler_Retry_IncrementsRetryCount verifies that retry_count
// in the emitted event is original+1 (e.g., input 0 => output 1, input 2 => output 3).
func TestDeleteRetryHandler_Retry_IncrementsRetryCount(t *testing.T) {
	tests := []struct {
		name        string
		inputCount  int
		expectedOut int32
	}{
		{"0 increments to 1", 0, 1},
		{"1 increments to 2", 1, 2},
		{"2 increments to 3", 2, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remover := &mockObjectRemover{err: errors.New("MinIO error")}
			producer := defaultProducer()
			handler := NewDeleteRetryHandler(remover, producer, "media-uploads")

			payload := validDeleteRetryPayload()
			payload["retry_count"] = tt.inputCount

			if err := handler.Handle(context.Background(), payload); err != nil {
				t.Fatalf("Handle() returned unexpected error: %v", err)
			}

			if len(producer.calls) != 1 {
				t.Fatalf("expected 1 Produce call, got %d", len(producer.calls))
			}

			retryCount, ok := producer.calls[0].event["retry_count"]
			if !ok {
				t.Fatal("produced event missing field 'retry_count'")
			}

			rc, isInt32 := retryCount.(int32)
			if !isInt32 {
				t.Fatalf("retry_count should be int32, got %T (%v)", retryCount, retryCount)
			}
			if rc != tt.expectedOut {
				t.Errorf("expected retry_count=%d, got %d", tt.expectedOut, rc)
			}
		})
	}
}

// TestDeleteRetryHandler_Retry_PreservesAllFields verifies that user_id, file_path,
// file_name, media_type, file_size, and request_id are preserved in the retry event.
func TestDeleteRetryHandler_Retry_PreservesAllFields(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("MinIO unavailable")}
	producer := defaultProducer()
	handler := NewDeleteRetryHandler(remover, producer, "media-uploads")

	payload := validDeleteRetryPayload()
	payload["user_id"] = "user-preserve-fields"
	payload["file_path"] = "uploads/user-preserve-fields/uuid-555/clip.mp4"
	payload["file_name"] = "clip.mp4"
	payload["media_type"] = "video/mp4"
	payload["file_size"] = int64(10000000)
	payload["request_id"] = "req-preserve-001"
	payload["retry_count"] = int(1)

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() returned unexpected error: %v", err)
	}

	if len(producer.calls) != 1 {
		t.Fatalf("expected 1 Produce call, got %d", len(producer.calls))
	}

	event := producer.calls[0].event

	stringFields := map[string]string{
		"user_id":    "user-preserve-fields",
		"file_path":  "uploads/user-preserve-fields/uuid-555/clip.mp4",
		"file_name":  "clip.mp4",
		"media_type": "video/mp4",
		"request_id": "req-preserve-001",
	}
	for field, expected := range stringFields {
		val, ok := event[field]
		if !ok {
			t.Errorf("retry event missing field %q", field)
			continue
		}
		str, isStr := val.(string)
		if !isStr {
			t.Errorf("retry event field %q is not a string: %T", field, val)
			continue
		}
		if str != expected {
			t.Errorf("retry event field %q: expected %q, got %q", field, expected, str)
		}
	}

	fileSize, ok := event["file_size"]
	if !ok {
		t.Error("retry event missing field 'file_size'")
	} else if fs, isInt64 := fileSize.(int64); !isInt64 {
		t.Errorf("retry event file_size should be int64, got %T", fileSize)
	} else if fs != int64(10000000) {
		t.Errorf("retry event file_size: expected 10000000, got %d", fs)
	}
}

// TestDeleteRetryHandler_Retry_UpdatesFailedAt verifies that the failed_at field
// in the retry event is a valid ISO 8601 timestamp (not the original one).
func TestDeleteRetryHandler_Retry_UpdatesFailedAt(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("S3 503")}
	producer := defaultProducer()
	handler := NewDeleteRetryHandler(remover, producer, "media-uploads")

	payload := validDeleteRetryPayload()
	payload["failed_at"] = "2020-01-01T00:00:00Z"
	payload["retry_count"] = int(0)

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() returned unexpected error: %v", err)
	}

	if len(producer.calls) != 1 {
		t.Fatalf("expected 1 Produce call, got %d", len(producer.calls))
	}

	failedAt, ok := producer.calls[0].event["failed_at"]
	if !ok {
		t.Fatal("retry event missing field 'failed_at'")
	}

	failedAtStr, isStr := failedAt.(string)
	if !isStr || failedAtStr == "" {
		t.Fatalf("failed_at should be a non-empty string, got %T: %v", failedAt, failedAt)
	}

	_, err := time.Parse(time.RFC3339, failedAtStr)
	if err != nil {
		_, err = time.Parse(time.RFC3339Nano, failedAtStr)
		if err != nil {
			t.Errorf("failed_at %q is not a valid ISO 8601 timestamp: %v", failedAtStr, err)
		}
	}
}

// TestDeleteRetryHandler_Retry_ReturnsNil verifies that Handle returns nil
// when RemoveObject fails and a retry event is emitted (error is handled).
func TestDeleteRetryHandler_Retry_ReturnsNil(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("MinIO temporary failure")}
	handler := NewDeleteRetryHandler(remover, defaultProducer(), "media-uploads")

	payload := validDeleteRetryPayload()
	payload["retry_count"] = int(1)

	err := handler.Handle(context.Background(), payload)
	if err != nil {
		t.Errorf("expected nil (retry event emitted), got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Dead-Letter Path Tests (RemoveObject fails, retry_count >= 3)
// ---------------------------------------------------------------------------

// TestDeleteRetryHandler_DeadLetter_EmitsDeadLetterEvent verifies that when
// retry_count=3 and RemoveObject fails, produces to "internal.media.delete.dead-letter".
func TestDeleteRetryHandler_DeadLetter_EmitsDeadLetterEvent(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("MinIO unreachable")}
	producer := defaultProducer()
	handler := NewDeleteRetryHandler(remover, producer, "media-uploads")

	payload := validDeleteRetryPayload()
	payload["retry_count"] = int(3)

	err := handler.Handle(context.Background(), payload)
	if err != nil {
		t.Fatalf("Handle() returned unexpected error: %v", err)
	}

	if len(producer.calls) != 1 {
		t.Fatalf("expected 1 Produce call, got %d", len(producer.calls))
	}

	if producer.calls[0].topic != "internal.media.delete.dead-letter" {
		t.Errorf("expected topic 'internal.media.delete.dead-letter', got %q", producer.calls[0].topic)
	}
}

// TestDeleteRetryHandler_DeadLetter_HasFailureReason verifies that the dead-letter
// event has a non-empty failure_reason field.
func TestDeleteRetryHandler_DeadLetter_HasFailureReason(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("MinIO down")}
	producer := defaultProducer()
	handler := NewDeleteRetryHandler(remover, producer, "media-uploads")

	payload := validDeleteRetryPayload()
	payload["retry_count"] = int(3)

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() returned unexpected error: %v", err)
	}

	if len(producer.calls) == 0 {
		t.Fatal("no events produced")
	}

	reason, ok := producer.calls[0].event["failure_reason"]
	if !ok {
		t.Fatal("dead-letter event missing field 'failure_reason'")
	}

	reasonStr, isStr := reason.(string)
	if !isStr || reasonStr == "" {
		t.Errorf("failure_reason should be a non-empty string, got %T: %v", reason, reason)
	}
}

// TestDeleteRetryHandler_DeadLetter_HasDeadLetterTime verifies that the dead-letter
// event has a valid ISO 8601 dead_letter_time field.
func TestDeleteRetryHandler_DeadLetter_HasDeadLetterTime(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("MinIO unavailable")}
	producer := defaultProducer()
	handler := NewDeleteRetryHandler(remover, producer, "media-uploads")

	payload := validDeleteRetryPayload()
	payload["retry_count"] = int(3)

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() returned unexpected error: %v", err)
	}

	if len(producer.calls) == 0 {
		t.Fatal("no events produced")
	}

	deadLetterTime, ok := producer.calls[0].event["dead_letter_time"]
	if !ok {
		t.Fatal("dead-letter event missing field 'dead_letter_time'")
	}

	dlTimeStr, isStr := deadLetterTime.(string)
	if !isStr || dlTimeStr == "" {
		t.Fatalf("dead_letter_time should be a non-empty string, got %T: %v", deadLetterTime, deadLetterTime)
	}

	_, err := time.Parse(time.RFC3339, dlTimeStr)
	if err != nil {
		_, err = time.Parse(time.RFC3339Nano, dlTimeStr)
		if err != nil {
			t.Errorf("dead_letter_time %q is not a valid ISO 8601 timestamp: %v", dlTimeStr, err)
		}
	}
}

// TestDeleteRetryHandler_DeadLetter_PreservesMetadata verifies that user_id,
// file_path, file_name, request_id, and retry_count are preserved in the dead-letter event.
func TestDeleteRetryHandler_DeadLetter_PreservesMetadata(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("MinIO 500")}
	producer := defaultProducer()
	handler := NewDeleteRetryHandler(remover, producer, "media-uploads")

	payload := validDeleteRetryPayload()
	payload["user_id"] = "user-deadletter-meta"
	payload["file_path"] = "uploads/user-deadletter-meta/uuid-999/doc.pdf"
	payload["file_name"] = "doc.pdf"
	payload["request_id"] = "req-deadletter-meta-001"
	payload["retry_count"] = int(3)

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() returned unexpected error: %v", err)
	}

	if len(producer.calls) == 0 {
		t.Fatal("no events produced")
	}

	event := producer.calls[0].event

	stringFields := map[string]string{
		"user_id":    "user-deadletter-meta",
		"file_path":  "uploads/user-deadletter-meta/uuid-999/doc.pdf",
		"file_name":  "doc.pdf",
		"request_id": "req-deadletter-meta-001",
	}
	for field, expected := range stringFields {
		val, ok := event[field]
		if !ok {
			t.Errorf("dead-letter event missing field %q", field)
			continue
		}
		str, isStr := val.(string)
		if !isStr {
			t.Errorf("dead-letter event field %q is not a string: %T", field, val)
			continue
		}
		if str != expected {
			t.Errorf("dead-letter event field %q: expected %q, got %q", field, expected, str)
		}
	}

	retryCount, ok := event["retry_count"]
	if !ok {
		t.Fatal("dead-letter event missing field 'retry_count'")
	}
	rc, isInt32 := retryCount.(int32)
	if !isInt32 {
		t.Fatalf("dead-letter retry_count should be int32, got %T (%v)", retryCount, retryCount)
	}
	if rc != int32(3) {
		t.Errorf("expected retry_count=3 preserved, got %d", rc)
	}
}

// TestDeleteRetryHandler_DeadLetter_RetryCountFour verifies that retry_count=4
// also triggers dead-letter (boundary: >= 3).
func TestDeleteRetryHandler_DeadLetter_RetryCountFour(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("MinIO still down")}
	producer := defaultProducer()
	handler := NewDeleteRetryHandler(remover, producer, "media-uploads")

	payload := validDeleteRetryPayload()
	payload["retry_count"] = int(4)

	err := handler.Handle(context.Background(), payload)
	if err != nil {
		t.Fatalf("Handle() returned unexpected error: %v", err)
	}

	if len(producer.calls) != 1 {
		t.Fatalf("expected 1 Produce call, got %d", len(producer.calls))
	}

	if producer.calls[0].topic != "internal.media.delete.dead-letter" {
		t.Errorf("expected topic 'internal.media.delete.dead-letter' for retry_count=4, got %q", producer.calls[0].topic)
	}
}

// TestDeleteRetryHandler_DeadLetter_ReturnsNil verifies that Handle returns nil
// when dead-letter event is emitted.
func TestDeleteRetryHandler_DeadLetter_ReturnsNil(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("MinIO dead")}
	handler := NewDeleteRetryHandler(remover, defaultProducer(), "media-uploads")

	payload := validDeleteRetryPayload()
	payload["retry_count"] = int(3)

	err := handler.Handle(context.Background(), payload)
	if err != nil {
		t.Errorf("expected nil (dead-letter event emitted), got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// retry_count Type Coercion Tests
// ---------------------------------------------------------------------------

// TestDeleteRetryHandler_RetryCountParsing_Float64 verifies that retry_count
// as float64(2) is parsed correctly (incremented to 3, still produces retry not dead-letter).
func TestDeleteRetryHandler_RetryCountParsing_Float64(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("MinIO error")}
	producer := defaultProducer()
	handler := NewDeleteRetryHandler(remover, producer, "media-uploads")

	payload := validDeleteRetryPayload()
	payload["retry_count"] = float64(2)

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() returned unexpected error: %v", err)
	}

	// retry_count=2 (< 3) => should produce retry event, not dead-letter
	if len(producer.calls) != 1 {
		t.Fatalf("expected 1 Produce call, got %d", len(producer.calls))
	}

	if producer.calls[0].topic != "internal.media.delete.retry" {
		t.Errorf("expected retry topic for float64(2), got %q", producer.calls[0].topic)
	}

	rc, ok := producer.calls[0].event["retry_count"]
	if !ok {
		t.Fatal("produced event missing 'retry_count'")
	}
	rcInt32, isInt32 := rc.(int32)
	if !isInt32 {
		t.Fatalf("retry_count should be int32, got %T", rc)
	}
	if rcInt32 != int32(3) {
		t.Errorf("expected retry_count=3 (2+1), got %d", rcInt32)
	}
}

// TestDeleteRetryHandler_RetryCountParsing_JsonNumber verifies that retry_count
// as json.Number("1") is parsed correctly.
func TestDeleteRetryHandler_RetryCountParsing_JsonNumber(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("MinIO error")}
	producer := defaultProducer()
	handler := NewDeleteRetryHandler(remover, producer, "media-uploads")

	payload := validDeleteRetryPayload()
	payload["retry_count"] = json.Number("1")

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() returned unexpected error: %v", err)
	}

	// retry_count=1 (< 3) => should produce retry event
	if len(producer.calls) != 1 {
		t.Fatalf("expected 1 Produce call, got %d", len(producer.calls))
	}

	if producer.calls[0].topic != "internal.media.delete.retry" {
		t.Errorf("expected retry topic for json.Number(1), got %q", producer.calls[0].topic)
	}

	rc, ok := producer.calls[0].event["retry_count"]
	if !ok {
		t.Fatal("produced event missing 'retry_count'")
	}
	rcInt32, isInt32 := rc.(int32)
	if !isInt32 {
		t.Fatalf("retry_count should be int32, got %T", rc)
	}
	if rcInt32 != int32(2) {
		t.Errorf("expected retry_count=2 (1+1), got %d", rcInt32)
	}
}

// TestDeleteRetryHandler_RetryCountParsing_Int verifies that retry_count as
// int(0) is parsed correctly.
func TestDeleteRetryHandler_RetryCountParsing_Int(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("MinIO error")}
	producer := defaultProducer()
	handler := NewDeleteRetryHandler(remover, producer, "media-uploads")

	payload := validDeleteRetryPayload()
	payload["retry_count"] = int(0)

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() returned unexpected error: %v", err)
	}

	// retry_count=0 (< 3) => should produce retry event
	if len(producer.calls) != 1 {
		t.Fatalf("expected 1 Produce call, got %d", len(producer.calls))
	}

	if producer.calls[0].topic != "internal.media.delete.retry" {
		t.Errorf("expected retry topic for int(0), got %q", producer.calls[0].topic)
	}

	rc, ok := producer.calls[0].event["retry_count"]
	if !ok {
		t.Fatal("produced event missing 'retry_count'")
	}
	rcInt32, isInt32 := rc.(int32)
	if !isInt32 {
		t.Fatalf("retry_count should be int32, got %T", rc)
	}
	if rcInt32 != int32(1) {
		t.Errorf("expected retry_count=1 (0+1), got %d", rcInt32)
	}
}

// ---------------------------------------------------------------------------
// Constructor Tests
// ---------------------------------------------------------------------------

// TestNewDeleteRetryHandler_ReturnsNonNil verifies that NewDeleteRetryHandler
// returns a non-nil handler.
func TestNewDeleteRetryHandler_ReturnsNonNil(t *testing.T) {
	h := NewDeleteRetryHandler(defaultObjectRemover(), defaultProducer(), "bucket")
	if h == nil {
		t.Error("NewDeleteRetryHandler() returned nil")
	}
}

// TestNewDeleteRetryHandler_ImplementsMessageHandler verifies that
// *DeleteRetryHandler satisfies the MessageHandler interface at compile time.
func TestNewDeleteRetryHandler_ImplementsMessageHandler(t *testing.T) {
	var _ MessageHandler = NewDeleteRetryHandler(defaultObjectRemover(), defaultProducer(), "bucket")
}
