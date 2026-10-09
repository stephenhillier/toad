package toad_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// This baseline uses only stdlib request/response handling. Keep its observable
// policies equivalent to the Toad fixture so the benchmark compares the same
// work, including validation and buffering, rather than just a bare JSON decoder.
func newStdlibCreateUserHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /users", func(w http.ResponseWriter, r *http.Request) {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			stdlibError(w, http.StatusUnsupportedMediaType)
			return
		}
		if r.Body == nil {
			stdlibError(w, http.StatusBadRequest)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 128)
		data, err := io.ReadAll(r.Body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				stdlibError(w, http.StatusRequestEntityTooLarge)
			} else {
				stdlibError(w, http.StatusBadRequest)
			}
			return
		}
		data = bytes.Trim(data, " \t\r\n")
		if len(data) == 0 || data[0] != '{' {
			stdlibError(w, http.StatusBadRequest)
			return
		}
		var body createUserRequest
		// Unmarshal accepts unknown fields but rejects trailing values/garbage.
		if err := json.Unmarshal(data, &body); err != nil {
			stdlibError(w, http.StatusBadRequest)
			return
		}
		result, err := createUser(r, body)
		if err != nil {
			if errors.Is(err, errNameTaken) {
				stdlibErrorDetail(w, http.StatusConflict, errNameTaken.Error())
			} else {
				slog.Error("stdlib baseline: unmatched handler error", "error", err)
				stdlibError(w, http.StatusInternalServerError)
			}
			return
		}
		// Marshal before writing headers, matching Toad's failure-before-commit policy.
		data, err = json.Marshal(result)
		if err != nil {
			slog.Error("stdlib baseline: encode response", "error", err)
			stdlibError(w, http.StatusInternalServerError)
			return
		}
		stdlibWriteJSON(w, http.StatusCreated, data)
	})
	return mux
}

func stdlibError(w http.ResponseWriter, status int) {
	stdlibErrorDetail(w, status, http.StatusText(status))
}

func stdlibErrorDetail(w http.ResponseWriter, status int, detail string) {
	// This string field cannot fail JSON serialization. Do not use Toad's
	// envelope or helpers: the baseline's response handling is stdlib-only.
	data, _ := json.Marshal(struct {
		Detail string `json:"detail"`
	}{detail})
	stdlibWriteJSON(w, status, data)
}

func stdlibWriteJSON(w http.ResponseWriter, status int, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		slog.Error("stdlib baseline: write response", "error", err)
	}
}

func TestStdlibCreateUserBaselineParity(t *testing.T) {
	toadHandler, stdlibHandler := newCreateUserHandler(), newStdlibCreateUserHandler()
	for _, tc := range []struct {
		name, body, contentType string
		status                  int
	}{
		{"created", `{"name":"Ada"}`, "application/json", 201},
		{"conflict", `{"name":"taken"}`, "application/json", 409},
		{"empty object", `{}`, "application/json", 201},
		{"unknown field", `{"name":"Ada","extra":true}`, "application/json", 201},
		{"media parameters", `{"name":"Ada"}`, "application/json; charset=utf-8", 201},
		{"whitespace", " \t{\"name\":\"Ada\"}\r\n", "application/json", 201},
		{"empty body", "", "application/json", 400},
		{"null", "null", "application/json", 400},
		{"array", "[]", "application/json", 400},
		{"wrong field type", `{"name":42}`, "application/json", 400},
		{"malformed", `{"name":`, "application/json", 400},
		{"trailing value", `{} {}`, "application/json", 400},
		{"trailing garbage", `{}x`, "application/json", 400},
		{"missing content type", `{}`, "", 415},
		{"unsupported content type", `{}`, "text/plain", 415},
		{"invalid content type", `{}`, "application/json; broken", 415},
		{"at limit", `{}` + strings.Repeat(" ", 126), "application/json", 201},
		{"over limit", `{}` + strings.Repeat(" ", 127), "application/json", 413},
		{"size before decoding", strings.Repeat("x", 129), "application/json", 413},
		{"content type before size", strings.Repeat("x", 129), "text/plain", 415},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Content-Length must not change enforcement of the size limit.
			for _, length := range []int64{-1, int64(len(tc.body)), 1} {
				serve := func(handler http.Handler) *httptest.ResponseRecorder {
					req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(tc.body))
					req.Header.Set("Content-Type", tc.contentType)
					req.ContentLength = length
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, req)
					return rec
				}
				toadResponse, stdlibResponse := serve(toadHandler), serve(stdlibHandler)
				if toadResponse.Code != tc.status || stdlibResponse.Code != tc.status {
					t.Fatalf("status: Toad=%d Stdlib=%d want=%d", toadResponse.Code, stdlibResponse.Code, tc.status)
				}
				if !reflect.DeepEqual(toadResponse.Header(), stdlibResponse.Header()) || toadResponse.Body.String() != stdlibResponse.Body.String() {
					t.Fatalf("responses differ: Toad=%v %s; Stdlib=%v %s", toadResponse.Header(), toadResponse.Body, stdlibResponse.Header(), stdlibResponse.Body)
				}
			}
		})
	}
}
