package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
)

const statusError = "error"

// ---------------------------------------------------------------------------
// Interfaces for health check dependencies
// ---------------------------------------------------------------------------

// DBPinger checks database connectivity.
type DBPinger interface {
	PingContext(ctx context.Context) error
}

// KafkaChecker checks Kafka consumer connectivity.
type KafkaChecker interface {
	Check(ctx context.Context) error
}

// MinioChecker checks MinIO connectivity.
type MinioChecker interface {
	Check(ctx context.Context) error
}

// ---------------------------------------------------------------------------
// Concrete adapters
// ---------------------------------------------------------------------------

// sqlDBPinger wraps *sql.DB to implement DBPinger.
type sqlDBPinger struct {
	db *sql.DB
}

func (s *sqlDBPinger) PingContext(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// ---------------------------------------------------------------------------
// /healthz — liveness probe (always ok)
// ---------------------------------------------------------------------------

// HealthzHandler is a simple liveness handler that always returns 200 {"status":"ok"}.
type HealthzHandler struct{}

// NewHealthzHandler constructs a HealthzHandler.
func NewHealthzHandler() *HealthzHandler {
	return &HealthzHandler{}
}

func (h *HealthzHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// ---------------------------------------------------------------------------
// /readyz — readiness probe (checks all dependencies)
// ---------------------------------------------------------------------------

// ReadyzHandler checks Kafka, DB, and MinIO connectivity.
type ReadyzHandler struct {
	db    DBPinger
	kafka KafkaChecker
	minio MinioChecker
}

// NewReadyzHandler constructs a ReadyzHandler with the given dependency checkers.
func NewReadyzHandler(db DBPinger, kafka KafkaChecker, minio MinioChecker) *ReadyzHandler {
	return &ReadyzHandler{db: db, kafka: kafka, minio: minio}
}

func (h *ReadyzHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	checks := map[string]string{
		"kafka":    "ok",
		"database": "ok",
		"minio":    "ok",
	}

	healthy := true

	if err := h.kafka.Check(ctx); err != nil {
		checks["kafka"] = statusError
		healthy = false
	}

	if err := h.db.PingContext(ctx); err != nil {
		checks["database"] = statusError
		healthy = false
	}

	if err := h.minio.Check(ctx); err != nil {
		checks["minio"] = statusError
		healthy = false
	}

	status := "ok"
	httpCode := http.StatusOK
	if !healthy {
		status = statusError
		httpCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpCode)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": status,
		"checks": checks,
	})
}
