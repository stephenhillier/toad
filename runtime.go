package buddy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
)

// These direct generic adapters connect the builder contracts to net/http.
// Managed-error policies are implemented in Task 4.
func decodeBody[B any](w http.ResponseWriter, r *http.Request, limit int64) (B, bool) {
	var body B
	fail := func(status int) (B, bool) {
		http.Error(w, http.StatusText(status), status)
		return body, false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return fail(http.StatusUnsupportedMediaType)
	}
	if r.Body == nil {
		return fail(http.StatusBadRequest)
	}
	// Bound the read before decoding, including whitespace and trailing data.
	// This also detects oversized bodies when Content-Length is absent or wrong.
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return fail(http.StatusRequestEntityTooLarge)
		}
		return fail(http.StatusBadRequest)
	}
	data = bytes.Trim(data, " \t\r\n")
	// Check the top-level shape even when B implements custom JSON unmarshaling.
	if len(data) == 0 || data[0] != '{' {
		return fail(http.StatusBadRequest)
	}
	// Unmarshal requires exactly one value, allowing only trailing whitespace.
	// Unknown fields remain permissive, following the standard JSON behavior.
	if err := json.Unmarshal(data, &body); err != nil {
		return fail(http.StatusBadRequest)
	}
	return body, true
}
func writeManaged[R any](w http.ResponseWriter, result R, err error, status int, mappings []errorMapping) {
	if err != nil {
		status = http.StatusInternalServerError
		for _, mapping := range mappings {
			if errors.Is(err, mapping.sentinel) {
				status = mapping.status
				break
			}
		}
		http.Error(w, http.StatusText(status), status)
		return
	}
	data, err := json.Marshal(result)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(data)
}
