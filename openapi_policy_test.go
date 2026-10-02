package buddy

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func generatedDocument(t *testing.T, api *Api) *openapi3.T {
	t.Helper()
	data, err := api.Generate()
	if err != nil {
		t.Fatal(err)
	}
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestOpenAPIResponsePolicy(t *testing.T) {
	for _, mode := range []string{"ordinary", "explicit", "inferred"} {
		for _, body := range []bool{false, true} {
			name := mode
			if body {
				name += "_body"
			}
			t.Run(name, func(t *testing.T) {
				api := NewApi(http.NewServeMux())
				route := api.Route("POST /users/{id}").Title("Create").Description("Create a user by ID")
				sentinel := errors.New("private conflict")
				handler := func(*http.Request) (User, error) { return User{}, nil }
				bodyHandler := func(*http.Request, CreateUserRequest) (User, error) { return User{}, nil }
				switch mode {
				case "ordinary":
					if body {
						route.Body(CreateUserRequest{Name: "prototype"}).HandlerFunc(func(http.ResponseWriter, *http.Request, CreateUserRequest) {})
					} else {
						route.HandlerFunc(noopHandler)
					}
				case "explicit":
					b := route.Response(201, User{ID: 99}).Error(409, sentinel).Error(409, errors.New("another conflict"))
					if body {
						b.Body(CreateUserRequest{}).HandlerFunc(bodyHandler)
					} else {
						b.HandlerFunc(handler)
					}
				case "inferred":
					b := route.Status(201).Error(409, sentinel).Error(409, errors.New("another conflict"))
					if body {
						b.Body(CreateUserRequest{}).HandlerFunc(bodyHandler)
					} else {
						b.HandlerFunc(handler)
					}
				}
				doc := generatedDocument(t, api)
				op := doc.Paths.Value("/users/{id}").Post
				statuses := []string{"default"}
				if mode != "ordinary" {
					statuses = []string{"201", "409", "500"}
				}
				if body {
					statuses = append(statuses, "400", "413", "415")
				}
				assertResponseStatuses(t, op.Responses, statuses...)
				if op.Summary != "Create" || op.Description != "Create a user by ID" {
					t.Fatalf("operation annotations lost: %+v", op)
				}
				if len(op.Parameters) != 1 {
					t.Fatal("missing path parameter")
				}
				param := op.Parameters[0].Value
				if param.Name != "id" || param.In != "path" || !param.Required || !param.Schema.Value.Type.Is("string") {
					t.Fatalf("path parameter: %+v", param)
				}
				if (op.RequestBody != nil) != body {
					t.Fatal("unexpected request body declaration")
				}
				if body {
					b := op.RequestBody.Value
					if !b.Required || len(b.Content) != 1 {
						t.Fatal("body must be required JSON")
					}
					schema := b.Content["application/json"].Schema.Value
					if schema.Properties["name"] == nil || schema.Properties["email"] == nil || len(schema.Required) != 0 {
						t.Fatal("body type or field requiredness differs")
					}
				}
				if mode == "ordinary" {
					response := op.Responses.Default().Value
					if response.Description == nil || *response.Description != "Handler-defined response" || len(response.Content) != 0 {
						t.Fatal("ordinary output must be handler-defined")
					}
				} else {
					schema := op.Responses.Value("201").Value.Content["application/json"].Schema.Value
					if schema.Properties["id"] == nil || schema.Properties["name"] == nil || schema.Properties["email"] == nil {
						t.Fatal("success model lost")
					}
					if err := schema.VisitJSON(map[string]any{"id": float64(0), "name": "", "email": ""}); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestOpenAPIMetadataAndDocs(t *testing.T) {
	mux := http.NewServeMux()
	api := NewApi(mux)
	if got := api.Title("Users API").Description("User management").Version("2026.10").Server("https://example.com/api"); got != api {
		t.Fatal("metadata setters must return the API")
	}
	api.Route("GET /users").Title("List users").Description("All users").HandlerFunc(noopHandler)
	api.Server("/api/v2")
	doc := generatedDocument(t, api)
	if doc.Info.Title != "Users API" || doc.Info.Description != "User management" || doc.Info.Version != "2026.10" {
		t.Fatalf("API metadata: %+v", doc.Info)
	}
	if len(doc.Servers) != 1 || doc.Servers[0].URL != "/api/v2" {
		t.Fatalf("server must be replaced: %+v", doc.Servers)
	}
	for _, path := range []string{"/openapi.json", "/docs"} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
		if rr.Code != 200 {
			t.Fatalf("%s: %d %s", path, rr.Code, rr.Body)
		}
		if path == "/openapi.json" {
			if rr.Header().Get("Content-Type") != "application/json" {
				t.Fatal("wrong OpenAPI content type")
			}
			var served openapi3.T
			if err := json.Unmarshal(rr.Body.Bytes(), &served); err != nil {
				t.Fatal(err)
			}
			if served.Info.Title != doc.Info.Title || served.Paths.Len() != 1 {
				t.Fatal("served document differs from configured routes")
			}
		} else if !strings.HasPrefix(rr.Header().Get("Content-Type"), "text/html") || !strings.Contains(rr.Body.String(), "Scalar.createApiReference") || !strings.Contains(rr.Body.String(), "/openapi.json") {
			t.Fatal("missing embedded docs UI")
		}
	}
	if doc.Paths.Len() != 1 || doc.Paths.Value("/docs") != nil || doc.Paths.Value("/openapi.json") != nil {
		t.Fatal("docs endpoints leaked into application metadata")
	}
	api.Server("").Description("")
	doc = generatedDocument(t, api)
	if len(doc.Servers) != 0 || doc.Info.Description != "" {
		t.Fatal("optional metadata not cleared")
	}
	for _, tc := range []struct {
		name string
		call func()
	}{
		{"Title", func() { api.Title(" \t") }},
		{"Version", func() { api.Version("") }},
		{"Server", func() { api.Server("https://example.com/has space") }},
		{"Server", func() { api.Server("https://[invalid") }},
	} {
		requireRoutePanic(t, "buddy:", tc.name, tc.call)
	}
	doc = generatedDocument(t, api)
	if doc.Info.Title != "Users API" || doc.Info.Version != "2026.10" || len(doc.Servers) != 0 {
		t.Fatal("invalid metadata mutated the API")
	}
}

func TestOpenAPIUnsupportedModels(t *testing.T) {
	for _, tc := range []struct {
		name, detail string
		register     func(*Api)
	}{
		{"body", "request body model", func(api *Api) {
			api.Route("POST /bad").Body(struct{ Value chan int }{}).HandlerFunc(func(http.ResponseWriter, *http.Request, struct{ Value chan int }) {})
		}},
		{"explicit", "field Value: unsupported schema type", func(api *Api) {
			api.Route("POST /bad").Response(201, struct{ Value func() }{}).HandlerFunc(func(*http.Request) (struct{ Value func() }, error) { return struct{ Value func() }{}, nil })
		}},
		{"inferred", "unsupported map key type", func(api *Api) {
			api.Route("POST /bad").Status(201).HandlerFunc(func(*http.Request) (struct{ Values map[int]string }, error) {
				return struct{ Values map[int]string }{}, nil
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			api := NewApi(mux)
			tc.register(api)
			data, err := api.Generate()
			if err == nil || data != nil || !strings.Contains(err.Error(), "POST /bad") || !strings.Contains(err.Error(), tc.detail) {
				t.Fatalf("missing contextual generation error: %s, %v", data, err)
			}
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, httptest.NewRequest("GET", "/openapi.json", nil))
			if rr.Code != 500 || !strings.Contains(rr.Body.String(), tc.detail) {
				t.Fatalf("docs error: %d %s", rr.Code, rr.Body)
			}
		})
	}
}
