package main

import (
	"context"
	"errors"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func defaultExpiredCleanupHandler() *ExpiredCleanupHandler {
	return NewExpiredCleanupHandler(defaultObjectRemover(), "media-uploads")
}

func validExpiredPayload() map[string]any {
	return map[string]any{
		"user_id":      "user-abc-123",
		"file_path":    "uploads/user-abc-123/some-uuid/photo.jpg",
		"file_name":    "photo.jpg",
		"file_size":    int64(2048000),
		"media_type":   "image/jpeg",
		"request_id":   "req-expired-101",
		"request_time": "2026-02-25T09:00:00Z",
		"expired_time": "2026-02-25T09:15:00Z",
	}
}

// ---------------------------------------------------------------------------
// Happy Path Tests
// ---------------------------------------------------------------------------

// TestExpiredCleanupHandler_HappyPath_RemovesObjectFromMinIO verifies that
// ObjectRemover.RemoveObject is called for a valid expired event.
func TestExpiredCleanupHandler_HappyPath_RemovesObjectFromMinIO(t *testing.T) {
	remover := defaultObjectRemover()
	handler := NewExpiredCleanupHandler(remover, "media-uploads")

	err := handler.Handle(context.Background(), validExpiredPayload())
	if err != nil {
		t.Fatalf("Handle() returned unexpected error: %v", err)
	}

	if remover.callCount != 1 {
		t.Errorf("expected 1 RemoveObject call, got %d", remover.callCount)
	}
}

// TestExpiredCleanupHandler_HappyPath_RemoveObjectCalledWithCorrectBucket verifies
// that RemoveObject is called with the configured bucket name.
func TestExpiredCleanupHandler_HappyPath_RemoveObjectCalledWithCorrectBucket(t *testing.T) {
	remover := defaultObjectRemover()
	handler := NewExpiredCleanupHandler(remover, "my-media-bucket")

	if err := handler.Handle(context.Background(), validExpiredPayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	if remover.capturedBucket != "my-media-bucket" {
		t.Errorf("expected bucket 'my-media-bucket', got %q", remover.capturedBucket)
	}
}

// TestExpiredCleanupHandler_HappyPath_RemoveObjectCalledWithFilePath verifies
// that RemoveObject is called with file_path from the expired event as objectKey.
func TestExpiredCleanupHandler_HappyPath_RemoveObjectCalledWithFilePath(t *testing.T) {
	remover := defaultObjectRemover()
	handler := NewExpiredCleanupHandler(remover, "media-uploads")

	payload := validExpiredPayload()
	payload["file_path"] = "uploads/user-abc/uuid-111/video.mp4"

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	if remover.capturedObjectKey != "uploads/user-abc/uuid-111/video.mp4" {
		t.Errorf("expected objectKey 'uploads/user-abc/uuid-111/video.mp4', got %q", remover.capturedObjectKey)
	}
}

// TestExpiredCleanupHandler_HappyPath_ReturnsNilOnSuccess verifies that
// Handle returns nil when RemoveObject succeeds.
func TestExpiredCleanupHandler_HappyPath_ReturnsNilOnSuccess(t *testing.T) {
	handler := NewExpiredCleanupHandler(defaultObjectRemover(), "media-uploads")

	err := handler.Handle(context.Background(), validExpiredPayload())
	if err != nil {
		t.Errorf("expected nil error on success, got: %v", err)
	}
}

// TestExpiredCleanupHandler_HappyPath_NoEventProducer verifies that
// ExpiredCleanupHandler requires no EventProducer (compile-time contract check).
// This handler emits no events — Flink handles notification and credit refund.
func TestExpiredCleanupHandler_HappyPath_NoEventProducer(t *testing.T) {
	// If this compiles, the constructor correctly takes no EventProducer.
	var _ MessageHandler = NewExpiredCleanupHandler(defaultObjectRemover(), "media-uploads")
}

// ---------------------------------------------------------------------------
// Edge Case Tests — Missing / Empty file_path (skip silently)
// ---------------------------------------------------------------------------

// TestExpiredCleanupHandler_MissingFilePath_Skips verifies that when file_path
// is absent, Handle returns nil without calling RemoveObject.
func TestExpiredCleanupHandler_MissingFilePath_Skips(t *testing.T) {
	remover := defaultObjectRemover()
	handler := NewExpiredCleanupHandler(remover, "media-uploads")

	payload := validExpiredPayload()
	delete(payload, "file_path")

	err := handler.Handle(context.Background(), payload)
	if err != nil {
		t.Errorf("expected nil error for missing file_path, got: %v", err)
	}

	if remover.callCount > 0 {
		t.Errorf("expected RemoveObject NOT called for missing file_path, got %d calls", remover.callCount)
	}
}

// TestExpiredCleanupHandler_EmptyFilePath_Skips verifies that when file_path
// is an empty string, Handle returns nil without calling RemoveObject.
func TestExpiredCleanupHandler_EmptyFilePath_Skips(t *testing.T) {
	remover := defaultObjectRemover()
	handler := NewExpiredCleanupHandler(remover, "media-uploads")

	payload := validExpiredPayload()
	payload["file_path"] = ""

	err := handler.Handle(context.Background(), payload)
	if err != nil {
		t.Errorf("expected nil error for empty file_path, got: %v", err)
	}

	if remover.callCount > 0 {
		t.Errorf("expected RemoveObject NOT called for empty file_path, got %d calls", remover.callCount)
	}
}

// TestExpiredCleanupHandler_NonStringFilePath_Skips verifies that a non-string
// file_path is treated as missing (skip silently, no error).
func TestExpiredCleanupHandler_NonStringFilePath_Skips(t *testing.T) {
	remover := defaultObjectRemover()
	handler := NewExpiredCleanupHandler(remover, "media-uploads")

	payload := validExpiredPayload()
	payload["file_path"] = 42 // wrong type

	err := handler.Handle(context.Background(), payload)
	if err != nil {
		t.Errorf("expected nil error for non-string file_path, got: %v", err)
	}

	if remover.callCount > 0 {
		t.Errorf("expected RemoveObject NOT called for non-string file_path, got %d calls", remover.callCount)
	}
}

// TestExpiredCleanupHandler_MissingUserID_StillProcesses verifies that a missing
// user_id does not prevent cleanup — file_path is sufficient for MinIO removal.
func TestExpiredCleanupHandler_MissingUserID_StillProcesses(t *testing.T) {
	remover := defaultObjectRemover()
	handler := NewExpiredCleanupHandler(remover, "media-uploads")

	payload := validExpiredPayload()
	delete(payload, "user_id")

	err := handler.Handle(context.Background(), payload)
	if err != nil {
		t.Errorf("expected nil error when user_id missing (best-effort), got: %v", err)
	}

	if remover.callCount != 1 {
		t.Errorf("expected RemoveObject called even without user_id, got %d calls", remover.callCount)
	}
}

// ---------------------------------------------------------------------------
// Best-Effort Error Handling Tests
// ---------------------------------------------------------------------------

// TestExpiredCleanupHandler_RemoveObjectError_ReturnsNil verifies that when
// RemoveObject fails, Handle still returns nil (best-effort, MinIO lifecycle is backstop).
func TestExpiredCleanupHandler_RemoveObjectError_ReturnsNil(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("MinIO connection timeout")}
	handler := NewExpiredCleanupHandler(remover, "media-uploads")

	err := handler.Handle(context.Background(), validExpiredPayload())
	if err != nil {
		t.Errorf("expected nil error (best-effort) on RemoveObject failure, got: %v", err)
	}
}

// TestExpiredCleanupHandler_RemoveObjectError_RemoveAttempted verifies that even
// on error, RemoveObject was still called (we tried, then failed gracefully).
func TestExpiredCleanupHandler_RemoveObjectError_RemoveAttempted(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("S3 error")}
	handler := NewExpiredCleanupHandler(remover, "media-uploads")

	_ = handler.Handle(context.Background(), validExpiredPayload())

	if remover.callCount != 1 {
		t.Errorf("expected 1 RemoveObject call attempt, got %d", remover.callCount)
	}
}

// ---------------------------------------------------------------------------
// Context Tests
// ---------------------------------------------------------------------------

// TestExpiredCleanupHandler_EdgeCase_ContextCancellation verifies that a
// cancelled context is handled gracefully (no panic).
func TestExpiredCleanupHandler_EdgeCase_ContextCancellation(t *testing.T) {
	handler := defaultExpiredCleanupHandler()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = handler.Handle(ctx, validExpiredPayload())
}

// ---------------------------------------------------------------------------
// Constructor Tests
// ---------------------------------------------------------------------------

// TestNewExpiredCleanupHandler_ReturnsNonNil verifies that NewExpiredCleanupHandler
// returns a non-nil handler.
func TestNewExpiredCleanupHandler_ReturnsNonNil(t *testing.T) {
	h := NewExpiredCleanupHandler(defaultObjectRemover(), "media-uploads")
	if h == nil {
		t.Error("NewExpiredCleanupHandler() returned nil")
	}
}

// TestNewExpiredCleanupHandler_ImplementsMessageHandler verifies that
// *ExpiredCleanupHandler satisfies the MessageHandler interface at compile time.
func TestNewExpiredCleanupHandler_ImplementsMessageHandler(t *testing.T) {
	var _ MessageHandler = NewExpiredCleanupHandler(defaultObjectRemover(), "media-uploads")
}
