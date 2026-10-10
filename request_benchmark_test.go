package toad_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const startupRequestPrefix = `{"name":"Ada","slug":"ada","status":"active","tags":["staff"],"metadata":{"source":"import"},"owner":{"name":"Ada","email":"ada@example.com","address":{"street":"1 Main St","city":"Vancouver","country":"CA"}},"details":`
const startupUserRequestBody = startupRequestPrefix + `{"email":"ada@example.com","roles":["admin"]}}`

var apiRequestBenchmarkCases = []struct {
	name, path, body string
}{
	{"Users", "/v1/users", startupUserRequestBody},
	{"Orders", "/v1/orders", startupRequestPrefix + `{"items":[{"product_id":"product-1","quantity":2},{"product_id":"product-2","quantity":3}],"currency":"CAD"}}`},
}

// Reuse request/response buffers in both modes so their allocation does not
// obscure validation cache costs. Reset work is excluded from both timers.
type apiRequestBenchmarkHarness struct {
	prototype *http.Request
	body      string
	want      string
	input     strings.Reader
	readBody  io.ReadCloser
	output    bytes.Buffer
	headers   http.Header
	request   http.Request
	recorder  httptest.ResponseRecorder
}

func newAPIRequestBenchmarkHarness(path, body string) *apiRequestBenchmarkHarness {
	h := &apiRequestBenchmarkHarness{
		prototype: httptest.NewRequest(http.MethodPost, path, nil),
		body:      body,
		want:      `{"id":"resource-1",` + strings.TrimPrefix(body, "{"),
		headers:   make(http.Header),
	}
	h.prototype.Header.Set("Content-Type", "application/json")
	h.prototype.ContentLength = int64(len(body))
	h.readBody = io.NopCloser(&h.input)
	h.output.Grow(len(h.want))
	return h
}

func (h *apiRequestBenchmarkHarness) reset() {
	h.input.Reset(h.body)
	h.request = *h.prototype
	h.request.Body = h.readBody
	h.output.Reset()
	clear(h.headers)
	h.recorder = httptest.ResponseRecorder{HeaderMap: h.headers, Body: &h.output}
}

func (h *apiRequestBenchmarkHarness) verify(tb testing.TB) {
	tb.Helper()
	if h.recorder.Code != http.StatusCreated || h.headers.Get("Content-Type") != "application/json" || h.output.String() != h.want {
		tb.Fatalf("unexpected response: %d %v %s; want 201 with %s", h.recorder.Code, h.headers, &h.output, h.want)
	}
}

// Cold measures one valid request per fresh 100-endpoint API, including lazy
// runtime validator metadata/tag parsing and pool initialization. Warm reuses
// an API that has handled the same request. Each sub-benchmark first handles an
// untimed request to warm process-wide JSON caches consistently. Cold discards
// that API; Warm keeps it. Cold measures per-API coldness, not a fresh process.
// Only ServeHTTP is timed. API setup, buffers, resets, verification, OpenAPI,
// and network transport are excluded. Fixed iteration counts keep the untimed
// work of rebuilding 100 endpoints for every cold sample bounded.
func BenchmarkAPIRequestColdWarm(b *testing.B) {
	registrations := startupRegistrations(100)
	for _, tc := range apiRequestBenchmarkCases {
		b.Run(tc.name, func(b *testing.B) {
			for _, mode := range []string{"Cold", "Warm"} {
				b.Run(mode, func(b *testing.B) {
					h := newAPIRequestBenchmarkHarness(tc.path, tc.body)
					_, mux := newStartupAPI(registrations)
					h.reset()
					mux.ServeHTTP(&h.recorder, &h.request)
					h.verify(b)
					b.ReportAllocs()
					for b.Loop() {
						b.StopTimer()
						if mode == "Cold" {
							_, mux = newStartupAPI(registrations)
						}
						h.reset()
						b.StartTimer()
						mux.ServeHTTP(&h.recorder, &h.request)
						b.StopTimer()
						h.verify(b)
						b.StartTimer()
					}
				})
			}
		})
	}
}

func TestAPIRequestBenchmarkFixture(t *testing.T) {
	for _, tc := range apiRequestBenchmarkCases {
		t.Run(tc.name, func(t *testing.T) {
			_, mux := newStartupAPI(startupRegistrations(100))
			h := newAPIRequestBenchmarkHarness(tc.path, tc.body)
			for range 2 {
				h.reset()
				mux.ServeHTTP(&h.recorder, &h.request)
				h.verify(t)
			}
		})
	}
}
