package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/stephenhillier/toad"
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
	api := toad.NewApi(mux)

	api.Route("GET /users/{id}").Description("Get user by ID").HandlerFunc(getUserHandler)
	api.Route("POST /users").Description("Create a new user").Body(CreateUserRequest{}).HandlerFunc(createUserHandler)
	api.Route("DELETE /users/{id}").Description("Delete a user").HandlerFunc(deleteUserHandler)

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

func createUserHandler(w http.ResponseWriter, r *http.Request, req CreateUserRequest) {
	// Mock implementation

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
