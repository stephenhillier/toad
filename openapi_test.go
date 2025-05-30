package buddy

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

type User struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type CreateUserRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

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
	data, err := api.Generate()
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

type UserResponse struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Code    int    `json:"code"`
}

func TestApiGenerateWithCustomResponseSchemas(t *testing.T) {
	api := &Api{
		mux: http.NewServeMux(),
		Routes: []Route{
			{
				Method:      "GET",
				Path:        "/users/{id}",
				Description: "Get user by ID",
				Responses: map[int]any{
					200: UserResponse{},
					404: ErrorResponse{},
				},
			},
			{
				Method:      "POST",
				Path:        "/users",
				Description: "Create a user",
				Body:        CreateUserRequest{},
				Responses: map[int]any{
					201: UserResponse{},
					400: ErrorResponse{},
				},
			},
		},
	}

	data, err := api.Generate()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	var spec openapi3.T
	err = json.Unmarshal(data, &spec)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	// Test GET /users/{id} custom responses
	getOp := spec.Paths.Find("/users/{id}").Get
	if getOp == nil {
		t.Fatal("GET /users/{id} operation not found")
	}

	// Check custom 200 response with UserResponse schema
	response200 := getOp.Responses.Value("200")
	if response200 == nil {
		t.Fatal("GET /users/{id} should have 200 response")
	}
	jsonContent200 := response200.Value.Content["application/json"]
	if jsonContent200 == nil {
		t.Fatal("200 response should have application/json content")
	}
	schema200 := jsonContent200.Schema.Value
	if schema200.Properties == nil {
		t.Fatal("200 response schema should have properties")
	}
	if schema200.Properties["id"] == nil {
		t.Fatal("UserResponse schema should have 'id' property")
	}
	if schema200.Properties["name"] == nil {
		t.Fatal("UserResponse schema should have 'name' property")
	}
	if schema200.Properties["email"] == nil {
		t.Fatal("UserResponse schema should have 'email' property")
	}

	// Check custom 404 response with ErrorResponse schema
	response404 := getOp.Responses.Value("404")
	if response404 == nil {
		t.Fatal("GET /users/{id} should have 404 response")
	}
	jsonContent404 := response404.Value.Content["application/json"]
	if jsonContent404 == nil {
		t.Fatal("404 response should have application/json content")
	}
	schema404 := jsonContent404.Schema.Value
	if schema404.Properties == nil {
		t.Fatal("404 response schema should have properties")
	}
	if schema404.Properties["error"] == nil {
		t.Fatal("ErrorResponse schema should have 'error' property")
	}
	if schema404.Properties["message"] == nil {
		t.Fatal("ErrorResponse schema should have 'message' property")
	}
	if schema404.Properties["code"] == nil {
		t.Fatal("ErrorResponse schema should have 'code' property")
	}

	// Verify that standard responses are still added for missing status codes
	if getOp.Responses.Value("400") == nil {
		t.Fatal("GET operation should still have standard 400 response")
	}
	if getOp.Responses.Value("500") == nil {
		t.Fatal("GET operation should still have standard 500 response")
	}

	// Test POST /users custom responses
	postOp := spec.Paths.Find("/users").Post
	if postOp == nil {
		t.Fatal("POST /users operation not found")
	}

	// Check custom 201 response
	response201 := postOp.Responses.Value("201")
	if response201 == nil {
		t.Fatal("POST /users should have 201 response")
	}
	jsonContent201 := response201.Value.Content["application/json"]
	if jsonContent201 == nil {
		t.Fatal("201 response should have application/json content")
	}

	// Check custom 400 response (should override standard one)
	response400 := postOp.Responses.Value("400")
	if response400 == nil {
		t.Fatal("POST /users should have 400 response")
	}
	jsonContent400 := response400.Value.Content["application/json"]
	if jsonContent400 == nil {
		t.Fatal("400 response should have application/json content")
	}
	schema400 := jsonContent400.Schema.Value
	if schema400.Properties["code"] == nil {
		t.Fatal("Custom 400 ErrorResponse should have 'code' property (not standard error response)")
	}

	// Verify that standard responses are still added for missing status codes
	if postOp.Responses.Value("200") == nil {
		t.Fatal("POST operation should still have standard 200 response")
	}
	if postOp.Responses.Value("500") == nil {
		t.Fatal("POST operation should still have standard 500 response")
	}
}

func TestApiGenerateWithRequestBodySchemas(t *testing.T) {
	api := &Api{
		mux: http.NewServeMux(),
		Routes: []Route{
			{
				Method:      "POST",
				Path:        "/users",
				Description: "Create a user",
				Body:        CreateUserRequest{},
			},
			{
				Method:      "PUT",
				Path:        "/users/{id}",
				Description: "Update a user",
				Body:        User{},
			},
			{
				Method:      "GET",
				Path:        "/users",
				Description: "Get all users",
				Body:        nil, // No body for GET
			},
		},
	}

	data, err := api.Generate()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	var spec openapi3.T
	err = json.Unmarshal(data, &spec)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	// Test POST /users has request body
	postOp := spec.Paths.Find("/users").Post
	if postOp == nil {
		t.Fatal("POST /users operation not found")
	}
	if postOp.RequestBody == nil {
		t.Fatal("POST /users should have request body")
	}

	// Verify request body content type
	jsonContent := postOp.RequestBody.Value.Content["application/json"]
	if jsonContent == nil {
		t.Fatal("POST /users request body should have application/json content")
	}

	// Verify request body schema is object type
	schema := jsonContent.Schema.Value
	if schema.Type == nil || (*schema.Type)[0] != "object" {
		t.Fatal("POST /users request body schema should be object type")
	}

	// Verify request body has expected properties
	if schema.Properties == nil {
		t.Fatal("POST /users request body schema should have properties")
	}
	if schema.Properties["name"] == nil {
		t.Fatal("POST /users request body should have 'name' property")
	}
	if schema.Properties["email"] == nil {
		t.Fatal("POST /users request body should have 'email' property")
	}

	// Test PUT /users/{id} has request body
	putOp := spec.Paths.Find("/users/{id}").Put
	if putOp == nil {
		t.Fatal("PUT /users/{id} operation not found")
	}
	if putOp.RequestBody == nil {
		t.Fatal("PUT /users/{id} should have request body")
	}

	// Test GET /users has no request body
	getOp := spec.Paths.Find("/users").Get
	if getOp == nil {
		t.Fatal("GET /users operation not found")
	}
	if getOp.RequestBody != nil {
		t.Fatal("GET /users should not have request body")
	}
}

func TestApiGenerateResponseSchemas(t *testing.T) {
	api := &Api{
		mux: http.NewServeMux(),
		Routes: []Route{
			{Method: "GET", Path: "/users", Description: "Get all users"},
			{Method: "POST", Path: "/users", Description: "Create a user"},
			{Method: "DELETE", Path: "/users/{id}", Description: "Delete a user"},
		},
	}

	data, err := api.Generate()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	var spec openapi3.T
	err = json.Unmarshal(data, &spec)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	// Test GET operation responses
	getOp := spec.Paths.Find("/users").Get
	if getOp.Responses.Value("200") == nil {
		t.Fatal("GET operation should have 200 response")
	}
	if getOp.Responses.Value("400") == nil {
		t.Fatal("GET operation should have 400 response")
	}
	if getOp.Responses.Value("404") == nil {
		t.Fatal("GET operation should have 404 response")
	}
	if getOp.Responses.Value("500") == nil {
		t.Fatal("GET operation should have 500 response")
	}

	// Test POST operation responses
	postOp := spec.Paths.Find("/users").Post
	if postOp.Responses.Value("200") == nil {
		t.Fatal("POST operation should have 200 response")
	}
	if postOp.Responses.Value("201") == nil {
		t.Fatal("POST operation should have 201 response")
	}
	if postOp.Responses.Value("400") == nil {
		t.Fatal("POST operation should have 400 response")
	}
	if postOp.Responses.Value("500") == nil {
		t.Fatal("POST operation should have 500 response")
	}

	// Test DELETE operation responses
	deleteOp := spec.Paths.Find("/users/{id}").Delete
	if deleteOp.Responses.Value("200") == nil {
		t.Fatal("DELETE operation should have 200 response")
	}
	if deleteOp.Responses.Value("204") == nil {
		t.Fatal("DELETE operation should have 204 response")
	}
	if deleteOp.Responses.Value("404") == nil {
		t.Fatal("DELETE operation should have 404 response")
	}
	if deleteOp.Responses.Value("500") == nil {
		t.Fatal("DELETE operation should have 500 response")
	}

	// Verify error response structure
	errorResponse := getOp.Responses.Value("400").Value
	if errorResponse.Content["application/json"] == nil {
		t.Fatal("Error responses should have application/json content")
	}
	
	errorSchema := errorResponse.Content["application/json"].Schema.Value
	if errorSchema.Properties["error"] == nil {
		t.Fatal("Error response should have 'error' property")
	}
	if errorSchema.Properties["message"] == nil {
		t.Fatal("Error response should have 'message' property")
	}
}

func TestApiGenerateEmpty(t *testing.T) {
	// Create a new API instance with no routes
	api := &Api{
		mux:    http.NewServeMux(),
		Routes: []Route{},
	}

	// Generate OpenAPI spec
	data, err := api.Generate()
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
	data, err := api.Generate()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Parse the JSON
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Generated JSON is invalid: %v", err)
	}

	// Validate OpenAPI spec
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(data)
	if err != nil {
		t.Fatalf("OpenAPI spec validation failed: %v", err)
	}

	// Validate the document
	if err := doc.Validate(loader.Context); err != nil {
		t.Fatalf("OpenAPI spec is invalid: %v", err)
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
