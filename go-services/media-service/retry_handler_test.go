package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestRetryHandler_CallsMoveObjectWithCorrectArgs(t *testing.T) {
	producer := &mockEventProducer{}
	mover := &mockObjectMover{}
	handler := NewRetryHandler(mover, producer, "media-uploads")

	payload := map[string]any{
		"user_id":        "user-123",
		"email":          "user@example.com",
		"file_path":      "uploads/user-123/uuid-1/photo.jpg",
		"permanent_path": "files/user-123/uuid-1/photo.jpg",
		"file_name":      "photo.jpg",
		"file_size":      json.Number("204800"),
		"media_type":     "image/jpeg",
		"upload_time":    "2026-02-26T10:00:00Z",
		"retry_count":    json.Number("1"),
		"retry_time":     "2026-02-26T10:02:00Z",
	}

	err := handler.Handle(context.Background(), payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mover.callCount != 1 {
		t.Fatalf("expected 1 MoveObject call, got %d", mover.callCount)
	}
	if mover.capturedBucket != "media-uploads" {
		t.Errorf("expected bucket=media-uploads, got %s", mover.capturedBucket)
	}
	if mover.capturedSrcKey != "uploads/user-123/uuid-1/photo.jpg" {
		t.Errorf("expected srcKey=uploads/..., got %s", mover.capturedSrcKey)
	}
	if mover.capturedDstKey != "files/user-123/uuid-1/photo.jpg" {
		t.Errorf("expected dstKey=files/..., got %s", mover.capturedDstKey)
	}
}

func TestRetryHandler_ReproducesUploadReceivedWithIncrementedRetryCount(t *testing.T) {
	producer := &mockEventProducer{}
	mover := &mockObjectMover{}
	handler := NewRetryHandler(mover, producer, "media-uploads")

	payload := map[string]any{
		"user_id":        "user-123",
		"email":          "user@example.com",
		"file_path":      "uploads/user-123/uuid-1/photo.jpg",
		"permanent_path": "files/user-123/uuid-1/photo.jpg",
		"file_name":      "photo.jpg",
		"file_size":      json.Number("204800"),
		"media_type":     "image/jpeg",
		"upload_time":    "2026-02-26T10:00:00Z",
		"retry_count":    json.Number("1"),
		"retry_time":     "2026-02-26T10:02:00Z",
	}

	err := handler.Handle(context.Background(), payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(producer.calls) != 1 {
		t.Fatalf("expected 1 produce call, got %d", len(producer.calls))
	}

	call := producer.calls[0]
	if call.topic != "internal.media.upload.received" {
		t.Errorf("expected topic=internal.media.upload.received, got %s", call.topic)
	}
	if call.event["user_id"] != "user-123" {
		t.Errorf("expected user_id=user-123, got %v", call.event["user_id"])
	}
	if call.event["email"] != "user@example.com" {
		t.Errorf("expected email=user@example.com, got %v", call.event["email"])
	}

	// retry_count should be incremented: 1 → 2
	rc := call.event["retry_count"]
	switch v := rc.(type) {
	case int32:
		if v != 2 {
			t.Errorf("expected retry_count=2, got %d", v)
		}
	case int64:
		if v != 2 {
			t.Errorf("expected retry_count=2, got %d", v)
		}
	case json.Number:
		if v.String() != "2" {
			t.Errorf("expected retry_count=2, got %v", v)
		}
	case int:
		if v != 2 {
			t.Errorf("expected retry_count=2, got %d", v)
		}
	default:
		t.Errorf("unexpected retry_count type: %T", rc)
	}
}

func TestRetryHandler_MoveObjectError(t *testing.T) {
	producer := &mockEventProducer{}
	mover := &mockObjectMover{err: errors.New("move failed")}
	handler := NewRetryHandler(mover, producer, "media-uploads")

	payload := map[string]any{
		"user_id":        "user-123",
		"email":          "user@example.com",
		"file_path":      "uploads/user-123/uuid-1/photo.jpg",
		"permanent_path": "files/user-123/uuid-1/photo.jpg",
		"file_name":      "photo.jpg",
		"file_size":      json.Number("204800"),
		"media_type":     "image/jpeg",
		"upload_time":    "2026-02-26T10:00:00Z",
		"retry_count":    json.Number("0"),
		"retry_time":     "2026-02-26T10:02:00Z",
	}

	err := handler.Handle(context.Background(), payload)
	if err == nil {
		t.Fatal("expected error for MoveObject failure, got nil")
	}
	if len(producer.calls) != 0 {
		t.Errorf("should not produce on MoveObject failure, got %d calls", len(producer.calls))
	}
}
