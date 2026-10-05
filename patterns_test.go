package toad

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPatternSubset(t *testing.T) {
	for _, pattern := range []string{
		"GET /", "HEAD /health", "POST /teams/{teamID}/users/{userID}",
		"PUT /users/{id}", "PATCH /users/{id}", "DELETE /users/{id}",
		"OPTIONS /users", "TRACE /users", "GET /users/{名前}",
	} {
		t.Run(pattern, func(t *testing.T) {
			api := NewApi(http.NewServeMux())
			api.Route(pattern).HandlerFunc(noopHandler)
			if len(api.routes) != 1 {
				t.Fatal("Supported pattern not registered")
			}
			if _, err := api.Generate(); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, pattern := range []string{
		"", "GET", "/users", "GET ", "GET users", "GET example.com/users",
		" GET /users", "GET /users ", "GET  /users", "GET\t/users", "GET /user\ns", "GET /user\u00a0s",
		"get /users", "CONNECT /users", "CUSTOM /users", "GET /users/",
		"GET //users", "GET /users//id", "GET /./users", "GET /users/..",
		"GET /users?query=yes", "GET /users#fragment", "GET /%75sers",
		"GET /users/{id...}", "GET /{$}", "GET /users/{_}", "GET /users/{}",
		"GET /users/{id}/posts/{id}", "GET /users/{1id}", "GET /users/{type}",
		"GET /users/{id", "GET /users/id}", "GET /users/pre{id}", "GET /users/{id}suffix",
		"GET /users/{{id}}", "GET /users/{a-b}",
	} {
		t.Run(pattern, func(t *testing.T) {
			api := NewApi(http.NewServeMux())
			requireRoutePanic(t, pattern, "toad:", func() { api.Route(pattern) })
			if len(api.routes) != 0 {
				t.Fatal("Invalid pattern published metadata")
			}
		})
	}
}

func TestServeMuxMatchingIsPreserved(t *testing.T) {
	api := NewApi(http.NewServeMux())
	api.Route("GET /").HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) })
	api.Route("GET /users/{id}").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("User", r.PathValue("id"))
		w.WriteHeader(200)
	})
	api.Route("HEAD /users/{id}").HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) })
	api.Route("POST /users/{id}").HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(203) })
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/fallback", 202}, {"HEAD", "/fallback", 202},
		{"GET", "/users/7", 200}, {"HEAD", "/users/7", 201}, {"POST", "/users/7", 203},
	} {
		rr := httptest.NewRecorder()
		api.mux.ServeHTTP(rr, httptest.NewRequest(test.method, test.path, nil))
		if rr.Code != test.status {
			t.Errorf("%s %s: got %d, want %d", test.method, test.path, rr.Code, test.status)
		}
		if test.method == "GET" && test.path == "/users/7" && rr.Header().Get("User") != "7" {
			t.Error("PathValue not preserved")
		}
	}
}

func TestNewApiRejectsNilMux(t *testing.T) {
	requireRoutePanic(t, "NewApi", "non-nil ServeMux", func() { NewApi(nil) })
}
