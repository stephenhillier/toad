package buddy_test

import (
	"log"
	"net/http"

	"github.com/stephenhillier/buddy"
)

func ExampleNewApi() {
	type User struct {
		Name string `json:"name"`
	}

	mux := http.NewServeMux()
	api := buddy.NewApi(mux)
	api.BodyLimit(128) // Optional: limit JSON request bodies to 128 bytes.

	api.Route("POST /users").
		Title("Create a user").
		Body(User{}).
		Status(http.StatusCreated).
		HandlerFunc(func(r *http.Request, user User) (User, error) {
			return user, nil
		})

	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}
