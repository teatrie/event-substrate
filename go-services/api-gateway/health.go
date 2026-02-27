package main

import (
	"encoding/json"
	"net/http"
)

// ReadinessChecker is the interface that reports whether the service is ready
// to serve traffic. Implementations may check Kafka connectivity, DB
// availability, etc.
type ReadinessChecker interface {
	IsReady() bool
}

// kafkaReadinessChecker uses the global kafkaClient to probe liveness.
type kafkaReadinessChecker struct{}

func (k *kafkaReadinessChecker) IsReady() bool {
	return kafkaClient != nil
}

// healthzHandler handles GET /healthz.
// It always returns {"status":"ok"} with HTTP 200. Kubernetes uses this as the
// liveness probe — if the process is alive and can serve HTTP the pod is live.
func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// readyzHandler returns a handler for GET /readyz that delegates readiness
// determination to the supplied ReadinessChecker.
// Returns {"status":"ok"} / 200 when ready, {"status":"not_ready"} / 503 otherwise.
func readyzHandler(checker ReadinessChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if checker.IsReady() {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"status": "not_ready"})
		}
	}
}
