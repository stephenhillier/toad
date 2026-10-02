package buddy

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
	first, second, third := errors.New("private first"), errors.New("private second"), errors.New("private third")
	for _, mode := range []string{"explicit", "inferred", "explicit-body", "body-explicit", "inferred-body", "body-inferred"} {
		for _, tc := range []struct {
			name   string
			result managedModel
			err    error
			status int
			code   string
			logged bool
		}{
			{"success", managedModel{7}, nil, 201, "", false},
			{"zero", managedModel{}, nil, 201, "", false},
			{"wrapped", managedModel{math.NaN()}, fmt.Errorf("private wrapper: %w", first), 409, CodeApplicationError, false},
			{"same status", managedModel{}, third, 409, CodeApplicationError, false},
			{"first match", managedModel{}, errors.Join(second, first), 409, CodeApplicationError, false},
			{"unknown", managedModel{math.NaN()}, errors.New("private unknown"), 500, CodeInternalError, true},
			{"encoding", managedModel{math.NaN()}, nil, 500, CodeInternalError, true},
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
				case "inferred":
					route.Status(201).Error(409, first).Error(404, second).Error(409, third).HandlerFunc(h)
				case "explicit-body":
					route.Response(201, managedModel{}).Body(struct{}{}).Error(409, first).Error(404, second).Error(409, third).HandlerFunc(hb)
				case "body-explicit":
					route.Body(struct{}{}).Response(201, managedModel{}).Error(409, first).Error(404, second).Error(409, third).HandlerFunc(hb)
				case "inferred-body":
					route.Status(201).Body(struct{}{}).Error(409, first).Error(404, second).Error(409, third).HandlerFunc(hb)
				case "body-inferred":
					route.Body(struct{}{}).Status(201).Error(409, first).Error(404, second).Error(409, third).HandlerFunc(hb)
				}
				req := httptest.NewRequest("POST", "/test", strings.NewReader("{}"))
				req.Header.Set("Content-Type", "application/json")
				rr := httptest.NewRecorder()
				mux.ServeHTTP(rr, req)
				if rr.Code != tc.status || rr.Header().Get("Content-Type") != "application/json" {
					t.Fatalf("response: %d %v %s", rr.Code, rr.Header(), rr.Body)
				}
				if tc.code != "" {
					var got ErrorResponse
					if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
						t.Fatal(err)
					}
					if got != (ErrorResponse{tc.code, http.StatusText(tc.status)}) {
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

func TestManagedResultAndSentinelValidation(t *testing.T) {
	api := NewApi(http.NewServeMux())
	b := api.Route("GET /test")
	requireRoutePanic(t, "GET /test", "struct-valued", func() { b.Response(200, (*User)(nil)) })
	requireRoutePanic(t, "GET /test", "custom top-level", func() { b.Response(200, customResult{}) })
	inferred := b.Status(200)
	requireRoutePanic(t, "GET /test", "struct-valued", func() { inferred.HandlerFunc(func(*http.Request) (any, error) { return nil, nil }) })
	requireRoutePanic(t, "GET /test", "struct-valued", func() { inferred.HandlerFunc(func(*http.Request) ([]User, error) { return nil, nil }) })
	requireRoutePanic(t, "GET /test", "custom top-level", func() { inferred.HandlerFunc(func(*http.Request) (customResult, error) { return customResult{}, nil }) })
	requireRoutePanic(t, "GET /test", "non-nil sentinel", func() { inferred.Error(400, (*pointerSentinel)(nil)) })
	requireRoutePanic(t, "GET /test", "comparable sentinel", func() { inferred.Error(400, sliceSentinel{"a"}) })
	if len(api.routes) != 0 {
		t.Fatal("invalid registration published")
	}
	sentinel := errors.New("sentinel")
	inferred.Error(499, sentinel).Error(499, sentinel).HandlerFunc(func(*http.Request) (User, error) { return User{}, sentinel })
	if len(api.routes[0].errors) != 1 {
		t.Fatal("duplicate mapping not idempotent")
	}
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/test", nil))
	if rr.Code != 499 || !strings.Contains(rr.Body.String(), "Request failed") {
		t.Fatalf("custom status response: %d %s", rr.Code, rr.Body)
	}
}

func TestErrorSchemaMergesDecoderAndApplicationCodes(t *testing.T) {
	mux := http.NewServeMux()
	api := NewApi(mux).BodyLimit(2)
	sentinel := errors.New("secret")
	api.Route("POST /test").Body(struct{}{}).Status(200).Error(400, sentinel).Error(400, errors.New("other")).Error(500, errors.New("internal")).HandlerFunc(func(*http.Request, struct{}) (User, error) { return User{}, sentinel })
	data, err := api.Generate()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body, content string
		status        int
		code          string
	}{
		{"{}", "application/json", 400, CodeApplicationError}, {"{", "application/json", 400, CodeInvalidBody}, {"{} ", "application/json", 413, CodeBodyTooLarge}, {"{}", "", 415, CodeUnsupportedMediaType},
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
		if !strings.Contains(rr.Body.String(), tc.code) {
			t.Fatalf("wrong code: %s", rr.Body)
		}
	}
	for _, status := range []string{"400", "500"} {
		schema := doc.Paths.Value("/test").Post.Responses.Value(status).Value.Content["application/json"].Schema.Value
		if len(schema.Properties["code"].Value.Enum) != 2 {
			t.Fatalf("codes did not merge: %+v", schema)
		}
	}
}
