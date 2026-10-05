package toad

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// ErrorResponse is the shared envelope for decoder and managed handler errors.
// Message contains only generic HTTP status text, never an application's error.
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

const (
	CodeInvalidBody          = "invalid_body"
	CodeBodyTooLarge         = "body_too_large"
	CodeUnsupportedMediaType = "unsupported_media_type"
	CodeApplicationError     = "application_error"
	CodeInternalError        = "internal_error"
)

// JSON encodes value before committing headers, then writes an application/json
// response. It accepts body-bearing final HTTP statuses (200-599 except 204,
// 205 and 304). Encoding/validation errors leave the writer untouched. A write
// error occurs after commitment; callers must not attempt a replacement response.
// Ordinary handlers own error handling; this utility does not log or emit a 500.
func JSON(w http.ResponseWriter, status int, value any) error {
	if status < 200 || status > 599 || status == 204 || status == 205 || status == 304 {
		return fmt.Errorf("toad: JSON requires a body-bearing final HTTP status")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeJSON(w, status, data)
}

func writeJSON(w http.ResponseWriter, status int, data []byte) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return err
}

func decoderErrorCode(status int) string {
	switch status {
	case http.StatusRequestEntityTooLarge:
		return CodeBodyTooLarge
	case http.StatusUnsupportedMediaType:
		return CodeUnsupportedMediaType
	default:
		return CodeInvalidBody
	}
}

func publicError(status int, code string) ErrorResponse {
	message := http.StatusText(status)
	if message == "" {
		message = "Request failed"
	}
	return ErrorResponse{Code: code, Message: message}
}

func writeError(w http.ResponseWriter, status int, code string) {
	// This envelope contains only strings, so marshaling cannot fail.
	data, _ := json.Marshal(publicError(status, code))
	if err := writeJSON(w, status, data); err != nil {
		reportResponseError("write error response", err)
	}
}

// Keep logging and public formatting separate for later customization.
func reportResponseError(operation string, err error) {
	slog.Error("toad: "+operation, "error", err)
}
