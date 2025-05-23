package buddy

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestApiGenerate(t *testing.T) {
	// Create a new API instance with some test routes
	api := &Api{
		mux: http.NewServeMux(),
		Routes: []Route{
			{Method: "GET", Path: "/users", Description: "Get all users"},
			{Method: "POST", Path: "/users", Description: "Create a user"},
			{Method: "GET", Path: "/users/{id}", Description: "Get user by ID"},
		},
	}

	// Generate OpenAPI spec
	data, err := api.generate()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify we got JSON data
	if len(data) == 0 {
		t.Fatal("Expected non-empty JSON data")
	}

	// Parse the JSON to verify it's valid
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Generated JSON is invalid: %v", err)
	}

	// Verify required OpenAPI fields
	if result["openapi"] != "3.0.0" {
		t.Errorf("Expected openapi version '3.0.0', got '%v'", result["openapi"])
	}

	info, ok := result["info"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected 'info' object in OpenAPI spec")
	}

	if info["title"] != "API Documentation" {
		t.Errorf("Expected title 'API Documentation', got '%v'", info["title"])
	}

	if info["version"] != "1.0.0" {
		t.Errorf("Expected version '1.0.0', got '%v'", info["version"])
	}

	// Verify paths object exists and contains our routes
	paths, ok := result["paths"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected 'paths' object in OpenAPI spec")
	}

	// Check that our test routes are present
	if _, exists := paths["/users"]; !exists {
		t.Error("Expected '/users' path in OpenAPI spec")
	}

	if _, exists := paths["/users/{id}"]; !exists {
		t.Error("Expected '/users/{id}' path in OpenAPI spec")
	}

	// Verify GET /users operation
	usersPath, ok := paths["/users"].(map[string]interface{})
	if ok {
		if getOp, exists := usersPath["get"]; exists {
			if getOpMap, ok := getOp.(map[string]interface{}); ok {
				if summary := getOpMap["summary"]; summary != "Get all users" {
					t.Errorf("Expected GET /users summary 'Get all users', got '%v'", summary)
				}
			}
		} else {
			t.Error("Expected GET operation for /users path")
		}

		// Verify POST /users operation
		if postOp, exists := usersPath["post"]; exists {
			if postOpMap, ok := postOp.(map[string]interface{}); ok {
				if summary := postOpMap["summary"]; summary != "Create a user" {
					t.Errorf("Expected POST /users summary 'Create a user', got '%v'", summary)
				}
			}
		} else {
			t.Error("Expected POST operation for /users path")
		}
	}

	// Verify GET /users/{id} operation has path parameter
	userByIdPath, ok := paths["/users/{id}"].(map[string]interface{})
	if ok {
		if getOp, exists := userByIdPath["get"]; exists {
			if getOpMap, ok := getOp.(map[string]interface{}); ok {
				if params, exists := getOpMap["parameters"]; exists {
					if paramsList, ok := params.([]interface{}); ok && len(paramsList) > 0 {
						if param, ok := paramsList[0].(map[string]interface{}); ok {
							if param["name"] != "id" {
								t.Errorf("Expected parameter name 'id', got '%v'", param["name"])
							}
							if param["in"] != "path" {
								t.Errorf("Expected parameter in 'path', got '%v'", param["in"])
							}
						}
					} else {
						t.Error("Expected parameters array for /users/{id} GET operation")
					}
				} else {
					t.Error("Expected parameters for /users/{id} GET operation")
				}
			}
		}
	}

	// Verify servers array exists
	servers, ok := result["servers"].([]interface{})
	if !ok {
		t.Fatal("Expected 'servers' array in OpenAPI spec")
	}

	if len(servers) == 0 {
		t.Fatal("Expected at least one server in OpenAPI spec")
	}
}

func TestApiGenerateEmpty(t *testing.T) {
	// Create a new API instance with no routes
	api := &Api{
		mux:    http.NewServeMux(),
		Routes: []Route{},
	}

	// Generate OpenAPI spec
	data, err := api.generate()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Verify we got valid JSON even with no routes
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Generated JSON is invalid: %v", err)
	}

	// Should still have basic OpenAPI structure
	if result["openapi"] != "3.0.0" {
		t.Errorf("Expected openapi version '3.0.0', got '%v'", result["openapi"])
	}
}

func TestApiGenerateMultipleOperationsOnSamePath(t *testing.T) {
	// Create API with multiple operations on same path
	api := &Api{
		mux: http.NewServeMux(),
		Routes: []Route{
			{Method: "GET", Path: "/items", Description: "Get all items"},
			{Method: "POST", Path: "/items", Description: "Create an item"},
			{Method: "PUT", Path: "/items", Description: "Update items"},
			{Method: "DELETE", Path: "/items", Description: "Delete all items"},
		},
	}

	// Generate OpenAPI spec
	data, err := api.generate()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Parse the JSON
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Generated JSON is invalid: %v", err)
	}

	// Get paths
	paths, ok := result["paths"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected 'paths' object in OpenAPI spec")
	}

	// Should have only one path entry for /items
	if len(paths) != 1 {
		t.Errorf("Expected 1 path, got %d", len(paths))
	}

	// Get /items path
	itemsPath, ok := paths["/items"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected '/items' path in OpenAPI spec")
	}

	// Verify all HTTP methods are present
	expectedMethods := []string{"get", "post", "put", "delete"}
	for _, method := range expectedMethods {
		if _, exists := itemsPath[method]; !exists {
			t.Errorf("Expected %s operation for /items path", method)
		}
	}

	// Verify specific operation details
	if getOp, ok := itemsPath["get"].(map[string]interface{}); ok {
		if summary := getOp["summary"]; summary != "Get all items" {
			t.Errorf("Expected GET summary 'Get all items', got '%v'", summary)
		}
	}

	if postOp, ok := itemsPath["post"].(map[string]interface{}); ok {
		if summary := postOp["summary"]; summary != "Create an item" {
			t.Errorf("Expected POST summary 'Create an item', got '%v'", summary)
		}
	}
}
