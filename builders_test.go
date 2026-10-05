package toad

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func requireRoutePanic(t *testing.T, pattern, rule string, call func()) {
	t.Helper()
	defer func() {
		failure := recover()
		if failure == nil {
			t.Errorf("Expected panic for %q", pattern)
			return
		}
		message := fmt.Sprint(failure)
		if (!strings.Contains(message, pattern) && !strings.Contains(message, fmt.Sprintf("%q", pattern))) || !strings.Contains(message, rule) {
			t.Errorf("Expected panic containing %q and %q, got %q", pattern, rule, message)
		}
	}()
	call()
}

func TestUnfinishedAndSharedBuilderState(t *testing.T) {
	api := NewApi(http.NewServeMux())
	old := api.Route("POST /test")
	copied := *old
	body := old.Body(CreateUserRequest{Name: "prototype"})
	old.Title("shared title").Description("shared description")
	if len(api.routes) != 0 {
		t.Fatal("Unfinished route published metadata")
	}
	data, err := api.Generate()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Paths.Len() != 0 {
		t.Fatal("Unfinished route appears in documentation")
	}
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, httptest.NewRequest("POST", "/test", nil))
	if rr.Code != 404 {
		t.Fatalf("Unfinished route registered a handler: %d", rr.Code)
	}
	requireRoutePanic(t, "POST /test", "obsolete builder", func() { copied.HandlerFunc(noopHandler) })
	if len(api.routes) != 0 || old.config.finalized {
		t.Fatal("Obsolete terminal finalized the route")
	}
	body.HandlerFunc(func(w http.ResponseWriter, r *http.Request, input CreateUserRequest) {
		if input.Name != "" || input.Email != "test@example.com" {
			t.Errorf("Prototype leaked into decoded body: %+v", input)
		}
		w.WriteHeader(201)
	})
	rr = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/test", strings.NewReader(`{"email":"test@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	api.mux.ServeHTTP(rr, req)
	if rr.Code != 201 {
		t.Fatalf("Registered body handler returned %d", rr.Code)
	}
	if len(api.routes) != 1 || api.routes[0].title != "shared title" || api.routes[0].description != "shared description" {
		t.Fatalf("Shared builder configuration was lost: %+v", api.routes)
	}
	for name, call := range map[string]func(){
		"old title":          func() { old.Title("changed") },
		"copied description": func() { copied.Description("changed") },
		"old body":           func() { old.Body(User{}) },
		"new response":       func() { body.Response(200, User{}) },
		"old terminal":       func() { old.HandlerFunc(noopHandler) },
		"body terminal":      func() { body.HandlerFunc(func(http.ResponseWriter, *http.Request, CreateUserRequest) {}) },
	} {
		t.Run(name, func(t *testing.T) { requireRoutePanic(t, "POST /test", "already been finalized", call) })
	}
	if api.routes[0].title != "shared title" {
		t.Fatal("Old builder changed finalized metadata")
	}
}

func TestTypedManagedFinalization(t *testing.T) {
	for _, order := range []string{"explicit-body", "body-explicit", "explicit"} {
		t.Run(order, func(t *testing.T) {
			api := NewApi(http.NewServeMux())
			route := api.Route("POST /users")
			sentinel := errors.New("missing")
			handler := func(r *http.Request, body CreateUserRequest) (User, error) { return User{ID: 7, Name: body.Name}, nil }
			switch order {
			case "explicit-body":
				route.Response(201, User{ID: 999}).Error(404, sentinel).Body(CreateUserRequest{}).Title("Create").Description("Long description").HandlerFunc(handler)
			case "body-explicit":
				route.Body(CreateUserRequest{}).Response(201, User{}).Title("Create").Description("Long description").Error(404, sentinel).HandlerFunc(handler)
			case "explicit":
				route.Response(201, User{ID: 999}).Title("Create").Description("Long description").Error(404, sentinel).HandlerFunc(func(r *http.Request) (User, error) { return User{ID: 7}, nil })
			}
			r := api.routes[0]
			if r.resultType != reflect.TypeFor[User]() || r.status != 201 || r.title != "Create" || r.description != "Long description" || len(r.errors) != 1 || r.errors[0].sentinel != sentinel {
				t.Fatalf("Lost finalized metadata: %+v", r)
			}
			hasBody := strings.Contains(order, "body")
			if (r.bodyType != nil) != hasBody {
				t.Fatalf("Unexpected body type: %v", r.bodyType)
			}
			rr := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/users", strings.NewReader(`{"name":"Ada"}`))
			req.Header.Set("Content-Type", "application/json")
			api.mux.ServeHTTP(rr, req)
			if rr.Code != 201 || !strings.Contains(rr.Body.String(), `"id":7`) {
				t.Fatalf("Unexpected response: %d %s", rr.Code, rr.Body)
			}
			data, err := api.Generate()
			if err != nil {
				t.Fatal(err)
			}
			doc, err := openapi3.NewLoader().LoadFromData(data)
			if err != nil {
				t.Fatal(err)
			}
			op := doc.Paths.Find("/users").Post
			if op.Summary != "Create" || op.Description != "Long description" || (op.RequestBody != nil) != hasBody {
				t.Fatalf("Generated operation differs from configuration: %+v", op)
			}
			if op.Responses.Value("201").Value.Content["application/json"].Schema.Value.Properties["id"] == nil {
				t.Fatal("Missing declared response schema")
			}
			requireRoutePanic(t, "POST /users", "already been finalized", func() { route.Description("changed") })
		})
	}
}

func TestSingleUseSelectorsAndObsoleteBuilders(t *testing.T) {
	tests := map[string]func(*RouteBuilder){
		"body twice":                       func(b *RouteBuilder) { b.Body(User{}).Body(User{}) },
		"body through old state":           func(b *RouteBuilder) { b.Body(User{}); b.Body(CreateUserRequest{}) },
		"response twice":                   func(b *RouteBuilder) { b.Response(200, User{}).Response(201, User{}) },
		"selector through old state":       func(b *RouteBuilder) { b.Response(200, User{}); b.Response(200, User{}) },
		"body response twice":              func(b *RouteBuilder) { b.Body(User{}).Response(200, User{}).Response(201, User{}) },
		"obsolete ordinary after response": func(b *RouteBuilder) { b.Response(200, User{}); b.HandlerFunc(noopHandler) },
		"obsolete body after response": func(b *RouteBuilder) {
			old := b.Body(User{})
			old.Response(200, User{})
			old.HandlerFunc(func(http.ResponseWriter, *http.Request, User) {})
		},
		"obsolete response after body": func(b *RouteBuilder) {
			old := b.Response(200, User{})
			old.Body(User{})
			old.HandlerFunc(func(*http.Request) (User, error) { return User{}, nil })
		},
	}
	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			api := NewApi(http.NewServeMux())
			b := api.Route("POST /test")
			requireRoutePanic(t, "POST /test", "toad:", func() { call(b) })
			if len(api.routes) != 0 || b.config.finalized {
				t.Fatal("Invalid selection or obsolete terminal finalized metadata")
			}
		})
	}
}

func TestFailedRegistrationPublishesNothing(t *testing.T) {
	for _, pattern := range []string{"GET /direct", "GET /docs", "GET /openapi.json", "GET /duplicate", "GET /users/{name}", "POST /users/{name}"} {
		t.Run(pattern, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /direct", noopHandler)
			api := NewApi(mux)
			api.Route("GET /duplicate").HandlerFunc(noopHandler)
			api.Route("GET /users/{id}").HandlerFunc(noopHandler)
			route := api.Route(pattern)
			requireRoutePanic(t, pattern, "toad:", func() { route.HandlerFunc(noopHandler) })
			if len(api.routes) != 2 || route.config.finalized {
				t.Fatal("Failed registration published metadata or finalized the builder")
			}
		})
	}
	api := NewApi(http.NewServeMux())
	b := api.Route("GET /retry")
	requireRoutePanic(t, "GET /retry", "non-nil handler", func() { b.HandlerFunc(nil) })
	if b.config.finalized || len(api.routes) != 0 {
		t.Fatal("Nil handler finalized the route")
	}
	b.HandlerFunc(noopHandler)
	if len(api.routes) != 1 {
		t.Fatal("Validation failure prevented a corrected registration")
	}
}

func TestManagedSnapshotAndErrorOrder(t *testing.T) {
	api := NewApi(http.NewServeMux())
	first, second := errors.New("first"), errors.New("second")
	b := api.Route("GET /test").Response(200, User{}).Error(409, first).Error(404, second)
	b.HandlerFunc(func(*http.Request) (User, error) { return User{}, first })
	record := api.routes[0]
	if record.errors[0].sentinel != first || record.errors[1].sentinel != second {
		t.Fatal("Error declaration order changed")
	}
	for name, call := range map[string]func(){
		"error":    func() { b.Error(500, first) },
		"title":    func() { b.Title("changed") },
		"terminal": func() { b.HandlerFunc(func(*http.Request) (User, error) { return User{}, nil }) },
	} {
		t.Run(name, func(t *testing.T) { requireRoutePanic(t, "GET /test", "already been finalized", call) })
	}
	// Deliberately modify the private setup record to prove the registry and
	// installed wrapper copied mutable collections rather than retaining them.
	b.config.record.errors[0].status = 418
	b.config.record.status = 202
	b.config.record.description = "changed"
	if record.errors[0].status != 409 || api.routes[0].status != 200 || api.routes[0].description != "" {
		t.Fatal("Finalized snapshot aliases configuration")
	}
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, httptest.NewRequest("GET", "/test", nil))
	if rr.Code != 409 {
		t.Fatalf("Registered wrapper aliases configuration: %d", rr.Code)
	}
}

func TestConfigurationValidationBeforeMutation(t *testing.T) {
	api := NewApi(http.NewServeMux())
	b := api.Route("POST /test")
	requireRoutePanic(t, "POST /test", "struct-valued", func() { b.Body(&User{}) })
	if b.config.record.bodyType != nil {
		t.Fatal("Invalid body changed configuration")
	}
	for _, status := range []int{0, 199, 204, 205, 300, 600} {
		requireRoutePanic(t, "POST /test", "success status", func() { b.Response(status, User{}) })
	}
	if b.config.record.mode != ordinary {
		t.Fatal("Invalid status selected a managed mode")
	}
	managed := b.Response(201, User{})
	requireRoutePanic(t, "POST /test", "non-nil sentinel", func() { managed.Error(400, nil) })
	requireRoutePanic(t, "POST /test", "error status", func() { managed.Error(200, errors.New("bad")) })
	first := errors.New("first")
	managed.Error(409, first)
	requireRoutePanic(t, "POST /test", "conflicting statuses", func() { managed.Error(404, first) })
	if len(managed.config.record.errors) != 1 || managed.config.record.errors[0].status != 409 {
		t.Fatal("Invalid error mapping changed configuration")
	}
	managed.HandlerFunc(func(*http.Request) (User, error) { return User{}, nil })
}

func TestNilHandlerAndRepeatedFinalizationInEveryState(t *testing.T) {
	tests := map[string]func(*RouteBuilder) func(bool){
		"ordinary": func(b *RouteBuilder) func(bool) {
			return func(valid bool) {
				var handler func(http.ResponseWriter, *http.Request)
				if valid {
					handler = noopHandler
				}
				b.HandlerFunc(handler)
			}
		},
		"body": func(b *RouteBuilder) func(bool) {
			typed := b.Body(CreateUserRequest{})
			return func(valid bool) {
				var handler func(http.ResponseWriter, *http.Request, CreateUserRequest)
				if valid {
					handler = func(http.ResponseWriter, *http.Request, CreateUserRequest) {}
				}
				typed.HandlerFunc(handler)
			}
		},
		"explicit": func(b *RouteBuilder) func(bool) {
			typed := b.Response(200, User{})
			return func(valid bool) {
				var handler func(*http.Request) (User, error)
				if valid {
					handler = func(*http.Request) (User, error) { return User{}, nil }
				}
				typed.HandlerFunc(handler)
			}
		},
		"body explicit": func(b *RouteBuilder) func(bool) {
			typed := b.Body(CreateUserRequest{}).Response(200, User{})
			return func(valid bool) {
				var handler func(*http.Request, CreateUserRequest) (User, error)
				if valid {
					handler = func(*http.Request, CreateUserRequest) (User, error) { return User{}, nil }
				}
				typed.HandlerFunc(handler)
			}
		},
	}
	for name, setup := range tests {
		t.Run(name, func(t *testing.T) {
			api := NewApi(http.NewServeMux())
			base := api.Route("POST /test")
			finalize := setup(base)
			requireRoutePanic(t, "POST /test", "non-nil handler", func() { finalize(false) })
			if len(api.routes) != 0 || base.config.finalized {
				t.Fatal("Nil handler registered a route")
			}
			finalize(true)
			requireRoutePanic(t, "POST /test", "already been finalized", func() { finalize(true) })
			if len(api.routes) != 1 || !base.config.finalized {
				t.Fatal("Route did not finalize exactly once")
			}
		})
	}
}
