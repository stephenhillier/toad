package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/stephenhillier/buddy"
)

// Response models
type User struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type CreateUserRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Code    int    `json:"code"`
}

type SuccessResponse struct {
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func main() {
	mux := http.NewServeMux()
	api := buddy.NewApi(mux)

	// Example 1: GET with custom success and error responses
	api.HandlerFunc("GET /users/{id}", getUserHandler,
		buddy.WithDescription("Get user by ID"),
		buddy.WithResponse(200, User{}),
		buddy.WithResponse(404, ErrorResponse{}),
	)

	// Example 2: POST with custom created and validation error responses
	api.HandlerFunc("POST /users", createUserHandler,
		buddy.WithDescription("Create a new user"),
		buddy.WithBody(CreateUserRequest{}),
		buddy.WithResponse(201, User{}),
		buddy.WithResponse(400, ErrorResponse{}),
		buddy.WithResponse(422, ErrorResponse{}),
	)

	// Example 3: DELETE with custom success response
	api.HandlerFunc("DELETE /users/{id}", deleteUserHandler,
		buddy.WithDescription("Delete a user"),
		buddy.WithResponse(200, SuccessResponse{}),
		buddy.WithResponse(404, ErrorResponse{}),
	)

	// Start the server
	fmt.Println("Server starting on :8080")
	fmt.Println("Try visiting:")
	fmt.Println("  http://localhost:8080/docs")
	fmt.Println("  http://localhost:8080/openapi.json")

	log.Fatal(http.ListenAndServe(":8080", mux))
}

// Handler implementations
func getUserHandler(w http.ResponseWriter, r *http.Request) {
	// Mock implementation
	user := User{ID: 1, Name: "John Doe", Email: "john@example.com"}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

func createUserHandler(w http.ResponseWriter, r *http.Request) {
	// Mock implementation
	var req CreateUserRequest
	json.NewDecoder(r.Body).Decode(&req)

	user := User{ID: 2, Name: req.Name, Email: req.Email}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)
}

func deleteUserHandler(w http.ResponseWriter, r *http.Request) {
	// Mock implementation
	response := SuccessResponse{Message: "User deleted successfully"}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
