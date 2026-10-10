package toad

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type validationInput struct {
	Name  string            `json:"name" validate:"required,min=2"`
	Email string            `json:"email" validate:"required,email"`
	Owner *validationOwner  `json:"owner,omitempty" validate:"omitempty"`
	Items []validationItem  `json:"items,omitempty" validate:"omitempty,dive"`
	Codes map[string]string `json:"codes,omitempty" validate:"omitempty,dive,keys,required,endkeys,required"`
	Skip  string            `json:"skip" validate:"-"`
}

type validationOwner struct {
	Email string `json:"email" validate:"required,email"`
}

type validationItem struct {
	Name string `json:"name" validate:"required"`
}

var validationModes = []string{"ordinary", "before-response", "after-response", "response-first"}

// Exercise callbacks on both body builders and on both Body/Response orders.
func registerValidationRoute[B any](api *Api, mode string, prototype B, checks []func(*http.Request, B) error, receive func(B)) {
	route := api.Route("POST /body")
	handler := func(_ *http.Request, body B) (B, error) {
		receive(body)
		return body, nil
	}
	switch mode {
	case "ordinary", "before-response":
		b := route.Body(prototype)
		for _, check := range checks {
			if b.Validator(check) != b {
				panic("Validator must return its builder")
			}
		}
		if mode == "ordinary" {
			b.HandlerFunc(func(w http.ResponseWriter, _ *http.Request, body B) {
				receive(body)
				if err := JSON(w, 201, body); err != nil {
					panic(err)
				}
			})
		} else {
			b.Response(201, prototype).HandlerFunc(handler)
		}
	case "after-response":
		b := route.Body(prototype).Response(201, prototype)
		for _, check := range checks {
			b.Validator(check)
		}
		b.HandlerFunc(handler)
	case "response-first":
		b := route.Response(201, prototype).Body(prototype)
		for _, check := range checks {
			b.Validator(check)
		}
		b.HandlerFunc(handler)
	}
}

func validationRequest(api *Api, data string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/body", strings.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	api.mux.ServeHTTP(rr, req)
	return rr
}

func TestAutomaticTagValidation(t *testing.T) {
	for _, mode := range validationModes {
		t.Run(mode, func(t *testing.T) {
			api := NewApi(http.NewServeMux())
			calls := 0
			registerValidationRoute(api, mode, validationInput{}, nil, func(body validationInput) {
				calls++
				if body.Name != "Ada" || body.Email != "ada@example.com" {
					t.Errorf("handler received wrong body: %+v", body)
				}
			})
			doc := generatedDocument(t, api)
			for _, tc := range []struct {
				name, body string
				fields     []FieldError
			}{
				{"valid", `{"name":"Ada","email":"ada@example.com"}`, nil},
				{"optional null", `{"name":"Ada","email":"ada@example.com","owner":null,"skip":""}`, nil},
				{"required", `{}`, []FieldError{{Field: "name", Code: "required"}, {Field: "email", Code: "required"}}},
				{"min and email", `{"name":"A","email":"private-value"}`, []FieldError{{Field: "name", Code: "min"}, {Field: "email", Code: "email"}}},
				{"nested", `{"name":"Ada","email":"ada@example.com","owner":{"email":"private-value"}}`, []FieldError{{Field: "owner.email", Code: "email"}}},
				{"slice", `{"name":"Ada","email":"ada@example.com","items":[{"name":""}]}`, []FieldError{{Field: "items[0].name", Code: "required"}}},
				{"map value", `{"name":"Ada","email":"ada@example.com","codes":{"x":""}}`, []FieldError{{Field: "codes[x]", Code: "required"}}},
				{"map key", `{"name":"Ada","email":"ada@example.com","codes":{"":"value"}}`, []FieldError{{Field: "codes[]", Code: "required"}}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					before := calls
					rr := validationRequest(api, tc.body)
					wantStatus, wantCalls := 201, 1
					if tc.fields != nil {
						wantStatus, wantCalls = 422, 0
						var response ValidationErrorResponse
						if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
							t.Fatal(err)
						}
						if response.Detail != publicError(422).Detail || !reflect.DeepEqual(response.Errors, tc.fields) {
							t.Fatalf("validation response: %+v, want %+v", response, tc.fields)
						}
						if strings.Contains(rr.Body.String(), "private-value") {
							t.Fatal("rejected value exposed")
						}
					}
					if rr.Code != wantStatus || calls-before != wantCalls {
						t.Fatalf("status %d, calls %d; %s", rr.Code, calls-before, rr.Body)
					}
					if mode != "ordinary" || rr.Code != 201 {
						schema := doc.Paths.Value("/body").Post.Responses.Value(fmt.Sprint(rr.Code)).Value.Content["application/json"].Schema.Value
						if err := schema.VisitJSON(jsonValue(t, rr.Body.Bytes())); err != nil {
							t.Fatalf("response violates OpenAPI: %v", err)
						}
					}
				})
			}
		})
	}
}

func TestTypedValidatorPipeline(t *testing.T) {
	for _, mode := range validationModes {
		t.Run(mode, func(t *testing.T) {
			api := NewApi(http.NewServeMux())
			var events []string
			type contextKey struct{}
			checks := []func(*http.Request, validationInput) error{
				func(r *http.Request, body validationInput) error {
					events = append(events, "first")
					if r.Context().Value(contextKey{}) != "context" || r.Header.Get("X-Check") != "header" {
						t.Error("callback did not receive the original request")
					}
					if body.Name == "reject" {
						return fmt.Errorf("private context: %w", Invalid("name", "Name is reserved"))
					}
					return nil
				},
				func(_ *http.Request, body validationInput) error {
					events = append(events, "second")
					if body.Name == "internal" {
						return errors.New("private database failure")
					}
					return nil
				},
			}
			registerValidationRoute(api, mode, validationInput{}, checks, func(validationInput) { events = append(events, "handler") })
			doc := generatedDocument(t, api)
			for _, tc := range []struct {
				name, body string
				status     int
				events     []string
			}{
				{"decode failure", `{`, 400, nil},
				{"tag failure", `{"name":"A","email":"ada@example.com"}`, 422, nil},
				{"callback failure", `{"name":"reject","email":"ada@example.com"}`, 422, []string{"first"}},
				{"internal failure", `{"name":"internal","email":"ada@example.com"}`, 500, []string{"first", "second"}},
				{"valid", `{"name":"Ada","email":"ada@example.com"}`, 201, []string{"first", "second", "handler"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					events = nil
					request := httptest.NewRequest("POST", "/body", strings.NewReader(tc.body))
					request = request.WithContext(context.WithValue(request.Context(), contextKey{}, "context"))
					request.Header.Set("Content-Type", "application/json")
					request.Header.Set("X-Check", "header")
					rr := httptest.NewRecorder()
					api.mux.ServeHTTP(rr, request)
					if rr.Code != tc.status || !reflect.DeepEqual(events, tc.events) {
						t.Fatalf("status %d, events %v; %s", rr.Code, events, rr.Body)
					}
					if strings.Contains(rr.Body.String(), "private") {
						t.Fatal("internal error context exposed")
					}
					if tc.name == "callback failure" {
						var response ValidationErrorResponse
						if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
							t.Fatal(err)
						}
						want := []FieldError{{Field: "name", Code: "invalid", Message: "Name is reserved"}}
						if !reflect.DeepEqual(response.Errors, want) {
							t.Fatalf("callback response: %+v", response)
						}
					}
					if mode != "ordinary" || rr.Code != 201 {
						schema := doc.Paths.Value("/body").Post.Responses.Value(fmt.Sprint(tc.status)).Value.Content["application/json"].Schema.Value
						if err := schema.VisitJSON(jsonValue(t, rr.Body.Bytes())); err != nil {
							t.Fatalf("response violates OpenAPI: %v", err)
						}
					}
				})
			}
		})
	}
}

func TestRequiredPointerAndStructValidation(t *testing.T) {
	api := NewApi(http.NewServeMux())
	input := struct {
		Completed *bool `json:"completed" validate:"required"`
		Owner     struct {
			Name string `json:"name"`
		} `json:"owner" validate:"required"`
	}{}
	registerValidationRoute(api, "ordinary", input, nil, func(body struct {
		Completed *bool `json:"completed" validate:"required"`
		Owner     struct {
			Name string `json:"name"`
		} `json:"owner" validate:"required"`
	}) {
		if body.Completed == nil || *body.Completed {
			t.Error("expected explicit false")
		}
	})
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"completed":false,"owner":{"name":"Ada"}}`, 201},
		{`{"owner":{"name":"Ada"}}`, 422},
		{`{"completed":null,"owner":{"name":"Ada"}}`, 422},
		{`{"completed":false,"owner":{}}`, 422},
	} {
		rr := validationRequest(api, tc.body)
		if rr.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.body, rr.Code, rr.Body)
		}
	}
}

func TestAnonymousBodyJSONFieldPaths(t *testing.T) {
	api := NewApi(http.NewServeMux())
	type input = struct {
		Name  string          `validate:"required"`
		Email string          `json:"email,omitempty" validate:"required"`
		Owner validationOwner `json:"owner"`
	}
	registerValidationRoute(api, "ordinary", input{}, nil, func(input) { t.Fatal("invalid body reached handler") })
	rr := validationRequest(api, `{}`)
	var response ValidationErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	want := []FieldError{{Field: "Name", Code: "required"}, {Field: "email", Code: "required"}, {Field: "owner.email", Code: "required"}}
	if rr.Code != 422 || !reflect.DeepEqual(response.Errors, want) {
		t.Fatalf("anonymous paths: %d %+v", rr.Code, response)
	}
}

func TestValidatorBuilderStateAndSnapshot(t *testing.T) {
	api := NewApi(http.NewServeMux())
	first := api.Route("POST /body").Body(validationInput{})
	requireRoutePanic(t, "POST /body", "non-nil callback", func() { first.Validator(nil) })
	if len(first.config.record.validators) != 0 {
		t.Fatal("invalid callback changed configuration")
	}
	var events []string
	first.Validator(func(*http.Request, validationInput) error { events = append(events, "first"); return nil })
	managed := first.Response(201, validationInput{})
	requireRoutePanic(t, "POST /body", "obsolete builder", func() {
		first.Validator(func(*http.Request, validationInput) error { t.Fatal("obsolete callback ran"); return nil })
	})
	requireRoutePanic(t, "POST /body", "non-nil callback", func() { managed.Validator(nil) })
	managed.Validator(func(*http.Request, validationInput) error { events = append(events, "second"); return nil })
	managed.HandlerFunc(func(_ *http.Request, body validationInput) (validationInput, error) {
		events = append(events, "handler")
		return body, nil
	})
	requireRoutePanic(t, "POST /body", "already been finalized", func() { managed.Validator(nil) })
	managed.config.record.validators[0] = func(*http.Request, any) error { return Invalid("", "changed") }
	rr := validationRequest(api, `{"name":"Ada","email":"ada@example.com"}`)
	if rr.Code != 201 || !reflect.DeepEqual(events, []string{"first", "second", "handler"}) {
		t.Fatalf("registered callbacks did not retain snapshot: %d %v", rr.Code, events)
	}
}

func TestValidationAndHandler422Schemas(t *testing.T) {
	api := NewApi(http.NewServeMux())
	sentinel := errors.New("Name is taken")
	api.Route("POST /body").Body(validationInput{}).Response(201, validationInput{}).
		Error(422, sentinel).HandlerFunc(func(_ *http.Request, body validationInput) (validationInput, error) {
		return validationInput{}, sentinel
	})
	doc := generatedDocument(t, api)
	schema := doc.Paths.Value("/body").Post.Responses.Value("422").Value.Content["application/json"].Schema.Value
	if len(schema.AnyOf) != 2 {
		t.Fatal("validation and handler 422 envelopes must both be documented")
	}
	for _, body := range []string{`{}`, `{"name":"Ada","email":"ada@example.com"}`} {
		rr := validationRequest(api, body)
		if rr.Code != 422 {
			t.Fatalf("expected 422: %d %s", rr.Code, rr.Body)
		}
		if err := schema.VisitJSON(jsonValue(t, rr.Body.Bytes())); err != nil {
			t.Fatalf("422 response violates schema: %v", err)
		}
	}
	// The validation branch itself requires at least one field error.
	validationSchema := schema.AnyOf[1].Value
	for _, value := range []any{
		map[string]any{"detail": publicError(422).Detail},
		map[string]any{"detail": publicError(422).Detail, "errors": []any{}},
		map[string]any{"detail": publicError(422).Detail, "errors": nil},
		map[string]any{"detail": publicError(422).Detail, "errors": []any{map[string]any{"field": "email"}}},
	} {
		if err := validationSchema.VisitJSON(value); err == nil {
			t.Fatalf("malformed validation envelope accepted: %+v", value)
		}
	}
}

func TestCallbacksCanReturnGoPlaygroundErrors(t *testing.T) {
	for _, invalidInput := range []bool{false, true} {
		api := NewApi(http.NewServeMux())
		v := newTagValidator()
		type input struct {
			Name string `json:"name"`
		}
		registerValidationRoute(api, "ordinary", input{}, []func(*http.Request, input) error{
			func(r *http.Request, body input) error {
				if invalidInput {
					return v.Struct("bad validator input")
				}
				return fmt.Errorf("private context: %w", v.StructCtx(r.Context(), struct {
					Name string `json:"name" validate:"required"`
				}{Name: body.Name}))
			},
		}, func(input) { t.Fatal("handler reached after validation error") })
		rr := validationRequest(api, `{}`)
		if invalidInput {
			if rr.Code != 500 || rr.Body.String() != `{"detail":"Internal Server Error"}` {
				t.Fatalf("invalid validator input: %d %s", rr.Code, rr.Body)
			}
		} else {
			var response ValidationErrorResponse
			if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if rr.Code != 422 || !reflect.DeepEqual(response.Errors, []FieldError{{Field: "name", Code: "required"}}) {
				t.Fatalf("callback validation errors: %d %+v", rr.Code, response)
			}
		}
	}
}

func TestValidationAppliesOnlyToTypedBodies(t *testing.T) {
	api := NewApi(http.NewServeMux())
	api.Route("POST /described").DescribeBody(validationInput{}).HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
	})
	api.Route("GET /response").Response(200, validationInput{}).HandlerFunc(func(*http.Request) (validationInput, error) {
		return validationInput{}, nil
	})
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"POST", "/described", 201},
		{"GET", "/response", 200},
	} {
		rr := httptest.NewRecorder()
		api.mux.ServeHTTP(rr, httptest.NewRequest(tc.method, tc.path, nil))
		if rr.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, rr.Code, rr.Body)
		}
	}
	doc := generatedDocument(t, api)
	if doc.Paths.Value("/described").Post.Responses.Value("422") != nil || doc.Paths.Value("/response").Get.Responses.Value("422") != nil {
		t.Fatal("body validation responses documented for a route without a typed body")
	}
}

func TestMalformedValidationTagsArePrivate(t *testing.T) {
	for _, mode := range validationModes {
		api := NewApi(http.NewServeMux())
		type malformed struct {
			Name string `validate:"unknown_rule"`
		}
		registerValidationRoute(api, mode, malformed{}, []func(*http.Request, malformed) error{
			func(*http.Request, malformed) error { t.Fatal("callback ran after invalid tags"); return nil },
		}, func(malformed) { t.Fatal("handler ran after invalid tags") })
		rr := validationRequest(api, `{}`)
		if rr.Code != 500 || rr.Body.String() != `{"detail":"Internal Server Error"}` {
			t.Fatalf("invalid configuration response: %d %s", rr.Code, rr.Body)
		}
	}
}

func TestTagValidationConcurrentRequests(t *testing.T) {
	api := NewApi(http.NewServeMux())
	registerValidationRoute(api, "after-response", validationInput{}, nil, func(validationInput) {})
	var group sync.WaitGroup
	for i := range 32 {
		group.Go(func() {
			body, want := `{"name":"Ada","email":"ada@example.com"}`, 201
			if i%2 == 0 {
				body, want = `{}`, 422
			}
			rr := validationRequest(api, body)
			if rr.Code != want {
				t.Errorf("concurrent request: %d %s", rr.Code, rr.Body)
			}
		})
	}
	group.Wait()
}
