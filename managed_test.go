package toad

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

type managedModel struct {
	Value float64 `json:"value"`
}

func captureResponseLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &logs
}

func TestManagedResponseOutcomes(t *testing.T) {
	first, second, third := errors.New("First public message"), errors.New("Second public message"), errors.New("Third public message")
	for _, mode := range []string{"explicit", "explicit-body", "body-explicit"} {
		for _, tc := range []struct {
			name   string
			result managedModel
			err    error
			status int
			logged bool
		}{
			{"success", managedModel{7}, nil, 201, false},
			{"zero", managedModel{}, nil, 201, false},
			{"wrapped", managedModel{math.NaN()}, fmt.Errorf("private wrapper: %w", first), 409, false},
			{"same status", managedModel{}, third, 409, false},
			{"first match", managedModel{}, errors.Join(second, first), 409, false},
			{"unknown", managedModel{math.NaN()}, errors.New("private unknown"), 500, true},
			{"encoding", managedModel{math.NaN()}, nil, 500, true},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				logs := captureResponseLogs(t)
				mux := http.NewServeMux()
				api := NewApi(mux)
				route := api.Route("POST /test")
				h := func(*http.Request) (managedModel, error) { return tc.result, tc.err }
				hb := func(r *http.Request, _ struct{}) (managedModel, error) { return h(r) }
				switch mode {
				case "explicit":
					route.Response(201, managedModel{99}).Error(409, first).Error(404, second).Error(409, third).HandlerFunc(h)
				case "explicit-body":
					route.Response(201, managedModel{}).Body(struct{}{}).Error(409, first).Error(404, second).Error(409, third).HandlerFunc(hb)
				case "body-explicit":
					route.Body(struct{}{}).Response(201, managedModel{}).Error(409, first).Error(404, second).Error(409, third).HandlerFunc(hb)
				}
				req := httptest.NewRequest("POST", "/test", strings.NewReader("{}"))
				req.Header.Set("Content-Type", "application/json")
				rr := httptest.NewRecorder()
				mux.ServeHTTP(rr, req)
				if rr.Code != tc.status || rr.Header().Get("Content-Type") != "application/json" {
					t.Fatalf("response: %d %v %s", rr.Code, rr.Header(), rr.Body)
				}
				if tc.status >= 400 {
					var got ErrorResponse
					if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
						t.Fatal(err)
					}
					detail := http.StatusText(tc.status)
					if tc.status == 409 {
						detail = first.Error()
						if tc.name == "same status" {
							detail = third.Error()
						}
					}
					if got != (ErrorResponse{Detail: detail}) || !reflect.DeepEqual(jsonValue(t, rr.Body.Bytes()), map[string]any{"detail": detail}) {
						t.Fatalf("public error: %+v", got)
					}
				} else if !reflect.DeepEqual(jsonValue(t, rr.Body.Bytes()), map[string]any{"value": tc.result.Value}) {
					t.Fatalf("success: %s", rr.Body)
				}
				if (logs.Len() > 0) != tc.logged {
					t.Fatalf("logs: %s", logs)
				}
				data, err := api.Generate()
				if err != nil {
					t.Fatal(err)
				}
				doc, err := openapi3.NewLoader().LoadFromData(data)
				if err != nil {
					t.Fatal(err)
				}
				schema := doc.Paths.Value("/test").Post.Responses.Value(fmt.Sprint(tc.status)).Value.Content["application/json"].Schema.Value
				if err := schema.VisitJSON(jsonValue(t, rr.Body.Bytes())); err != nil {
					t.Fatalf("response violates schema: %v", err)
				}
			})
		}
	}
}

type failedWriter struct {
	header   http.Header
	statuses []int
	writes   int
	short    bool
}

func (w *failedWriter) Header() http.Header    { return w.header }
func (w *failedWriter) WriteHeader(status int) { w.statuses = append(w.statuses, status) }
func (w *failedWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.short {
		return len(p) - 1, nil
	}
	return 0, io.ErrClosedPipe
}

func TestManagedWriteFailuresDoNotReplaceResponse(t *testing.T) {
	for _, short := range []bool{false, true} {
		for _, tc := range []struct {
			name   string
			result managedModel
			err    error
			status int
		}{
			{"success", managedModel{}, nil, 201}, {"unknown", managedModel{}, errors.New("secret"), 500}, {"encode", managedModel{math.Inf(1)}, nil, 500},
		} {
			t.Run(fmt.Sprint(short)+tc.name, func(t *testing.T) {
				logs := captureResponseLogs(t)
				w := &failedWriter{header: make(http.Header), short: short}
				writeManaged(w, tc.result, tc.err, 201, nil)
				if !reflect.DeepEqual(w.statuses, []int{tc.status}) || w.writes != 1 {
					t.Fatalf("replacement response: %+v", w)
				}
				if !strings.Contains(logs.String(), "write ") {
					t.Fatalf("missing write log: %s", logs)
				}
			})
		}
	}
}

func TestJSONUtility(t *testing.T) {
	rr := httptest.NewRecorder()
	if err := JSON(rr, 202, map[string]string{"hello": "world"}); err != nil {
		t.Fatal(err)
	}
	if rr.Code != 202 || rr.Header().Get("Content-Type") != "application/json" || rr.Body.String() != `{"hello":"world"}` {
		t.Fatalf("response: %+v", rr)
	}
	for _, status := range []int{99, 199, 204, 205, 304, 600, 200} {
		w := &failedWriter{header: make(http.Header)}
		if err := JSON(w, status, math.NaN()); err == nil {
			t.Fatal("expected validation/encoding error")
		}
		if len(w.statuses) != 0 || w.writes != 0 || len(w.header) != 0 {
			t.Fatalf("committed before encoding: %+v", w)
		}
	}
	w := &failedWriter{header: make(http.Header)}
	if err := JSON(w, 200, struct{}{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write error: %v", err)
	}
}

type customResult struct{}

func (customResult) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

type sliceSentinel []string

func (sliceSentinel) Error() string { return "slice" }

type pointerSentinel struct{}

func (*pointerSentinel) Error() string { return "pointer" }

type mutableSentinel struct{ message string }

func (e *mutableSentinel) Error() string { return e.message }

func TestRegisteredErrorDetailCapturedAtRegistration(t *testing.T) {
	sentinel := &mutableSentinel{message: "That name is already in use"}
	api := NewApi(http.NewServeMux())
	route := api.Route("POST /users").Response(201, User{}).Error(409, sentinel)
	sentinel.message = "private database context"
	route.Error(409, sentinel).HandlerFunc(func(*http.Request) (User, error) {
		return User{}, fmt.Errorf("insert user failed: %w", sentinel)
	})
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, httptest.NewRequest("POST", "/users", nil))
	if rr.Code != 409 || rr.Body.String() != `{"detail":"That name is already in use"}` {
		t.Fatalf("public response changed: %d %s", rr.Code, rr.Body)
	}
	data, err := api.Generate()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	schema := doc.Paths.Value("/users").Post.Responses.Value("409").Value.Content["application/json"].Schema.Value
	if !reflect.DeepEqual(schema.Properties["detail"].Value.Enum, []any{"That name is already in use"}) {
		t.Fatal("documented detail changed after registration")
	}
	if err := schema.VisitJSON(jsonValue(t, rr.Body.Bytes())); err != nil {
		t.Fatal(err)
	}
}

func TestManagedResultAndSentinelValidation(t *testing.T) {
	api := NewApi(http.NewServeMux())
	b := api.Route("GET /test")
	requireRoutePanic(t, "GET /test", "struct-valued", func() { b.Response(200, (*User)(nil)) })
	requireRoutePanic(t, "GET /test", "custom top-level", func() { b.Response(200, customResult{}) })
	requireRoutePanic(t, "GET /test", "struct-valued", func() { b.Response[any](200, nil) })
	requireRoutePanic(t, "GET /test", "struct-valued", func() { b.Response(200, []User(nil)) })
	managed := b.Response(200, User{})
	requireRoutePanic(t, "GET /test", "non-nil sentinel", func() { managed.Error(400, (*pointerSentinel)(nil)) })
	requireRoutePanic(t, "GET /test", "comparable sentinel", func() { managed.Error(400, sliceSentinel{"a"}) })
	if len(api.routes) != 0 {
		t.Fatal("invalid registration published")
	}
	sentinel := errors.New("sentinel")
	managed.Error(499, sentinel).Error(499, sentinel).HandlerFunc(func(*http.Request) (User, error) { return User{}, sentinel })
	if len(api.routes[0].errors) != 1 {
		t.Fatal("duplicate mapping not idempotent")
	}
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/test", nil))
	if rr.Code != 499 || rr.Body.String() != `{"detail":"sentinel"}` {
		t.Fatalf("custom status response: %d %s", rr.Code, rr.Body)
	}
}

func TestErrorSchemaSharesDecoderAndApplicationResponses(t *testing.T) {
	mux := http.NewServeMux()
	api := NewApi(mux).BodyLimit(2)
	sentinel := errors.New("Public message")
	api.Route("POST /test").Body(struct{}{}).Response(200, User{}).Error(400, sentinel).Error(400, errors.New("other")).Error(500, errors.New("internal")).HandlerFunc(func(*http.Request, struct{}) (User, error) { return User{}, sentinel })
	data, err := api.Generate()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	assertResponseStatuses(t, doc.Paths.Value("/test").Post.Responses, "200", "400", "413", "415", "422", "500")
	for _, tc := range []struct {
		body, content string
		status        int
		detail        string
	}{
		{"{}", "application/json", 400, sentinel.Error()}, {"{", "application/json", 400, "Bad Request"}, {"{} ", "application/json", 413, "Request Entity Too Large"}, {"{}", "", 415, "Unsupported Media Type"},
	} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/test", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", tc.content)
		mux.ServeHTTP(rr, req)
		if rr.Code != tc.status {
			t.Fatalf("status %d", rr.Code)
		}
		schema := doc.Paths.Value("/test").Post.Responses.Value(fmt.Sprint(tc.status)).Value.Content["application/json"].Schema.Value
		if err := schema.VisitJSON(jsonValue(t, rr.Body.Bytes())); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(jsonValue(t, rr.Body.Bytes()), map[string]any{"detail": tc.detail}) {
			t.Fatalf("wrong error envelope: %s", rr.Body)
		}
	}
	for status, details := range map[int][]any{
		400: {"Bad Request", sentinel.Error(), "other"},
		500: {"Internal Server Error", "internal"},
	} {
		schema := doc.Paths.Value("/test").Post.Responses.Value(fmt.Sprint(status)).Value.Content["application/json"].Schema.Value
		if len(schema.Properties) != 1 || !reflect.DeepEqual(schema.Properties["detail"].Value.Enum, details) {
			t.Fatalf("unexpected error schema: %+v", schema)
		}
	}
}
