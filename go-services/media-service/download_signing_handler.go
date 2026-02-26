package main

import (
	"context"
	"fmt"
	"time"
)

const downloadSignedTopic = "public.media.download.signed"
const downloadRejectedTopic = "public.media.download.rejected"

// DownloadSigningHandler handles DownloadIntent events by verifying file ownership,
// generating a presigned GET URL, and emitting a FileDownloadUrlSigned or
// FileDownloadRejected event.
type DownloadSigningHandler struct {
	signer        DownloadURLSigner
	lookup        FileMetadataLookup
	producer      EventProducer
	bucket        string
	expirySeconds int
}

// NewDownloadSigningHandler constructs a DownloadSigningHandler.
func NewDownloadSigningHandler(signer DownloadURLSigner, lookup FileMetadataLookup, producer EventProducer, bucket string, expirySeconds int) *DownloadSigningHandler {
	return &DownloadSigningHandler{
		signer:        signer,
		lookup:        lookup,
		producer:      producer,
		bucket:        bucket,
		expirySeconds: expirySeconds,
	}
}

// Handle processes a DownloadIntent payload and emits the appropriate event.
func (h *DownloadSigningHandler) Handle(ctx context.Context, payload map[string]any) error {
	userID, err := requireString(payload, "user_id")
	if err != nil {
		return err
	}
	filePath, err := requireString(payload, "file_path")
	if err != nil {
		return err
	}
	requestID, err := requireString(payload, "request_id")
	if err != nil {
		return err
	}

	// Verify ownership and file existence.
	metadata, err := h.lookup.GetFileMetadata(ctx, userID, filePath)
	if err != nil {
		return fmt.Errorf("metadata lookup: %w", err)
	}

	if metadata == nil {
		return h.producer.Produce(ctx, downloadRejectedTopic, map[string]any{
			"user_id":       userID,
			"file_path":     filePath,
			"request_id":    requestID,
			"reason":        "not_found",
			"rejected_time": time.Now().UTC().Format(time.RFC3339),
		})
	}

	// Generate presigned GET URL.
	expiry := time.Duration(h.expirySeconds) * time.Second
	downloadURL, err := h.signer.PresignedGetURL(ctx, h.bucket, filePath, expiry)
	if err != nil {
		return h.producer.Produce(ctx, downloadRejectedTopic, map[string]any{
			"user_id":       userID,
			"file_path":     filePath,
			"request_id":    requestID,
			"reason":        "internal_error",
			"rejected_time": time.Now().UTC().Format(time.RFC3339),
		})
	}

	return h.producer.Produce(ctx, downloadSignedTopic, map[string]any{
		"user_id":      userID,
		"file_path":    filePath,
		"download_url": downloadURL,
		"expires_in":   h.expirySeconds,
		"request_id":   requestID,
		"signed_time":  time.Now().UTC().Format(time.RFC3339),
	})
}
