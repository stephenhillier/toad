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
	api.Route("POST /test").Body(Input{}).HandlerFunc(func(r *http.Request, body Input) (Output, error) { return Output{}, nil })
}
