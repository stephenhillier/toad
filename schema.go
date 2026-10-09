package toad

import (
	"crypto/sha256"
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode"

	"github.com/getkin/kin-openapi/openapi3"
)

// A registry belongs to one Generate call; neither reflection state nor mutable
// schemas are shared with request handlers or subsequent document generations.
type schemaRegistry struct {
	components openapi3.Schemas
	models     map[reflect.Type]*openapi3.Schema
	refs       map[reflect.Type][]*openapi3.SchemaRef
}

func newSchemaRegistry(components openapi3.Schemas) *schemaRegistry {
	return &schemaRegistry{components: components, models: make(map[reflect.Type]*openapi3.Schema), refs: make(map[reflect.Type][]*openapi3.SchemaRef)}
}

func (r *schemaRegistry) schema(t reflect.Type) (*openapi3.SchemaRef, error) {
	for _, custom := range []reflect.Type{
		reflect.TypeFor[json.Marshaler](), reflect.TypeFor[json.Unmarshaler](),
		reflect.TypeFor[encoding.TextMarshaler](), reflect.TypeFor[encoding.TextUnmarshaler](),
	} {
		if t.Implements(custom) || reflect.PointerTo(t).Implements(custom) {
			return nil, fmt.Errorf("custom JSON/text encoding for %s is unsupported", t)
		}
	}
	if t.Name() != "" && t.PkgPath() != "" {
		model, exists := r.models[t]
		if !exists {
			model = &openapi3.Schema{}
			r.models[t] = model // Publish before descending into recursive fields.
		}
		ref := &openapi3.SchemaRef{Value: model}
		r.refs[t] = append(r.refs[t], ref)
		if !exists {
			schema, err := r.shape(t)
			if err != nil {
				return nil, err
			}
			*model = *schema
		}
		return ref, nil
	}
	schema, err := r.shape(t)
	if err != nil {
		return nil, err
	}
	return &openapi3.SchemaRef{Value: schema}, nil
}

// A nullable use of a component cannot put siblings beside $ref in OpenAPI 3.0.
// Union with a null-only schema also avoids making the shared component nullable.
func nullable(ref *openapi3.SchemaRef) *openapi3.Schema {
	return &openapi3.Schema{AnyOf: openapi3.SchemaRefs{ref, {Value: &openapi3.Schema{Type: &openapi3.Types{"object"}, Nullable: true, Enum: []any{nil}}}}}
}

func (r *schemaRegistry) shape(t reflect.Type) (*openapi3.Schema, error) {
	switch t.Kind() {
	case reflect.Pointer:
		ref, err := r.schema(t.Elem())
		if err != nil {
			return nil, err
		}
		return nullable(ref), nil
	case reflect.String:
		return openapi3.NewStringSchema(), nil
	case reflect.Bool:
		return openapi3.NewBoolSchema(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return openapi3.NewIntegerSchema(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		schema := openapi3.NewIntegerSchema()
		zero := float64(0)
		schema.Min = &zero
		return schema, nil
	case reflect.Float32, reflect.Float64:
		return openapi3.NewFloat64Schema(), nil
	case reflect.Slice, reflect.Array:
		item, err := r.schema(t.Elem())
		if err != nil {
			return nil, fmt.Errorf("element: %w", err)
		}
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			schema := openapi3.NewStringSchema()
			schema.Format = "byte"
			schema.Nullable = true
			return schema, nil
		}
		schema := openapi3.NewArraySchema()
		schema.Items = item
		schema.Nullable = t.Kind() == reflect.Slice
		return schema, nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("unsupported map key type %s", t.Key())
		}
		if _, err := r.schema(t.Key()); err != nil {
			return nil, fmt.Errorf("map key: %w", err)
		}
		value, err := r.schema(t.Elem())
		if err != nil {
			return nil, fmt.Errorf("map value: %w", err)
		}
		schema := openapi3.NewObjectSchema()
		schema.Nullable = true
		schema.AdditionalProperties = openapi3.AdditionalProperties{Schema: value}
		return schema, nil
	case reflect.Struct:
		schema := openapi3.NewObjectSchema()
		for i := range t.NumField() {
			field := t.Field(i)
			tag := field.Tag.Get("json")
			if tag == "-" {
				continue
			}
			if field.Anonymous {
				return nil, fmt.Errorf("field %s: embedded fields are unsupported", field.Name)
			}
			if !field.IsExported() {
				continue
			}
			name, options, _ := strings.Cut(tag, ",")
			if name == "" || !validJSONName(name) {
				name = field.Name
			}
			if schema.Properties[name] != nil {
				return nil, fmt.Errorf("field %s: duplicate JSON name %q is unsupported", field.Name, name)
			}
			ref, err := r.schema(field.Type)
			if err != nil {
				return nil, fmt.Errorf("field %s: %w", field.Name, err)
			}
			for _, option := range strings.Split(options, ",") {
				if option != "string" {
					continue
				} // omitempty/omitzero affect presence, not validation.
				base := field.Type
				if base.Kind() == reflect.Pointer {
					base = base.Elem()
				}
				switch base.Kind() {
				case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
					reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64:
					quoted := openapi3.NewStringSchema()
					quoted.Nullable = field.Type.Kind() == reflect.Pointer
					ref = &openapi3.SchemaRef{Value: quoted}
				}
			}
			schema.Properties[name] = ref
		}
		// This field is guaranteed by Toad's own envelope writer.
		if t == reflect.TypeFor[ErrorResponse]() {
			schema.Required = []string{"detail"}
		}
		return schema, nil
	default:
		return nil, fmt.Errorf("unsupported schema type %s (%s)", t, t.Kind())
	}
}

// Match encoding/json's accepted tag names; invalid names fall back to Go names.
func validJSONName(name string) bool {
	for _, c := range name {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && !strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c) {
			return false
		}
	}
	return true
}

// Resolve names only after discovery so route registration order cannot decide
// which of two same-named Go types receives the short component name.
func (r *schemaRegistry) finish() error {
	groups := make(map[string]int)
	names := make(map[reflect.Type]string)
	for t := range r.models {
		name := strings.Map(func(c rune) rune {
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._-", c) {
				return c
			}
			return '_'
		}, t.Name())
		names[t] = name
		groups[name]++
	}
	for t, name := range names {
		if groups[name] > 1 {
			hash := sha256.Sum256([]byte(t.PkgPath() + "." + t.String()))
			name = fmt.Sprintf("%s_%x", name, hash)
		}
		if _, exists := r.components[name]; exists {
			return fmt.Errorf("toad: schema component name collision for %s", t)
		}
		r.components[name] = &openapi3.SchemaRef{Value: r.models[t]}
		for _, ref := range r.refs[t] {
			ref.Ref = "#/components/schemas/" + name
		}
	}
	return nil
}
