package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ---------------------------------------------------------------------------
// Mock implementations for download/delete tests
// ---------------------------------------------------------------------------

type mockFileStore struct {
	metadata *FileMetadata
	metaErr  error
	owned    bool
	ownErr   error
	deleted  bool
	delErr   error
}

func (m *mockFileStore) VerifyOwnership(ctx context.Context, filePath, userID string) (bool, error) {
	if m.ownErr != nil {
		return false, m.ownErr
	}
	return m.owned, nil
}

func (m *mockFileStore) SoftDelete(ctx context.Context, filePath, userID string) (bool, error) {
	if m.delErr != nil {
		return false, m.delErr
	}
	return m.deleted, nil
}

func (m *mockFileStore) GetFileMetadata(ctx context.Context, filePath, userID string) (*FileMetadata, error) {
	if m.metaErr != nil {
		return nil, m.metaErr
	}
	return m.metadata, nil
}

type mockFullURLSigner struct {
	putURL string
	putErr error
	getURL string
	getErr error
}

func (m *mockFullURLSigner) GeneratePresignedPUT(bucket, key string, expiry time.Duration) (string, error) {
	if m.putErr != nil {
		return "", m.putErr
	}
	return m.putURL, nil
}

func (m *mockFullURLSigner) GeneratePresignedGET(bucket, key string, expiry time.Duration) (string, error) {
	if m.getErr != nil {
		return "", m.getErr
	}
	return m.getURL, nil
}

type mockEventProducer struct {
	called  bool
	topic   string
	payload map[string]any
	err     error
}

func (m *mockEventProducer) produce(ctx context.Context, topic string, payload map[string]any) error {
	m.called = true
	m.topic = topic
	m.payload = payload
	return m.err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func defaultDownloadHandler() (*DownloadHandler, *mockEventProducer) {
	jwtSecret = testJWTSecret
	producer := &mockEventProducer{}
	return &DownloadHandler{
		Files: &mockFileStore{
			metadata: &FileMetadata{
				FilePath:  "uploads/user-123/some-uuid/photo.jpg",
				FileName:  "photo.jpg",
				MediaType: "image/jpeg",
				FileSize:  2048000,
				UserID:    "user-123",
			},
			owned:   true,
			deleted: true,
		},
		Signer: &mockFullURLSigner{
			getURL: "https://storage.example.com/presigned-get-url",
			putURL: "https://storage.example.com/presigned-put-url",
		},
		Config:       defaultUploadConfig(),
		ProduceEvent: producer.produce,
	}, producer
}

func validDownloadBody() string {
	return `{"file_path":"uploads/user-123/some-uuid/photo.jpg"}`
}

func doDownloadRequest(handler *DownloadHandler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
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
// Download Handler Test Cases
// ---------------------------------------------------------------------------

func TestDownloadURL_MissingAuthorizationHeader(t *testing.T) {
	h, _ := defaultDownloadHandler()
	rr := doDownloadRequest(h, http.MethodPost, "/api/v1/media/download-url", validDownloadBody(), nil)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rr.Code)
	}
}

func TestDownloadURL_InvalidMalformedJWT(t *testing.T) {
	h, _ := defaultDownloadHandler()
	headers := map[string]string{"Authorization": "Bearer not-a-valid-jwt-token"}
	rr := doDownloadRequest(h, http.MethodPost, "/api/v1/media/download-url", validDownloadBody(), headers)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rr.Code)
	}
}

func TestDownloadURL_MissingBearerPrefix(t *testing.T) {
	h, _ := defaultDownloadHandler()
	headers := map[string]string{"Authorization": validJWT("user-123")}
	rr := doDownloadRequest(h, http.MethodPost, "/api/v1/media/download-url", validDownloadBody(), headers)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for missing Bearer prefix, got %d", rr.Code)
	}
}

func TestDownloadURL_ExpiredJWT(t *testing.T) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-123",
		"exp": time.Now().Add(-1 * time.Hour).Unix(),
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
	})
	signed, _ := token.SignedString(testJWTSecret)

	h, _ := defaultDownloadHandler()
	headers := map[string]string{"Authorization": "Bearer " + signed}
	rr := doDownloadRequest(h, http.MethodPost, "/api/v1/media/download-url", validDownloadBody(), headers)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for expired JWT, got %d", rr.Code)
	}
}

func TestDownloadURL_GETMethodNotAllowed(t *testing.T) {
	h, _ := defaultDownloadHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDownloadRequest(h, http.MethodGet, "/api/v1/media/download-url", "", headers)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 for GET, got %d", rr.Code)
	}
}

func TestDownloadURL_PUTMethodNotAllowed(t *testing.T) {
	h, _ := defaultDownloadHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDownloadRequest(h, http.MethodPut, "/api/v1/media/download-url", validDownloadBody(), headers)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 for PUT, got %d", rr.Code)
	}
}

func TestDownloadURL_DELETEMethodNotAllowed(t *testing.T) {
	h, _ := defaultDownloadHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDownloadRequest(h, http.MethodDelete, "/api/v1/media/download-url", "", headers)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 for DELETE, got %d", rr.Code)
	}
}

func TestDownloadURL_EmptyBody(t *testing.T) {
	h, _ := defaultDownloadHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDownloadRequest(h, http.MethodPost, "/api/v1/media/download-url", "", headers)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for empty body, got %d", rr.Code)
	}
}

func TestDownloadURL_MalformedJSON(t *testing.T) {
	h, _ := defaultDownloadHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDownloadRequest(h, http.MethodPost, "/api/v1/media/download-url", "{not json at all", headers)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for malformed JSON, got %d", rr.Code)
	}
}

func TestDownloadURL_MissingFilePath(t *testing.T) {
	h, _ := defaultDownloadHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDownloadRequest(h, http.MethodPost, "/api/v1/media/download-url", `{}`, headers)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for missing file_path, got %d", rr.Code)
	}
}

func TestDownloadURL_EmptyFilePath(t *testing.T) {
	h, _ := defaultDownloadHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDownloadRequest(h, http.MethodPost, "/api/v1/media/download-url", `{"file_path":""}`, headers)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for empty file_path, got %d", rr.Code)
	}
}

func TestDownloadURL_FileNotFound(t *testing.T) {
	h, _ := defaultDownloadHandler()
	h.Files = &mockFileStore{metadata: nil, metaErr: nil}
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDownloadRequest(h, http.MethodPost, "/api/v1/media/download-url", validDownloadBody(), headers)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404 for file not found, got %d", rr.Code)
	}
}

func TestDownloadURL_FileStoreError(t *testing.T) {
	h, _ := defaultDownloadHandler()
	h.Files = &mockFileStore{metaErr: errors.New("connection refused")}
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDownloadRequest(h, http.MethodPost, "/api/v1/media/download-url", validDownloadBody(), headers)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500 for file store error, got %d", rr.Code)
	}
}

func TestDownloadURL_PresignedGETFailure(t *testing.T) {
	h, _ := defaultDownloadHandler()
	h.Signer = &mockFullURLSigner{getErr: errors.New("S3 connection timeout")}
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDownloadRequest(h, http.MethodPost, "/api/v1/media/download-url", validDownloadBody(), headers)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500 for presigned GET failure, got %d", rr.Code)
	}
}

func TestDownloadURL_SuccessfulRequest(t *testing.T) {
	h, _ := defaultDownloadHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDownloadRequest(h, http.MethodPost, "/api/v1/media/download-url", validDownloadBody(), headers)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	if _, ok := resp["download_url"]; !ok {
		t.Error("expected response to contain 'download_url' field")
	}
	if _, ok := resp["expires_in"]; !ok {
		t.Error("expected response to contain 'expires_in' field")
	}
}

func TestDownloadURL_ResponseContentTypeIsJSON(t *testing.T) {
	h, _ := defaultDownloadHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDownloadRequest(h, http.MethodPost, "/api/v1/media/download-url", validDownloadBody(), headers)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("expected Content-Type to contain 'application/json', got %q", ct)
	}
}

func TestDownloadURL_EventProducedOnSuccess(t *testing.T) {
	h, producer := defaultDownloadHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDownloadRequest(h, http.MethodPost, "/api/v1/media/download-url", validDownloadBody(), headers)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	if !producer.called {
		t.Error("expected event producer to be called on successful download")
	}
	if producer.topic != "public.media.download.events" {
		t.Errorf("expected topic 'public.media.download.events', got %q", producer.topic)
	}
}

func TestDownloadURL_EventNotProducedOnNotFound(t *testing.T) {
	h, producer := defaultDownloadHandler()
	h.Files = &mockFileStore{metadata: nil, metaErr: nil}
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDownloadRequest(h, http.MethodPost, "/api/v1/media/download-url", validDownloadBody(), headers)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rr.Code)
	}

	if producer.called {
		t.Error("expected event producer NOT to be called on file not found")
	}
}
