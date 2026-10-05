package toad

import (
	"net/http"
	"reflect"
)

// RouteBuilder configures an ordinary HTTP handler.
type RouteBuilder struct{ builderState }

func (b *RouteBuilder) Title(text string) *RouteBuilder { b.builderState.title(text); return b }

func (b *RouteBuilder) Description(text string) *RouteBuilder {
	b.builderState.description(text)
	return b
}

// Body accepts a struct type and uses it to build a schema for the endpoint.
//
// This method will alter the signature of the Handler or HandlerFunc associated with
// this endpoint.
//
// Instead of a stdlib-like func(w, r) handler, the signature will include a body param
// of the same type passed to Body(...). This means that the request body will be deserialized
// and passed to your handler function.
func (b *RouteBuilder) Body[B any](_ B) *BodyBuilder[B] {
	return &BodyBuilder[B]{builderState: b.builderState.body(reflect.TypeFor[B]())}
}

// Response accepts a struct type denoting the shape of the response (for successful responses).
// This will be used as the schema for the response in the generated OpenAPI spec.
//
// Like Body(), this method will alter the signature of the Handler or HandlerFunc associated with
// this endpoint. Instead of writing responses to the response writer in the handler function,
// you will instead return an instance of the type declared in Response(...) and it will be
// serialized for you.
func (b *RouteBuilder) Response[R any](status int, _ R) *ResponseBuilder[R] {
	return &ResponseBuilder[R]{builderState: b.builderState.response(status, reflect.TypeFor[R]())}
}

// HandlerFunc adds a stdlib-compatible handler function to the endpoint, completing registration.
func (b *RouteBuilder) HandlerFunc(handler func(w http.ResponseWriter, r *http.Request)) {
	b.builderState.finalize(func(routeRecord) http.Handler { return http.HandlerFunc(handler) }, handler == nil)
}

// BodyBuilder configures an ordinary HTTP handler with a typed JSON body.
type BodyBuilder[B any] struct{ builderState }

func (b *BodyBuilder[B]) Title(text string) *BodyBuilder[B] { b.builderState.title(text); return b }

func (b *BodyBuilder[B]) Description(text string) *BodyBuilder[B] {
	b.builderState.description(text)
	return b
}

// Body accepts a struct type and uses it to build a schema for the endpoint.
//
// This method will alter the signature of the Handler or HandlerFunc associated with
// this endpoint.
//
// Instead of a stdlib-like func(w, r) handler, the signature will include a body param
// of the same type passed to Body(...). This means that the request body will be deserialized
// and passed to your handler function.
func (b *BodyBuilder[B]) Body[C any](_ C) *BodyBuilder[C] {
	return &BodyBuilder[C]{builderState: b.builderState.body(reflect.TypeFor[C]())}
}

// Response accepts a struct type denoting the shape of the response (for successful responses).
// This will be used as the schema for the response in the generated OpenAPI spec.
//
// Like Body(), this method will alter the signature of the Handler or HandlerFunc associated with
// this endpoint. Instead of writing responses to the response writer in the handler function,
// you will instead return an instance of the type declared in Response(...) and it will be
// serialized for you.
func (b *BodyBuilder[B]) Response[R any](status int, _ R) *BodyResponseBuilder[B, R] {
	return &BodyResponseBuilder[B, R]{builderState: b.builderState.response(status, reflect.TypeFor[R]())}
}

// HandlerFunc adds a stdlib-compatible handler function to the endpoint, completing registration.
func (b *BodyBuilder[B]) HandlerFunc(handler func(w http.ResponseWriter, r *http.Request, body B)) {
	b.builderState.finalize(func(record routeRecord) http.Handler {
		api := b.config.api
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, ok := decodeBody[B](w, r, api.bodyLimit)
			if !ok {
				return
			}
			handler(w, r, body)
		})
	}, handler == nil)
}

// ResponseBuilder configures a managed handler with an explicit result type.
type ResponseBuilder[R any] struct{ builderState }

func (b *ResponseBuilder[R]) Title(text string) *ResponseBuilder[R] {
	b.builderState.title(text)
	return b
}

func (b *ResponseBuilder[R]) Description(text string) *ResponseBuilder[R] {
	b.builderState.description(text)
	return b
}

// Body accepts a struct type and uses it to build a schema for the endpoint.
//
// This method will alter the signature of the Handler or HandlerFunc associated with
// this endpoint.
//
// Instead of a stdlib-like func(w, r) handler, the signature will include a body param
// of the same type passed to Body(...). This means that the request body will be deserialized
// and passed to your handler function.
func (b *ResponseBuilder[R]) Body[B any](_ B) *BodyResponseBuilder[B, R] {
	return &BodyResponseBuilder[B, R]{builderState: b.builderState.body(reflect.TypeFor[B]())}
}

// Response accepts a struct type denoting the shape of the response (for successful responses).
// This will be used as the schema for the response in the generated OpenAPI spec.
//
// Like Body(), this method will alter the signature of the Handler or HandlerFunc associated with
// this endpoint. Instead of writing responses to the response writer in the handler function,
// you will instead return an instance of the type declared in Response(...) and it will be
// serialized for you.
func (b *ResponseBuilder[R]) Response[S any](status int, _ S) *ResponseBuilder[S] {
	return &ResponseBuilder[S]{builderState: b.builderState.response(status, reflect.TypeFor[S]())}
}

// Error registers a possible error and its status code to the endpoint.
// Registered error types can be returned from the handler function, triggering
// an error response.
func (b *ResponseBuilder[R]) Error(status int, sentinel error) *ResponseBuilder[R] {
	b.builderState.addError(status, sentinel)
	return b
}

// HandlerFunc adds a handler function (with a response manager) to the endpoint, completing registration.
func (b *ResponseBuilder[R]) HandlerFunc(handler func(r *http.Request) (R, error)) {
	b.builderState.finalize(func(record routeRecord) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			result, err := handler(r)
			writeManaged(w, result, err, record.status, record.errors)
		})
	}, handler == nil)
}

// BodyResponseBuilder configures a managed handler with typed body and result.
type BodyResponseBuilder[B, R any] struct{ builderState }

func (b *BodyResponseBuilder[B, R]) Title(text string) *BodyResponseBuilder[B, R] {
	b.builderState.title(text)
	return b
}

func (b *BodyResponseBuilder[B, R]) Description(text string) *BodyResponseBuilder[B, R] {
	b.builderState.description(text)
	return b
}

// Body accepts a struct type and uses it to build a schema for the endpoint.
//
// This method will alter the signature of the Handler or HandlerFunc associated with
// this endpoint.
//
// Instead of a stdlib-like func(w, r) handler, the signature will include a body param
// of the same type passed to Body(...). This means that the request body will be deserialized
// and passed to your handler function.
func (b *BodyResponseBuilder[B, R]) Body[C any](_ C) *BodyResponseBuilder[C, R] {
	return &BodyResponseBuilder[C, R]{builderState: b.builderState.body(reflect.TypeFor[C]())}
}

func (b *BodyResponseBuilder[B, R]) Response[S any](status int, _ S) *BodyResponseBuilder[B, S] {
	return &BodyResponseBuilder[B, S]{builderState: b.builderState.response(status, reflect.TypeFor[S]())}
}

// Error registers a possible error and its status code to the endpoint.
// Registered error types can be returned from the handler function, triggering
// an error response.
func (b *BodyResponseBuilder[B, R]) Error(status int, sentinel error) *BodyResponseBuilder[B, R] {
	b.builderState.addError(status, sentinel)
	return b
}

// HandlerFunc adds a non-stdlib handler function (with a pre-defined body and a response manager)
// to the endpoint, completing registration.
func (b *BodyResponseBuilder[B, R]) HandlerFunc(handler func(r *http.Request, body B) (R, error)) {
	b.builderState.finalize(func(record routeRecord) http.Handler {
		api := b.config.api
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, ok := decodeBody[B](w, r, api.bodyLimit)
			if !ok {
				return
			}
			result, err := handler(r, body)
			writeManaged(w, result, err, record.status, record.errors)
		})
	}, handler == nil)
}
