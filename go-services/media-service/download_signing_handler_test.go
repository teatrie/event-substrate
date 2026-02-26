package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Mock implementations for DownloadSigningHandler
// ---------------------------------------------------------------------------

// mockDownloadURLSigner implements DownloadURLSigner for testing.
type mockDownloadURLSigner struct {
	url string
	err error

	capturedBucket    string
	capturedObjectKey string
	capturedExpiry    time.Duration
}

func (m *mockDownloadURLSigner) PresignedGetURL(ctx context.Context, bucket, objectKey string, expiry time.Duration) (string, error) {
	m.capturedBucket = bucket
	m.capturedObjectKey = objectKey
	m.capturedExpiry = expiry
	if m.err != nil {
		return "", m.err
	}
	return m.url, nil
}

// mockFileMetadataLookup implements FileMetadataLookup for testing.
// Shared by both download and delete handler tests.
type mockFileMetadataLookup struct {
	metadata *FileMetadata
	err      error

	capturedUserID   string
	capturedFilePath string
	callCount        int
}

func (m *mockFileMetadataLookup) GetFileMetadata(ctx context.Context, userID, filePath string) (*FileMetadata, error) {
	m.capturedUserID = userID
	m.capturedFilePath = filePath
	m.callCount++
	if m.err != nil {
		return nil, m.err
	}
	return m.metadata, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func defaultDownloadSigner() *mockDownloadURLSigner {
	return &mockDownloadURLSigner{url: "https://minio.example.com/presigned-get-url?X-Amz-Signature=xyz789"}
}

func defaultMetadataLookup() *mockFileMetadataLookup {
	return &mockFileMetadataLookup{
		metadata: &FileMetadata{
			FileName:  "photo.jpg",
			MediaType: "image/jpeg",
			FileSize:  int64(2048000),
		},
	}
}

func defaultDownloadHandler() *DownloadSigningHandler {
	return NewDownloadSigningHandler(defaultDownloadSigner(), defaultMetadataLookup(), defaultProducer(), "media-uploads", 3600)
}

func validDownloadPayload() map[string]any {
	return map[string]any{
		"user_id":      "user-abc-123",
		"file_path":    "uploads/user-abc-123/some-uuid/photo.jpg",
		"request_id":   "req-download-456",
		"request_time": "2026-02-25T10:00:00Z",
	}
}

// ---------------------------------------------------------------------------
// Happy Path Tests
// ---------------------------------------------------------------------------

// TestDownloadSigningHandler_HappyPath_ProducesCorrectEvent verifies that a valid
// DownloadIntent causes a FileDownloadUrlSigned event to be produced on the correct
// Kafka topic with all required fields populated.
func TestDownloadSigningHandler_HappyPath_ProducesCorrectEvent(t *testing.T) {
	producer := defaultProducer()
	handler := NewDownloadSigningHandler(defaultDownloadSigner(), defaultMetadataLookup(), producer, "media-uploads", 3600)

	err := handler.Handle(context.Background(), validDownloadPayload())
	if err != nil {
		t.Fatalf("Handle() returned unexpected error: %v", err)
	}

	if len(producer.calls) != 1 {
		t.Fatalf("expected 1 Produce call, got %d", len(producer.calls))
	}

	call := producer.calls[0]

	if call.topic != "public.media.download.signed" {
		t.Errorf("expected topic 'public.media.download.signed', got %q", call.topic)
	}

	for _, field := range []string{"user_id", "file_path", "download_url", "request_id", "signed_time"} {
		val, ok := call.event[field]
		if !ok {
			t.Errorf("produced event missing field %q", field)
			continue
		}
		str, isStr := val.(string)
		if !isStr || str == "" {
			t.Errorf("produced event field %q is empty or not a string: %v", field, val)
		}
	}

	expiresIn, ok := call.event["expires_in"]
	if !ok {
		t.Error("produced event missing field 'expires_in'")
	} else if _, isInt := expiresIn.(int); !isInt {
		t.Errorf("expires_in should be int, got %T", expiresIn)
	}
}

// TestDownloadSigningHandler_HappyPath_DownloadURLFromSigner verifies that the
// download_url in the produced event is the URL returned by the DownloadURLSigner.
func TestDownloadSigningHandler_HappyPath_DownloadURLFromSigner(t *testing.T) {
	signer := &mockDownloadURLSigner{url: "https://minio.internal/bucket/key?sig=getpresigned"}
	producer := defaultProducer()
	handler := NewDownloadSigningHandler(signer, defaultMetadataLookup(), producer, "media-uploads", 3600)

	if err := handler.Handle(context.Background(), validDownloadPayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	downloadURL, _ := producer.calls[0].event["download_url"].(string)
	if downloadURL != "https://minio.internal/bucket/key?sig=getpresigned" {
		t.Errorf("expected download_url from signer, got %q", downloadURL)
	}
}

// TestDownloadSigningHandler_HappyPath_ExpiresInMatchesConfig verifies that
// expires_in in the produced event matches the configured expiry seconds.
func TestDownloadSigningHandler_HappyPath_ExpiresInMatchesConfig(t *testing.T) {
	producer := defaultProducer()
	handler := NewDownloadSigningHandler(defaultDownloadSigner(), defaultMetadataLookup(), producer, "media-uploads", 7200)

	if err := handler.Handle(context.Background(), validDownloadPayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	expiresIn, _ := producer.calls[0].event["expires_in"].(int)
	if expiresIn != 7200 {
		t.Errorf("expected expires_in = 7200, got %d", expiresIn)
	}
}

// TestDownloadSigningHandler_HappyPath_SignedTimeIsISO8601 verifies that
// signed_time is a valid ISO 8601 timestamp.
func TestDownloadSigningHandler_HappyPath_SignedTimeIsISO8601(t *testing.T) {
	producer := defaultProducer()
	handler := NewDownloadSigningHandler(defaultDownloadSigner(), defaultMetadataLookup(), producer, "media-uploads", 3600)

	if err := handler.Handle(context.Background(), validDownloadPayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	signedTime, _ := producer.calls[0].event["signed_time"].(string)
	if signedTime == "" {
		t.Fatal("signed_time is empty")
	}

	_, err := time.Parse(time.RFC3339, signedTime)
	if err != nil {
		_, err = time.Parse(time.RFC3339Nano, signedTime)
		if err != nil {
			t.Errorf("signed_time %q is not a valid ISO 8601 timestamp: %v", signedTime, err)
		}
	}
}

// TestDownloadSigningHandler_HappyPath_RequestIDPassedThrough verifies that
// request_id from the input event is passed through unchanged.
func TestDownloadSigningHandler_HappyPath_RequestIDPassedThrough(t *testing.T) {
	producer := defaultProducer()
	handler := NewDownloadSigningHandler(defaultDownloadSigner(), defaultMetadataLookup(), producer, "media-uploads", 3600)

	payload := validDownloadPayload()
	payload["request_id"] = "unique-download-correlation-id"

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	requestID, _ := producer.calls[0].event["request_id"].(string)
	if requestID != "unique-download-correlation-id" {
		t.Errorf("expected request_id 'unique-download-correlation-id', got %q", requestID)
	}
}

// TestDownloadSigningHandler_HappyPath_UserIDPassedThrough verifies that
// user_id is passed through correctly in the produced event.
func TestDownloadSigningHandler_HappyPath_UserIDPassedThrough(t *testing.T) {
	producer := defaultProducer()
	handler := NewDownloadSigningHandler(defaultDownloadSigner(), defaultMetadataLookup(), producer, "media-uploads", 3600)

	payload := validDownloadPayload()
	payload["user_id"] = "user-download-999"

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	userID, _ := producer.calls[0].event["user_id"].(string)
	if userID != "user-download-999" {
		t.Errorf("expected user_id 'user-download-999', got %q", userID)
	}
}

// TestDownloadSigningHandler_HappyPath_FilePathPassedThrough verifies that
// file_path is passed through correctly in the produced event.
func TestDownloadSigningHandler_HappyPath_FilePathPassedThrough(t *testing.T) {
	producer := defaultProducer()
	handler := NewDownloadSigningHandler(defaultDownloadSigner(), defaultMetadataLookup(), producer, "media-uploads", 3600)

	payload := validDownloadPayload()
	payload["file_path"] = "uploads/user-abc/uuid-123/video.mp4"

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	filePath, _ := producer.calls[0].event["file_path"].(string)
	if filePath != "uploads/user-abc/uuid-123/video.mp4" {
		t.Errorf("expected file_path 'uploads/user-abc/uuid-123/video.mp4', got %q", filePath)
	}
}

// TestDownloadSigningHandler_HappyPath_KafkaTopicIsDownloadSigned verifies that
// the event is produced to 'public.media.download.signed'.
func TestDownloadSigningHandler_HappyPath_KafkaTopicIsDownloadSigned(t *testing.T) {
	producer := defaultProducer()
	handler := NewDownloadSigningHandler(defaultDownloadSigner(), defaultMetadataLookup(), producer, "media-uploads", 3600)

	if err := handler.Handle(context.Background(), validDownloadPayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	if producer.calls[0].topic != "public.media.download.signed" {
		t.Errorf("expected topic 'public.media.download.signed', got %q", producer.calls[0].topic)
	}
}

// TestDownloadSigningHandler_HappyPath_MetadataLookupCalledWithCorrectArgs verifies
// that GetFileMetadata is called with user_id and file_path from the event.
func TestDownloadSigningHandler_HappyPath_MetadataLookupCalledWithCorrectArgs(t *testing.T) {
	lookup := defaultMetadataLookup()
	handler := NewDownloadSigningHandler(defaultDownloadSigner(), lookup, defaultProducer(), "media-uploads", 3600)

	payload := validDownloadPayload()
	payload["user_id"] = "user-lookup-test"
	payload["file_path"] = "uploads/user-lookup-test/uuid/file.jpg"

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	if lookup.capturedUserID != "user-lookup-test" {
		t.Errorf("expected lookup userID 'user-lookup-test', got %q", lookup.capturedUserID)
	}
	if lookup.capturedFilePath != "uploads/user-lookup-test/uuid/file.jpg" {
		t.Errorf("expected lookup filePath 'uploads/user-lookup-test/uuid/file.jpg', got %q", lookup.capturedFilePath)
	}
}

// ---------------------------------------------------------------------------
// Input Validation Tests
// ---------------------------------------------------------------------------

func TestDownloadSigningHandler_Validation_MissingUserID(t *testing.T) {
	handler := defaultDownloadHandler()
	payload := validDownloadPayload()
	delete(payload, "user_id")

	err := handler.Handle(context.Background(), payload)
	if err == nil {
		t.Error("expected error for missing user_id, got nil")
	}
}

func TestDownloadSigningHandler_Validation_MissingFilePath(t *testing.T) {
	handler := defaultDownloadHandler()
	payload := validDownloadPayload()
	delete(payload, "file_path")

	err := handler.Handle(context.Background(), payload)
	if err == nil {
		t.Error("expected error for missing file_path, got nil")
	}
}

func TestDownloadSigningHandler_Validation_MissingRequestID(t *testing.T) {
	handler := defaultDownloadHandler()
	payload := validDownloadPayload()
	delete(payload, "request_id")

	err := handler.Handle(context.Background(), payload)
	if err == nil {
		t.Error("expected error for missing request_id, got nil")
	}
}

func TestDownloadSigningHandler_Validation_EmptyUserID(t *testing.T) {
	handler := defaultDownloadHandler()
	payload := validDownloadPayload()
	payload["user_id"] = ""

	err := handler.Handle(context.Background(), payload)
	if err == nil {
		t.Error("expected error for empty user_id, got nil")
	}
}

func TestDownloadSigningHandler_Validation_EmptyFilePath(t *testing.T) {
	handler := defaultDownloadHandler()
	payload := validDownloadPayload()
	payload["file_path"] = ""

	err := handler.Handle(context.Background(), payload)
	if err == nil {
		t.Error("expected error for empty file_path, got nil")
	}
}

func TestDownloadSigningHandler_Validation_WrongTypeUserID(t *testing.T) {
	handler := defaultDownloadHandler()
	payload := validDownloadPayload()
	payload["user_id"] = 12345

	err := handler.Handle(context.Background(), payload)
	if err == nil {
		t.Error("expected error when user_id is int (wrong type), got nil")
	}
}

func TestDownloadSigningHandler_Validation_EmptyPayload(t *testing.T) {
	handler := defaultDownloadHandler()
	err := handler.Handle(context.Background(), map[string]any{})
	if err == nil {
		t.Error("expected error for empty payload, got nil")
	}
}

// TestDownloadSigningHandler_Validation_RequiredFields uses a table to verify all
// required fields individually cause errors when absent.
func TestDownloadSigningHandler_Validation_RequiredFields(t *testing.T) {
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
			handler := defaultDownloadHandler()
			payload := validDownloadPayload()
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

// TestDownloadSigningHandler_FileNotFound_EmitsRejectedEvent verifies that when
// GetFileMetadata returns nil (file not found/not owned), a FileDownloadRejected
// event is emitted and Handle returns nil (event-based rejection).
func TestDownloadSigningHandler_FileNotFound_EmitsRejectedEvent(t *testing.T) {
	lookup := &mockFileMetadataLookup{metadata: nil}
	producer := defaultProducer()
	handler := NewDownloadSigningHandler(defaultDownloadSigner(), lookup, producer, "media-uploads", 3600)

	err := handler.Handle(context.Background(), validDownloadPayload())
	if err != nil {
		t.Fatalf("Handle() returned unexpected error on not-found: %v", err)
	}

	if len(producer.calls) != 1 {
		t.Fatalf("expected 1 Produce call (rejected event), got %d", len(producer.calls))
	}

	if producer.calls[0].topic != "public.media.download.rejected" {
		t.Errorf("expected topic 'public.media.download.rejected', got %q", producer.calls[0].topic)
	}
}

// TestDownloadSigningHandler_FileNotFound_NoSignerCalled verifies that the
// DownloadURLSigner is NOT called when the file is not found.
func TestDownloadSigningHandler_FileNotFound_NoSignerCalled(t *testing.T) {
	lookup := &mockFileMetadataLookup{metadata: nil}
	signer := defaultDownloadSigner()
	handler := NewDownloadSigningHandler(signer, lookup, defaultProducer(), "media-uploads", 3600)

	_ = handler.Handle(context.Background(), validDownloadPayload())

	if signer.capturedBucket != "" {
		t.Error("expected signer NOT to be called when file not found")
	}
}

// TestDownloadSigningHandler_FileNotFound_RejectionHasReasonNotFound verifies
// that the rejection event has reason="not_found".
func TestDownloadSigningHandler_FileNotFound_RejectionHasReasonNotFound(t *testing.T) {
	lookup := &mockFileMetadataLookup{metadata: nil}
	producer := defaultProducer()
	handler := NewDownloadSigningHandler(defaultDownloadSigner(), lookup, producer, "media-uploads", 3600)

	_ = handler.Handle(context.Background(), validDownloadPayload())

	if len(producer.calls) == 0 {
		t.Fatal("no events produced")
	}

	reason, _ := producer.calls[0].event["reason"].(string)
	if reason != "not_found" {
		t.Errorf("expected reason 'not_found', got %q", reason)
	}
}

// TestDownloadSigningHandler_FileNotFound_RejectionHasRejectedTime verifies
// that the rejection event has a valid rejected_time field.
func TestDownloadSigningHandler_FileNotFound_RejectionHasRejectedTime(t *testing.T) {
	lookup := &mockFileMetadataLookup{metadata: nil}
	producer := defaultProducer()
	handler := NewDownloadSigningHandler(defaultDownloadSigner(), lookup, producer, "media-uploads", 3600)

	_ = handler.Handle(context.Background(), validDownloadPayload())

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

// TestDownloadSigningHandler_FileNotFound_RejectionPreservesRequestID verifies
// that the rejection event preserves the request_id from the input.
func TestDownloadSigningHandler_FileNotFound_RejectionPreservesRequestID(t *testing.T) {
	lookup := &mockFileMetadataLookup{metadata: nil}
	producer := defaultProducer()
	handler := NewDownloadSigningHandler(defaultDownloadSigner(), lookup, producer, "media-uploads", 3600)

	payload := validDownloadPayload()
	payload["request_id"] = "correlation-id-not-found"

	_ = handler.Handle(context.Background(), payload)

	if len(producer.calls) == 0 {
		t.Fatal("no events produced")
	}

	requestID, _ := producer.calls[0].event["request_id"].(string)
	if requestID != "correlation-id-not-found" {
		t.Errorf("expected request_id 'correlation-id-not-found' in rejected event, got %q", requestID)
	}
}

// ---------------------------------------------------------------------------
// Signer Error Tests
// ---------------------------------------------------------------------------

// TestDownloadSigningHandler_SignerError_EmitsRejectedEvent verifies that when
// PresignedGetURL fails, a FileDownloadRejected event is emitted (not an error return).
func TestDownloadSigningHandler_SignerError_EmitsRejectedEvent(t *testing.T) {
	signer := &mockDownloadURLSigner{err: errors.New("MinIO connection timeout")}
	producer := defaultProducer()
	handler := NewDownloadSigningHandler(signer, defaultMetadataLookup(), producer, "media-uploads", 3600)

	err := handler.Handle(context.Background(), validDownloadPayload())
	if err != nil {
		t.Fatalf("Handle() returned error (expected rejection event instead): %v", err)
	}

	if len(producer.calls) != 1 {
		t.Fatalf("expected 1 Produce call (rejected event), got %d", len(producer.calls))
	}

	if producer.calls[0].topic != "public.media.download.rejected" {
		t.Errorf("expected rejection topic, got %q", producer.calls[0].topic)
	}
}

// TestDownloadSigningHandler_SignerError_ReasonIsInternalError verifies that
// when the signer fails, reason="internal_error" in the rejection event.
func TestDownloadSigningHandler_SignerError_ReasonIsInternalError(t *testing.T) {
	signer := &mockDownloadURLSigner{err: errors.New("S3 error")}
	producer := defaultProducer()
	handler := NewDownloadSigningHandler(signer, defaultMetadataLookup(), producer, "media-uploads", 3600)

	_ = handler.Handle(context.Background(), validDownloadPayload())

	if len(producer.calls) == 0 {
		t.Fatal("no events produced")
	}

	reason, _ := producer.calls[0].event["reason"].(string)
	if reason != "internal_error" {
		t.Errorf("expected reason 'internal_error' on signer failure, got %q", reason)
	}
}

// ---------------------------------------------------------------------------
// Metadata Lookup Error Tests
// ---------------------------------------------------------------------------

// TestDownloadSigningHandler_MetadataLookupError_ReturnsError verifies that
// an internal error from GetFileMetadata is propagated as a handler error.
func TestDownloadSigningHandler_MetadataLookupError_ReturnsError(t *testing.T) {
	lookup := &mockFileMetadataLookup{err: errors.New("db connection error")}
	handler := NewDownloadSigningHandler(defaultDownloadSigner(), lookup, defaultProducer(), "media-uploads", 3600)

	err := handler.Handle(context.Background(), validDownloadPayload())
	if err == nil {
		t.Error("expected error when metadata lookup fails, got nil")
	}
}

// TestDownloadSigningHandler_MetadataLookupError_NoEventProduced verifies that
// no event is produced when metadata lookup fails with an internal error.
func TestDownloadSigningHandler_MetadataLookupError_NoEventProduced(t *testing.T) {
	lookup := &mockFileMetadataLookup{err: errors.New("db timeout")}
	producer := defaultProducer()
	handler := NewDownloadSigningHandler(defaultDownloadSigner(), lookup, producer, "media-uploads", 3600)

	_ = handler.Handle(context.Background(), validDownloadPayload())

	if len(producer.calls) > 0 {
		t.Errorf("expected no Produce calls after lookup error, got %d", len(producer.calls))
	}
}

// TestDownloadSigningHandler_ProducerError_ReturnsError verifies that when the
// EventProducer returns an error, the handler returns that error.
func TestDownloadSigningHandler_ProducerError_ReturnsError(t *testing.T) {
	producer := &mockEventProducer{err: errors.New("Kafka broker unavailable")}
	handler := NewDownloadSigningHandler(defaultDownloadSigner(), defaultMetadataLookup(), producer, "media-uploads", 3600)

	err := handler.Handle(context.Background(), validDownloadPayload())
	if err == nil {
		t.Error("expected error when EventProducer fails, got nil")
	}
}

// ---------------------------------------------------------------------------
// Signer Argument Verification
// ---------------------------------------------------------------------------

// TestDownloadSigningHandler_SignerReceivesCorrectBucket verifies that the
// DownloadURLSigner is called with the configured bucket name.
func TestDownloadSigningHandler_SignerReceivesCorrectBucket(t *testing.T) {
	signer := defaultDownloadSigner()
	handler := NewDownloadSigningHandler(signer, defaultMetadataLookup(), defaultProducer(), "my-custom-bucket", 3600)

	if err := handler.Handle(context.Background(), validDownloadPayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	if signer.capturedBucket != "my-custom-bucket" {
		t.Errorf("expected signer bucket 'my-custom-bucket', got %q", signer.capturedBucket)
	}
}

// TestDownloadSigningHandler_SignerReceivesFilePathAsObjectKey verifies that
// the DownloadURLSigner is called with the file_path from the event as the objectKey.
func TestDownloadSigningHandler_SignerReceivesFilePathAsObjectKey(t *testing.T) {
	signer := defaultDownloadSigner()
	handler := NewDownloadSigningHandler(signer, defaultMetadataLookup(), defaultProducer(), "media-uploads", 3600)

	payload := validDownloadPayload()
	payload["file_path"] = "uploads/user-abc/uuid-456/video.mp4"

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	if signer.capturedObjectKey != "uploads/user-abc/uuid-456/video.mp4" {
		t.Errorf("expected signer objectKey to match file_path, got %q", signer.capturedObjectKey)
	}
}

// TestDownloadSigningHandler_SignerReceivesCorrectExpiry verifies that the
// DownloadURLSigner is called with the correct expiry duration.
func TestDownloadSigningHandler_SignerReceivesCorrectExpiry(t *testing.T) {
	signer := defaultDownloadSigner()
	handler := NewDownloadSigningHandler(signer, defaultMetadataLookup(), defaultProducer(), "media-uploads", 3600)

	if err := handler.Handle(context.Background(), validDownloadPayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	expected := 3600 * time.Second
	if signer.capturedExpiry != expected {
		t.Errorf("expected signer expiry %v, got %v", expected, signer.capturedExpiry)
	}
}

// ---------------------------------------------------------------------------
// Edge Case Tests
// ---------------------------------------------------------------------------

// TestDownloadSigningHandler_EdgeCase_ContextCancellation verifies that a
// cancelled context is handled gracefully (no panic).
func TestDownloadSigningHandler_EdgeCase_ContextCancellation(t *testing.T) {
	handler := defaultDownloadHandler()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = handler.Handle(ctx, validDownloadPayload())
}

// TestDownloadSigningHandler_EdgeCase_NilContext verifies that a nil context
// does not cause a panic.
func TestDownloadSigningHandler_EdgeCase_NilContext(t *testing.T) {
	handler := defaultDownloadHandler()

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Handle() panicked with nil context: %v", r)
		}
	}()

	//nolint:staticcheck
	_ = handler.Handle(nil, validDownloadPayload()) //nolint:staticcheck
}

// ---------------------------------------------------------------------------
// Constructor Tests
// ---------------------------------------------------------------------------

// TestNewDownloadSigningHandler_ReturnsNonNil verifies that NewDownloadSigningHandler
// returns a non-nil handler.
func TestNewDownloadSigningHandler_ReturnsNonNil(t *testing.T) {
	h := NewDownloadSigningHandler(defaultDownloadSigner(), defaultMetadataLookup(), defaultProducer(), "bucket", 3600)
	if h == nil {
		t.Error("NewDownloadSigningHandler() returned nil")
	}
}

// TestNewDownloadSigningHandler_ImplementsMessageHandler verifies that
// *DownloadSigningHandler satisfies the MessageHandler interface at compile time.
func TestNewDownloadSigningHandler_ImplementsMessageHandler(t *testing.T) {
	var _ MessageHandler = NewDownloadSigningHandler(defaultDownloadSigner(), defaultMetadataLookup(), defaultProducer(), "bucket", 3600)
}
