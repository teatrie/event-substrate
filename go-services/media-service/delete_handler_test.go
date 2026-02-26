package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Mock implementations for DeleteHandler
// ---------------------------------------------------------------------------

// mockObjectRemover implements ObjectRemover for testing.
// Also used by expired_cleanup_handler_test.go.
type mockObjectRemover struct {
	err error

	capturedBucket    string
	capturedObjectKey string
	callCount         int
}

func (m *mockObjectRemover) RemoveObject(ctx context.Context, bucket, objectKey string) error {
	m.capturedBucket = bucket
	m.capturedObjectKey = objectKey
	m.callCount++
	return m.err
}

// mockFileDeleter implements FileDeleter for testing.
type mockFileDeleter struct {
	err error

	capturedUserID   string
	capturedFilePath string
	callCount        int
}

func (m *mockFileDeleter) SoftDelete(ctx context.Context, userID, filePath string) error {
	m.capturedUserID = userID
	m.capturedFilePath = filePath
	m.callCount++
	return m.err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func defaultObjectRemover() *mockObjectRemover {
	return &mockObjectRemover{}
}

func defaultFileDeleter() *mockFileDeleter {
	return &mockFileDeleter{}
}

func defaultDeleteMetadataLookup() *mockFileMetadataLookup {
	return &mockFileMetadataLookup{
		metadata: &FileMetadata{
			FileName:  "photo.jpg",
			MediaType: "image/jpeg",
			FileSize:  int64(2048000),
		},
	}
}

func defaultDeleteHandler() *DeleteHandler {
	return NewDeleteHandler(defaultDeleteMetadataLookup(), defaultObjectRemover(), defaultFileDeleter(), defaultProducer(), "media-uploads")
}

func validDeletePayload() map[string]any {
	return map[string]any{
		"user_id":      "user-abc-123",
		"file_path":    "uploads/user-abc-123/some-uuid/photo.jpg",
		"request_id":   "req-delete-789",
		"request_time": "2026-02-25T10:00:00Z",
	}
}

// ---------------------------------------------------------------------------
// Happy Path Tests
// ---------------------------------------------------------------------------

// TestDeleteHandler_HappyPath_EmitsFileDeletedEvent verifies that a valid
// DeleteIntent causes a FileDeleted event to be produced on the correct Kafka topic.
func TestDeleteHandler_HappyPath_EmitsFileDeletedEvent(t *testing.T) {
	producer := defaultProducer()
	handler := NewDeleteHandler(defaultDeleteMetadataLookup(), defaultObjectRemover(), defaultFileDeleter(), producer, "media-uploads")

	err := handler.Handle(context.Background(), validDeletePayload())
	if err != nil {
		t.Fatalf("Handle() returned unexpected error: %v", err)
	}

	if len(producer.calls) != 1 {
		t.Fatalf("expected 1 Produce call, got %d", len(producer.calls))
	}

	if producer.calls[0].topic != "public.media.delete.events" {
		t.Errorf("expected topic 'public.media.delete.events', got %q", producer.calls[0].topic)
	}
}

// TestDeleteHandler_HappyPath_FileDeletedEventHasRequiredFields verifies all
// required fields are present in the FileDeleted event.
func TestDeleteHandler_HappyPath_FileDeletedEventHasRequiredFields(t *testing.T) {
	producer := defaultProducer()
	handler := NewDeleteHandler(defaultDeleteMetadataLookup(), defaultObjectRemover(), defaultFileDeleter(), producer, "media-uploads")

	if err := handler.Handle(context.Background(), validDeletePayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	event := producer.calls[0].event

	for _, field := range []string{"user_id", "file_path", "file_name", "media_type", "delete_time"} {
		val, ok := event[field]
		if !ok {
			t.Errorf("produced event missing field %q", field)
			continue
		}
		str, isStr := val.(string)
		if !isStr || str == "" {
			t.Errorf("produced event field %q is empty or not a string: %v", field, val)
		}
	}

	fileSizeVal, ok := event["file_size"]
	if !ok {
		t.Error("produced event missing field 'file_size'")
	} else if _, isInt64 := fileSizeVal.(int64); !isInt64 {
		t.Errorf("file_size should be int64, got %T", fileSizeVal)
	}
}

// TestDeleteHandler_HappyPath_FileNameFromMetadata verifies that file_name in
// the FileDeleted event comes from FileMetadataLookup.
func TestDeleteHandler_HappyPath_FileNameFromMetadata(t *testing.T) {
	lookup := &mockFileMetadataLookup{
		metadata: &FileMetadata{FileName: "my-video.mp4", MediaType: "video/mp4", FileSize: 5000000},
	}
	producer := defaultProducer()
	handler := NewDeleteHandler(lookup, defaultObjectRemover(), defaultFileDeleter(), producer, "media-uploads")

	if err := handler.Handle(context.Background(), validDeletePayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	fileName, _ := producer.calls[0].event["file_name"].(string)
	if fileName != "my-video.mp4" {
		t.Errorf("expected file_name 'my-video.mp4' from metadata, got %q", fileName)
	}
}

// TestDeleteHandler_HappyPath_MediaTypeFromMetadata verifies that media_type in
// the FileDeleted event comes from FileMetadataLookup.
func TestDeleteHandler_HappyPath_MediaTypeFromMetadata(t *testing.T) {
	lookup := &mockFileMetadataLookup{
		metadata: &FileMetadata{FileName: "clip.mp4", MediaType: "video/mp4", FileSize: 1000000},
	}
	producer := defaultProducer()
	handler := NewDeleteHandler(lookup, defaultObjectRemover(), defaultFileDeleter(), producer, "media-uploads")

	if err := handler.Handle(context.Background(), validDeletePayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	mediaType, _ := producer.calls[0].event["media_type"].(string)
	if mediaType != "video/mp4" {
		t.Errorf("expected media_type 'video/mp4' from metadata, got %q", mediaType)
	}
}

// TestDeleteHandler_HappyPath_FileSizeFromMetadata verifies that file_size in
// the FileDeleted event comes from FileMetadataLookup.
func TestDeleteHandler_HappyPath_FileSizeFromMetadata(t *testing.T) {
	lookup := &mockFileMetadataLookup{
		metadata: &FileMetadata{FileName: "doc.pdf", MediaType: "application/pdf", FileSize: int64(999999)},
	}
	producer := defaultProducer()
	handler := NewDeleteHandler(lookup, defaultObjectRemover(), defaultFileDeleter(), producer, "media-uploads")

	if err := handler.Handle(context.Background(), validDeletePayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	fileSize, _ := producer.calls[0].event["file_size"].(int64)
	if fileSize != int64(999999) {
		t.Errorf("expected file_size 999999 from metadata, got %d", fileSize)
	}
}

// TestDeleteHandler_HappyPath_DeleteTimeIsISO8601 verifies that delete_time
// in the FileDeleted event is a valid ISO 8601 timestamp.
func TestDeleteHandler_HappyPath_DeleteTimeIsISO8601(t *testing.T) {
	producer := defaultProducer()
	handler := NewDeleteHandler(defaultDeleteMetadataLookup(), defaultObjectRemover(), defaultFileDeleter(), producer, "media-uploads")

	if err := handler.Handle(context.Background(), validDeletePayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	deleteTime, _ := producer.calls[0].event["delete_time"].(string)
	if deleteTime == "" {
		t.Fatal("delete_time is empty")
	}

	_, err := time.Parse(time.RFC3339, deleteTime)
	if err != nil {
		_, err = time.Parse(time.RFC3339Nano, deleteTime)
		if err != nil {
			t.Errorf("delete_time %q is not a valid ISO 8601 timestamp: %v", deleteTime, err)
		}
	}
}

// TestDeleteHandler_HappyPath_UserIDPassedThrough verifies that user_id is
// correctly present in the FileDeleted event.
func TestDeleteHandler_HappyPath_UserIDPassedThrough(t *testing.T) {
	producer := defaultProducer()
	handler := NewDeleteHandler(defaultDeleteMetadataLookup(), defaultObjectRemover(), defaultFileDeleter(), producer, "media-uploads")

	payload := validDeletePayload()
	payload["user_id"] = "user-delete-777"

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	userID, _ := producer.calls[0].event["user_id"].(string)
	if userID != "user-delete-777" {
		t.Errorf("expected user_id 'user-delete-777', got %q", userID)
	}
}

// TestDeleteHandler_HappyPath_FilePathPassedThrough verifies that file_path is
// correctly present in the FileDeleted event.
func TestDeleteHandler_HappyPath_FilePathPassedThrough(t *testing.T) {
	producer := defaultProducer()
	handler := NewDeleteHandler(defaultDeleteMetadataLookup(), defaultObjectRemover(), defaultFileDeleter(), producer, "media-uploads")

	payload := validDeletePayload()
	payload["file_path"] = "uploads/user-abc/uuid-999/audio.mp3"

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	filePath, _ := producer.calls[0].event["file_path"].(string)
	if filePath != "uploads/user-abc/uuid-999/audio.mp3" {
		t.Errorf("expected file_path 'uploads/user-abc/uuid-999/audio.mp3', got %q", filePath)
	}
}

// TestDeleteHandler_HappyPath_RemoveObjectCalled verifies that
// ObjectRemover.RemoveObject is called with the correct bucket and file_path.
func TestDeleteHandler_HappyPath_RemoveObjectCalled(t *testing.T) {
	remover := defaultObjectRemover()
	handler := NewDeleteHandler(defaultDeleteMetadataLookup(), remover, defaultFileDeleter(), defaultProducer(), "my-bucket")

	payload := validDeletePayload()
	payload["file_path"] = "uploads/user-abc/uuid-123/photo.jpg"

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	if remover.callCount != 1 {
		t.Errorf("expected 1 RemoveObject call, got %d", remover.callCount)
	}
	if remover.capturedBucket != "my-bucket" {
		t.Errorf("expected bucket 'my-bucket', got %q", remover.capturedBucket)
	}
	if remover.capturedObjectKey != "uploads/user-abc/uuid-123/photo.jpg" {
		t.Errorf("expected objectKey 'uploads/user-abc/uuid-123/photo.jpg', got %q", remover.capturedObjectKey)
	}
}

// TestDeleteHandler_HappyPath_SoftDeleteCalled verifies that
// FileDeleter.SoftDelete is called with the correct user_id and file_path.
func TestDeleteHandler_HappyPath_SoftDeleteCalled(t *testing.T) {
	deleter := defaultFileDeleter()
	handler := NewDeleteHandler(defaultDeleteMetadataLookup(), defaultObjectRemover(), deleter, defaultProducer(), "media-uploads")

	payload := validDeletePayload()
	payload["user_id"] = "user-soft-delete"
	payload["file_path"] = "uploads/user-soft-delete/uuid-555/file.jpg"

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	if deleter.callCount != 1 {
		t.Errorf("expected 1 SoftDelete call, got %d", deleter.callCount)
	}
	if deleter.capturedUserID != "user-soft-delete" {
		t.Errorf("expected userID 'user-soft-delete', got %q", deleter.capturedUserID)
	}
	if deleter.capturedFilePath != "uploads/user-soft-delete/uuid-555/file.jpg" {
		t.Errorf("expected filePath 'uploads/user-soft-delete/uuid-555/file.jpg', got %q", deleter.capturedFilePath)
	}
}

// TestDeleteHandler_HappyPath_OrderOfOperations verifies that RemoveObject is
// called before SoftDelete (MinIO before DB soft-delete for safety).
func TestDeleteHandler_HappyPath_OrderOfOperations(t *testing.T) {
	callOrder := []string{}

	remover := &mockObjectRemoverOrdered{onCall: func() { callOrder = append(callOrder, "remover") }}
	deleter := &mockFileDeleterOrdered{onCall: func() { callOrder = append(callOrder, "deleter") }}
	handler := NewDeleteHandler(defaultDeleteMetadataLookup(), remover, deleter, defaultProducer(), "media-uploads")

	if err := handler.Handle(context.Background(), validDeletePayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	if len(callOrder) != 2 {
		t.Fatalf("expected 2 operations, got %d: %v", len(callOrder), callOrder)
	}
	if callOrder[0] != "remover" || callOrder[1] != "deleter" {
		t.Errorf("expected [remover, deleter] order, got %v", callOrder)
	}
}

// mockObjectRemoverOrdered and mockFileDeleterOrdered are helpers for order-of-operations test.
type mockObjectRemoverOrdered struct {
	onCall func()
}

func (m *mockObjectRemoverOrdered) RemoveObject(ctx context.Context, bucket, objectKey string) error {
	m.onCall()
	return nil
}

type mockFileDeleterOrdered struct {
	onCall func()
}

func (m *mockFileDeleterOrdered) SoftDelete(ctx context.Context, userID, filePath string) error {
	m.onCall()
	return nil
}

// ---------------------------------------------------------------------------
// Input Validation Tests
// ---------------------------------------------------------------------------

func TestDeleteHandler_Validation_MissingUserID(t *testing.T) {
	handler := defaultDeleteHandler()
	payload := validDeletePayload()
	delete(payload, "user_id")

	err := handler.Handle(context.Background(), payload)
	if err == nil {
		t.Error("expected error for missing user_id, got nil")
	}
}

func TestDeleteHandler_Validation_MissingFilePath(t *testing.T) {
	handler := defaultDeleteHandler()
	payload := validDeletePayload()
	delete(payload, "file_path")

	err := handler.Handle(context.Background(), payload)
	if err == nil {
		t.Error("expected error for missing file_path, got nil")
	}
}

func TestDeleteHandler_Validation_MissingRequestID(t *testing.T) {
	handler := defaultDeleteHandler()
	payload := validDeletePayload()
	delete(payload, "request_id")

	err := handler.Handle(context.Background(), payload)
	if err == nil {
		t.Error("expected error for missing request_id, got nil")
	}
}

func TestDeleteHandler_Validation_EmptyPayload(t *testing.T) {
	handler := defaultDeleteHandler()
	err := handler.Handle(context.Background(), map[string]any{})
	if err == nil {
		t.Error("expected error for empty payload, got nil")
	}
}

// TestDeleteHandler_Validation_RequiredFields uses a table to verify all
// required fields individually cause errors when absent.
func TestDeleteHandler_Validation_RequiredFields(t *testing.T) {
	tests := []struct {
		name        string
		omitField   string
		replaceWith any
	}{
		{"missing user_id", "user_id", nil},
		{"missing file_path", "file_path", nil},
		{"missing request_id", "request_id", nil},
		{"empty user_id", "user_id", ""},
		{"empty file_path", "file_path", ""},
		{"user_id as int", "user_id", 42},
		{"file_path as int", "file_path", 42},
		{"request_id as int", "request_id", 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := defaultDeleteHandler()
			payload := validDeletePayload()
			if tt.replaceWith == nil {
				delete(payload, tt.omitField)
			} else {
				payload[tt.omitField] = tt.replaceWith
			}

			err := handler.Handle(context.Background(), payload)
			if err == nil {
				t.Errorf("expected error for %s, got nil", tt.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// File Not Found (Rejection) Tests
// ---------------------------------------------------------------------------

// TestDeleteHandler_FileNotFound_EmitsRejectedEvent verifies that when
// GetFileMetadata returns nil, a FileDeleteRejected event is emitted.
func TestDeleteHandler_FileNotFound_EmitsRejectedEvent(t *testing.T) {
	lookup := &mockFileMetadataLookup{metadata: nil}
	producer := defaultProducer()
	handler := NewDeleteHandler(lookup, defaultObjectRemover(), defaultFileDeleter(), producer, "media-uploads")

	err := handler.Handle(context.Background(), validDeletePayload())
	if err != nil {
		t.Fatalf("Handle() returned unexpected error on not-found: %v", err)
	}

	if len(producer.calls) != 1 {
		t.Fatalf("expected 1 Produce call (rejected event), got %d", len(producer.calls))
	}

	if producer.calls[0].topic != "public.media.delete.rejected" {
		t.Errorf("expected topic 'public.media.delete.rejected', got %q", producer.calls[0].topic)
	}
}

// TestDeleteHandler_FileNotFound_NoRemoveObjectCalled verifies that
// ObjectRemover is NOT called when the file is not found.
func TestDeleteHandler_FileNotFound_NoRemoveObjectCalled(t *testing.T) {
	lookup := &mockFileMetadataLookup{metadata: nil}
	remover := defaultObjectRemover()
	handler := NewDeleteHandler(lookup, remover, defaultFileDeleter(), defaultProducer(), "media-uploads")

	_ = handler.Handle(context.Background(), validDeletePayload())

	if remover.callCount > 0 {
		t.Errorf("expected RemoveObject NOT to be called when file not found, got %d calls", remover.callCount)
	}
}

// TestDeleteHandler_FileNotFound_NoSoftDeleteCalled verifies that
// FileDeleter is NOT called when the file is not found.
func TestDeleteHandler_FileNotFound_NoSoftDeleteCalled(t *testing.T) {
	lookup := &mockFileMetadataLookup{metadata: nil}
	deleter := defaultFileDeleter()
	handler := NewDeleteHandler(lookup, defaultObjectRemover(), deleter, defaultProducer(), "media-uploads")

	_ = handler.Handle(context.Background(), validDeletePayload())

	if deleter.callCount > 0 {
		t.Errorf("expected SoftDelete NOT to be called when file not found, got %d calls", deleter.callCount)
	}
}

// TestDeleteHandler_FileNotFound_RejectionReasonIsNotFound verifies that
// the rejection event has reason="not_found".
func TestDeleteHandler_FileNotFound_RejectionReasonIsNotFound(t *testing.T) {
	lookup := &mockFileMetadataLookup{metadata: nil}
	producer := defaultProducer()
	handler := NewDeleteHandler(lookup, defaultObjectRemover(), defaultFileDeleter(), producer, "media-uploads")

	_ = handler.Handle(context.Background(), validDeletePayload())

	if len(producer.calls) == 0 {
		t.Fatal("no events produced")
	}

	reason, _ := producer.calls[0].event["reason"].(string)
	if reason != "not_found" {
		t.Errorf("expected reason 'not_found', got %q", reason)
	}
}

// TestDeleteHandler_FileNotFound_RejectedEventPreservesRequestID verifies
// that the rejection event preserves request_id from the input.
func TestDeleteHandler_FileNotFound_RejectedEventPreservesRequestID(t *testing.T) {
	lookup := &mockFileMetadataLookup{metadata: nil}
	producer := defaultProducer()
	handler := NewDeleteHandler(lookup, defaultObjectRemover(), defaultFileDeleter(), producer, "media-uploads")

	payload := validDeletePayload()
	payload["request_id"] = "delete-correlation-id-not-found"

	_ = handler.Handle(context.Background(), payload)

	if len(producer.calls) == 0 {
		t.Fatal("no events produced")
	}

	requestID, _ := producer.calls[0].event["request_id"].(string)
	if requestID != "delete-correlation-id-not-found" {
		t.Errorf("expected request_id 'delete-correlation-id-not-found' in rejected event, got %q", requestID)
	}
}

// TestDeleteHandler_FileNotFound_RejectedEventHasRejectedTime verifies that
// the rejection event has a valid rejected_time field.
func TestDeleteHandler_FileNotFound_RejectedEventHasRejectedTime(t *testing.T) {
	lookup := &mockFileMetadataLookup{metadata: nil}
	producer := defaultProducer()
	handler := NewDeleteHandler(lookup, defaultObjectRemover(), defaultFileDeleter(), producer, "media-uploads")

	_ = handler.Handle(context.Background(), validDeletePayload())

	if len(producer.calls) == 0 {
		t.Fatal("no events produced")
	}

	rejectedTime, _ := producer.calls[0].event["rejected_time"].(string)
	if rejectedTime == "" {
		t.Fatal("rejected_time is empty")
	}

	_, err := time.Parse(time.RFC3339, rejectedTime)
	if err != nil {
		_, err = time.Parse(time.RFC3339Nano, rejectedTime)
		if err != nil {
			t.Errorf("rejected_time %q is not a valid ISO 8601 timestamp: %v", rejectedTime, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Error Tests (MinIO and DB failures)
// ---------------------------------------------------------------------------

// TestDeleteHandler_MetadataLookupError_ReturnsError verifies that an internal
// error from GetFileMetadata is propagated.
func TestDeleteHandler_MetadataLookupError_ReturnsError(t *testing.T) {
	lookup := &mockFileMetadataLookup{err: errors.New("db connection error")}
	handler := NewDeleteHandler(lookup, defaultObjectRemover(), defaultFileDeleter(), defaultProducer(), "media-uploads")

	err := handler.Handle(context.Background(), validDeletePayload())
	if err == nil {
		t.Error("expected error when metadata lookup fails, got nil")
	}
}

// TestDeleteHandler_MetadataLookupError_NoOperationsCalled verifies that
// neither RemoveObject nor SoftDelete are called when lookup fails.
func TestDeleteHandler_MetadataLookupError_NoOperationsCalled(t *testing.T) {
	lookup := &mockFileMetadataLookup{err: errors.New("db timeout")}
	remover := defaultObjectRemover()
	deleter := defaultFileDeleter()
	handler := NewDeleteHandler(lookup, remover, deleter, defaultProducer(), "media-uploads")

	_ = handler.Handle(context.Background(), validDeletePayload())

	if remover.callCount > 0 {
		t.Errorf("expected RemoveObject NOT called on lookup error, got %d calls", remover.callCount)
	}
	if deleter.callCount > 0 {
		t.Errorf("expected SoftDelete NOT called on lookup error, got %d calls", deleter.callCount)
	}
}

// TestDeleteHandler_RemoveObjectError_EmitsRejectedEvent verifies that when
// RemoveObject fails, a FileDeleteRejected event is emitted (not returned as error).
func TestDeleteHandler_RemoveObjectError_EmitsRejectedEvent(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("MinIO connection failed")}
	producer := defaultProducer()
	handler := NewDeleteHandler(defaultDeleteMetadataLookup(), remover, defaultFileDeleter(), producer, "media-uploads")

	err := handler.Handle(context.Background(), validDeletePayload())
	if err != nil {
		t.Fatalf("Handle() returned error (expected rejection event): %v", err)
	}

	if len(producer.calls) != 1 {
		t.Fatalf("expected 1 Produce call (rejected event), got %d", len(producer.calls))
	}

	if producer.calls[0].topic != "public.media.delete.rejected" {
		t.Errorf("expected rejection topic, got %q", producer.calls[0].topic)
	}
}

// TestDeleteHandler_RemoveObjectError_ReasonIsInternalError verifies that when
// RemoveObject fails, reason="internal_error" in the rejection event.
func TestDeleteHandler_RemoveObjectError_ReasonIsInternalError(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("S3 error")}
	producer := defaultProducer()
	handler := NewDeleteHandler(defaultDeleteMetadataLookup(), remover, defaultFileDeleter(), producer, "media-uploads")

	_ = handler.Handle(context.Background(), validDeletePayload())

	if len(producer.calls) == 0 {
		t.Fatal("no events produced")
	}

	reason, _ := producer.calls[0].event["reason"].(string)
	if reason != "internal_error" {
		t.Errorf("expected reason 'internal_error' on RemoveObject failure, got %q", reason)
	}
}

// TestDeleteHandler_RemoveObjectError_NoSoftDeleteCalled verifies that
// SoftDelete is NOT called when RemoveObject fails.
func TestDeleteHandler_RemoveObjectError_NoSoftDeleteCalled(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("MinIO down")}
	deleter := defaultFileDeleter()
	handler := NewDeleteHandler(defaultDeleteMetadataLookup(), remover, deleter, defaultProducer(), "media-uploads")

	_ = handler.Handle(context.Background(), validDeletePayload())

	if deleter.callCount > 0 {
		t.Errorf("expected SoftDelete NOT called when RemoveObject fails, got %d calls", deleter.callCount)
	}
}

// TestDeleteHandler_SoftDeleteError_ReturnsError verifies that when SoftDelete
// fails, the handler returns an error.
func TestDeleteHandler_SoftDeleteError_ReturnsError(t *testing.T) {
	deleter := &mockFileDeleter{err: errors.New("DB update failed")}
	handler := NewDeleteHandler(defaultDeleteMetadataLookup(), defaultObjectRemover(), deleter, defaultProducer(), "media-uploads")

	err := handler.Handle(context.Background(), validDeletePayload())
	if err == nil {
		t.Error("expected error when SoftDelete fails, got nil")
	}
}

// TestDeleteHandler_ProducerError_ReturnsError verifies that when EventProducer
// returns an error, the handler returns that error.
func TestDeleteHandler_ProducerError_ReturnsError(t *testing.T) {
	producer := &mockEventProducer{err: errors.New("Kafka broker unavailable")}
	handler := NewDeleteHandler(defaultDeleteMetadataLookup(), defaultObjectRemover(), defaultFileDeleter(), producer, "media-uploads")

	err := handler.Handle(context.Background(), validDeletePayload())
	if err == nil {
		t.Error("expected error when EventProducer fails, got nil")
	}
}

// ---------------------------------------------------------------------------
// Constructor Tests
// ---------------------------------------------------------------------------

// TestNewDeleteHandler_ReturnsNonNil verifies that NewDeleteHandler returns
// a non-nil handler.
func TestNewDeleteHandler_ReturnsNonNil(t *testing.T) {
	h := NewDeleteHandler(defaultDeleteMetadataLookup(), defaultObjectRemover(), defaultFileDeleter(), defaultProducer(), "bucket")
	if h == nil {
		t.Error("NewDeleteHandler() returned nil")
	}
}

// TestNewDeleteHandler_ImplementsMessageHandler verifies that *DeleteHandler
// satisfies the MessageHandler interface at compile time.
func TestNewDeleteHandler_ImplementsMessageHandler(t *testing.T) {
	var _ MessageHandler = NewDeleteHandler(defaultDeleteMetadataLookup(), defaultObjectRemover(), defaultFileDeleter(), defaultProducer(), "bucket")
}
