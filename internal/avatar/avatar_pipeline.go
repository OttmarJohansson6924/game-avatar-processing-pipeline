package avatar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
)

type Processor interface {
	Upload(context.Context, string, []byte, string) (imageData, error)
	SmartCrop(context.Context, string, string) (imageData, error)
	Optimize(context.Context, string, string) (imageData, error)
	UpdateUserAvatar(context.Context, string, imageData, string) error
}

type Handler struct {
	processor Processor
	maxBytes  int64
}

type uploadResult struct {
	UserID          string `json:"user_id"`
	AvatarImageID   string `json:"avatar_image_id"`
	AvatarURL       string `json:"avatar_url"`
	ModerationState string `json:"moderation_state"`
	Event           string `json:"event"`
}

func NewHandler(processor Processor, maxBytes int64) http.Handler {
	return &Handler{processor: processor, maxBytes: maxBytes}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/avatars" {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBytes+(1<<20))
	if err := r.ParseMultipartForm(h.maxBytes); err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "avatar exceeds upload limit"})
		return
	}

	userID := strings.TrimSpace(r.FormValue("user_id"))
	requestID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	file, header, err := r.FormFile("file")
	if userID == "" || requestID == "" || err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id, file, and Idempotency-Key are required"})
		return
	}
	defer file.Close()

	content, err := readAvatar(file, header, h.maxBytes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	uploaded, err := h.processor.Upload(r.Context(), header.Filename, content, requestID)
	if err != nil {
		h.writePipelineError(w, err)
		return
	}
	cropped, err := h.processor.SmartCrop(r.Context(), firstNonEmpty(uploaded.ID, uploaded.URL), requestID)
	if err != nil {
		h.writePipelineError(w, err)
		return
	}
	optimized, err := h.processor.Optimize(r.Context(), firstNonEmpty(cropped.ID, cropped.URL), requestID)
	if err != nil {
		h.writePipelineError(w, err)
		return
	}
	if err := h.processor.UpdateUserAvatar(r.Context(), userID, optimized, requestID); err != nil {
		h.writePipelineError(w, err)
		return
	}

	writeJSON(w, http.StatusAccepted, uploadResult{
		UserID: userID, AvatarImageID: optimized.ID, AvatarURL: optimized.URL,
		ModerationState: "pending_review", Event: "avatar.processed",
	})
}

func readAvatar(file multipart.File, header *multipart.FileHeader, maxBytes int64) ([]byte, error) {
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp" {
		return nil, errors.New("avatar must be JPEG, PNG, or WebP")
	}
	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read avatar: %w", err)
	}
	if int64(len(content)) > maxBytes {
		return nil, errors.New("avatar exceeds upload limit")
	}
	if len(content) == 0 {
		return nil, errors.New("avatar is empty")
	}
	return content, nil
}

func (h *Handler) writePipelineError(w http.ResponseWriter, err error) {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatus >= 400 && apiErr.HTTPStatus < 500 {
		writeJSON(w, apiErr.HTTPStatus, map[string]string{"error": apiErr.Message, "code": apiErr.Code})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": "avatar pipeline did not complete"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
