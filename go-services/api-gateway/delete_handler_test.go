package main

import (
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
// Helpers
// ---------------------------------------------------------------------------

func defaultDeleteHandler() (*DeleteHandler, *mockEventProducer) {
	jwtSecret = testJWTSecret
	producer := &mockEventProducer{}
	return &DeleteHandler{
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
		Config:       defaultUploadConfig(),
		ProduceEvent: producer.produce,
	}, producer
}

func validDeleteBody() string {
	return testFilePathBody
}

func doDeleteRequest(handler *DeleteHandler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
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
// Delete Handler Test Cases
// ---------------------------------------------------------------------------

func TestDeleteFile_MissingAuthorizationHeader(t *testing.T) {
	h, _ := defaultDeleteHandler()
	rr := doDeleteRequest(h, http.MethodPost, "/api/v1/media/delete", validDeleteBody(), nil)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rr.Code)
	}
}

func TestDeleteFile_InvalidMalformedJWT(t *testing.T) {
	h, _ := defaultDeleteHandler()
	headers := map[string]string{"Authorization": "Bearer not-a-valid-jwt-token"}
	rr := doDeleteRequest(h, http.MethodPost, "/api/v1/media/delete", validDeleteBody(), headers)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rr.Code)
	}
}

func TestDeleteFile_MissingBearerPrefix(t *testing.T) {
	h, _ := defaultDeleteHandler()
	headers := map[string]string{"Authorization": validJWT("user-123")}
	rr := doDeleteRequest(h, http.MethodPost, "/api/v1/media/delete", validDeleteBody(), headers)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for missing Bearer prefix, got %d", rr.Code)
	}
}

func TestDeleteFile_ExpiredJWT(t *testing.T) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-123",
		"exp": time.Now().Add(-1 * time.Hour).Unix(),
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
	})
	signed, _ := token.SignedString(testJWTSecret)

	h, _ := defaultDeleteHandler()
	headers := map[string]string{"Authorization": "Bearer " + signed}
	rr := doDeleteRequest(h, http.MethodPost, "/api/v1/media/delete", validDeleteBody(), headers)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for expired JWT, got %d", rr.Code)
	}
}

func TestDeleteFile_GETMethodNotAllowed(t *testing.T) {
	h, _ := defaultDeleteHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDeleteRequest(h, http.MethodGet, "/api/v1/media/delete", "", headers)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 for GET, got %d", rr.Code)
	}
}

func TestDeleteFile_PUTMethodNotAllowed(t *testing.T) {
	h, _ := defaultDeleteHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDeleteRequest(h, http.MethodPut, "/api/v1/media/delete", validDeleteBody(), headers)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 for PUT, got %d", rr.Code)
	}
}

func TestDeleteFile_EmptyBody(t *testing.T) {
	h, _ := defaultDeleteHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDeleteRequest(h, http.MethodPost, "/api/v1/media/delete", "", headers)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for empty body, got %d", rr.Code)
	}
}

func TestDeleteFile_MalformedJSON(t *testing.T) {
	h, _ := defaultDeleteHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDeleteRequest(h, http.MethodPost, "/api/v1/media/delete", "{not json at all", headers)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for malformed JSON, got %d", rr.Code)
	}
}

func TestDeleteFile_MissingFilePath(t *testing.T) {
	h, _ := defaultDeleteHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDeleteRequest(h, http.MethodPost, "/api/v1/media/delete", `{}`, headers)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for missing file_path, got %d", rr.Code)
	}
}

func TestDeleteFile_EmptyFilePath(t *testing.T) {
	h, _ := defaultDeleteHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDeleteRequest(h, http.MethodPost, "/api/v1/media/delete", `{"file_path":""}`, headers)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for empty file_path, got %d", rr.Code)
	}
}

func TestDeleteFile_FileNotFound(t *testing.T) {
	h, _ := defaultDeleteHandler()
	h.Files = &mockFileStore{metadata: nil, metaErr: nil}
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDeleteRequest(h, http.MethodPost, "/api/v1/media/delete", validDeleteBody(), headers)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404 for file not found, got %d", rr.Code)
	}
}

func TestDeleteFile_FileStoreGetMetadataError(t *testing.T) {
	h, _ := defaultDeleteHandler()
	h.Files = &mockFileStore{metaErr: errors.New("connection refused")}
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDeleteRequest(h, http.MethodPost, "/api/v1/media/delete", validDeleteBody(), headers)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500 for file store metadata error, got %d", rr.Code)
	}
}

func TestDeleteFile_FileStoreSoftDeleteError(t *testing.T) {
	h, _ := defaultDeleteHandler()
	h.Files = &mockFileStore{
		metadata: &FileMetadata{
			FilePath: "uploads/user-123/some-uuid/photo.jpg",
			UserID:   "user-123",
		},
		delErr: errors.New("database write failed"),
	}
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDeleteRequest(h, http.MethodPost, "/api/v1/media/delete", validDeleteBody(), headers)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500 for soft delete error, got %d", rr.Code)
	}
}

func TestDeleteFile_SuccessfulRequest(t *testing.T) {
	h, _ := defaultDeleteHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDeleteRequest(h, http.MethodPost, "/api/v1/media/delete", validDeleteBody(), headers)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	if success, ok := resp["success"]; !ok || success != true {
		t.Errorf("expected response to contain 'success': true, got %v", resp)
	}
}

func TestDeleteFile_ResponseContentTypeIsJSON(t *testing.T) {
	h, _ := defaultDeleteHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDeleteRequest(h, http.MethodPost, "/api/v1/media/delete", validDeleteBody(), headers)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("expected Content-Type to contain 'application/json', got %q", ct)
	}
}

func TestDeleteFile_EventProducedOnSuccess(t *testing.T) {
	h, producer := defaultDeleteHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDeleteRequest(h, http.MethodPost, "/api/v1/media/delete", validDeleteBody(), headers)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	if !producer.called {
		t.Error("expected event producer to be called on successful delete")
	}
	if producer.topic != "public.media.delete.events" {
		t.Errorf("expected topic 'public.media.delete.events', got %q", producer.topic)
	}
}

func TestDeleteFile_EventNotProducedOnNotFound(t *testing.T) {
	h, producer := defaultDeleteHandler()
	h.Files = &mockFileStore{metadata: nil, metaErr: nil}
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doDeleteRequest(h, http.MethodPost, "/api/v1/media/delete", validDeleteBody(), headers)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rr.Code)
	}

	if producer.called {
		t.Error("expected event producer NOT to be called on file not found")
	}
}
