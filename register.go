package buddy

import (
	"net/http"
	"strings"
)

type Api struct {
	mux    *http.ServeMux
	Routes []Route
}

type Route struct {
	Description string
	Method      string
	Path        string
	Body        any
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

// AddRoute adds a route to the API.
func (api *Api) AddRoute(pattern string) {
	method, path := SplitPattern(pattern)
	api.Routes = append(api.Routes, Route{Method: method, Path: path})
}

// SplitPattern splits an http.ServeMux pattern into method and path,
// e.g. `GET /users`
// Validation is minimal: if the http.ServeMux methods (HandlerFunc etc.) accept
// the pattern, we can assume it's valid.
func SplitPattern(pattern string) (string, string) {
	parts := strings.SplitN(pattern, " ", 2)
	return parts[0], parts[1]
}

func (api *Api) HandlerFunc(pattern string, handler func(w http.ResponseWriter, r *http.Request)) {
	api.mux.HandleFunc(pattern, handler)
	api.AddRoute(pattern)
}
