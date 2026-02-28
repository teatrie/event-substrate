package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ---------------------------------------------------------------------------
// Mock dependencies for HealthHandler
// ---------------------------------------------------------------------------

// mockDBPinger implements DBPinger for testing.
type mockDBPinger struct {
	err error
}

func (m *mockDBPinger) PingContext(ctx context.Context) error {
	return m.err
}

// mockKafkaChecker implements KafkaChecker for testing.
type mockKafkaChecker struct {
	err error
}

func (m *mockKafkaChecker) Check(ctx context.Context) error {
	return m.err
}

// mockMinioChecker implements MinioChecker for testing.
type mockMinioChecker struct {
	err error
}

func (m *mockMinioChecker) Check(ctx context.Context) error {
	return m.err
}

// ---------------------------------------------------------------------------
// /healthz tests
// ---------------------------------------------------------------------------

// TestHealthzHandler_AlwaysReturns200 verifies that GET /healthz always returns 200.
func TestHealthzHandler_AlwaysReturns200(t *testing.T) {
	handler := NewHealthzHandler()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// TestHealthzHandler_ReturnsJSONBody verifies that /healthz returns {"status":"ok"}.
func TestHealthzHandler_ReturnsJSONBody(t *testing.T) {
	handler := NewHealthzHandler()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}

	status, ok := body["status"]
	if !ok {
		t.Fatal("response body missing field 'status'")
	}
	if status != "ok" {
		t.Errorf("expected status='ok', got %q", status)
	}
}

// TestHealthzHandler_ContentTypeIsJSON verifies that /healthz sets Content-Type: application/json.
func TestHealthzHandler_ContentTypeIsJSON(t *testing.T) {
	handler := NewHealthzHandler()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}
}

// ---------------------------------------------------------------------------
// /readyz tests — all healthy
// ---------------------------------------------------------------------------

// TestReadyzHandler_AllHealthy_Returns200 verifies that when all checks pass, /readyz returns 200.
func TestReadyzHandler_AllHealthy_Returns200(t *testing.T) {
	handler := NewReadyzHandler(
		&mockDBPinger{},
		&mockKafkaChecker{},
		&mockMinioChecker{},
	)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// TestReadyzHandler_AllHealthy_ReturnsOKBody verifies the body structure when all checks pass.
func TestReadyzHandler_AllHealthy_ReturnsOKBody(t *testing.T) {
	handler := NewReadyzHandler(
		&mockDBPinger{},
		&mockKafkaChecker{},
		&mockMinioChecker{},
	)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}

	if body["status"] != "ok" {
		t.Errorf("expected status='ok', got %q", body["status"])
	}

	checks, ok := body["checks"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'checks' to be a map, got %T", body["checks"])
	}

	for _, key := range []string{"kafka", "database", "minio"} {
		val, exists := checks[key]
		if !exists {
			t.Errorf("checks missing key %q", key)
			continue
		}
		if val != "ok" {
			t.Errorf("expected checks[%q]='ok', got %q", key, val)
		}
	}
}

// TestReadyzHandler_AllHealthy_ContentTypeIsJSON verifies Content-Type on healthy response.
func TestReadyzHandler_AllHealthy_ContentTypeIsJSON(t *testing.T) {
	handler := NewReadyzHandler(
		&mockDBPinger{},
		&mockKafkaChecker{},
		&mockMinioChecker{},
	)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}
}

// ---------------------------------------------------------------------------
// /readyz tests — partial failures
// ---------------------------------------------------------------------------

// TestReadyzHandler_DBFailure_Returns503 verifies that a DB ping failure causes 503.
func TestReadyzHandler_DBFailure_Returns503(t *testing.T) {
	handler := NewReadyzHandler(
		&mockDBPinger{err: errors.New("db connection refused")},
		&mockKafkaChecker{},
		&mockMinioChecker{},
	)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 on DB failure, got %d", w.Code)
	}
}

// TestReadyzHandler_DBFailure_DatabaseCheckIsError verifies that the database check is "error" on failure.
func TestReadyzHandler_DBFailure_DatabaseCheckIsError(t *testing.T) {
	handler := NewReadyzHandler(
		&mockDBPinger{err: errors.New("db down")},
		&mockKafkaChecker{},
		&mockMinioChecker{},
	)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}

	checks, ok := body["checks"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'checks' to be a map, got %T", body["checks"])
	}

	if checks["database"] != statusError {
		t.Errorf("expected checks['database']='error', got %q", checks["database"])
	}
	if checks["kafka"] != "ok" {
		t.Errorf("expected checks['kafka']='ok' when kafka healthy, got %q", checks["kafka"])
	}
	if checks["minio"] != "ok" {
		t.Errorf("expected checks['minio']='ok' when minio healthy, got %q", checks["minio"])
	}
}

// TestReadyzHandler_DBFailure_StatusIsError verifies the top-level status is "error" on any failure.
func TestReadyzHandler_DBFailure_StatusIsError(t *testing.T) {
	handler := NewReadyzHandler(
		&mockDBPinger{err: errors.New("db down")},
		&mockKafkaChecker{},
		&mockMinioChecker{},
	)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}

	if body["status"] != statusError {
		t.Errorf("expected status='error' on failure, got %q", body["status"])
	}
}

// TestReadyzHandler_KafkaFailure_Returns503 verifies that a Kafka check failure causes 503.
func TestReadyzHandler_KafkaFailure_Returns503(t *testing.T) {
	handler := NewReadyzHandler(
		&mockDBPinger{},
		&mockKafkaChecker{err: errors.New("kafka unreachable")},
		&mockMinioChecker{},
	)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 on Kafka failure, got %d", w.Code)
	}
}

// TestReadyzHandler_KafkaFailure_KafkaCheckIsError verifies that the kafka check is "error" on failure.
func TestReadyzHandler_KafkaFailure_KafkaCheckIsError(t *testing.T) {
	handler := NewReadyzHandler(
		&mockDBPinger{},
		&mockKafkaChecker{err: errors.New("kafka unreachable")},
		&mockMinioChecker{},
	)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}

	checks, ok := body["checks"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'checks' to be a map, got %T", body["checks"])
	}

	if checks["kafka"] != statusError {
		t.Errorf("expected checks['kafka']='error', got %q", checks["kafka"])
	}
	if checks["database"] != "ok" {
		t.Errorf("expected checks['database']='ok' when db healthy, got %q", checks["database"])
	}
	if checks["minio"] != "ok" {
		t.Errorf("expected checks['minio']='ok' when minio healthy, got %q", checks["minio"])
	}
}

// TestReadyzHandler_MinioFailure_Returns503 verifies that a MinIO check failure causes 503.
func TestReadyzHandler_MinioFailure_Returns503(t *testing.T) {
	handler := NewReadyzHandler(
		&mockDBPinger{},
		&mockKafkaChecker{},
		&mockMinioChecker{err: errors.New("minio connection refused")},
	)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 on MinIO failure, got %d", w.Code)
	}
}

// TestReadyzHandler_MinioFailure_MinioCheckIsError verifies that the minio check is "error" on failure.
func TestReadyzHandler_MinioFailure_MinioCheckIsError(t *testing.T) {
	handler := NewReadyzHandler(
		&mockDBPinger{},
		&mockKafkaChecker{},
		&mockMinioChecker{err: errors.New("minio down")},
	)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}

	checks, ok := body["checks"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'checks' to be a map, got %T", body["checks"])
	}

	if checks["minio"] != statusError {
		t.Errorf("expected checks['minio']='error', got %q", checks["minio"])
	}
	if checks["database"] != "ok" {
		t.Errorf("expected checks['database']='ok' when db healthy, got %q", checks["database"])
	}
	if checks["kafka"] != "ok" {
		t.Errorf("expected checks['kafka']='ok' when kafka healthy, got %q", checks["kafka"])
	}
}

// TestReadyzHandler_AllFail_AllChecksAreError verifies all checks marked error when all fail.
func TestReadyzHandler_AllFail_AllChecksAreError(t *testing.T) {
	handler := NewReadyzHandler(
		&mockDBPinger{err: errors.New("db down")},
		&mockKafkaChecker{err: errors.New("kafka down")},
		&mockMinioChecker{err: errors.New("minio down")},
	)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}

	checks, ok := body["checks"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'checks' to be a map, got %T", body["checks"])
	}

	for _, key := range []string{"kafka", "database", "minio"} {
		if checks[key] != statusError {
			t.Errorf("expected checks[%q]='error', got %q", key, checks[key])
		}
	}
}

// TestReadyzHandler_UsesRealDB verifies the readyz handler can accept a *sql.DB via adapter.
// This is a compile-time / adapter wiring test.
func TestReadyzHandler_UsesRealDB(t *testing.T) {
	// Open a dummy DB connection (won't actually connect)
	db, err := sql.Open("postgres", "postgres://invalid:invalid@localhost:9999/invalid?sslmode=disable")
	if err != nil {
		t.Fatalf("sql.Open error: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Verify sqlDBPinger wraps *sql.DB correctly
	pinger := &sqlDBPinger{db: db}
	// Just ensure it compiles and satisfies the interface
	var _ DBPinger = pinger
}
