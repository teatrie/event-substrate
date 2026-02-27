package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// IntentHandler handles media intent endpoints (upload-intent, download-intent,
// delete-intent) as a thin Kafka producer. It validates the JWT, parses the
// request body, generates a request_id, and produces an intent event.
type IntentHandler struct {
	ProduceEvent EventProducer
}

func (h *IntentHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID, ok := authenticateRequest(w, r)
	if !ok {
		return
	}

	// Derive intent type from URL path
	intentType := intentTypeFromPath(r.URL.Path)
	if intentType == "" {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	topic := "internal.media." + intentType + ".intent"

	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Validate required fields per intent type
	if !validateIntentBody(intentType, body) {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	requestID := uuid.New().String()

	payload := map[string]any{
		"user_id":      userID,
		"request_id":   requestID,
		"request_time": time.Now().UTC().Format(time.RFC3339),
	}
	for k, v := range body {
		payload[k] = v
	}

	if err := h.ProduceEvent(r.Context(), topic, payload); err != nil {
		log.Error().Err(err).Str("intent_type", intentType).Msg("Intent handler: failed to produce event")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]any{"request_id": requestID})
}

// intentTypeFromPath maps URL path suffixes to intent type strings.
// Returns an empty string if the path does not match a known intent.
func intentTypeFromPath(path string) string {
	switch {
	case strings.HasSuffix(path, "/upload-intent"):
		return "upload"
	case strings.HasSuffix(path, "/download-intent"):
		return "download"
	case strings.HasSuffix(path, "/delete-intent"):
		return "delete"
	default:
		return ""
	}
}

// validateIntentBody checks that the request body contains the required fields
// for the given intent type.
func validateIntentBody(intentType string, body map[string]any) bool {
	switch intentType {
	case "upload":
		fileName, _ := body["file_name"].(string)
		mediaType, _ := body["media_type"].(string)
		fileSize, _ := body["file_size"].(float64)
		return fileName != "" && mediaType != "" && fileSize > 0
	case "download", "delete":
		filePath, _ := body["file_path"].(string)
		return filePath != ""
	default:
		return false
	}
}
