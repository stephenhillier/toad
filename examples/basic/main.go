package main

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/stephenhillier/toad"
)

// Response models
type User struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type CreateUserRequest struct {
	Name  string `json:"name" validate:"required,min=2,max=100"`
	Email string `json:"email" validate:"required,email"`
}

type SuccessResponse struct {
	Message string `json:"message"`
}

func main() {
	mux := http.NewServeMux()
	api := toad.NewApi(mux)

	api.Route("GET /users/{id}").Title("Get a user").
		Description("Show a sample user for this example.").
		Response(http.StatusOK, User{}).HandlerFunc(getUserHandler)

	api.Route("POST /users").Title("Create a user").
		Description("Build a sample user from the supplied details.").
		Body(CreateUserRequest{}).Validator(validateCreateUser).
		Response(http.StatusCreated, User{}).HandlerFunc(createUserHandler)

	api.Route("DELETE /users/{id}").Title("Delete a user").
		Description("Show a sample message confirming user deletion.").
		Response(http.StatusOK, SuccessResponse{}).HandlerFunc(deleteUserHandler)

	if _, err := api.Generate(); err != nil {
		log.Fatal(err)
	}

	// Start the server
	fmt.Println("Server starting on :8080")
	fmt.Println("Try visiting:")
	fmt.Println("  http://localhost:8080/docs")
	fmt.Println("  http://localhost:8080/openapi.json")

	log.Fatal(http.ListenAndServe(":8080", mux))
}

// These handlers return sample data without storing it.
func getUserHandler(r *http.Request) (User, error) {
	return User{ID: 1, Name: "John Doe", Email: "john@example.com"}, nil
}

func createUserHandler(r *http.Request, req CreateUserRequest) (User, error) {
	return User{ID: 2, Name: req.Name, Email: req.Email}, nil
}

func validateCreateUser(_ *http.Request, body CreateUserRequest) error {
	if strings.TrimSpace(body.Name) == "" {
		return toad.Invalid("name", "Name must not be blank")
	}
	return nil
}

func deleteUserHandler(r *http.Request) (SuccessResponse, error) {
	return SuccessResponse{Message: "User deleted successfully"}, nil
}
