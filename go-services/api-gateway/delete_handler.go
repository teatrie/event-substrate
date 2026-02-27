package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// DeleteHandler handles soft-deletion of files with ownership verification.
type DeleteHandler struct {
	Files        FileStore
	Config       *UploadConfig
	ProduceEvent EventProducer
}

// ServeHTTP handles the POST /api/v1/media/delete endpoint.
func (h *DeleteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		log.Error().Err(err).Msg("Delete handler: file store error")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if meta == nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	// Soft delete the file
	_, err = h.Files.SoftDelete(r.Context(), req.FilePath, userID)
	if err != nil {
		log.Error().Err(err).Msg("Delete handler: soft delete failed")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Produce delete event
	_ = h.ProduceEvent(r.Context(), "public.media.delete.events", map[string]any{
		"user_id":     userID,
		"file_path":   meta.FilePath,
		"file_name":   meta.FileName,
		"media_type":  meta.MediaType,
		"file_size":   json.Number(fmt.Sprintf("%d", meta.FileSize)),
		"delete_time": time.Now().UTC().Format(time.RFC3339),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"success": true,
	})
}
