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
	api := NewApi(http.NewServeMux())
	api.Route("GET /users").Title("Get all users").Description("Get all users").HandlerFunc(noopHandler)
	api.Route("POST /users").Title("Create a user").Description("Create a user").HandlerFunc(noopHandler)
	api.Route("GET /users/{id}").Title("Get user by ID").Description("Get user by ID").HandlerFunc(noopHandler)

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
	api := NewApi(http.NewServeMux())
	api.Route("GET /users/{id}").Description("Get user by ID").Response(http.StatusOK, UserResponse{}).
		HandlerFunc(func(r *http.Request) (UserResponse, error) { return UserResponse{}, nil })
	api.Route("POST /users").Description("Create a user").Body(CreateUserRequest{}).Status(http.StatusCreated).
		HandlerFunc(func(r *http.Request, body CreateUserRequest) (UserResponse, error) { return UserResponse{}, nil })
	data, err := api.Generate()
	if err != nil {
		t.Fatal(err)
	}
	var spec openapi3.T
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		operation *openapi3.Operation
		status    string
	}{
		{spec.Paths.Find("/users/{id}").Get, "200"},
		{spec.Paths.Find("/users").Post, "201"},
	} {
		assertResponseStatuses(t, test.operation.Responses, "default", test.status)
		content := test.operation.Responses.Value(test.status).Value.Content["application/json"]
		if content == nil || content.Schema == nil {
			t.Fatal("Expected JSON success schema")
		}
		for _, property := range []string{"id", "name", "email"} {
			if content.Schema.Value.Properties[property] == nil {
				t.Errorf("Missing UserResponse property %s", property)
			}
		}
	}
}

func assertResponseStatuses(t *testing.T, responses *openapi3.Responses, statuses ...string) {
	t.Helper()
	if responses == nil {
		t.Fatal("Expected responses")
	}
	if responses.Len() != len(statuses) {
		t.Errorf("Expected response statuses %v, got %v", statuses, responses.Map())
	}
	for _, status := range statuses {
		if responses.Value(status) == nil {
			t.Errorf("Expected response status %s", status)
		}
	}
}

func TestApiGenerateWithRequestBodySchemas(t *testing.T) {
	api := NewApi(http.NewServeMux())
	api.Route("POST /users").Description("Create a user").Body(CreateUserRequest{}).
		HandlerFunc(func(w http.ResponseWriter, r *http.Request, body CreateUserRequest) {})
	api.Route("PUT /users/{id}").Description("Update a user").Body(User{}).
		HandlerFunc(func(w http.ResponseWriter, r *http.Request, body User) {})
	api.Route("GET /users").Description("Get all users").HandlerFunc(noopHandler)

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
	api := NewApi(http.NewServeMux())
	api.Route("GET /users").Description("Get all users").HandlerFunc(noopHandler)
	api.Route("POST /users").Description("Create a user").HandlerFunc(noopHandler)
	api.Route("DELETE /users/{id}").Description("Delete a user").HandlerFunc(noopHandler)

	data, err := api.Generate()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	var spec openapi3.T
	err = json.Unmarshal(data, &spec)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	// With no declared schemas, kin-openapi supplies only a default response.
	// Managed and decoder response policies are implemented in later tasks.
	for name, operation := range map[string]*openapi3.Operation{
		"GET /users":         spec.Paths.Find("/users").Get,
		"POST /users":        spec.Paths.Find("/users").Post,
		"DELETE /users/{id}": spec.Paths.Find("/users/{id}").Delete,
	} {
		t.Run(name, func(t *testing.T) {
			if operation == nil {
				t.Fatal("Expected operation")
			}
			assertResponseStatuses(t, operation.Responses, "default")
			if len(operation.Responses.Default().Value.Content) != 0 {
				t.Error("Default response should not claim a payload schema")
			}
		})
	}
}

func TestApiGenerateEmpty(t *testing.T) {
	// Create a new API instance with no routes
	api := NewApi(http.NewServeMux())

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
	api := NewApi(http.NewServeMux())
	api.Route("GET /items").Title("Get all items").HandlerFunc(noopHandler)
	api.Route("POST /items").Title("Create an item").HandlerFunc(noopHandler)
	api.Route("PUT /items").Title("Update items").HandlerFunc(noopHandler)
	api.Route("DELETE /items").Title("Delete all items").HandlerFunc(noopHandler)

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
