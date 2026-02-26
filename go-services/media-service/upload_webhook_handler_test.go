package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockObjectMover implements ObjectMover for testing.
type mockObjectMover struct {
	err            error
	capturedBucket string
	capturedSrcKey string
	capturedDstKey string
	callCount      int
}

func (m *mockObjectMover) MoveObject(ctx context.Context, bucket, srcKey, dstKey string) error {
	m.capturedBucket = bucket
	m.capturedSrcKey = srcKey
	m.capturedDstKey = dstKey
	m.callCount++
	return m.err
}

func TestUploadWebhookHandler_ValidEvent(t *testing.T) {
	producer := &mockEventProducer{}
	mover := &mockObjectMover{}
	handler := NewUploadWebhookHandler(producer, mover, "media-uploads")

	event := minioEvent{
		Records: []minioRecord{{
			EventTime: "2026-02-26T10:00:00Z",
			S3: minioS3{
				Bucket: minioS3Bucket{Name: "media-uploads"},
				Object: minioS3Object{
					Key:         "uploads/user-123/uuid-1/photo.jpg",
					Size:        204800,
					ContentType: "image/jpeg",
					UserMeta:    map[string]string{"email": "user@example.com"},
				},
			},
		}},
	}

	body, _ := json.Marshal(event)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/media-upload", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if len(producer.calls) != 1 {
		t.Fatalf("expected 1 produce call, got %d", len(producer.calls))
	}

	call := producer.calls[0]
	if call.topic != "internal.media.upload.received" {
		t.Errorf("expected topic internal.media.upload.received, got %s", call.topic)
	}
	if call.event["user_id"] != "user-123" {
		t.Errorf("expected user_id=user-123, got %v", call.event["user_id"])
	}
	if call.event["email"] != "user@example.com" {
		t.Errorf("expected email=user@example.com, got %v", call.event["email"])
	}
	if call.event["file_path"] != "uploads/user-123/uuid-1/photo.jpg" {
		t.Errorf("expected file_path=uploads/..., got %v", call.event["file_path"])
	}
	permanentPath, ok := call.event["permanent_path"].(string)
	if !ok || permanentPath != "files/user-123/uuid-1/photo.jpg" {
		t.Errorf("expected permanent_path=files/..., got %v", call.event["permanent_path"])
	}
	retryCount, ok := call.event["retry_count"]
	if !ok {
		t.Error("missing retry_count in event")
	}
	// retry_count should be 0 (as json.Number or int)
	switch v := retryCount.(type) {
	case json.Number:
		if v.String() != "0" {
			t.Errorf("expected retry_count=0, got %v", v)
		}
	case int:
		if v != 0 {
			t.Errorf("expected retry_count=0, got %d", v)
		}
	case int64:
		if v != 0 {
			t.Errorf("expected retry_count=0, got %d", v)
		}
	}
}

func TestUploadWebhookHandler_NonUploadKeySkipped(t *testing.T) {
	producer := &mockEventProducer{}
	mover := &mockObjectMover{}
	handler := NewUploadWebhookHandler(producer, mover, "media-uploads")

	event := minioEvent{
		Records: []minioRecord{{
			S3: minioS3{
				Object: minioS3Object{Key: "other/file.txt"},
			},
		}},
	}

	body, _ := json.Marshal(event)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/media-upload", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for skipped event, got %d", w.Code)
	}
	if len(producer.calls) != 0 {
		t.Errorf("expected 0 produce calls for non-upload key, got %d", len(producer.calls))
	}
}

func TestUploadWebhookHandler_ProducerError(t *testing.T) {
	producer := &mockEventProducer{err: context.DeadlineExceeded}
	mover := &mockObjectMover{}
	handler := NewUploadWebhookHandler(producer, mover, "media-uploads")

	event := minioEvent{
		Records: []minioRecord{{
			EventTime: "2026-02-26T10:00:00Z",
			S3: minioS3{
				Object: minioS3Object{
					Key:         "uploads/user-123/uuid-1/photo.jpg",
					Size:        100,
					ContentType: "image/png",
				},
			},
		}},
	}

	body, _ := json.Marshal(event)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/media-upload", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on producer error, got %d", w.Code)
	}
}

func TestUploadWebhookHandler_MalformedJSON(t *testing.T) {
	producer := &mockEventProducer{}
	mover := &mockObjectMover{}
	handler := NewUploadWebhookHandler(producer, mover, "media-uploads")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/media-upload", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed JSON, got %d", w.Code)
	}
}

func TestUploadWebhookHandler_MethodNotAllowed(t *testing.T) {
	producer := &mockEventProducer{}
	mover := &mockObjectMover{}
	handler := NewUploadWebhookHandler(producer, mover, "media-uploads")

	req := httptest.NewRequest(http.MethodGet, "/webhooks/media-upload", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestUploadWebhookHandler_URLDecodesKey(t *testing.T) {
	producer := &mockEventProducer{}
	mover := &mockObjectMover{}
	handler := NewUploadWebhookHandler(producer, mover, "media-uploads")

	event := minioEvent{
		Records: []minioRecord{{
			EventTime: "2026-02-26T10:00:00Z",
			S3: minioS3{
				Object: minioS3Object{
					Key:         "uploads/user-123/uuid-1/my%20photo.jpg",
					Size:        100,
					ContentType: "image/jpeg",
				},
			},
		}},
	}

	body, _ := json.Marshal(event)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/media-upload", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	call := producer.calls[0]
	if call.event["file_name"] != "my photo.jpg" {
		t.Errorf("expected decoded file_name='my photo.jpg', got %v", call.event["file_name"])
	}
	if call.event["permanent_path"] != "files/user-123/uuid-1/my photo.jpg" {
		t.Errorf("expected decoded permanent_path, got %v", call.event["permanent_path"])
	}
}
