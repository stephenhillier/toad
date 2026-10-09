package toad

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// ErrorResponse is the shared envelope for decoder and managed handler errors.
// Detail contains registered sentinel text or generic HTTP status text.
type ErrorResponse struct {
	Detail string `json:"detail"`
}

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

func publicError(status int) ErrorResponse {
	message := http.StatusText(status)
	if message == "" {
		message = "Request failed"
	}
	return ErrorResponse{Detail: message}
}

func writeError(w http.ResponseWriter, status int) {
	writeErrorDetail(w, status, publicError(status).Detail)
}

func writeErrorDetail(w http.ResponseWriter, status int, detail string) {
	// This envelope contains only strings, so marshaling cannot fail.
	data, _ := json.Marshal(ErrorResponse{Detail: detail})
	if err := writeJSON(w, status, data); err != nil {
		reportResponseError("write error response", err)
	}
}

// Keep logging and public formatting separate for later customization.
func reportResponseError(operation string, err error) {
	slog.Error("toad: "+operation, "error", err)
}
