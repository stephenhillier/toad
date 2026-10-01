package buddy

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type decoderInput struct {
	Name   string         `json:"name"`
	Count  int            `json:"count"`
	Labels map[string]int `json:"labels"`
	Values []int          `json:"values"`
	Nested *decoderNested `json:"nested"`
}

type decoderNested struct {
	Value int `json:"value"`
}

var bodyModes = []string{"ordinary", "explicit-body", "body-explicit", "inferred-body", "body-inferred"}

func registerDecoderRoute(api *Api, mode string, receive func(decoderInput)) {
	prototype := decoderInput{Name: "prototype", Labels: map[string]int{"prototype": 1}}
	route := api.Route("POST /body")
	managed := func(_ *http.Request, body decoderInput) (decoderInput, error) {
		receive(body)
		return body, nil
	}
	switch mode {
	case "ordinary":
		route.Body(prototype).HandlerFunc(func(w http.ResponseWriter, _ *http.Request, body decoderInput) {
			receive(body)
			w.WriteHeader(http.StatusCreated)
		})
	case "explicit-body":
		route.Response(201, decoderInput{}).Body(prototype).HandlerFunc(managed)
	case "body-explicit":
		route.Body(prototype).Response(201, decoderInput{}).HandlerFunc(managed)
	case "inferred-body":
		route.Status(201).Body(prototype).HandlerFunc(managed)
	case "body-inferred":
		route.Body(prototype).Status(201).HandlerFunc(managed)
	}
}

func TestRequiredJSONBody(t *testing.T) {
	tests := []struct {
		name, contentType, body string
		status                  int
	}{
		{"object", "application/json", `{"name":"Ada","count":2}`, 201},
		{"empty object", "application/json", `{}`, 201},
		{"parameters", "application/json; charset=utf-8; profile=example", `{}`, 201},
		{"quoted parameter", `Application/JSON; charset="utf-8"`, `{}`, 201},
		{"unknown fields", "application/json", `{"unknown":{"anything":[1,2]}}`, 201},
		{"whitespace", "application/json", " \t\r\n{} \t\r\n", 201},
		{"missing content type", "", `{}`, 415},
		{"unsupported content type", "text/plain", `{}`, 415},
		{"json suffix", "application/problem+json", `{}`, 415},
		{"invalid parameter", "application/json; charset", `{}`, 415},
		{"multiple media types", "application/json, text/plain", `{}`, 415},
		{"empty", "application/json", "", 400},
		{"whitespace only", "application/json", " \t\r\n", 400},
		{"null", "application/json", `null`, 400},
		{"array", "application/json", `[]`, 400},
		{"string", "application/json", `"hello"`, 400},
		{"number", "application/json", `42`, 400},
		{"boolean", "application/json", `true`, 400},
		{"malformed", "application/json", `{"name":`, 400},
		{"wrong field type", "application/json", `{"count":"two"}`, 400},
		{"trailing object", "application/json", `{} {}`, 400},
		{"trailing null", "application/json", `{} null`, 400},
		{"trailing garbage", "application/json", `{} garbage`, 400},
		{"unicode leading whitespace", "application/json", "\u00a0{}", 400},
		{"unicode trailing whitespace", "application/json", "{}\u00a0", 400},
	}
	for _, mode := range bodyModes {
		t.Run(mode, func(t *testing.T) {
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					api := NewApi(http.NewServeMux())
					calls := 0
					registerDecoderRoute(api, mode, func(body decoderInput) {
						calls++
						if test.name == "object" {
							if body.Name != "Ada" || body.Count != 2 {
								t.Errorf("decoded body: %+v", body)
							}
						} else if !reflect.DeepEqual(body, decoderInput{}) {
							t.Errorf("prototype supplied defaults: %+v", body)
						}
					})
					req := httptest.NewRequest("POST", "/body", strings.NewReader(test.body))
					req.Header.Set("Content-Type", test.contentType)
					rr := httptest.NewRecorder()
					api.mux.ServeHTTP(rr, req)
					if rr.Code != test.status {
						t.Fatalf("status = %d, want %d; %s", rr.Code, test.status, rr.Body)
					}
					wantCalls := 0
					if test.status == 201 {
						wantCalls = 1
					}
					if calls != wantCalls {
						t.Fatalf("handler called %d times, want %d", calls, wantCalls)
					}
					if wantCalls == 0 && rr.Body.String() != http.StatusText(test.status)+"\n" {
						t.Errorf("unexpected public error: %q", rr.Body.String())
					}
				})
			}
		})
	}
}

func TestJSONBodyLimit(t *testing.T) {
	for _, mode := range bodyModes {
		for _, configureAfter := range []bool{false, true} {
			t.Run(mode+"/after="+strconv.FormatBool(configureAfter), func(t *testing.T) {
				api := NewApi(http.NewServeMux())
				if !configureAfter {
					api.BodyLimit(8)
				}
				calls := 0
				registerDecoderRoute(api, mode, func(decoderInput) { calls++ })
				if configureAfter && api.BodyLimit(8) != api {
					t.Fatal("BodyLimit must return the API")
				}
				for _, length := range []int{7, 8, 9} {
					for _, declared := range []int64{-1, 0, int64(length), 1000} {
						req := httptest.NewRequest("POST", "/body", strings.NewReader("{}"+strings.Repeat(" ", length-2)))
						req.ContentLength = declared
						req.Header.Set("Content-Type", "application/json")
						rr := httptest.NewRecorder()
						before := calls
						api.mux.ServeHTTP(rr, req)
						want, wantCalls := 201, 1
						if length > 8 {
							want, wantCalls = 413, 0
						}
						if rr.Code != want || calls-before != wantCalls {
							t.Fatalf("length %d, declared %d: status %d, calls %d", length, declared, rr.Code, calls-before)
						}
					}
				}
			})
		}
	}
	api := NewApi(http.NewServeMux())
	registerDecoderRoute(api, "ordinary", func(decoderInput) {})
	for _, length := range []int{int(DefaultBodyLimit), int(DefaultBodyLimit) + 1} {
		req := httptest.NewRequest("POST", "/body", strings.NewReader("{}"+strings.Repeat(" ", length-2)))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		api.mux.ServeHTTP(rr, req)
		want := 201
		if length > int(DefaultBodyLimit) {
			want = 413
		}
		if rr.Code != want {
			t.Fatalf("default limit at %d: got %d, want %d", length, rr.Code, want)
		}
	}
}

func TestBodyLimitRejectsNonPositive(t *testing.T) {
	for _, limit := range []int64{0, -1} {
		func() {
			defer func() {
				if failure := recover(); failure == nil || !strings.Contains(failure.(string), "positive byte limit") {
					t.Errorf("unexpected panic: %v", failure)
				}
			}()
			NewApi(http.NewServeMux()).BodyLimit(limit)
		}()
	}
}

func TestJSONBodyFreshState(t *testing.T) {
	for _, mode := range bodyModes {
		t.Run(mode, func(t *testing.T) {
			api := NewApi(http.NewServeMux())
			var received []decoderInput
			registerDecoderRoute(api, mode, func(body decoderInput) { received = append(received, body) })
			for _, data := range []string{`{"labels":{"x":1},"values":[1],"nested":{"value":1}}`, `{"labels":{"x":1},"values":[1],"nested":{"value":1}}`, `{}`} {
				req := httptest.NewRequest("POST", "/body", strings.NewReader(data))
				req.Header.Set("Content-Type", "application/json")
				api.mux.ServeHTTP(httptest.NewRecorder(), req)
			}
			if len(received) != 3 {
				t.Fatalf("received %d bodies", len(received))
			}
			received[0].Labels["x"], received[0].Values[0], received[0].Nested.Value = 9, 9, 9
			if received[1].Labels["x"] != 1 || received[1].Values[0] != 1 || received[1].Nested.Value != 1 || !reflect.DeepEqual(received[2], decoderInput{}) {
				t.Fatalf("request state shared: %+v", received)
			}
		})
	}
}

type failingBodyReader struct{}

func (failingBodyReader) Read([]byte) (int, error) { return 0, errors.New("private read failure") }
func (failingBodyReader) Close() error             { return nil }

func TestJSONBodyReadFailure(t *testing.T) {
	for _, mode := range bodyModes {
		api := NewApi(http.NewServeMux())
		registerDecoderRoute(api, mode, func(decoderInput) { t.Fatal("handler invoked after read failure") })
		for _, body := range []io.ReadCloser{nil, failingBodyReader{}} {
			req := httptest.NewRequest("POST", "/body", nil)
			req.Body = body
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			api.mux.ServeHTTP(rr, req)
			if rr.Code != 400 || rr.Body.String() != "Bad Request\n" {
				t.Fatalf("read failure: %d %s", rr.Code, rr.Body)
			}
		}
	}
}
