package main

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const uploadSignedTopic = "public.media.upload.signed"

// URLSigner generates presigned PUT URLs for object storage.
type URLSigner interface {
	PresignedPutURL(ctx context.Context, bucket, objectKey, contentType string, expiry time.Duration) (string, error)
}

// EventProducer publishes events to a Kafka topic.
type EventProducer interface {
	Produce(ctx context.Context, topic string, event map[string]any) error
}

// UploadSigningHandler handles FileUploadApproved events by generating a
// presigned PUT URL and emitting a FileUploadUrlSigned event.
type UploadSigningHandler struct {
	signer        URLSigner
	producer      EventProducer
	bucket        string
	expirySeconds int
}

// NewUploadSigningHandler constructs an UploadSigningHandler.
func NewUploadSigningHandler(signer URLSigner, producer EventProducer, bucket string, expirySeconds int) *UploadSigningHandler {
	return &UploadSigningHandler{
		signer:        signer,
		producer:      producer,
		bucket:        bucket,
		expirySeconds: expirySeconds,
	}
}

// Handle processes a FileUploadApproved payload and emits a signed URL event.
func (h *UploadSigningHandler) Handle(ctx context.Context, payload map[string]any) error {
	userID, err := requireString(payload, "user_id")
	if err != nil {
		return err
	}
	fileName, err := requireString(payload, "file_name")
	if err != nil {
		return err
	}
	mediaType, err := requireString(payload, "media_type")
	if err != nil {
		return err
	}
	requestID, err := requireString(payload, "request_id")
	if err != nil {
		return err
	}

	filePath := fmt.Sprintf("uploads/%s/%s/%s", userID, uuid.New().String(), fileName)
	expiry := time.Duration(h.expirySeconds) * time.Second

	uploadURL, err := h.signer.PresignedPutURL(ctx, h.bucket, filePath, mediaType, expiry)
	if err != nil {
		return fmt.Errorf("presigned put url: %w", err)
	}

	event := map[string]any{
		"user_id":     userID,
		"file_path":   filePath,
		"file_name":   fileName,
		"upload_url":  uploadURL,
		"expires_in":  h.expirySeconds,
		"request_id":  requestID,
		"signed_time": time.Now().UTC().Format(time.RFC3339),
	}

	return h.producer.Produce(ctx, uploadSignedTopic, event)
}

// requireString extracts a non-empty string field from the payload.
// It returns an error if the field is missing, non-string, or empty.
func requireString(payload map[string]any, key string) (string, error) {
	val, ok := payload[key]
	if !ok {
		return "", fmt.Errorf("missing required field: %s", key)
	}

	str, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("field %s must be string, got %T", key, val)
	}

	if str == "" {
		return "", fmt.Errorf("field %s must not be empty", key)
	}

	return str, nil
}
