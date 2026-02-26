package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFileReadyWebhookHandler_ValidFilesEvent(t *testing.T) {
	producer := &mockEventProducer{}
	handler := NewFileReadyWebhookHandler(producer)

	event := minioEvent{
		Records: []minioRecord{{
			EventTime: "2026-02-26T10:05:00Z",
			S3: minioS3{
				Object: minioS3Object{
					Key:  "files/user-123/uuid-1/photo.jpg",
					Size: 204800,
				},
			},
		}},
	}

	body, _ := json.Marshal(event)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/file-ready", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if len(producer.calls) != 1 {
		t.Fatalf("expected 1 produce call, got %d", len(producer.calls))
	}

	call := producer.calls[0]
	if call.topic != "internal.media.file.ready" {
		t.Errorf("expected topic internal.media.file.ready, got %s", call.topic)
	}
	if call.event["file_path"] != "files/user-123/uuid-1/photo.jpg" {
		t.Errorf("expected file_path=files/..., got %v", call.event["file_path"])
	}
}

func TestFileReadyWebhookHandler_NonFilesKeySkipped(t *testing.T) {
	producer := &mockEventProducer{}
	handler := NewFileReadyWebhookHandler(producer)

	event := minioEvent{
		Records: []minioRecord{{
			S3: minioS3{
				Object: minioS3Object{Key: "uploads/user-123/uuid-1/photo.jpg"},
			},
		}},
	}

	body, _ := json.Marshal(event)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/file-ready", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(producer.calls) != 0 {
		t.Errorf("expected 0 produce calls for non-files key, got %d", len(producer.calls))
	}
}

func TestFileReadyWebhookHandler_MethodNotAllowed(t *testing.T) {
	producer := &mockEventProducer{}
	handler := NewFileReadyWebhookHandler(producer)

	req := httptest.NewRequest(http.MethodGet, "/webhooks/file-ready", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}
