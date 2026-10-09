// This example adds documentation to ordinary net/http handlers.
package main

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/stephenhillier/toad"
)

type UpdateUser struct {
	Name string `json:"name"`
}
type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type ExistingError struct {
	Message string `json:"message"`
}
type UserParams struct {
	ID string `path:"id"`
}

// The existing handler still owns decoding, validation, and all response writes.
func updateUser(w http.ResponseWriter, r *http.Request) {
	var input UpdateUser
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ExistingError{Message: "Invalid request"})
		return
	}
	json.NewEncoder(w).Encode(User{ID: r.PathValue("id"), Name: input.Name})
}

func existingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Service", "legacy")
		next.ServeHTTP(w, r)
	})
}

func main() {
	mux := http.NewServeMux()
	api := toad.NewApi(mux)
	// Replace mux.Handle("PUT /users/{id}", existingMiddleware(http.HandlerFunc(updateUser))).
	api.Route("PUT /users/{id}").
		DescribeBody(UpdateUser{}).
		DescribeParams(UserParams{}).
		DescribeResponse(http.StatusOK, User{}).
		DescribeResponse(http.StatusBadRequest, ExistingError{}).
		Handler(existingMiddleware(http.HandlerFunc(updateUser)))
	// nil documents a response without a body.
	api.Route("GET /health").DescribeResponse(http.StatusNoContent, nil).
		HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	if _, err := api.Generate(); err != nil {
		log.Fatal(err)
	}
	log.Fatal(http.ListenAndServe(":8080", mux))
}
