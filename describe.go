package toad

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"unicode"

	"github.com/getkin/kin-openapi/openapi3"
)

type describedPayload struct {
	typ    reflect.Type
	status int
}
type describedParameter struct {
	name, in string
	typ      reflect.Type
}
type routeDescriptions struct {
	enabled       bool
	body          *describedPayload
	query, params bool
	parameters    []describedParameter
	responses     []describedPayload
}

func (s builderState) describe() {
	s.current()
	if s.config.record.bodyType != nil || s.config.record.mode != ordinary {
		s.config.fail("Describe methods cannot be combined with typed inputs or managed responses")
	}
}
func (s builderState) rejectDescriptions() {
	if s.config.record.descriptions.enabled {
		s.config.fail("typed inputs and managed responses cannot be combined with Describe methods")
	}
}

// DescribeBody documents a required JSON body without reading or validating it.
// Prototypes supply types, never defaults. Individual fields are not required.
func (b *RouteBuilder) DescribeBody[B any](_ B) *RouteBuilder {
	b.describe()
	d := &b.config.record.descriptions
	if d.body != nil {
		b.config.fail("DescribeBody may only be selected once")
	}
	d.body, d.enabled = &describedPayload{typ: reflect.TypeFor[B]()}, true
	return b
}

// DescribeResponse documents a JSON outcome, or a bodyless outcome when model is nil.
// Repeat with distinct statuses, including application-defined error models.
// The prototype's dynamic type supplies the schema; its field values are ignored.
func (b *RouteBuilder) DescribeResponse(status int, model any) *RouteBuilder {
	b.describe()
	if status < 100 || status > 599 {
		b.config.fail("documented response status must be 100-599")
	}
	if model != nil && (status < 200 || status == 204 || status == 205 || status == 304) {
		b.config.fail("bodyless response statuses require a nil model")
	}
	d := &b.config.record.descriptions
	for _, existing := range d.responses {
		if existing.status == status {
			b.config.fail("duplicate documented response status")
		}
	}
	d.responses = append(d.responses, describedPayload{typ: reflect.TypeOf(model), status: status})
	d.enabled = true
	return b
}

// DescribeQuery documents exported struct fields using query:"name" tags.
// Untagged fields use their Go names and are optional. query:"-" skips a field.
func (b *RouteBuilder) DescribeQuery[Q any](_ Q) *RouteBuilder {
	b.describeParameters(reflect.TypeFor[Q](), "query")
	return b
}

// DescribeParams documents all route parameters using path:"name" tags.
// Names must exactly cover the route's named parameters; all are required.
func (b *RouteBuilder) DescribeParams[P any](_ P) *RouteBuilder {
	b.describeParameters(reflect.TypeFor[P](), "path")
	return b
}
func (b *RouteBuilder) describeParameters(t reflect.Type, location string) {
	b.describe()
	d := &b.config.record.descriptions
	if location == "query" && d.query || location == "path" && d.params {
		b.config.fail("duplicate parameter declaration")
	}
	if t.Kind() != reflect.Struct {
		b.config.fail("parameter descriptions require a struct value")
	}
	names := make(map[string]bool)
	parameters := []describedParameter{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get(location)
		if tag == "-" {
			continue
		}
		if f.Anonymous {
			b.config.fail("embedded parameter fields are unsupported")
		}
		if !f.IsExported() {
			continue
		}
		name := tag
		if name == "" {
			name = f.Name
		}
		if strings.IndexFunc(name, unicode.IsSpace) >= 0 || strings.ContainsAny(name, "{},") {
			b.config.fail("invalid parameter name")
		}
		if names[name] {
			b.config.fail("duplicate parameter name " + name)
		}
		names[name] = true
		parameters = append(parameters, describedParameter{name: name, in: location, typ: f.Type})
	}
	if location == "path" {
		expected := extractPathParameters(b.config.record.path)
		if len(expected) != len(names) {
			b.config.fail("DescribeParams must match all route parameter names")
		}
		for _, name := range expected {
			if !names[name] {
				b.config.fail("DescribeParams missing route parameter " + name)
			}
		}
	}
	d.parameters = append(d.parameters, parameters...)
	if location == "query" {
		d.query = true
	} else {
		d.params = true
	}
	d.enabled = true
}

// Handler registers an existing ordinary handler, including middleware wrappers.
func (b *RouteBuilder) Handler(handler http.Handler) {
	nilHandler := handler == nil
	if !nilHandler {
		v := reflect.ValueOf(handler)
		switch v.Kind() {
		case reflect.Pointer, reflect.Func, reflect.Map, reflect.Slice, reflect.Interface, reflect.Chan:
			nilHandler = v.IsNil()
		}
	}
	b.finalize(func(routeRecord) http.Handler { return handler }, nilHandler)
}

func addDescriptions(operation *openapi3.Operation, route routeRecord, registry *schemaRegistry) error {
	d := route.descriptions
	schema := func(t reflect.Type, context string) (*openapi3.SchemaRef, error) {
		ref, err := registry.schema(t)
		if err != nil {
			return nil, fmt.Errorf("toad: route %q %s: %w", route.method+" "+route.path, context, err)
		}
		return ref, nil
	}
	if d.body != nil {
		ref, err := schema(d.body.typ, "described body")
		if err != nil {
			return err
		}
		operation.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{Required: true, Content: openapi3.Content{"application/json": &openapi3.MediaType{Schema: ref}}}}
	}
	for _, p := range d.parameters {
		// Parameters use scalar wire values only; JSON objects, nullable pointers and
		// collections require separate serialization contracts and are deferred.
		switch p.typ.Kind() {
		case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64:
		default:
			return fmt.Errorf("toad: route %q %s parameter %q: only scalar parameters are supported", route.method+" "+route.path, p.in, p.name)
		}
		ref, err := schema(p.typ, p.in+" parameter "+p.name)
		if err != nil {
			return err
		}
		style, explode := "form", true
		if p.in == "path" {
			style, explode = "simple", false
		}
		parameter := &openapi3.ParameterRef{Value: &openapi3.Parameter{Name: p.name, In: p.in, Required: p.in == "path", Schema: ref, Style: style, Explode: &explode}}
		replaced := false
		for i, existing := range operation.Parameters {
			if existing.Value.In == p.in && existing.Value.Name == p.name {
				operation.Parameters[i] = parameter
				replaced = true
				break
			}
		}
		if !replaced {
			operation.Parameters = append(operation.Parameters, parameter)
		}
	}
	if len(d.responses) > 0 {
		operation.Responses = &openapi3.Responses{}
		for _, payload := range d.responses {
			response := &openapi3.Response{Description: ptr(getResponseDescription(payload.status))}
			if payload.typ != nil {
				ref, err := schema(payload.typ, fmt.Sprintf("described response %d", payload.status))
				if err != nil {
					return err
				}
				response.Content = openapi3.Content{"application/json": &openapi3.MediaType{Schema: ref}}
			}
			operation.Responses.Set(fmt.Sprint(payload.status), &openapi3.ResponseRef{Value: response})
		}
	}
	return nil
}
