package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ---------------------------------------------------------------------------
// Mock / Spy for intent event production
// ---------------------------------------------------------------------------

type spyIntentProducer struct {
	calls []intentProducerCall
	err   error
}

type intentProducerCall struct {
	topic   string
	payload map[string]any
}

func (s *spyIntentProducer) produce(ctx context.Context, topic string, payload map[string]any) error {
	s.calls = append(s.calls, intentProducerCall{topic: topic, payload: payload})
	return s.err
}

func (s *spyIntentProducer) lastCall() intentProducerCall {
	return s.calls[len(s.calls)-1]
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func defaultIntentHandler() (*IntentHandler, *spyIntentProducer) {
	jwtSecret = testJWTSecret
	producer := &spyIntentProducer{}
	return &IntentHandler{
		ProduceEvent: producer.produce,
	}, producer
}

func authHeaders(userID string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + validJWT(userID)}
}

func doIntentRequest(handler *IntentHandler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// ---------------------------------------------------------------------------
// Upload Intent Tests
// ---------------------------------------------------------------------------

func TestIntentUpload_HappyPath(t *testing.T) {
	h, producer := defaultIntentHandler()
	body := `{"file_name":"photo.jpg","media_type":"image/jpeg","file_size":2048000}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/upload-intent", body, authHeaders("user-123"))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	requestID, ok := resp["request_id"].(string)
	if !ok || requestID == "" {
		t.Fatal("expected non-empty request_id in response")
	}

	if len(producer.calls) != 1 {
		t.Fatalf("expected exactly 1 event produced, got %d", len(producer.calls))
	}

	call := producer.lastCall()
	if call.topic != "internal.media.upload.intent" {
		t.Errorf("expected topic 'internal.media.upload.intent', got %q", call.topic)
	}

	if call.payload["user_id"] != "user-123" {
		t.Errorf("expected user_id 'user-123', got %v", call.payload["user_id"])
	}
	if call.payload["request_id"] != requestID {
		t.Errorf("expected request_id in event to match response, got %v", call.payload["request_id"])
	}
	if call.payload["file_name"] != "photo.jpg" {
		t.Errorf("expected file_name 'photo.jpg', got %v", call.payload["file_name"])
	}
	if call.payload["media_type"] != "image/jpeg" {
		t.Errorf("expected media_type 'image/jpeg', got %v", call.payload["media_type"])
	}
}

func TestIntentUpload_MissingFileName(t *testing.T) {
	h, _ := defaultIntentHandler()
	body := `{"media_type":"image/jpeg","file_size":2048000}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/upload-intent", body, authHeaders("user-123"))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for missing file_name, got %d", rr.Code)
	}
}

func TestIntentUpload_MissingMediaType(t *testing.T) {
	h, _ := defaultIntentHandler()
	body := `{"file_name":"photo.jpg","file_size":2048000}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/upload-intent", body, authHeaders("user-123"))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for missing media_type, got %d", rr.Code)
	}
}

func TestIntentUpload_MissingFileSize(t *testing.T) {
	h, _ := defaultIntentHandler()
	body := `{"file_name":"photo.jpg","media_type":"image/jpeg"}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/upload-intent", body, authHeaders("user-123"))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for missing file_size, got %d", rr.Code)
	}
}

func TestIntentUpload_EmptyFileName(t *testing.T) {
	h, _ := defaultIntentHandler()
	body := `{"file_name":"","media_type":"image/jpeg","file_size":2048000}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/upload-intent", body, authHeaders("user-123"))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for empty file_name, got %d", rr.Code)
	}
}

func TestIntentUpload_EmptyMediaType(t *testing.T) {
	h, _ := defaultIntentHandler()
	body := `{"file_name":"photo.jpg","media_type":"","file_size":2048000}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/upload-intent", body, authHeaders("user-123"))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for empty media_type, got %d", rr.Code)
	}
}

func TestIntentUpload_ZeroFileSize(t *testing.T) {
	h, _ := defaultIntentHandler()
	body := `{"file_name":"photo.jpg","media_type":"image/jpeg","file_size":0}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/upload-intent", body, authHeaders("user-123"))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for file_size 0, got %d", rr.Code)
	}
}

func TestIntentUpload_NegativeFileSize(t *testing.T) {
	h, _ := defaultIntentHandler()
	body := `{"file_name":"photo.jpg","media_type":"image/jpeg","file_size":-100}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/upload-intent", body, authHeaders("user-123"))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for negative file_size, got %d", rr.Code)
	}
}

func TestIntentUpload_NoAuth(t *testing.T) {
	h, _ := defaultIntentHandler()
	body := `{"file_name":"photo.jpg","media_type":"image/jpeg","file_size":2048000}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/upload-intent", body, nil)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rr.Code)
	}
}

func TestIntentUpload_InvalidJWT(t *testing.T) {
	h, _ := defaultIntentHandler()
	body := `{"file_name":"photo.jpg","media_type":"image/jpeg","file_size":2048000}`
	headers := map[string]string{"Authorization": "Bearer not-a-valid-jwt"}
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/upload-intent", body, headers)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rr.Code)
	}
}

func TestIntentUpload_ExpiredJWT(t *testing.T) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-123",
		"exp": time.Now().Add(-1 * time.Hour).Unix(),
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
	})
	signed, _ := token.SignedString(testJWTSecret)

	h, _ := defaultIntentHandler()
	body := `{"file_name":"photo.jpg","media_type":"image/jpeg","file_size":2048000}`
	headers := map[string]string{"Authorization": "Bearer " + signed}
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/upload-intent", body, headers)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for expired JWT, got %d", rr.Code)
	}
}

func TestIntentUpload_MissingBearerPrefix(t *testing.T) {
	h, _ := defaultIntentHandler()
	body := `{"file_name":"photo.jpg","media_type":"image/jpeg","file_size":2048000}`
	headers := map[string]string{"Authorization": validJWT("user-123")}
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/upload-intent", body, headers)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for missing Bearer prefix, got %d", rr.Code)
	}
}

func TestIntentUpload_WrongMethod(t *testing.T) {
	h, _ := defaultIntentHandler()
	rr := doIntentRequest(h, http.MethodGet, "/api/v1/media/upload-intent", "", authHeaders("user-123"))

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 for GET, got %d", rr.Code)
	}
}

func TestIntentUpload_PUTMethodNotAllowed(t *testing.T) {
	h, _ := defaultIntentHandler()
	body := `{"file_name":"photo.jpg","media_type":"image/jpeg","file_size":2048000}`
	rr := doIntentRequest(h, http.MethodPut, "/api/v1/media/upload-intent", body, authHeaders("user-123"))

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 for PUT, got %d", rr.Code)
	}
}

func TestIntentUpload_EmptyBody(t *testing.T) {
	h, _ := defaultIntentHandler()
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/upload-intent", "", authHeaders("user-123"))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for empty body, got %d", rr.Code)
	}
}

func TestIntentUpload_MalformedJSON(t *testing.T) {
	h, _ := defaultIntentHandler()
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/upload-intent", "{not json", authHeaders("user-123"))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for malformed JSON, got %d", rr.Code)
	}
}

func TestIntentUpload_ResponseContentTypeIsJSON(t *testing.T) {
	h, _ := defaultIntentHandler()
	body := `{"file_name":"photo.jpg","media_type":"image/jpeg","file_size":2048000}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/upload-intent", body, authHeaders("user-123"))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("expected Content-Type to contain 'application/json', got %q", ct)
	}
}

func TestIntentUpload_EventContainsRequestTime(t *testing.T) {
	h, producer := defaultIntentHandler()
	body := `{"file_name":"photo.jpg","media_type":"image/jpeg","file_size":2048000}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/upload-intent", body, authHeaders("user-123"))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	call := producer.lastCall()
	if _, ok := call.payload["request_time"]; !ok {
		t.Error("expected event payload to contain 'request_time'")
	}
}

func TestIntentUpload_EventContainsFileSize(t *testing.T) {
	h, producer := defaultIntentHandler()
	body := `{"file_name":"photo.jpg","media_type":"image/jpeg","file_size":2048000}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/upload-intent", body, authHeaders("user-123"))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	call := producer.lastCall()
	fileSize, ok := call.payload["file_size"]
	if !ok {
		t.Error("expected event payload to contain 'file_size'")
	}
	if fs, isFloat := fileSize.(float64); isFloat && fs != 2048000 {
		t.Errorf("expected file_size 2048000, got %v", fs)
	}
}

// ---------------------------------------------------------------------------
// Download Intent Tests
// ---------------------------------------------------------------------------

func TestIntentDownload_HappyPath(t *testing.T) {
	h, producer := defaultIntentHandler()
	body := `{"file_path":"uploads/user-123/some-uuid/photo.jpg"}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/download-intent", body, authHeaders("user-123"))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	requestID, ok := resp["request_id"].(string)
	if !ok || requestID == "" {
		t.Fatal("expected non-empty request_id in response")
	}

	if len(producer.calls) != 1 {
		t.Fatalf("expected exactly 1 event produced, got %d", len(producer.calls))
	}

	call := producer.lastCall()
	if call.topic != "internal.media.download.intent" {
		t.Errorf("expected topic 'internal.media.download.intent', got %q", call.topic)
	}

	if call.payload["user_id"] != "user-123" {
		t.Errorf("expected user_id 'user-123', got %v", call.payload["user_id"])
	}
	if call.payload["request_id"] != requestID {
		t.Errorf("expected request_id in event to match response, got %v", call.payload["request_id"])
	}
	if call.payload["file_path"] != "uploads/user-123/some-uuid/photo.jpg" {
		t.Errorf("expected file_path in event, got %v", call.payload["file_path"])
	}
}

func TestIntentDownload_MissingFilePath(t *testing.T) {
	h, _ := defaultIntentHandler()
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/download-intent", `{}`, authHeaders("user-123"))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for missing file_path, got %d", rr.Code)
	}
}

func TestIntentDownload_EmptyFilePath(t *testing.T) {
	h, _ := defaultIntentHandler()
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/download-intent", `{"file_path":""}`, authHeaders("user-123"))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for empty file_path, got %d", rr.Code)
	}
}

func TestIntentDownload_NoAuth(t *testing.T) {
	h, _ := defaultIntentHandler()
	body := `{"file_path":"uploads/user-123/some-uuid/photo.jpg"}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/download-intent", body, nil)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rr.Code)
	}
}

func TestIntentDownload_WrongMethod(t *testing.T) {
	h, _ := defaultIntentHandler()
	rr := doIntentRequest(h, http.MethodGet, "/api/v1/media/download-intent", "", authHeaders("user-123"))

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 for GET, got %d", rr.Code)
	}
}

func TestIntentDownload_EmptyBody(t *testing.T) {
	h, _ := defaultIntentHandler()
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/download-intent", "", authHeaders("user-123"))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for empty body, got %d", rr.Code)
	}
}

func TestIntentDownload_MalformedJSON(t *testing.T) {
	h, _ := defaultIntentHandler()
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/download-intent", "{bad json", authHeaders("user-123"))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for malformed JSON, got %d", rr.Code)
	}
}

func TestIntentDownload_EventContainsRequestTime(t *testing.T) {
	h, producer := defaultIntentHandler()
	body := `{"file_path":"uploads/user-123/some-uuid/photo.jpg"}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/download-intent", body, authHeaders("user-123"))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	call := producer.lastCall()
	if _, ok := call.payload["request_time"]; !ok {
		t.Error("expected event payload to contain 'request_time'")
	}
}

// ---------------------------------------------------------------------------
// Delete Intent Tests
// ---------------------------------------------------------------------------

func TestIntentDelete_HappyPath(t *testing.T) {
	h, producer := defaultIntentHandler()
	body := `{"file_path":"uploads/user-123/some-uuid/photo.jpg"}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/delete-intent", body, authHeaders("user-123"))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	requestID, ok := resp["request_id"].(string)
	if !ok || requestID == "" {
		t.Fatal("expected non-empty request_id in response")
	}

	if len(producer.calls) != 1 {
		t.Fatalf("expected exactly 1 event produced, got %d", len(producer.calls))
	}

	call := producer.lastCall()
	if call.topic != "internal.media.delete.intent" {
		t.Errorf("expected topic 'internal.media.delete.intent', got %q", call.topic)
	}

	if call.payload["user_id"] != "user-123" {
		t.Errorf("expected user_id 'user-123', got %v", call.payload["user_id"])
	}
	if call.payload["request_id"] != requestID {
		t.Errorf("expected request_id in event to match response, got %v", call.payload["request_id"])
	}
	if call.payload["file_path"] != "uploads/user-123/some-uuid/photo.jpg" {
		t.Errorf("expected file_path in event, got %v", call.payload["file_path"])
	}
}

func TestIntentDelete_MissingFilePath(t *testing.T) {
	h, _ := defaultIntentHandler()
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/delete-intent", `{}`, authHeaders("user-123"))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for missing file_path, got %d", rr.Code)
	}
}

func TestIntentDelete_EmptyFilePath(t *testing.T) {
	h, _ := defaultIntentHandler()
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/delete-intent", `{"file_path":""}`, authHeaders("user-123"))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for empty file_path, got %d", rr.Code)
	}
}

func TestIntentDelete_NoAuth(t *testing.T) {
	h, _ := defaultIntentHandler()
	body := `{"file_path":"uploads/user-123/some-uuid/photo.jpg"}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/delete-intent", body, nil)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rr.Code)
	}
}

func TestIntentDelete_WrongMethod(t *testing.T) {
	h, _ := defaultIntentHandler()
	rr := doIntentRequest(h, http.MethodGet, "/api/v1/media/delete-intent", "", authHeaders("user-123"))

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 for GET, got %d", rr.Code)
	}
}

func TestIntentDelete_EmptyBody(t *testing.T) {
	h, _ := defaultIntentHandler()
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/delete-intent", "", authHeaders("user-123"))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for empty body, got %d", rr.Code)
	}
}

func TestIntentDelete_MalformedJSON(t *testing.T) {
	h, _ := defaultIntentHandler()
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/delete-intent", "{bad json", authHeaders("user-123"))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for malformed JSON, got %d", rr.Code)
	}
}

func TestIntentDelete_EventContainsRequestTime(t *testing.T) {
	h, producer := defaultIntentHandler()
	body := `{"file_path":"uploads/user-123/some-uuid/photo.jpg"}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/delete-intent", body, authHeaders("user-123"))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	call := producer.lastCall()
	if _, ok := call.payload["request_time"]; !ok {
		t.Error("expected event payload to contain 'request_time'")
	}
}

// ---------------------------------------------------------------------------
// Cross-cutting Tests
// ---------------------------------------------------------------------------

func TestIntentRequestID_Uniqueness(t *testing.T) {
	h, _ := defaultIntentHandler()
	body := `{"file_name":"photo.jpg","media_type":"image/jpeg","file_size":2048000}`
	headers := authHeaders("user-123")

	rr1 := doIntentRequest(h, http.MethodPost, "/api/v1/media/upload-intent", body, headers)
	rr2 := doIntentRequest(h, http.MethodPost, "/api/v1/media/upload-intent", body, headers)

	if rr1.Code != http.StatusAccepted || rr2.Code != http.StatusAccepted {
		t.Fatalf("expected both requests to return 202, got %d and %d", rr1.Code, rr2.Code)
	}

	var resp1, resp2 map[string]any
	json.Unmarshal(rr1.Body.Bytes(), &resp1)
	json.Unmarshal(rr2.Body.Bytes(), &resp2)

	id1 := resp1["request_id"].(string)
	id2 := resp2["request_id"].(string)

	if id1 == id2 {
		t.Errorf("expected unique request_ids across calls, both were %q", id1)
	}
}

func TestIntentUpload_EventPayloadCorrectness(t *testing.T) {
	h, producer := defaultIntentHandler()
	body := `{"file_name":"video.mp4","media_type":"video/mp4","file_size":50000000}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/upload-intent", body, authHeaders("user-456"))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &resp)
	requestID := resp["request_id"].(string)

	call := producer.lastCall()
	payload := call.payload

	requiredFields := []string{"user_id", "request_id", "request_time", "file_name", "media_type", "file_size"}
	for _, field := range requiredFields {
		if _, ok := payload[field]; !ok {
			t.Errorf("expected event payload to contain field %q", field)
		}
	}

	if payload["user_id"] != "user-456" {
		t.Errorf("expected user_id 'user-456', got %v", payload["user_id"])
	}
	if payload["request_id"] != requestID {
		t.Errorf("expected request_id %q, got %v", requestID, payload["request_id"])
	}
	if payload["file_name"] != "video.mp4" {
		t.Errorf("expected file_name 'video.mp4', got %v", payload["file_name"])
	}
	if payload["media_type"] != "video/mp4" {
		t.Errorf("expected media_type 'video/mp4', got %v", payload["media_type"])
	}
}

func TestIntentDownload_EventPayloadCorrectness(t *testing.T) {
	h, producer := defaultIntentHandler()
	body := `{"file_path":"uploads/user-789/uuid-abc/doc.pdf"}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/download-intent", body, authHeaders("user-789"))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &resp)
	requestID := resp["request_id"].(string)

	call := producer.lastCall()
	payload := call.payload

	requiredFields := []string{"user_id", "request_id", "request_time", "file_path"}
	for _, field := range requiredFields {
		if _, ok := payload[field]; !ok {
			t.Errorf("expected event payload to contain field %q", field)
		}
	}

	if payload["user_id"] != "user-789" {
		t.Errorf("expected user_id 'user-789', got %v", payload["user_id"])
	}
	if payload["request_id"] != requestID {
		t.Errorf("expected request_id %q, got %v", requestID, payload["request_id"])
	}
	if payload["file_path"] != "uploads/user-789/uuid-abc/doc.pdf" {
		t.Errorf("expected file_path in event, got %v", payload["file_path"])
	}
}

func TestIntentDelete_EventPayloadCorrectness(t *testing.T) {
	h, producer := defaultIntentHandler()
	body := `{"file_path":"uploads/user-321/uuid-xyz/image.png"}`
	rr := doIntentRequest(h, http.MethodPost, "/api/v1/media/delete-intent", body, authHeaders("user-321"))

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &resp)
	requestID := resp["request_id"].(string)

	call := producer.lastCall()
	payload := call.payload

	requiredFields := []string{"user_id", "request_id", "request_time", "file_path"}
	for _, field := range requiredFields {
		if _, ok := payload[field]; !ok {
			t.Errorf("expected event payload to contain field %q", field)
		}
	}

	if payload["user_id"] != "user-321" {
		t.Errorf("expected user_id 'user-321', got %v", payload["user_id"])
	}
	if payload["request_id"] != requestID {
		t.Errorf("expected request_id %q, got %v", requestID, payload["request_id"])
	}
	if payload["file_path"] != "uploads/user-321/uuid-xyz/image.png" {
		t.Errorf("expected file_path in event, got %v", payload["file_path"])
	}
}
