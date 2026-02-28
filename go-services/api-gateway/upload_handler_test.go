package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const errInsufficientCredits = "insufficient_credits"

// ---------------------------------------------------------------------------
// Mock implementations of CreditChecker and URLSigner
// ---------------------------------------------------------------------------

type mockCreditChecker struct {
	balance int
	err     error
	found   bool
}

func (m *mockCreditChecker) GetBalance(ctx context.Context, userID string) (int, error) {
	if m.err != nil {
		return 0, m.err
	}
	if !m.found {
		return 0, fmt.Errorf("no rows")
	}
	return m.balance, nil
}

type mockURLSigner struct {
	url string
	err error
}

func (m *mockURLSigner) GeneratePresignedPUT(bucket, key string, expiry time.Duration) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.url, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

var testJWTSecret = []byte("test-secret-key-for-unit-tests")

func validJWT(userID string) string {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"exp": time.Now().Add(1 * time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})
	signed, err := token.SignedString(testJWTSecret)
	if err != nil {
		panic(fmt.Sprintf("failed to sign test JWT: %v", err))
	}
	return signed
}

func defaultUploadConfig() *UploadConfig {
	return &UploadConfig{ //nolint:gosec // test fixture credentials
		StorageBucketName: "media-uploads",
		StorageEndpoint:   "http://localhost:9000",
		StorageAccessKey:  "admin",
		StorageSecretKey:  "password",
		StorageURLExpiry:  900,
		SupabaseDBURL:     "postgres://postgres:postgres@localhost:54322/postgres?sslmode=disable",
	}
}

func defaultHandler() *UploadHandler {
	// Set the global jwtSecret so jwtKeyFunc can verify test tokens
	jwtSecret = testJWTSecret
	return &UploadHandler{
		Credits: &mockCreditChecker{balance: 100, found: true},
		Signer: &mockURLSigner{
			url: "https://storage.example.com/presigned-put-url",
		},
		Config: defaultUploadConfig(),
	}
}

func validBody() string {
	return `{"file_name":"photo.jpg","media_type":"image/jpeg","file_size":2048000}`
}

func doRequest(handler *UploadHandler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
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
// Test Cases
// ---------------------------------------------------------------------------

func TestUploadURL_MissingAuthorizationHeader(t *testing.T) {
	h := defaultHandler()
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", validBody(), nil)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rr.Code)
	}
}

func TestUploadURL_InvalidMalformedJWT(t *testing.T) {
	h := defaultHandler()
	headers := map[string]string{"Authorization": "Bearer not-a-valid-jwt-token"}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", validBody(), headers)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rr.Code)
	}
}

func TestUploadURL_MissingBearerPrefix(t *testing.T) {
	h := defaultHandler()
	headers := map[string]string{"Authorization": validJWT("user-123")}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", validBody(), headers)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for missing Bearer prefix, got %d", rr.Code)
	}
}

func TestUploadURL_ExpiredJWT(t *testing.T) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-123",
		"exp": time.Now().Add(-1 * time.Hour).Unix(),
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
	})
	signed, _ := token.SignedString(testJWTSecret)

	h := defaultHandler()
	headers := map[string]string{"Authorization": "Bearer " + signed}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", validBody(), headers)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for expired JWT, got %d", rr.Code)
	}
}

func TestUploadURL_MissingFileName(t *testing.T) {
	h := defaultHandler()
	body := `{"media_type":"image/jpeg","file_size":2048000}`
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", body, headers)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for missing file_name, got %d", rr.Code)
	}
}

func TestUploadURL_MissingMediaType(t *testing.T) {
	h := defaultHandler()
	body := `{"file_name":"photo.jpg","file_size":2048000}`
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", body, headers)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for missing media_type, got %d", rr.Code)
	}
}

func TestUploadURL_MissingFileSize(t *testing.T) {
	h := defaultHandler()
	body := `{"file_name":"photo.jpg","media_type":"image/jpeg"}`
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", body, headers)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for missing file_size, got %d", rr.Code)
	}
}

func TestUploadURL_UnsupportedMediaType(t *testing.T) {
	unsupported := []string{
		"application/pdf",
		"text/plain",
		"image/bmp",
		"video/avi",
		"audio/flac",
		"application/octet-stream",
	}
	h := defaultHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}

	for _, mt := range unsupported {
		body := fmt.Sprintf(`{"file_name":"file.bin","media_type":"%s","file_size":1024}`, mt)
		rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", body, headers)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("media_type %q: expected status 400, got %d", mt, rr.Code)
		}
	}
}

func TestUploadURL_AllSupportedMediaTypes(t *testing.T) {
	supported := []string{
		"image/jpeg", "image/png", "image/gif", "image/webp",
		"video/mp4", "video/webm",
		"audio/mpeg", "audio/wav", "audio/ogg",
	}
	h := defaultHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}

	for _, mt := range supported {
		body := fmt.Sprintf(`{"file_name":"file.dat","media_type":"%s","file_size":1024}`, mt)
		rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", body, headers)
		if rr.Code != http.StatusOK {
			t.Errorf("media_type %q: expected status 200, got %d (body: %s)", mt, rr.Code, rr.Body.String())
		}
	}
}

func TestUploadURL_FileSizeZero(t *testing.T) {
	h := defaultHandler()
	body := `{"file_name":"photo.jpg","media_type":"image/jpeg","file_size":0}`
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", body, headers)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for file_size 0, got %d", rr.Code)
	}
}

func TestUploadURL_FileSizeNegative(t *testing.T) {
	h := defaultHandler()
	body := `{"file_name":"photo.jpg","media_type":"image/jpeg","file_size":-500}`
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", body, headers)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for negative file_size, got %d", rr.Code)
	}
}

func TestUploadURL_ZeroCredits(t *testing.T) {
	h := defaultHandler()
	h.Credits = &mockCreditChecker{balance: 0, found: true}
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", validBody(), headers)

	if rr.Code != http.StatusPaymentRequired {
		t.Errorf("expected status 402, got %d", rr.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response body as JSON: %v", err)
	}
	if resp["error"] != errInsufficientCredits {
		t.Errorf("expected error 'insufficient_credits', got %q", resp["error"])
	}
}

func TestUploadURL_UserNotFound_NoCreditRow(t *testing.T) {
	h := defaultHandler()
	h.Credits = &mockCreditChecker{balance: 0, found: false, err: fmt.Errorf("no rows")}
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-999")}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", validBody(), headers)

	if rr.Code != http.StatusPaymentRequired {
		t.Errorf("expected status 402, got %d", rr.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response body as JSON: %v", err)
	}
	if resp["error"] != errInsufficientCredits {
		t.Errorf("expected error 'insufficient_credits', got %q", resp["error"])
	}
}

func TestUploadURL_DatabaseErrorDuringCreditCheck(t *testing.T) {
	h := defaultHandler()
	h.Credits = &mockCreditChecker{err: errors.New("connection refused")}
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", validBody(), headers)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500 for DB error, got %d", rr.Code)
	}
}

func TestUploadURL_PresignedURLGenerationFailure(t *testing.T) {
	h := defaultHandler()
	h.Signer = &mockURLSigner{err: errors.New("S3 connection timeout")}
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", validBody(), headers)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500 for signer error, got %d", rr.Code)
	}
}

func TestUploadURL_SuccessfulRequest(t *testing.T) {
	h := defaultHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", validBody(), headers)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	var resp UploadResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	if resp.UploadURL == "" {
		t.Error("expected non-empty upload_url")
	}
	if resp.FilePath == "" {
		t.Error("expected non-empty file_path")
	}
	if resp.ExpiresIn != 900 {
		t.Errorf("expected expires_in 900, got %d", resp.ExpiresIn)
	}
}

func TestUploadURL_FilePathMatchesPattern(t *testing.T) {
	h := defaultHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", validBody(), headers)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	var resp UploadResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	// Pattern: uploads/{user_id}/{uuid}/{file_name}
	// UUID v4 regex: [0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}
	pattern := `^uploads/user-123/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/photo\.jpg$`
	matched, err := regexp.MatchString(pattern, resp.FilePath)
	if err != nil {
		t.Fatalf("regex error: %v", err)
	}
	if !matched {
		t.Errorf("file_path %q does not match expected pattern %q", resp.FilePath, pattern)
	}
}

func TestUploadURL_ResponseExpiresInMatchesConfig(t *testing.T) {
	h := defaultHandler()
	h.Config.StorageURLExpiry = 1800 // custom expiry
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", validBody(), headers)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	var resp UploadResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	if resp.ExpiresIn != 1800 {
		t.Errorf("expected expires_in to match config value 1800, got %d", resp.ExpiresIn)
	}
}

func TestUploadURL_ResponseContentTypeIsJSON(t *testing.T) {
	h := defaultHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", validBody(), headers)

	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("expected Content-Type to contain 'application/json', got %q", ct)
	}
}

func TestUploadURL_GETMethodNotAllowed(t *testing.T) {
	h := defaultHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodGet, "/api/v1/media/upload-url", "", headers)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 for GET, got %d", rr.Code)
	}
}

func TestUploadURL_PUTMethodNotAllowed(t *testing.T) {
	h := defaultHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodPut, "/api/v1/media/upload-url", validBody(), headers)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 for PUT, got %d", rr.Code)
	}
}

func TestUploadURL_DELETEMethodNotAllowed(t *testing.T) {
	h := defaultHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodDelete, "/api/v1/media/upload-url", "", headers)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 for DELETE, got %d", rr.Code)
	}
}

func TestUploadURL_EmptyBody(t *testing.T) {
	h := defaultHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", "", headers)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for empty body, got %d", rr.Code)
	}
}

func TestUploadURL_MalformedJSON(t *testing.T) {
	h := defaultHandler()
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", "{not json at all", headers)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for malformed JSON, got %d", rr.Code)
	}
}

func TestUploadURL_EmptyFileName(t *testing.T) {
	h := defaultHandler()
	body := `{"file_name":"","media_type":"image/jpeg","file_size":1024}`
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", body, headers)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for empty file_name, got %d", rr.Code)
	}
}

func TestUploadURL_EmptyMediaType(t *testing.T) {
	h := defaultHandler()
	body := `{"file_name":"photo.jpg","media_type":"","file_size":1024}`
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", body, headers)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for empty media_type, got %d", rr.Code)
	}
}

func TestUploadURL_NegativeCredits(t *testing.T) {
	h := defaultHandler()
	h.Credits = &mockCreditChecker{balance: -5, found: true}
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", validBody(), headers)

	if rr.Code != http.StatusPaymentRequired {
		t.Errorf("expected status 402 for negative credits, got %d", rr.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response body as JSON: %v", err)
	}
	if resp["error"] != errInsufficientCredits {
		t.Errorf("expected error 'insufficient_credits', got %q", resp["error"])
	}
}

func TestUploadURL_JWTSubClaimUsedAsUserID(t *testing.T) {
	h := defaultHandler()
	userID := "abc-def-ghi-789"
	headers := map[string]string{"Authorization": "Bearer " + validJWT(userID)}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", validBody(), headers)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	var resp UploadResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	if !strings.HasPrefix(resp.FilePath, "uploads/"+userID+"/") {
		t.Errorf("expected file_path to start with 'uploads/%s/', got %q", userID, resp.FilePath)
	}
}

func TestUploadURL_SignerReceivesCorrectBucket(t *testing.T) {
	var capturedBucket string
	signer := &capturingURLSigner{
		url:            "https://example.com/presigned",
		capturedBucket: &capturedBucket,
	}
	h := defaultHandler()
	h.Signer = signer
	h.Config.StorageBucketName = "custom-bucket"
	headers := map[string]string{"Authorization": "Bearer " + validJWT("user-123")}
	rr := doRequest(h, http.MethodPost, "/api/v1/media/upload-url", validBody(), headers)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	if capturedBucket != "custom-bucket" {
		t.Errorf("expected signer to receive bucket 'custom-bucket', got %q", capturedBucket)
	}
}

// capturingURLSigner records the arguments passed to GeneratePresignedPUT.
type capturingURLSigner struct {
	url            string
	capturedBucket *string
	capturedKey    *string
}

func (c *capturingURLSigner) GeneratePresignedPUT(bucket, key string, expiry time.Duration) (string, error) {
	if c.capturedBucket != nil {
		*c.capturedBucket = bucket
	}
	if c.capturedKey != nil {
		*c.capturedKey = key
	}
	return c.url, nil
}
