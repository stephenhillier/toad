package fixture

import (
	"github.com/stephenhillier/buddy"
	"net/http"
)

type Input struct{ Name string }
type OtherInput struct{ Name string }
type Output struct{ ID int }
type OtherOutput struct{ ID int }

func register(api *buddy.Api) {
	api.Route("GET /test").Status(200).HandlerFunc(func(w http.ResponseWriter, r *http.Request) (Output, error) { return Output{}, nil })
}
