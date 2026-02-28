package main

import (
	"encoding/json"
	"net/http"
	"time"
)

// DownloadHandler handles presigned download URL generation with file ownership verification.
type DownloadHandler struct {
	Files        FileStore
	Signer       FullURLSigner
	Config       *UploadConfig
	ProduceEvent EventProducer
}

// ServeHTTP handles the POST /api/v1/media/download-url endpoint.
func (h *DownloadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	userID, ok := authenticateRequest(w, r)
	if !ok {
		return
	}

	// Method check after auth
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse and validate request body
	var req struct {
		FilePath string `json:"file_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	if req.FilePath == "" {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Look up file metadata
	meta, err := h.Files.GetFileMetadata(r.Context(), req.FilePath, userID)
	if err != nil {
		log.Error().Err(err).Msg("Download handler: file store error")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if meta == nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	// Generate presigned GET URL
	expiry := time.Duration(h.Config.StorageURLExpiry) * time.Second
	downloadURL, err := h.Signer.GeneratePresignedGET(h.Config.StorageBucketName, meta.FilePath, expiry)
	if err != nil {
		log.Error().Err(err).Msg("Download handler: presigned GET generation failed")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Produce download event
	_ = h.ProduceEvent(r.Context(), "public.media.download.events", map[string]any{
		"user_id":       userID,
		"file_path":     meta.FilePath,
		"file_name":     meta.FileName,
		"media_type":    meta.MediaType,
		"download_time": time.Now().UTC().Format(time.RFC3339),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"download_url": downloadURL,
		"expires_in":   h.Config.StorageURLExpiry,
	})
}
