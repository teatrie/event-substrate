package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const contentTypeJSON = "application/json"

// ---------------------------------------------------------------------------
// /healthz tests
// ---------------------------------------------------------------------------

func TestHealthz_Returns200(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	healthzHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestHealthz_ReturnsJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	healthzHandler(rr, req)

	ct := rr.Header().Get("Content-Type")
	if ct != contentTypeJSON {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}
}

func TestHealthz_BodyHasStatusOk(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	healthzHandler(rr, req)

	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response body as JSON: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", resp["status"])
	}
}

func TestHealthz_AlwaysOk(t *testing.T) {
	// Call multiple times to ensure it always returns ok
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rr := httptest.NewRecorder()
		healthzHandler(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("iteration %d: expected 200, got %d", i, rr.Code)
		}
	}
}

// ---------------------------------------------------------------------------
// /readyz tests
// ---------------------------------------------------------------------------

func TestReadyz_Returns200WhenReady(t *testing.T) {
	checker := &mockReadinessChecker{ready: true}
	handler := readyzHandler(checker)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200 when ready, got %d", rr.Code)
	}
}

func TestReadyz_Returns503WhenNotReady(t *testing.T) {
	checker := &mockReadinessChecker{ready: false}
	handler := readyzHandler(checker)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503 when not ready, got %d", rr.Code)
	}
}

func TestReadyz_BodyHasStatusOkWhenReady(t *testing.T) {
	checker := &mockReadinessChecker{ready: true}
	handler := readyzHandler(checker)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response body as JSON: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", resp["status"])
	}
}

func TestReadyz_BodyHasStatusNotReadyWhenNotReady(t *testing.T) {
	checker := &mockReadinessChecker{ready: false}
	handler := readyzHandler(checker)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response body as JSON: %v", err)
	}
	if resp["status"] != "not_ready" {
		t.Errorf("expected status 'not_ready', got %q", resp["status"])
	}
}

func TestReadyz_ReturnsJSON(t *testing.T) {
	checker := &mockReadinessChecker{ready: true}
	handler := readyzHandler(checker)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	ct := rr.Header().Get("Content-Type")
	if ct != contentTypeJSON {
		t.Errorf("expected Content-Type 'application/json', got %q", ct)
	}
}

func TestReadyz_Returns503JSONWhenNotReady(t *testing.T) {
	checker := &mockReadinessChecker{ready: false}
	handler := readyzHandler(checker)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	ct := rr.Header().Get("Content-Type")
	if ct != contentTypeJSON {
		t.Errorf("expected Content-Type 'application/json' for 503, got %q", ct)
	}
}

// ---------------------------------------------------------------------------
// Mock readiness checker
// ---------------------------------------------------------------------------

type mockReadinessChecker struct {
	ready bool
}

func (m *mockReadinessChecker) IsReady() bool {
	return m.ready
}
