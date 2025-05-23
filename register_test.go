package buddy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestApiHandlerFunc(t *testing.T) {
	// Create a new API instance
	api := &Api{
		mux:    http.NewServeMux(),
		Routes: []Route{},
	}

	// Define a test handler
	testHandler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test response"))
	}

	// Test pattern
	pattern := "GET /test"

	// Call HandlerFunc
	api.HandlerFunc(pattern, testHandler)

	// Verify that the route was added to Routes
	if len(api.Routes) != 1 {
		t.Errorf("Expected 1 route, got %d", len(api.Routes))
	}

	route := api.Routes[0]
	if route.Method != "GET" {
		t.Errorf("Expected method 'GET', got '%s'", route.Method)
	}
	if route.Path != "/test" {
		t.Errorf("Expected path '/test', got '%s'", route.Path)
	}

	// Test that the handler was actually registered with the mux
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	// check that the handler was registered
	api.mux.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, status)
	}

	expected := "test response"
	if rr.Body.String() != expected {
		t.Errorf("Expected body '%s', got '%s'", expected, rr.Body.String())
	}
}
