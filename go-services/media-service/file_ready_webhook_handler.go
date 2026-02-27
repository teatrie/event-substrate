package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const fileReadyTopic = "internal.media.file.ready"

// FileReadyWebhookHandler handles MinIO webhook notifications for files
// appearing in the files/ prefix (permanent storage).
type FileReadyWebhookHandler struct {
	producer EventProducer
}

func NewFileReadyWebhookHandler(producer EventProducer) *FileReadyWebhookHandler {
	return &FileReadyWebhookHandler{producer: producer}
}

func (h *FileReadyWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusInternalServerError)
		return
	}

	var event minioEvent
	if err := json.Unmarshal(body, &event); err != nil {
		log.Error().Err(err).Msg("Failed to parse MinIO event")
		http.Error(w, "Invalid event format", http.StatusBadRequest)
		return
	}

	for _, record := range event.Records {
		objectKey := record.S3.Object.Key
		if decoded, err := url.QueryUnescape(objectKey); err == nil {
			objectKey = decoded
		}

		if !strings.HasPrefix(objectKey, "files/") {
			log.Info().Str("object_key", objectKey).Msg("Skipping non-files object key")
			continue
		}

		uploadTime := record.EventTime
		if uploadTime == "" {
			uploadTime = time.Now().UTC().Format(time.RFC3339)
		}

		payload := map[string]any{
			"file_path":   objectKey,
			"file_size":   record.S3.Object.Size,
			"upload_time": uploadTime,
		}

		if err := h.producer.Produce(r.Context(), fileReadyTopic, payload); err != nil {
			log.Error().Err(err).Msg("Failed to produce file.ready")
			http.Error(w, "Failed to produce event", http.StatusInternalServerError)
			return
		}

		log.Info().Str("file_path", objectKey).Msg("Produced FileReady")
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "No processable file-ready records")
}
