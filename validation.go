package toad

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

// FieldError describes a request-body validation failure.
// Field is a JSON field path such as "owner.email" or "items[0].name".
// Code is the failed validate tag, or "invalid" for an Invalid error.
// Message is optional public callback text.
// Rejected values (i.e. user input) and internal error messages are never included.
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// ValidationErrorResponse is the 422 envelope for typed-body validation errors.
type ValidationErrorResponse struct {
	Detail string       `json:"detail"`
	Errors []FieldError `json:"errors"`
}

type invalidInputError struct{ field FieldError }

func (e *invalidInputError) Error() string { return e.field.Message }

// Invalid creates a public 422 error for a route Validator callback.
// field may use JSON field paths.
func Invalid(field, message string) error {
	return &invalidInputError{field: FieldError{Field: field, Code: "invalid", Message: message}}
}

func newTagValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	v.RegisterTagNameFunc(func(field reflect.StructField) string {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" || !validJSONName(name) {
			return field.Name
		}
		return name
	})
	return v
}

func addValidator[B any](s builderState, fn func(*http.Request, B) error) {
	s.current()
	if fn == nil {
		s.config.fail("Validator requires a non-nil callback")
	}
	s.config.record.validators = append(s.config.record.validators, func(r *http.Request, body any) error {
		return fn(r, body.(B))
	})
}

func validateTags(ctx context.Context, v *validator.Validate, body any) (err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("toad: invalid validation configuration: %v", failure)
		}
	}()
	return v.StructCtx(ctx, body)
}

func (api *Api) validateBody(w http.ResponseWriter, r *http.Request, body any, checks []func(*http.Request, any) error) bool {
	if err := validateTags(r.Context(), api.validator, body); err != nil {
		writeValidationError(w, body, err)
		return false
	}
	for _, check := range checks {
		if err := check(r, body); err != nil {
			writeValidationError(w, body, err)
			return false
		}
	}
	return true
}

func writeValidationError(w http.ResponseWriter, body any, err error) {
	var fields []FieldError
	var invalid *invalidInputError
	var failures validator.ValidationErrors
	switch {
	case errors.As(err, &invalid):
		fields = []FieldError{invalid.field}
	case errors.As(err, &failures) && len(failures) > 0:
		root := reflect.TypeOf(body).Name()
		fields = make([]FieldError, 0, len(failures))
		for _, failure := range failures {
			field := failure.Namespace()
			if root != "" {
				field = strings.TrimPrefix(field, root+".")
			}
			fields = append(fields, FieldError{Field: field, Code: failure.Tag()})
		}
	default:
		reportResponseError("validate request body", err)
		writeError(w, http.StatusInternalServerError)
		return
	}
	response := ValidationErrorResponse{Detail: publicError(http.StatusUnprocessableEntity).Detail, Errors: fields}
	if err := JSON(w, http.StatusUnprocessableEntity, response); err != nil {
		reportResponseError("write validation error", err)
	}
}
