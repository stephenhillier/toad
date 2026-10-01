package fixture

import (
	"errors"
	"github.com/stephenhillier/buddy"
	"net/http"
)

type Input struct{ Name string }
type Output struct{ ID int }

var sentinel = errors.New("missing")

func ordinary(w http.ResponseWriter, r *http.Request)                 {}
func ordinaryBody(w http.ResponseWriter, r *http.Request, body Input) {}
func managed(r *http.Request) (Output, error)                         { return Output{}, nil }
func managedBody(r *http.Request, body Input) (Output, error)         { return Output{}, nil }

func register(api *buddy.Api) {
	api.Route("GET /ordinary").Title("title").Description("description").HandlerFunc(ordinary)
	api.Route("POST /body").Body(Input{}).Title("title").Description("description").HandlerFunc(ordinaryBody)
	api.Route("GET /explicit").Response(200, Output{}).Title("title").Description("description").Error(404, sentinel).HandlerFunc(managed)
	api.Route("GET /inferred").Status(200).Title("title").Description("description").Error(404, sentinel).HandlerFunc(managed)
	api.Route("POST /body-explicit").Body(Input{}).Response(201, Output{}).Title("title").Description("description").Error(404, sentinel).HandlerFunc(managedBody)
	api.Route("POST /explicit-body").Response(201, Output{}).Error(404, sentinel).Body(Input{}).Title("title").Description("description").HandlerFunc(managedBody)
	api.Route("POST /body-inferred").Body(Input{}).Status(201).Title("title").Description("description").Error(404, sentinel).HandlerFunc(managedBody)
	api.Route("POST /inferred-body").Status(201).Error(404, sentinel).Body(Input{}).Title("title").Description("description").HandlerFunc(managedBody)
	// Inference also retains pointer and collection result types without a prototype.
	api.Route("GET /pointer").Status(200).HandlerFunc(func(r *http.Request) (*Output, error) { return nil, nil })
	api.Route("GET /slice").Status(200).HandlerFunc(func(r *http.Request) ([]Output, error) { return nil, nil })
}
