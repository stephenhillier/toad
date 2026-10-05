package toad_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stephenhillier/toad"
)

type createUserRequest struct {
	Name string `json:"name"`
}

type createdUser struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

var errNameTaken = errors.New("name already taken")

func createUser(_ *http.Request, body createUserRequest) (createdUser, error) {
	if body.Name == "taken" {
		return createdUser{}, errNameTaken
	}
	return createdUser{ID: 1, Name: body.Name}, nil
}

// Share the exact route and application handler between the HTTP test and the
// benchmark. The handler is deterministic and has no growing store or I/O.
func newCreateUserHandler() http.Handler {
	mux := http.NewServeMux()
	api := toad.NewApi(mux).BodyLimit(128)
	route := api.Route("POST /users").Description("Create a user").Body(createUserRequest{})
	route.Response(http.StatusCreated, createdUser{}).Error(http.StatusConflict, errNameTaken).HandlerFunc(createUser)
	return mux
}

func TestCreateUserEndToEnd(t *testing.T) {
	server := httptest.NewServer(newCreateUserHandler())
	t.Cleanup(server.Close)
	client := server.Client()

	// Retrieve the public document over HTTP, resolve its schemas, and validate
	// the complete OpenAPI document independently of Toad's Generate method.
	response, err := client.Get(server.URL + "/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	data := readJSONResponse(t, response, http.StatusOK)
	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("invalid OpenAPI: %v", err)
	}
	path := doc.Paths.Value("/users")
	if path == nil || path.Post == nil {
		t.Fatal("missing POST /users operation")
	}
	operation := path.Post
	if operation.Description != "Create a user" {
		t.Fatalf("description: %q", operation.Description)
	}
	if operation.RequestBody == nil || operation.RequestBody.Value == nil || !operation.RequestBody.Value.Required {
		t.Fatal("missing required request body")
	}
	bodySchema := jsonSchema(t, operation.RequestBody.Value.Content)
	assertPropertyType(t, bodySchema, "name", "string")
	for _, status := range []int{201, 409, 400, 413, 415, 500} {
		ref := operation.Responses.Value(strconv.Itoa(status))
		if ref == nil || ref.Value == nil {
			t.Fatalf("missing response schema for %d", status)
		}
		schema := jsonSchema(t, ref.Value.Content)
		if status == http.StatusCreated {
			assertPropertyType(t, schema, "id", "integer")
			assertPropertyType(t, schema, "name", "string")
		} else {
			assertPropertyType(t, schema, "code", "string")
			assertPropertyType(t, schema, "message", "string")
		}
	}

	for _, tc := range []struct {
		name, body, contentType string
		status                  int
		want                    string
	}{
		{"created", `{"name":"Ada"}`, "application/json", 201, `{"id":1,"name":"Ada"}`},
		{"conflict", `{"name":"taken"}`, "application/json", 409, `{"code":"application_error","message":"Conflict"}`},
		{"invalid body", `{"name":`, "application/json", 400, `{"code":"invalid_body","message":"Bad Request"}`},
		{"unsupported media type", `{"name":"Ada"}`, "text/plain", 415, `{"code":"unsupported_media_type","message":"Unsupported Media Type"}`},
		{"body too large", `{"name":"` + strings.Repeat("a", 128) + `"}`, "application/json", 413, `{"code":"body_too_large","message":"Request Entity Too Large"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.status == 201 || tc.status == 409 {
				if err := bodySchema.VisitJSON(decodeJSON(t, []byte(tc.body))); err != nil {
					t.Fatalf("request violates schema: %v", err)
				}
			}
			req, err := http.NewRequest(http.MethodPost, server.URL+"/users", strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", tc.contentType)
			response, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			data := readJSONResponse(t, response, tc.status)
			// Exact payload assertions catch missing fields and accidental disclosure;
			// schema validation independently checks the documented contract.
			if string(data) != tc.want {
				t.Fatalf("body = %s, want %s", data, tc.want)
			}
			schema := jsonSchema(t, operation.Responses.Value(strconv.Itoa(tc.status)).Value.Content)
			if err := schema.VisitJSON(decodeJSON(t, data)); err != nil {
				t.Fatalf("response violates schema: %v", err)
			}
		})
	}
}

func readJSONResponse(t *testing.T, response *http.Response, status int) []byte {
	t.Helper()
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status {
		t.Fatalf("status = %d, want %d: %s", response.StatusCode, status, data)
	}
	if got := response.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	return data
}

func decodeJSON(t *testing.T, data []byte) any {
	t.Helper()
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func jsonSchema(t *testing.T, content openapi3.Content) *openapi3.Schema {
	t.Helper()
	media := content["application/json"]
	if media == nil || media.Schema == nil || media.Schema.Value == nil {
		t.Fatal("missing resolved JSON schema")
	}
	return media.Schema.Value
}

func assertPropertyType(t *testing.T, schema *openapi3.Schema, name, kind string) {
	t.Helper()
	property := schema.Properties[name]
	if property == nil || property.Value == nil || !property.Value.Type.Is(kind) {
		t.Fatalf("missing %s property %q", kind, name)
	}
}

func BenchmarkCreateUserRequestHandling(b *testing.B) {
	for _, implementation := range []struct {
		name       string
		newHandler func() http.Handler
	}{
		{"Toad", newCreateUserHandler},
		{"Stdlib", newStdlibCreateUserHandler},
	} {
		b.Run(implementation.name, func(b *testing.B) {
			for _, tc := range []struct {
				name, body string
				status     int
			}{
				{"Created", `{"name":"Ada"}`, http.StatusCreated},
			} {
				b.Run(tc.name, func(b *testing.B) {
					benchmarkCreateUserRequests(b, implementation.newHandler(), tc.body, tc.status)
				})
			}
		})
	}
}

// Both Toad and the stdlib-only baseline use this harness with their mux.
// Route registration, request/recorder allocation, network transport, and OpenAPI
// generation are excluded. Each iteration measures ServeHTTP plus lightweight
// resets of reusable request/response buffers. Routing, body validation/decoding,
// application logic, encoding, and response writing are included.
func benchmarkCreateUserRequests(b *testing.B, handler http.Handler, body string, status int) {
	prototype := httptest.NewRequest(http.MethodPost, "/users", nil)
	prototype.Header.Set("Content-Type", "application/json")
	prototype.ContentLength = int64(len(body))
	var input strings.Reader
	requestBody := io.NopCloser(&input)
	var output bytes.Buffer
	output.Grow(256)
	headers := make(http.Header)
	var request http.Request
	var recorder httptest.ResponseRecorder
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	for b.Loop() {
		input.Reset(body)
		request = *prototype
		request.Body = requestBody
		output.Reset()
		clear(headers)
		recorder = httptest.ResponseRecorder{HeaderMap: headers, Body: &output}
		handler.ServeHTTP(&recorder, &request)
	}
	// b.Loop stops the timer before verification.
	if recorder.Code != status || headers.Get("Content-Type") != "application/json" || !json.Valid(output.Bytes()) {
		b.Fatalf("unexpected response: %d %v %s", recorder.Code, headers, output.Bytes())
	}
}
