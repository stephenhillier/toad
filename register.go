package buddy

import (
	"net/http"
	"strings"
)

type RouteOption func(*Route)

func WithDescription(description string) RouteOption {
	return func(route *Route) {
		route.Description = description
	}
}

func WithBody(body any) RouteOption {
	return func(route *Route) {
		route.Body = body
	}
}

func WithResponse(status int, schema any) RouteOption {
	return func(route *Route) {
		if route.Responses == nil {
			route.Responses = make(map[int]any)
		}

		// ensure there is not already a schema registered for this status
		if _, exists := route.Responses[status]; exists {
			panic("response schema already registered")
		}

		route.Responses[status] = schema
	}
}

type Api struct {
	mux    *http.ServeMux
	Routes []Route
}

type Route struct {
	Description string
	Method      string
	Path        string
	Body        any
	Responses   map[int]any
}

func NewApi(mux *http.ServeMux) *Api {
	api := &Api{
		mux:    mux,
		Routes: []Route{},
	}

	api.mux.HandleFunc("GET /openapi.json", api.ServeDocs)
	api.mux.HandleFunc("GET /docs", api.ServeDocsHTML)

	return api
}

// AddRoute adds a route to the API, and applies any provided options.
// Options add metadata to the route (e.g. description)
func (api *Api) AddRoute(pattern string, options ...RouteOption) {
	method, path := SplitPattern(pattern)
	route := Route{Method: method, Path: path}

	for _, option := range options {
		option(&route)
	}

	api.Routes = append(api.Routes, route)

}

// SplitPattern splits an http.ServeMux pattern into method and path,
// e.g. `GET /users`
// Validation is minimal: if the http.ServeMux methods (HandlerFunc etc.) accept
// the pattern, we can assume it's valid.
func SplitPattern(pattern string) (string, string) {
	parts := strings.SplitN(pattern, " ", 2)
	return parts[0], parts[1]
}

func (api *Api) HandlerFunc(pattern string, handler func(w http.ResponseWriter, r *http.Request), options ...RouteOption) {
	api.mux.HandleFunc(pattern, handler)
	api.AddRoute(pattern, options...)
}
