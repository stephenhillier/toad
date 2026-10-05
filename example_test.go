package toad_test

import (
	"log"
	"net/http"

	"github.com/stephenhillier/toad"
)

func ExampleNewApi() {
	type User struct {
		Name string `json:"name"`
	}

	mux := http.NewServeMux()
	api := toad.NewApi(mux)
	api.BodyLimit(128) // Optional: limit JSON request bodies to 128 bytes.

	api.Route("POST /users").
		Title("Create a user").
		Body(User{}).
		Response(http.StatusCreated, User{}).
		HandlerFunc(func(r *http.Request, user User) (User, error) {
			return user, nil
		})

	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}
