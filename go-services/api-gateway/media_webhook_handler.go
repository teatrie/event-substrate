package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"
)

// MinIO S3 event notification structures (subset of fields we need).
// See: https://min.io/docs/minio/linux/administration/monitoring/bucket-notifications.html

type minioEvent struct {
	Records []minioRecord `json:"Records"`
}

type minioRecord struct {
	EventTime string        `json:"eventTime"`
	S3        minioS3       `json:"s3"`
	UserMeta  minioUserMeta `json:"userMetadata,omitempty"`
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

type minioUserMeta map[string]string

// mediaWebhookHandler handles MinIO bucket notifications for completed file uploads.
// It transforms the S3 event format into our NewFileUploaded Avro schema and produces to Kafka.
func mediaWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Note: MinIO webhooks don't support custom auth headers (auth_token not
	// available in this version). This endpoint is authenticated by network
	// isolation — only MinIO within the Docker/K8s network can reach it.
	// TODO(prod): Add IP allowlist or mutual TLS for production.

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusInternalServerError)
		return
	}

	var event minioEvent
	if err := json.Unmarshal(body, &event); err != nil {
		log.Printf("Failed to parse MinIO event: %v", err)
		http.Error(w, "Invalid event format", http.StatusBadRequest)
		return
	}

	for _, record := range event.Records {
		objectKey := record.S3.Object.Key

		// MinIO URL-encodes the key in S3 event notifications
		if decoded, err := url.QueryUnescape(objectKey); err == nil {
			objectKey = decoded
		}

		// Extract user_id from path: uploads/{user_id}/{uuid}/{file_name}
		parts := strings.Split(objectKey, "/")
		if len(parts) < 4 || parts[0] != "uploads" {
			log.Printf("Skipping non-upload object key: %s", objectKey)
			continue
		}

		userID := parts[1]
		fileName := parts[len(parts)-1]
		filePath := objectKey

		// Determine media type from S3 event or fall back to empty
		mediaType := record.S3.Object.ContentType
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}

		// Extract email from object user metadata if available
		email := ""
		if record.S3.Object.UserMeta != nil {
			if e, ok := record.S3.Object.UserMeta["X-Amz-Meta-Email"]; ok {
				email = e
			}
			if e, ok := record.S3.Object.UserMeta["email"]; ok {
				email = e
			}
		}

		// Parse upload time from S3 event, fall back to now
		uploadTime := record.EventTime
		if uploadTime == "" {
			uploadTime = time.Now().UTC().Format(time.RFC3339)
		}

		// Construct Avro-compatible payload matching NewFileUploaded schema
		// Note: file_size must be a json.Number (not int64) because processAndProduceEvent
		// deserializes JSON via map[string]any, which would convert int64 → float64.
		// The Avro encoder rejects float64 for "long" fields.
		avroPayload := map[string]any{
			"user_id":     userID,
			"email":       email,
			"file_path":   filePath,
			"file_name":   fileName,
			"file_size":   json.Number(fmt.Sprintf("%d", record.S3.Object.Size)),
			"media_type":  mediaType,
			"upload_time": uploadTime,
		}

		payloadBytes, err := json.Marshal(avroPayload)
		if err != nil {
			log.Printf("Failed to marshal transformed payload: %v", err)
			continue
		}

		log.Printf("Processing media upload event: user=%s file=%s size=%d",
			userID, fileName, record.S3.Object.Size)

		// Re-use the existing processAndProduceEvent flow by creating a synthetic request
		topicName := "public.media.upload.events"
		syntheticReq, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "/", strings.NewReader(string(payloadBytes)))
		syntheticReq.Header.Set("Content-Type", "application/json")

		// Capture the response to check if production succeeded
		recorder := httptest.NewRecorder()
		processAndProduceEvent(r.Context(), recorder, syntheticReq, topicName)

		if recorder.Code >= 400 {
			log.Printf("Failed to produce event to %s: status %d, body: %s",
				topicName, recorder.Code, recorder.Body.String())
			w.WriteHeader(recorder.Code)
			return
		}

		log.Printf("Produced NewFileUploaded event to %s for user %s, file %s",
			topicName, userID, fileName)
		w.WriteHeader(http.StatusOK)
		return
	}

	// If we got here with no valid records, still return OK to prevent MinIO retries
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "No processable upload records")
}
