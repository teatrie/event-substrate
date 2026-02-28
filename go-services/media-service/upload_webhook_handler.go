package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MinIO S3 event notification structures.
type minioEvent struct {
	Records []minioRecord `json:"Records"`
}

type minioRecord struct {
	EventTime string  `json:"eventTime"`
	S3        minioS3 `json:"s3"`
}

type minioS3 struct {
	Bucket minioS3Bucket `json:"bucket"`
	Object minioS3Object `json:"object"`
}

type minioS3Bucket struct {
	Name string `json:"name"`
}

type minioS3Object struct {
	Key         string            `json:"key"`
	Size        int64             `json:"size"`
	ContentType string            `json:"contentType"`
	UserMeta    map[string]string `json:"userMetadata,omitempty"`
}

const uploadReceivedTopic = "internal.media.upload.received"

// UploadWebhookHandler handles MinIO webhook notifications for completed file
// uploads in the uploads/ prefix. It produces an UploadReceived event and
// triggers an async MoveObject to the permanent files/ prefix.
type UploadWebhookHandler struct {
	producer EventProducer
	mover    ObjectMover
	bucket   string
}

func NewUploadWebhookHandler(producer EventProducer, mover ObjectMover, bucket string) *UploadWebhookHandler {
	return &UploadWebhookHandler{producer: producer, mover: mover, bucket: bucket}
}

func (h *UploadWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

		// Extract user_id from path: uploads/{user_id}/{uuid}/{file_name}
		parts := strings.Split(objectKey, "/")
		if len(parts) < 4 || parts[0] != "uploads" {
			log.Info().Str("object_key", objectKey).Msg("Skipping non-upload object key")
			continue
		}

		userID := parts[1]
		fileName := parts[len(parts)-1]
		permanentPath := "files/" + strings.TrimPrefix(objectKey, "uploads/")

		mediaType := record.S3.Object.ContentType
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}

		email := ""
		if record.S3.Object.UserMeta != nil {
			if e, ok := record.S3.Object.UserMeta["X-Amz-Meta-Email"]; ok {
				email = e
			}
			if e, ok := record.S3.Object.UserMeta["email"]; ok {
				email = e
			}
		}

		uploadTime := record.EventTime
		if uploadTime == "" {
			uploadTime = time.Now().UTC().Format(time.RFC3339)
		}

		payload := map[string]any{
			"user_id":        userID,
			"email":          email,
			"file_path":      objectKey,
			"file_name":      fileName,
			"file_size":      record.S3.Object.Size,
			"media_type":     mediaType,
			"upload_time":    uploadTime,
			"permanent_path": permanentPath,
			"retry_count":    int32(0),
		}

		log.Info().Str("user_id", userID).Str("file_name", fileName).Int64("size", record.S3.Object.Size).Msg("Processing upload webhook")

		if err := h.producer.Produce(r.Context(), uploadReceivedTopic, payload); err != nil {
			log.Error().Err(err).Msg("Failed to produce upload.received")
			http.Error(w, "Failed to produce event", http.StatusInternalServerError)
			return
		}

		// Fire-and-forget move to permanent storage.
		// Use Background context — r.Context() is cancelled when handler returns.
		go func(bucket, src, dst string) {
			if err := h.mover.MoveObject(context.Background(), bucket, src, dst); err != nil {
				log.Warn().Err(err).Msg("Async MoveObject failed (saga will retry)")
			}
		}(h.bucket, objectKey, permanentPath)

		log.Info().Str("user_id", userID).Str("file_name", fileName).Msg("Produced UploadReceived")
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, "No processable upload records")
}
