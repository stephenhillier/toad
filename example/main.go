package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/stephenhillier/buddy"
)

func main() {
	// Create a new HTTP ServeMux
	mux := http.NewServeMux()

	// Create a new Api object
	api := buddy.NewApi(mux)

	// Register a "hello world" handler
	api.HandlerFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello, World!")
	}, buddy.WithDescription("Say hello"))

	// Register another example handler
	api.HandlerFunc("GET /greet/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		fmt.Fprintf(w, "Hello, %s!", name)
	})

	// Start the server
	fmt.Println("Server starting on :8080")
	fmt.Println("Try visiting:")
	fmt.Println("  http://localhost:8080/hello")
	fmt.Println("  http://localhost:8080/greet/World")
	fmt.Println("  http://localhost:8080/docs")
	fmt.Println("  http://localhost:8080/openapi.json")

	log.Fatal(http.ListenAndServe(":8080", mux))
}
