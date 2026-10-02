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
func decodeBody[B any](w http.ResponseWriter, r *http.Request, limit int64) (B, bool) {
	var body B
	fail := func(status int) (B, bool) {
		writeError(w, status, decoderErrorCode(status))
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
		for _, mapping := range mappings {
			if errors.Is(err, mapping.sentinel) {
				writeError(w, mapping.status, CodeApplicationError)
				return
			}
		}
		reportResponseError("unmatched handler error", err)
		writeError(w, http.StatusInternalServerError, CodeInternalError)
		return
	}
	data, err := json.Marshal(result)
	if err != nil {
		reportResponseError("encode managed response", err)
		writeError(w, http.StatusInternalServerError, CodeInternalError)
		return
	}
	if err := writeJSON(w, status, data); err != nil {
		reportResponseError("write managed response", err)
	}
}
