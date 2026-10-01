package buddy

import (
	"encoding/json"
	"errors"
	"net/http"
)

// These direct generic adapters connect the builder contracts to net/http.
// Full required-body and managed-error policies are implemented in Tasks 3/4.
func decodeBody[B any](w http.ResponseWriter, r *http.Request) (B, bool) {
	var body B
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return body, false
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
