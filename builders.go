package buddy

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

func (b *RouteBuilder) Body[B any](_ B) *BodyBuilder[B] {
	return &BodyBuilder[B]{builderState: b.builderState.body(reflect.TypeFor[B]())}
}

func (b *RouteBuilder) Response[R any](status int, _ R) *ResponseBuilder[R] {
	return &ResponseBuilder[R]{builderState: b.builderState.response(explicitResponse, status, reflect.TypeFor[R]())}
}

func (b *RouteBuilder) Status(status int) *StatusBuilder {
	return &StatusBuilder{builderState: b.builderState.response(inferredResponse, status, nil)}
}

func (b *RouteBuilder) HandlerFunc(handler func(w http.ResponseWriter, r *http.Request)) {
	b.builderState.finalize(http.HandlerFunc(handler), handler == nil, nil)
}

// BodyBuilder configures an ordinary HTTP handler with a typed JSON body.
type BodyBuilder[B any] struct{ builderState }

func (b *BodyBuilder[B]) Title(text string) *BodyBuilder[B] { b.builderState.title(text); return b }

func (b *BodyBuilder[B]) Description(text string) *BodyBuilder[B] {
	b.builderState.description(text)
	return b
}

func (b *BodyBuilder[B]) Body[C any](_ C) *BodyBuilder[C] {
	return &BodyBuilder[C]{builderState: b.builderState.body(reflect.TypeFor[C]())}
}

func (b *BodyBuilder[B]) Response[R any](status int, _ R) *BodyResponseBuilder[B, R] {
	return &BodyResponseBuilder[B, R]{builderState: b.builderState.response(explicitResponse, status, reflect.TypeFor[R]())}
}

func (b *BodyBuilder[B]) Status(status int) *BodyStatusBuilder[B] {
	return &BodyStatusBuilder[B]{builderState: b.builderState.response(inferredResponse, status, nil)}
}

func (b *BodyBuilder[B]) HandlerFunc(handler func(w http.ResponseWriter, r *http.Request, body B)) {
	b.builderState.current()
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := decodeBody[B](w, r)
		if !ok {
			return
		}
		handler(w, r, body)
	})
	b.builderState.finalize(wrapped, handler == nil, nil)
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

func (b *ResponseBuilder[R]) Body[B any](_ B) *BodyResponseBuilder[B, R] {
	return &BodyResponseBuilder[B, R]{builderState: b.builderState.body(reflect.TypeFor[B]())}
}

func (b *ResponseBuilder[R]) Response[S any](status int, _ S) *ResponseBuilder[S] {
	return &ResponseBuilder[S]{builderState: b.builderState.response(explicitResponse, status, reflect.TypeFor[S]())}
}

func (b *ResponseBuilder[R]) Status(status int) *StatusBuilder {
	return &StatusBuilder{builderState: b.builderState.response(inferredResponse, status, nil)}
}

func (b *ResponseBuilder[R]) Error(status int, sentinel error) *ResponseBuilder[R] {
	b.builderState.addError(status, sentinel)
	return b
}

func (b *ResponseBuilder[R]) HandlerFunc(handler func(r *http.Request) (R, error)) {
	b.builderState.current()
	status := b.config.record.status
	mappings := append([]errorMapping(nil), b.config.record.errors...)
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, err := handler(r)
		writeManaged(w, result, err, status, mappings)
	})
	b.builderState.finalize(wrapped, handler == nil, reflect.TypeFor[R]())
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

func (b *BodyResponseBuilder[B, R]) Body[C any](_ C) *BodyResponseBuilder[C, R] {
	return &BodyResponseBuilder[C, R]{builderState: b.builderState.body(reflect.TypeFor[C]())}
}

func (b *BodyResponseBuilder[B, R]) Response[S any](status int, _ S) *BodyResponseBuilder[B, S] {
	return &BodyResponseBuilder[B, S]{builderState: b.builderState.response(explicitResponse, status, reflect.TypeFor[S]())}
}

func (b *BodyResponseBuilder[B, R]) Status(status int) *BodyStatusBuilder[B] {
	return &BodyStatusBuilder[B]{builderState: b.builderState.response(inferredResponse, status, nil)}
}

func (b *BodyResponseBuilder[B, R]) Error(status int, sentinel error) *BodyResponseBuilder[B, R] {
	b.builderState.addError(status, sentinel)
	return b
}

func (b *BodyResponseBuilder[B, R]) HandlerFunc(handler func(r *http.Request, body B) (R, error)) {
	b.builderState.current()
	status := b.config.record.status
	mappings := append([]errorMapping(nil), b.config.record.errors...)
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := decodeBody[B](w, r)
		if !ok {
			return
		}
		result, err := handler(r, body)
		writeManaged(w, result, err, status, mappings)
	})
	b.builderState.finalize(wrapped, handler == nil, reflect.TypeFor[R]())
}

// StatusBuilder configures a managed handler whose result type is inferred.
type StatusBuilder struct{ builderState }

func (b *StatusBuilder) Title(text string) *StatusBuilder { b.builderState.title(text); return b }

func (b *StatusBuilder) Description(text string) *StatusBuilder {
	b.builderState.description(text)
	return b
}

func (b *StatusBuilder) Body[B any](_ B) *BodyStatusBuilder[B] {
	return &BodyStatusBuilder[B]{builderState: b.builderState.body(reflect.TypeFor[B]())}
}

func (b *StatusBuilder) Response[R any](status int, _ R) *ResponseBuilder[R] {
	return &ResponseBuilder[R]{builderState: b.builderState.response(explicitResponse, status, reflect.TypeFor[R]())}
}

func (b *StatusBuilder) Status(status int) *StatusBuilder {
	return &StatusBuilder{builderState: b.builderState.response(inferredResponse, status, nil)}
}

func (b *StatusBuilder) Error(status int, sentinel error) *StatusBuilder {
	b.builderState.addError(status, sentinel)
	return b
}

func (b *StatusBuilder) HandlerFunc[R any](handler func(r *http.Request) (R, error)) {
	b.builderState.current()
	status := b.config.record.status
	mappings := append([]errorMapping(nil), b.config.record.errors...)
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, err := handler(r)
		writeManaged(w, result, err, status, mappings)
	})
	b.builderState.finalize(wrapped, handler == nil, reflect.TypeFor[R]())
}

// BodyStatusBuilder configures a managed handler with a typed JSON body and an
// inferred result type.
type BodyStatusBuilder[B any] struct{ builderState }

func (b *BodyStatusBuilder[B]) Title(text string) *BodyStatusBuilder[B] {
	b.builderState.title(text)
	return b
}

func (b *BodyStatusBuilder[B]) Description(text string) *BodyStatusBuilder[B] {
	b.builderState.description(text)
	return b
}

func (b *BodyStatusBuilder[B]) Body[C any](_ C) *BodyStatusBuilder[C] {
	return &BodyStatusBuilder[C]{builderState: b.builderState.body(reflect.TypeFor[C]())}
}

func (b *BodyStatusBuilder[B]) Response[R any](status int, _ R) *BodyResponseBuilder[B, R] {
	return &BodyResponseBuilder[B, R]{builderState: b.builderState.response(explicitResponse, status, reflect.TypeFor[R]())}
}

func (b *BodyStatusBuilder[B]) Status(status int) *BodyStatusBuilder[B] {
	return &BodyStatusBuilder[B]{builderState: b.builderState.response(inferredResponse, status, nil)}
}

func (b *BodyStatusBuilder[B]) Error(status int, sentinel error) *BodyStatusBuilder[B] {
	b.builderState.addError(status, sentinel)
	return b
}

func (b *BodyStatusBuilder[B]) HandlerFunc[R any](handler func(r *http.Request, body B) (R, error)) {
	b.builderState.current()
	status := b.config.record.status
	mappings := append([]errorMapping(nil), b.config.record.errors...)
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := decodeBody[B](w, r)
		if !ok {
			return
		}
		result, err := handler(r, body)
		writeManaged(w, result, err, status, mappings)
	})
	b.builderState.finalize(wrapped, handler == nil, reflect.TypeFor[R]())
}
