package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

// mockURLSigner implements URLSigner for testing.
type mockURLSigner struct {
	url string
	err error

	capturedBucket      string
	capturedObjectKey   string
	capturedContentType string
	capturedExpiry      time.Duration
}

func (m *mockURLSigner) PresignedPutURL(ctx context.Context, bucket, objectKey, contentType string, expiry time.Duration) (string, error) {
	m.capturedBucket = bucket
	m.capturedObjectKey = objectKey
	m.capturedContentType = contentType
	m.capturedExpiry = expiry
	if m.err != nil {
		return "", m.err
	}
	return m.url, nil
}

// mockEventProducer implements EventProducer for testing.
type mockEventProducer struct {
	err            error
	calls          []producerCall
}

type producerCall struct {
	topic string
	event map[string]any
}

func (m *mockEventProducer) Produce(ctx context.Context, topic string, event map[string]any) error {
	m.calls = append(m.calls, producerCall{topic: topic, event: event})
	return m.err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func defaultSigner() *mockURLSigner {
	return &mockURLSigner{url: "https://minio.example.com/presigned-put-url?X-Amz-Signature=abc123"}
}

func defaultProducer() *mockEventProducer {
	return &mockEventProducer{}
}

func defaultHandler() *UploadSigningHandler {
	return NewUploadSigningHandler(defaultSigner(), defaultProducer(), "media-uploads", 900)
}

// validPayload returns a well-formed FileUploadApproved event payload.
func validPayload() map[string]any {
	return map[string]any{
		"user_id":      "user-abc-123",
		"file_name":    "photo.jpg",
		"media_type":   "image/jpeg",
		"file_size":    int64(2048000),
		"request_id":   "req-xyz-456",
		"request_time": "2026-02-25T10:00:00Z",
	}
}

// ---------------------------------------------------------------------------
// Happy Path Tests
// ---------------------------------------------------------------------------

// TestUploadSigningHandler_HappyPath_ProducesCorrectEvent verifies that a valid
// FileUploadApproved payload causes a FileUploadUrlSigned event to be produced
// on the correct Kafka topic with all required fields populated.
func TestUploadSigningHandler_HappyPath_ProducesCorrectEvent(t *testing.T) {
	producer := defaultProducer()
	handler := NewUploadSigningHandler(defaultSigner(), producer, "media-uploads", 900)

	err := handler.Handle(context.Background(), validPayload())
	if err != nil {
		t.Fatalf("Handle() returned unexpected error: %v", err)
	}

	if len(producer.calls) != 1 {
		t.Fatalf("expected 1 Produce call, got %d", len(producer.calls))
	}

	call := producer.calls[0]

	// Verify topic
	if call.topic != "public.media.upload.signed" {
		t.Errorf("expected topic 'public.media.upload.signed', got %q", call.topic)
	}

	// Verify required event fields are present and non-empty
	for _, field := range []string{"user_id", "file_path", "file_name", "upload_url", "request_id", "signed_time"} {
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

	// Verify expires_in is present and an int
	expiresIn, ok := call.event["expires_in"]
	if !ok {
		t.Error("produced event missing field 'expires_in'")
	} else if _, isInt := expiresIn.(int); !isInt {
		t.Errorf("expires_in should be int, got %T", expiresIn)
	}
}

// TestUploadSigningHandler_HappyPath_FilePathFormat verifies the generated
// file_path follows the pattern: uploads/{user_id}/{uuid}/{file_name}.
func TestUploadSigningHandler_HappyPath_FilePathFormat(t *testing.T) {
	producer := defaultProducer()
	handler := NewUploadSigningHandler(defaultSigner(), producer, "media-uploads", 900)

	payload := validPayload()
	payload["user_id"] = "user-abc-123"
	payload["file_name"] = "photo.jpg"

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() returned unexpected error: %v", err)
	}

	filePath, _ := producer.calls[0].event["file_path"].(string)

	// Pattern: uploads/{user_id}/{uuid}/{file_name}
	uuidPattern := `[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`
	pattern := fmt.Sprintf(`^uploads/user-abc-123/%s/photo\.jpg$`, uuidPattern)
	matched, err := regexp.MatchString(pattern, filePath)
	if err != nil {
		t.Fatalf("regex error: %v", err)
	}
	if !matched {
		t.Errorf("file_path %q does not match expected pattern %q", filePath, pattern)
	}
}

// TestUploadSigningHandler_HappyPath_FilePathContainsUUID verifies that the
// UUID segment in the file_path is unique (i.e. two calls produce different UUIDs).
func TestUploadSigningHandler_HappyPath_FilePathContainsUUID(t *testing.T) {
	producer := defaultProducer()
	handler := NewUploadSigningHandler(defaultSigner(), producer, "media-uploads", 900)

	if err := handler.Handle(context.Background(), validPayload()); err != nil {
		t.Fatalf("first Handle() error: %v", err)
	}
	if err := handler.Handle(context.Background(), validPayload()); err != nil {
		t.Fatalf("second Handle() error: %v", err)
	}

	path1, _ := producer.calls[0].event["file_path"].(string)
	path2, _ := producer.calls[1].event["file_path"].(string)

	if path1 == path2 {
		t.Errorf("expected unique file paths for each invocation, but both are %q", path1)
	}
}

// TestUploadSigningHandler_HappyPath_ExpiresInMatchesConfig verifies that the
// expires_in field in the produced event matches the configured expiry seconds.
func TestUploadSigningHandler_HappyPath_ExpiresInMatchesConfig(t *testing.T) {
	producer := defaultProducer()
	handler := NewUploadSigningHandler(defaultSigner(), producer, "media-uploads", 1800)

	if err := handler.Handle(context.Background(), validPayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	expiresIn, _ := producer.calls[0].event["expires_in"].(int)
	if expiresIn != 1800 {
		t.Errorf("expected expires_in = 1800, got %d", expiresIn)
	}
}

// TestUploadSigningHandler_HappyPath_SignedTimeIsISO8601 verifies that the
// signed_time field is a valid ISO 8601 timestamp.
func TestUploadSigningHandler_HappyPath_SignedTimeIsISO8601(t *testing.T) {
	producer := defaultProducer()
	handler := NewUploadSigningHandler(defaultSigner(), producer, "media-uploads", 900)

	if err := handler.Handle(context.Background(), validPayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	signedTime, _ := producer.calls[0].event["signed_time"].(string)
	if signedTime == "" {
		t.Fatal("signed_time is empty")
	}

	// Try parsing as RFC3339 (a superset of ISO 8601)
	_, err := time.Parse(time.RFC3339, signedTime)
	if err != nil {
		// Also try RFC3339Nano
		_, err = time.Parse(time.RFC3339Nano, signedTime)
		if err != nil {
			t.Errorf("signed_time %q is not a valid ISO 8601 timestamp: %v", signedTime, err)
		}
	}
}

// TestUploadSigningHandler_HappyPath_RequestIDPassedThrough verifies that the
// request_id from the input event is passed through unchanged in the output event.
func TestUploadSigningHandler_HappyPath_RequestIDPassedThrough(t *testing.T) {
	producer := defaultProducer()
	handler := NewUploadSigningHandler(defaultSigner(), producer, "media-uploads", 900)

	payload := validPayload()
	payload["request_id"] = "unique-correlation-id-789"

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	requestID, _ := producer.calls[0].event["request_id"].(string)
	if requestID != "unique-correlation-id-789" {
		t.Errorf("expected request_id 'unique-correlation-id-789', got %q", requestID)
	}
}

// TestUploadSigningHandler_HappyPath_KafkaTopicIsUploadSigned verifies that the
// event is produced to the exact topic 'public.media.upload.signed'.
func TestUploadSigningHandler_HappyPath_KafkaTopicIsUploadSigned(t *testing.T) {
	producer := defaultProducer()
	handler := NewUploadSigningHandler(defaultSigner(), producer, "media-uploads", 900)

	if err := handler.Handle(context.Background(), validPayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	if producer.calls[0].topic != "public.media.upload.signed" {
		t.Errorf("expected topic 'public.media.upload.signed', got %q", producer.calls[0].topic)
	}
}

// TestUploadSigningHandler_HappyPath_UserIDPassedThrough verifies that user_id
// is passed through correctly in the produced event.
func TestUploadSigningHandler_HappyPath_UserIDPassedThrough(t *testing.T) {
	producer := defaultProducer()
	handler := NewUploadSigningHandler(defaultSigner(), producer, "media-uploads", 900)

	payload := validPayload()
	payload["user_id"] = "user-def-999"

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	userID, _ := producer.calls[0].event["user_id"].(string)
	if userID != "user-def-999" {
		t.Errorf("expected user_id 'user-def-999', got %q", userID)
	}
}

// TestUploadSigningHandler_HappyPath_FileNamePassedThrough verifies that file_name
// is passed through correctly in the produced event.
func TestUploadSigningHandler_HappyPath_FileNamePassedThrough(t *testing.T) {
	producer := defaultProducer()
	handler := NewUploadSigningHandler(defaultSigner(), producer, "media-uploads", 900)

	payload := validPayload()
	payload["file_name"] = "my-video.mp4"

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	fileName, _ := producer.calls[0].event["file_name"].(string)
	if fileName != "my-video.mp4" {
		t.Errorf("expected file_name 'my-video.mp4', got %q", fileName)
	}
}

// TestUploadSigningHandler_HappyPath_UploadURLFromSigner verifies that the
// upload_url in the produced event is the URL returned by the URLSigner.
func TestUploadSigningHandler_HappyPath_UploadURLFromSigner(t *testing.T) {
	signer := &mockURLSigner{url: "https://minio.internal/bucket/key?sig=presigned"}
	producer := defaultProducer()
	handler := NewUploadSigningHandler(signer, producer, "media-uploads", 900)

	if err := handler.Handle(context.Background(), validPayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	uploadURL, _ := producer.calls[0].event["upload_url"].(string)
	if uploadURL != "https://minio.internal/bucket/key?sig=presigned" {
		t.Errorf("expected upload_url from signer, got %q", uploadURL)
	}
}

// ---------------------------------------------------------------------------
// Input Validation Tests
// ---------------------------------------------------------------------------

// TestUploadSigningHandler_Validation_MissingUserID verifies that a missing
// user_id field returns an error.
func TestUploadSigningHandler_Validation_MissingUserID(t *testing.T) {
	handler := defaultHandler()
	payload := validPayload()
	delete(payload, "user_id")

	err := handler.Handle(context.Background(), payload)
	if err == nil {
		t.Error("expected error for missing user_id, got nil")
	}
}

// TestUploadSigningHandler_Validation_MissingFileName verifies that a missing
// file_name field returns an error.
func TestUploadSigningHandler_Validation_MissingFileName(t *testing.T) {
	handler := defaultHandler()
	payload := validPayload()
	delete(payload, "file_name")

	err := handler.Handle(context.Background(), payload)
	if err == nil {
		t.Error("expected error for missing file_name, got nil")
	}
}

// TestUploadSigningHandler_Validation_MissingMediaType verifies that a missing
// media_type field returns an error.
func TestUploadSigningHandler_Validation_MissingMediaType(t *testing.T) {
	handler := defaultHandler()
	payload := validPayload()
	delete(payload, "media_type")

	err := handler.Handle(context.Background(), payload)
	if err == nil {
		t.Error("expected error for missing media_type, got nil")
	}
}

// TestUploadSigningHandler_Validation_MissingRequestID verifies that a missing
// request_id field returns an error.
func TestUploadSigningHandler_Validation_MissingRequestID(t *testing.T) {
	handler := defaultHandler()
	payload := validPayload()
	delete(payload, "request_id")

	err := handler.Handle(context.Background(), payload)
	if err == nil {
		t.Error("expected error for missing request_id, got nil")
	}
}

// TestUploadSigningHandler_Validation_EmptyUserID verifies that an empty string
// user_id returns an error.
func TestUploadSigningHandler_Validation_EmptyUserID(t *testing.T) {
	handler := defaultHandler()
	payload := validPayload()
	payload["user_id"] = ""

	err := handler.Handle(context.Background(), payload)
	if err == nil {
		t.Error("expected error for empty user_id, got nil")
	}
}

// TestUploadSigningHandler_Validation_EmptyFileName verifies that an empty string
// file_name returns an error.
func TestUploadSigningHandler_Validation_EmptyFileName(t *testing.T) {
	handler := defaultHandler()
	payload := validPayload()
	payload["file_name"] = ""

	err := handler.Handle(context.Background(), payload)
	if err == nil {
		t.Error("expected error for empty file_name, got nil")
	}
}

// TestUploadSigningHandler_Validation_WrongTypeUserID verifies that a non-string
// user_id (e.g. int) returns an error.
func TestUploadSigningHandler_Validation_WrongTypeUserID(t *testing.T) {
	handler := defaultHandler()
	payload := validPayload()
	payload["user_id"] = 12345 // wrong type: int instead of string

	err := handler.Handle(context.Background(), payload)
	if err == nil {
		t.Error("expected error when user_id is int (wrong type), got nil")
	}
}

// TestUploadSigningHandler_Validation_WrongTypeFileName verifies that a non-string
// file_name returns an error.
func TestUploadSigningHandler_Validation_WrongTypeFileName(t *testing.T) {
	handler := defaultHandler()
	payload := validPayload()
	payload["file_name"] = 99.9 // wrong type: float64

	err := handler.Handle(context.Background(), payload)
	if err == nil {
		t.Error("expected error when file_name is float (wrong type), got nil")
	}
}

// TestUploadSigningHandler_Validation_WrongTypeMediaType verifies that a non-string
// media_type returns an error.
func TestUploadSigningHandler_Validation_WrongTypeMediaType(t *testing.T) {
	handler := defaultHandler()
	payload := validPayload()
	payload["media_type"] = true // wrong type: bool

	err := handler.Handle(context.Background(), payload)
	if err == nil {
		t.Error("expected error when media_type is bool (wrong type), got nil")
	}
}

// TestUploadSigningHandler_Validation_EmptyPayload verifies that an entirely
// empty payload returns an error.
func TestUploadSigningHandler_Validation_EmptyPayload(t *testing.T) {
	handler := defaultHandler()
	err := handler.Handle(context.Background(), map[string]any{})
	if err == nil {
		t.Error("expected error for empty payload, got nil")
	}
}

// ---------------------------------------------------------------------------
// Input Validation — table-driven
// ---------------------------------------------------------------------------

// TestUploadSigningHandler_Validation_RequiredFields uses a table to verify all
// required fields individually cause errors when absent.
func TestUploadSigningHandler_Validation_RequiredFields(t *testing.T) {
	tests := []struct {
		name        string
		omitField   string
		replaceWith any // nil means delete the key; non-nil means set to this value
	}{
		{"missing user_id", "user_id", nil},
		{"missing file_name", "file_name", nil},
		{"missing media_type", "media_type", nil},
		{"missing request_id", "request_id", nil},
		{"empty user_id", "user_id", ""},
		{"empty file_name", "file_name", ""},
		{"user_id as int", "user_id", 42},
		{"file_name as int", "file_name", 42},
		{"media_type as int", "media_type", 42},
		{"request_id as int", "request_id", 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := defaultHandler()
			payload := validPayload()
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
// Dependency Error Tests
// ---------------------------------------------------------------------------

// TestUploadSigningHandler_SignerError_ReturnsError verifies that when the
// URLSigner returns an error, the handler returns that error and no event is produced.
func TestUploadSigningHandler_SignerError_ReturnsError(t *testing.T) {
	signer := &mockURLSigner{err: errors.New("MinIO connection timeout")}
	producer := defaultProducer()
	handler := NewUploadSigningHandler(signer, producer, "media-uploads", 900)

	err := handler.Handle(context.Background(), validPayload())
	if err == nil {
		t.Error("expected error when URLSigner fails, got nil")
	}

	if len(producer.calls) != 0 {
		t.Errorf("expected 0 Produce calls when signer fails, got %d", len(producer.calls))
	}
}

// TestUploadSigningHandler_ProducerError_ReturnsError verifies that when the
// EventProducer returns an error, the handler returns that error.
func TestUploadSigningHandler_ProducerError_ReturnsError(t *testing.T) {
	producer := &mockEventProducer{err: errors.New("Kafka broker unavailable")}
	handler := NewUploadSigningHandler(defaultSigner(), producer, "media-uploads", 900)

	err := handler.Handle(context.Background(), validPayload())
	if err == nil {
		t.Error("expected error when EventProducer fails, got nil")
	}
}

// TestUploadSigningHandler_SignerError_NoEventProduced verifies that when the
// signer fails, zero events are produced (no partial writes).
func TestUploadSigningHandler_SignerError_NoEventProduced(t *testing.T) {
	signer := &mockURLSigner{err: errors.New("S3 error")}
	producer := defaultProducer()
	handler := NewUploadSigningHandler(signer, producer, "media-uploads", 900)

	_ = handler.Handle(context.Background(), validPayload())

	if len(producer.calls) > 0 {
		t.Errorf("expected no Produce calls after signer error, got %d", len(producer.calls))
	}
}

// ---------------------------------------------------------------------------
// Signer Argument Verification
// ---------------------------------------------------------------------------

// TestUploadSigningHandler_SignerReceivesCorrectBucket verifies that the URLSigner
// is called with the configured bucket name.
func TestUploadSigningHandler_SignerReceivesCorrectBucket(t *testing.T) {
	signer := defaultSigner()
	handler := NewUploadSigningHandler(signer, defaultProducer(), "my-custom-bucket", 900)

	if err := handler.Handle(context.Background(), validPayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	if signer.capturedBucket != "my-custom-bucket" {
		t.Errorf("expected signer bucket 'my-custom-bucket', got %q", signer.capturedBucket)
	}
}

// TestUploadSigningHandler_SignerReceivesCorrectContentType verifies that the
// URLSigner is called with the media_type from the input event.
func TestUploadSigningHandler_SignerReceivesCorrectContentType(t *testing.T) {
	signer := defaultSigner()
	handler := NewUploadSigningHandler(signer, defaultProducer(), "media-uploads", 900)

	payload := validPayload()
	payload["media_type"] = "video/mp4"

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	if signer.capturedContentType != "video/mp4" {
		t.Errorf("expected signer content-type 'video/mp4', got %q", signer.capturedContentType)
	}
}

// TestUploadSigningHandler_SignerReceivesCorrectExpiry verifies that the URLSigner
// is called with the correct expiry duration derived from the configured seconds.
func TestUploadSigningHandler_SignerReceivesCorrectExpiry(t *testing.T) {
	signer := defaultSigner()
	handler := NewUploadSigningHandler(signer, defaultProducer(), "media-uploads", 900)

	if err := handler.Handle(context.Background(), validPayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	expected := 900 * time.Second
	if signer.capturedExpiry != expected {
		t.Errorf("expected signer expiry %v, got %v", expected, signer.capturedExpiry)
	}
}

// TestUploadSigningHandler_SignerReceivesObjectKeyMatchingFilePath verifies that
// the object key passed to the URLSigner matches the file_path in the produced event.
func TestUploadSigningHandler_SignerReceivesObjectKeyMatchingFilePath(t *testing.T) {
	signer := defaultSigner()
	producer := defaultProducer()
	handler := NewUploadSigningHandler(signer, producer, "media-uploads", 900)

	if err := handler.Handle(context.Background(), validPayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	filePath, _ := producer.calls[0].event["file_path"].(string)
	if signer.capturedObjectKey != filePath {
		t.Errorf("signer object key %q does not match produced file_path %q",
			signer.capturedObjectKey, filePath)
	}
}

// ---------------------------------------------------------------------------
// Edge Case Tests
// ---------------------------------------------------------------------------

// TestUploadSigningHandler_EdgeCase_FileNameWithSpaces verifies that file names
// containing spaces are handled correctly (preserved in file_path and file_name).
func TestUploadSigningHandler_EdgeCase_FileNameWithSpaces(t *testing.T) {
	producer := defaultProducer()
	handler := NewUploadSigningHandler(defaultSigner(), producer, "media-uploads", 900)

	payload := validPayload()
	payload["file_name"] = "my vacation video.mp4"

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	filePath, _ := producer.calls[0].event["file_path"].(string)
	if !strings.HasSuffix(filePath, "my vacation video.mp4") {
		t.Errorf("file_path %q should end with 'my vacation video.mp4'", filePath)
	}

	fileName, _ := producer.calls[0].event["file_name"].(string)
	if fileName != "my vacation video.mp4" {
		t.Errorf("expected file_name 'my vacation video.mp4', got %q", fileName)
	}
}

// TestUploadSigningHandler_EdgeCase_FileNameWithSpecialChars verifies that file
// names with special characters (e.g. parentheses, dots) are handled correctly.
func TestUploadSigningHandler_EdgeCase_FileNameWithSpecialChars(t *testing.T) {
	producer := defaultProducer()
	handler := NewUploadSigningHandler(defaultSigner(), producer, "media-uploads", 900)

	payload := validPayload()
	payload["file_name"] = "my file (1).mp4"

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() returned unexpected error for special-char filename: %v", err)
	}

	filePath, _ := producer.calls[0].event["file_path"].(string)
	if !strings.HasSuffix(filePath, "my file (1).mp4") {
		t.Errorf("file_path %q should end with 'my file (1).mp4'", filePath)
	}
}

// TestUploadSigningHandler_EdgeCase_LongFileName verifies that file names with
// 255+ characters are handled (not truncated or causing overflow).
func TestUploadSigningHandler_EdgeCase_LongFileName(t *testing.T) {
	producer := defaultProducer()
	handler := NewUploadSigningHandler(defaultSigner(), producer, "media-uploads", 900)

	longName := strings.Repeat("a", 250) + ".jpg" // 254 chars total
	payload := validPayload()
	payload["file_name"] = longName

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() returned unexpected error for long filename: %v", err)
	}

	fileName, _ := producer.calls[0].event["file_name"].(string)
	if fileName != longName {
		t.Errorf("expected file_name preserved as-is for long name, got len=%d", len(fileName))
	}
}

// TestUploadSigningHandler_EdgeCase_VariousMIMETypes uses a table to verify that
// all common MIME types are handled without errors.
func TestUploadSigningHandler_EdgeCase_VariousMIMETypes(t *testing.T) {
	mimeTypes := []string{
		"video/mp4",
		"image/png",
		"image/jpeg",
		"image/gif",
		"image/webp",
		"audio/mpeg",
		"audio/wav",
		"audio/ogg",
		"application/pdf",
	}

	for _, mt := range mimeTypes {
		t.Run(mt, func(t *testing.T) {
			producer := defaultProducer()
			handler := NewUploadSigningHandler(defaultSigner(), producer, "media-uploads", 900)

			payload := validPayload()
			payload["media_type"] = mt

			if err := handler.Handle(context.Background(), payload); err != nil {
				t.Errorf("Handle() returned unexpected error for media_type %q: %v", mt, err)
			}

			if len(producer.calls) != 1 {
				t.Errorf("expected 1 Produce call for media_type %q, got %d", mt, len(producer.calls))
			}
		})
	}
}

// TestUploadSigningHandler_EdgeCase_LargeFileSize verifies that a very large
// file_size (int64 max) does not cause overflow or errors.
func TestUploadSigningHandler_EdgeCase_LargeFileSize(t *testing.T) {
	producer := defaultProducer()
	handler := NewUploadSigningHandler(defaultSigner(), producer, "media-uploads", 900)

	payload := validPayload()
	payload["file_size"] = int64(9223372036854775807) // math.MaxInt64

	if err := handler.Handle(context.Background(), payload); err != nil {
		t.Fatalf("Handle() returned unexpected error for large file_size: %v", err)
	}

	if len(producer.calls) != 1 {
		t.Errorf("expected 1 Produce call for large file_size, got %d", len(producer.calls))
	}
}

// TestUploadSigningHandler_EdgeCase_ContextCancellation verifies that a cancelled
// context is propagated appropriately (no panic, returns error or nil gracefully).
func TestUploadSigningHandler_EdgeCase_ContextCancellation(t *testing.T) {
	handler := defaultHandler()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	// Should not panic; may return an error due to cancelled context
	_ = handler.Handle(ctx, validPayload())
}

// TestUploadSigningHandler_EdgeCase_NilContext verifies that a nil context does
// not cause a panic (defensive programming check).
func TestUploadSigningHandler_EdgeCase_NilContext(t *testing.T) {
	handler := defaultHandler()

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Handle() panicked with nil context: %v", r)
		}
	}()

	//nolint:staticcheck // intentional nil context for edge case test
	_ = handler.Handle(nil, validPayload()) //nolint:staticcheck
}

// ---------------------------------------------------------------------------
// Constructor Tests
// ---------------------------------------------------------------------------

// TestNewUploadSigningHandler_ReturnsNonNil verifies that NewUploadSigningHandler
// returns a non-nil handler.
func TestNewUploadSigningHandler_ReturnsNonNil(t *testing.T) {
	h := NewUploadSigningHandler(defaultSigner(), defaultProducer(), "bucket", 900)
	if h == nil {
		t.Error("NewUploadSigningHandler() returned nil")
	}
}

// TestNewUploadSigningHandler_ImplementsMessageHandler verifies that
// *UploadSigningHandler satisfies the MessageHandler interface at compile time.
func TestNewUploadSigningHandler_ImplementsMessageHandler(t *testing.T) {
	var _ MessageHandler = NewUploadSigningHandler(defaultSigner(), defaultProducer(), "bucket", 900)
}

// TestUploadSigningHandler_HappyPath_ExpiresInDefault verifies that the default
// expiry of 900 seconds is reflected in the produced event.
func TestUploadSigningHandler_HappyPath_ExpiresInDefault(t *testing.T) {
	producer := defaultProducer()
	handler := NewUploadSigningHandler(defaultSigner(), producer, "media-uploads", 900)

	if err := handler.Handle(context.Background(), validPayload()); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	expiresIn, _ := producer.calls[0].event["expires_in"].(int)
	if expiresIn != 900 {
		t.Errorf("expected expires_in = 900 (default), got %d", expiresIn)
	}
}
