package toad

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestRoutingMetadataIntegration(t *testing.T) {
	type input struct {
		Name string `json:"name"`
	}
	type output struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	type contextKey struct{}
	mux := http.NewServeMux()
	api := NewApi(mux)
	const path = "/teams/{team}/users/{id}"
	methods := []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD", "TRACE"}
	checkRequest := func(r *http.Request) {
		t.Helper()
		if r.PathValue("team") != "engineering" || r.PathValue("id") != "42" {
			t.Errorf("path values lost: %q, %q", r.PathValue("team"), r.PathValue("id"))
		}
		if r.Context().Value(contextKey{}) != "middleware" || r.Context().Err() != context.Canceled {
			t.Error("middleware context or cancellation lost")
		}
		if r.Pattern != r.Method+" "+path {
			t.Errorf("matched pattern lost: %q", r.Pattern)
		}
	}
	for i, method := range methods {
		route := api.Route(method + " " + path).Title(method).Description("Handle " + method)
		managed := func(r *http.Request) (output, error) {
			checkRequest(r)
			return output{ID: r.PathValue("id")}, nil
		}
		managedBody := func(r *http.Request, body input) (output, error) {
			checkRequest(r)
			return output{ID: r.PathValue("id"), Name: body.Name}, nil
		}
		switch i {
		case 0:
			route.Response(200, output{ID: "prototype"}).HandlerFunc(managed)
		case 1:
			route.Body(input{Name: "prototype"}).Response(201, output{}).HandlerFunc(managedBody)
		case 2:
			route.Response(202, output{}).Body(input{}).HandlerFunc(managedBody)
		case 3:
			route.Response(203, output{}).HandlerFunc(managed)
		case 4:
			route.Body(input{}).HandlerFunc(func(w http.ResponseWriter, r *http.Request, body input) {
				checkRequest(r)
				if body.Name != "Ada" {
					t.Errorf("decoded body: %+v", body)
				}
				w.WriteHeader(204)
			})
		default:
			route.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				checkRequest(r)
				w.WriteHeader(204)
			})
		}
	}
	// Exercise the public documentation endpoint through the same mux.
	docs := httptest.NewRecorder()
	mux.ServeHTTP(docs, httptest.NewRequest("GET", "/openapi.json", nil))
	if docs.Code != 200 {
		t.Fatalf("documentation: %d %s", docs.Code, docs.Body)
	}
	doc, err := openapi3.NewLoader().LoadFromData(docs.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if doc.Paths.Len() != 1 {
		t.Fatalf("unexpected documented paths: %d", doc.Paths.Len())
	}
	item := doc.Paths.Value(path)
	if item == nil || len(item.Operations()) != len(methods) {
		t.Fatal("methods were lost from shared path")
	}
	middleware := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Middleware", "present")
		ctx, cancel := context.WithCancel(context.WithValue(r.Context(), contextKey{}, "middleware"))
		cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
	for i, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/teams/engineering/users/42", strings.NewReader(`{"name":"Ada"}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			middleware.ServeHTTP(rec, req)
			wantStatus := 204
			if i < 4 {
				wantStatus = 200 + i
			}
			if rec.Code != wantStatus || rec.Header().Get("X-Middleware") != "present" {
				t.Fatalf("runtime response: %d %v", rec.Code, rec.Header())
			}
			op := item.GetOperation(method)
			if op.Summary != method || op.Description != "Handle "+method {
				t.Fatal("operation metadata differs")
			}
			hasBody := i == 1 || i == 2 || i == 4
			if (op.RequestBody != nil) != hasBody {
				t.Fatal("request body metadata differs")
			}
			if hasBody && (!op.RequestBody.Value.Required || op.RequestBody.Value.Content["application/json"].Schema.Value.Properties["name"] == nil) {
				t.Fatal("missing required body schema")
			}
			if len(op.Parameters) != 2 {
				t.Fatal("missing path parameters")
			}
			for j, name := range []string{"team", "id"} {
				param := op.Parameters[j].Value
				if param.Name != name || param.In != "path" || !param.Required || !param.Schema.Value.Type.Is("string") {
					t.Fatalf("incorrect parameter: %+v", param)
				}
			}
			if i < 4 {
				schema := op.Responses.Value(strconv.Itoa(rec.Code)).Value.Content["application/json"].Schema.Value
				if err := schema.VisitJSON(jsonValue(t, rec.Body.Bytes())); err != nil {
					t.Fatal(err)
				}
				var got output
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.ID != "42" || (hasBody && got.Name != "Ada") {
					t.Fatalf("unexpected model: %+v", got)
				}
			}
		})
	}
}

func jsonValue(t *testing.T, data []byte) any {
	t.Helper()
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestRoutingPrecedenceAndConflicts(t *testing.T) {
	mux := http.NewServeMux()
	api := NewApi(mux)
	api.Route("GET /users/{id}").HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) })
	api.Route("GET /users/me").HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(203) })
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/users/me", 203}, {"GET", "/users/42", 202},
		{"HEAD", "/users/42", 202}, {"POST", "/users/42", 405}, {"GET", "/missing", 404},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(test.method, test.path, nil))
		if rec.Code != test.status {
			t.Errorf("%s %s: %d", test.method, test.path, rec.Code)
		}
	}
	data, err := api.Generate()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Paths.Value("/users/{id}").Head != nil {
		t.Fatal("invented HEAD operation")
	}

	// Neither overlapping pattern is more specific; preserve ServeMux's rejection.
	api.Route("GET /a/{x}/c").HandlerFunc(noopHandler)
	route := api.Route("GET /a/b/{y}")
	requireRoutePanic(t, "GET /a/b/{y}", "ServeMux registration failed", func() { route.HandlerFunc(noopHandler) })
	if route.config.finalized || len(api.routes) != 3 {
		t.Fatal("conflict published metadata")
	}
}
