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

// TestDeleteHandler_HappyPath_OrderOfOperations verifies that SoftDelete runs
// first, then FileDeleted is emitted, then RemoveObject runs last
// (SoftDelete-first ordering for user-facing responsiveness).
func TestDeleteHandler_HappyPath_OrderOfOperations(t *testing.T) {
	callOrder := []string{}

	remover := &mockObjectRemoverOrdered{onCall: func() { callOrder = append(callOrder, "remover") }}
	deleter := &mockFileDeleterOrdered{onCall: func() { callOrder = append(callOrder, "deleter") }}
	producer := &mockEventProducerOrdered{onCall: func() { callOrder = append(callOrder, "producer") }}
	handler := NewDeleteHandler(defaultDeleteMetadataLookup(), remover, deleter, producer, "media-uploads")

	if err := handler.Handle(context.Background(), validDeletePayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	if len(callOrder) != 3 {
		t.Fatalf("expected 3 operations, got %d: %v", len(callOrder), callOrder)
	}
	if callOrder[0] != "deleter" || callOrder[1] != "producer" || callOrder[2] != "remover" {
		t.Errorf("expected [deleter, producer, remover] order, got %v", callOrder)
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

// mockEventProducerOrdered implements EventProducer for order-of-operations testing.
type mockEventProducerOrdered struct {
	onCall func()
}

func (m *mockEventProducerOrdered) Produce(ctx context.Context, topic string, event map[string]any) error {
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

// TestDeleteHandler_RemoveObjectError_EmitsRetryEvent verifies that when
// RemoveObject fails, a FileDeleteRetry event is emitted to the internal
// retry topic (not a rejection -- the user already got FileDeleted).
func TestDeleteHandler_RemoveObjectError_EmitsRetryEvent(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("MinIO connection failed")}
	producer := defaultProducer()
	handler := NewDeleteHandler(defaultDeleteMetadataLookup(), remover, defaultFileDeleter(), producer, "media-uploads")

	err := handler.Handle(context.Background(), validDeletePayload())
	if err != nil {
		t.Fatalf("Handle() returned error (expected retry event, no error): %v", err)
	}

	// The second Produce call should be the retry event.
	if len(producer.calls) < 2 {
		t.Fatalf("expected at least 2 Produce calls (FileDeleted + retry), got %d", len(producer.calls))
	}

	retryCall := producer.calls[1]
	if retryCall.topic != "internal.media.delete.retry" {
		t.Errorf("expected retry topic 'internal.media.delete.retry', got %q", retryCall.topic)
	}
}

// TestDeleteHandler_RemoveObjectError_RetryEventHasZeroRetryCount verifies
// that retry_count is int32(0) in the produced retry event.
func TestDeleteHandler_RemoveObjectError_RetryEventHasZeroRetryCount(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("S3 error")}
	producer := defaultProducer()
	handler := NewDeleteHandler(defaultDeleteMetadataLookup(), remover, defaultFileDeleter(), producer, "media-uploads")

	_ = handler.Handle(context.Background(), validDeletePayload())

	if len(producer.calls) < 2 {
		t.Fatalf("expected at least 2 Produce calls, got %d", len(producer.calls))
	}

	retryEvent := producer.calls[1].event
	retryCount, ok := retryEvent["retry_count"]
	if !ok {
		t.Fatal("retry event missing field 'retry_count'")
	}
	rc, isInt32 := retryCount.(int32)
	if !isInt32 {
		t.Fatalf("retry_count should be int32, got %T (%v)", retryCount, retryCount)
	}
	if rc != int32(0) {
		t.Errorf("expected retry_count = 0, got %d", rc)
	}
}

// TestDeleteHandler_RemoveObjectError_FileDeletedStillEmitted verifies that
// when RemoveObject fails, TWO events are produced: first FileDeleted on
// "public.media.delete.events", then FileDeleteRetry on "internal.media.delete.retry".
func TestDeleteHandler_RemoveObjectError_FileDeletedStillEmitted(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("MinIO unreachable")}
	producer := defaultProducer()
	handler := NewDeleteHandler(defaultDeleteMetadataLookup(), remover, defaultFileDeleter(), producer, "media-uploads")

	err := handler.Handle(context.Background(), validDeletePayload())
	if err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	if len(producer.calls) != 2 {
		t.Fatalf("expected exactly 2 Produce calls, got %d", len(producer.calls))
	}

	if producer.calls[0].topic != "public.media.delete.events" {
		t.Errorf("expected first event topic 'public.media.delete.events', got %q", producer.calls[0].topic)
	}
	if producer.calls[1].topic != "internal.media.delete.retry" {
		t.Errorf("expected second event topic 'internal.media.delete.retry', got %q", producer.calls[1].topic)
	}
}

// TestDeleteHandler_RemoveObjectError_RetryEventPreservesMetadata verifies
// that the retry event (second Produce call) contains user_id, file_path,
// file_name, media_type, file_size, and request_id from the original
// payload/metadata.
func TestDeleteHandler_RemoveObjectError_RetryEventPreservesMetadata(t *testing.T) {
	lookup := &mockFileMetadataLookup{
		metadata: &FileMetadata{
			FileName:  "vacation.mp4",
			MediaType: "video/mp4",
			FileSize:  int64(50000000),
		},
	}
	remover := &mockObjectRemover{err: errors.New("MinIO timeout")}
	producer := defaultProducer()
	handler := NewDeleteHandler(lookup, remover, defaultFileDeleter(), producer, "media-uploads")

	payload := validDeletePayload()
	payload["user_id"] = "user-retry-meta"
	payload["file_path"] = "uploads/user-retry-meta/uuid-111/vacation.mp4"
	payload["request_id"] = "req-retry-meta-001"

	err := handler.Handle(context.Background(), payload)
	if err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	if len(producer.calls) < 2 {
		t.Fatalf("expected at least 2 Produce calls, got %d", len(producer.calls))
	}

	retryEvent := producer.calls[1].event

	checks := map[string]string{
		"user_id":    "user-retry-meta",
		"file_path":  "uploads/user-retry-meta/uuid-111/vacation.mp4",
		"file_name":  "vacation.mp4",
		"media_type": "video/mp4",
		"request_id": "req-retry-meta-001",
	}

	for field, expected := range checks {
		val, ok := retryEvent[field]
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

	fileSize, ok := retryEvent["file_size"]
	if !ok {
		t.Error("retry event missing field 'file_size'")
	} else if fs, isInt64 := fileSize.(int64); !isInt64 {
		t.Errorf("retry event file_size should be int64, got %T", fileSize)
	} else if fs != int64(50000000) {
		t.Errorf("retry event file_size: expected 50000000, got %d", fs)
	}
}

// TestDeleteHandler_RemoveObjectError_RetryEventHasFailedAt verifies that the
// retry event has a valid ISO 8601 failed_at timestamp.
func TestDeleteHandler_RemoveObjectError_RetryEventHasFailedAt(t *testing.T) {
	remover := &mockObjectRemover{err: errors.New("MinIO 503")}
	producer := defaultProducer()
	handler := NewDeleteHandler(defaultDeleteMetadataLookup(), remover, defaultFileDeleter(), producer, "media-uploads")

	err := handler.Handle(context.Background(), validDeletePayload())
	if err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	if len(producer.calls) < 2 {
		t.Fatalf("expected at least 2 Produce calls, got %d", len(producer.calls))
	}

	retryEvent := producer.calls[1].event
	failedAt, ok := retryEvent["failed_at"]
	if !ok {
		t.Fatal("retry event missing field 'failed_at'")
	}

	failedAtStr, isStr := failedAt.(string)
	if !isStr || failedAtStr == "" {
		t.Fatalf("failed_at should be a non-empty string, got %T: %v", failedAt, failedAt)
	}

	_, err = time.Parse(time.RFC3339, failedAtStr)
	if err != nil {
		_, err = time.Parse(time.RFC3339Nano, failedAtStr)
		if err != nil {
			t.Errorf("failed_at %q is not a valid ISO 8601 timestamp: %v", failedAtStr, err)
		}
	}
}

// TestDeleteHandler_SoftDeleteError_EmitsRejectedEvent verifies that when
// SoftDelete fails, the handler does NOT return an error -- it emits a
// FileDeleteRejected event with reason="internal_error" to the rejected topic.
func TestDeleteHandler_SoftDeleteError_EmitsRejectedEvent(t *testing.T) {
	deleter := &mockFileDeleter{err: errors.New("DB update failed")}
	producer := defaultProducer()
	handler := NewDeleteHandler(defaultDeleteMetadataLookup(), defaultObjectRemover(), deleter, producer, "media-uploads")

	err := handler.Handle(context.Background(), validDeletePayload())
	if err != nil {
		t.Fatalf("Handle() returned error (expected rejected event, no error): %v", err)
	}

	if len(producer.calls) == 0 {
		t.Fatal("expected at least 1 Produce call (rejected event), got 0")
	}

	rejectedCall := producer.calls[0]
	if rejectedCall.topic != "public.media.delete.rejected" {
		t.Errorf("expected rejected topic 'public.media.delete.rejected', got %q", rejectedCall.topic)
	}

	reason, _ := rejectedCall.event["reason"].(string)
	if reason != "internal_error" {
		t.Errorf("expected reason 'internal_error' on SoftDelete failure, got %q", reason)
	}
}

// TestDeleteHandler_SoftDeleteError_NoRemoveObjectCalled verifies that when
// SoftDelete fails, RemoveObject is NOT called (we do not touch MinIO if DB fails).
func TestDeleteHandler_SoftDeleteError_NoRemoveObjectCalled(t *testing.T) {
	deleter := &mockFileDeleter{err: errors.New("DB connection lost")}
	remover := defaultObjectRemover()
	handler := NewDeleteHandler(defaultDeleteMetadataLookup(), remover, deleter, defaultProducer(), "media-uploads")

	_ = handler.Handle(context.Background(), validDeletePayload())

	if remover.callCount > 0 {
		t.Errorf("expected RemoveObject NOT called when SoftDelete fails, got %d calls", remover.callCount)
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
