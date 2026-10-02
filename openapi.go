// openapi.go contains functions for generating OpenAPI specifications.
package buddy

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"regexp"

	"github.com/getkin/kin-openapi/openapi3"
)

//go:embed static/scalar.html
var ui embed.FS

// Generate validates and serializes the OpenAPI document from finalized routes.
// Unsupported model shapes return errors with route and field context.
func (api *Api) Generate() ([]byte, error) {
	// Create a new OpenAPI 3.0 document
	doc := &openapi3.T{
		OpenAPI: "3.0.0",
		Info: &openapi3.Info{
			Title:       api.title,
			Description: api.description,
			Version:     api.version,
		},
		Paths: &openapi3.Paths{},
		Components: &openapi3.Components{
			Schemas: make(openapi3.Schemas),
		},
	}
	if api.server != "" {
		doc.Servers = openapi3.Servers{&openapi3.Server{URL: api.server}}
	}

	// Convert routes to OpenAPI paths
	for _, route := range api.routes {
		for _, model := range []struct {
			name string
			typ  reflect.Type
		}{
			{"request body", route.bodyType}, {"response", route.resultType},
		} {
			if model.typ != nil {
				if err := validateSchemaType(model.typ, make(map[reflect.Type]bool)); err != nil {
					return nil, fmt.Errorf("buddy: route %q %s model %s: %w", route.method+" "+route.path, model.name, model.typ, err)
				}
			}
		}
		// Get or create path item
		pathItem := doc.Paths.Value(route.path)
		if pathItem == nil {
			pathItem = &openapi3.PathItem{
				Description: "path item",
			}
			doc.Paths.Set(route.path, pathItem)
		}

		// Create operation
		operation := &openapi3.Operation{
			Summary:     route.title,
			Description: route.description,
			Responses:   &openapi3.Responses{},
		}

		// Add a request body when a body type was selected
		if route.bodyType != nil {
			bodySchema := createSchemaFromType(route.bodyType)
			operation.RequestBody = &openapi3.RequestBodyRef{
				Value: &openapi3.RequestBody{
					Description: "Request body",
					Required:    true,
					Content: openapi3.Content{
						"application/json": &openapi3.MediaType{
							Schema: &openapi3.SchemaRef{
								Value: bodySchema,
							},
						},
					},
				},
			}
		}

		// Extract and add path parameters
		pathParams := extractPathParameters(route.path)
		for _, param := range pathParams {
			if operation.Parameters == nil {
				operation.Parameters = make([]*openapi3.ParameterRef, 0)
			}
			operation.Parameters = append(operation.Parameters, &openapi3.ParameterRef{
				Value: &openapi3.Parameter{
					Name:        param,
					In:          "path",
					Required:    true,
					Description: "Path parameter: " + param,
					Schema: &openapi3.SchemaRef{
						Value: &openapi3.Schema{
							Type: &openapi3.Types{"string"},
						},
					},
				},
			})
		}

		// Add the selected managed success response
		addResponses(operation, route)
		pathItem.SetOperation(route.method, operation)
	}

	// Validate the document
	if err := doc.Validate(openapi3.NewLoader().Context); err != nil {
		return nil, fmt.Errorf("buddy: validate OpenAPI document: %w", err)
	}

	// Marshal to JSON
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// ptr returns a pointer to the given string
func ptr(s string) *string {
	return &s
}

// extractPathParameters extracts parameter names from a path like "/users/{id}"
func extractPathParameters(path string) []string {
	re := regexp.MustCompile(`\{([^}]+)\}`)
	matches := re.FindAllStringSubmatch(path, -1)

	params := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			params = append(params, match[1])
		}
	}
	return params
}

// validateSchemaType bounds the inline schema walker and reports unsupported
// shapes during generation, never during application-request dispatch.
// Recursive components and richer JSON model support belong to the registry.
func validateSchemaType(t reflect.Type, visiting map[reflect.Type]bool) error {
	if visiting[t] {
		return fmt.Errorf("recursive type %s requires reusable schema support", t)
	}
	visiting[t] = true
	defer delete(visiting, t)
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return validateSchemaType(t.Elem(), visiting)
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return fmt.Errorf("unsupported map key type %s", t.Key())
		}
		return validateSchemaType(t.Elem(), visiting)
	case reflect.Struct:
		for i := range t.NumField() {
			field := t.Field(i)
			if field.IsExported() {
				if err := validateSchemaType(field.Type, visiting); err != nil {
					return fmt.Errorf("field %s: %w", field.Name, err)
				}
			}
		}
		return nil
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Float32, reflect.Float64:
		return nil
	default:
		return fmt.Errorf("unsupported schema type %s (%s)", t, t.Kind())
	}
}

// createSchemaFromValue creates an OpenAPI schema from a Go value
func createSchemaFromValue(value any) *openapi3.Schema {
	if value == nil {
		return &openapi3.Schema{
			Type: &openapi3.Types{"object"},
		}
	}

	t := reflect.TypeOf(value)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.String:
		return &openapi3.Schema{
			Type: &openapi3.Types{"string"},
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return &openapi3.Schema{
			Type: &openapi3.Types{"integer"},
		}
	case reflect.Float32, reflect.Float64:
		return &openapi3.Schema{
			Type: &openapi3.Types{"number"},
		}
	case reflect.Bool:
		return &openapi3.Schema{
			Type: &openapi3.Types{"boolean"},
		}
	case reflect.Struct:
		schema := &openapi3.Schema{
			Type:       &openapi3.Types{"object"},
			Properties: make(openapi3.Schemas),
		}

		for i := range t.NumField() {
			field := t.Field(i)
			if field.IsExported() {
				fieldName := getJSONFieldName(field)
				fieldSchema := createSchemaFromType(field.Type)
				schema.Properties[fieldName] = &openapi3.SchemaRef{Value: fieldSchema}
			}
		}
		return schema
	case reflect.Slice, reflect.Array:
		return &openapi3.Schema{
			Type: &openapi3.Types{"array"},
			Items: &openapi3.SchemaRef{
				Value: createSchemaFromType(t.Elem()),
			},
		}
	case reflect.Map:
		return &openapi3.Schema{
			Type: &openapi3.Types{"object"},
			AdditionalProperties: openapi3.AdditionalProperties{
				Schema: &openapi3.SchemaRef{
					Value: createSchemaFromType(t.Elem()),
				},
			},
		}
	default:
		return &openapi3.Schema{
			Type: &openapi3.Types{"object"},
		}
	}
}

// createSchemaFromType creates an OpenAPI schema from a reflect.Type
func createSchemaFromType(t reflect.Type) *openapi3.Schema {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.String:
		return &openapi3.Schema{Type: &openapi3.Types{"string"}}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return &openapi3.Schema{Type: &openapi3.Types{"integer"}}
	case reflect.Float32, reflect.Float64:
		return &openapi3.Schema{Type: &openapi3.Types{"number"}}
	case reflect.Bool:
		return &openapi3.Schema{Type: &openapi3.Types{"boolean"}}
	case reflect.Struct:
		schema := &openapi3.Schema{
			Type:       &openapi3.Types{"object"},
			Properties: make(openapi3.Schemas),
		}

		for i := range t.NumField() {
			field := t.Field(i)
			if field.IsExported() {
				fieldName := getJSONFieldName(field)
				fieldSchema := createSchemaFromType(field.Type)
				schema.Properties[fieldName] = &openapi3.SchemaRef{Value: fieldSchema}
			}
		}
		return schema
	case reflect.Slice, reflect.Array:
		return &openapi3.Schema{
			Type: &openapi3.Types{"array"},
			Items: &openapi3.SchemaRef{
				Value: createSchemaFromType(t.Elem()),
			},
		}
	case reflect.Map:
		return &openapi3.Schema{
			Type: &openapi3.Types{"object"},
			AdditionalProperties: openapi3.AdditionalProperties{
				Schema: &openapi3.SchemaRef{
					Value: createSchemaFromType(t.Elem()),
				},
			},
		}
	default:
		return &openapi3.Schema{Type: &openapi3.Types{"object"}}
	}
}

// getJSONFieldName gets the JSON field name from a struct field
func getJSONFieldName(field reflect.StructField) string {
	if jsonTag := field.Tag.Get("json"); jsonTag != "" {
		if jsonTag == "-" {
			return ""
		}
		if idx := regexp.MustCompile(",").FindStringIndex(jsonTag); idx != nil {
			return jsonTag[:idx[0]]
		}
		return jsonTag
	}
	return field.Name
}

// addResponses documents only the route contract, using the runtime error envelope.
func addResponses(operation *openapi3.Operation, route routeRecord) {
	codes := make(map[int][]string)
	addCode := func(status int, code string) {
		for _, existing := range codes[status] {
			if existing == code {
				return
			}
		}
		codes[status] = append(codes[status], code)
	}
	if route.bodyType != nil {
		for _, status := range []int{400, 413, 415} {
			addCode(status, decoderErrorCode(status))
		}
	}
	if route.mode != ordinary {
		addCode(500, CodeInternalError)
		for _, mapping := range route.errors {
			addCode(mapping.status, CodeApplicationError)
		}
	}
	for status, values := range codes {
		schema := createSchemaFromType(reflect.TypeFor[ErrorResponse]())
		schema.Required = []string{"code", "message"}
		for _, code := range values {
			schema.Properties["code"].Value.Enum = append(schema.Properties["code"].Value.Enum, code)
		}
		schema.Properties["message"].Value.Enum = []any{publicError(status, values[0]).Message}
		operation.Responses.Set(fmt.Sprintf("%d", status), &openapi3.ResponseRef{Value: &openapi3.Response{
			Description: ptr(publicError(status, values[0]).Message),
			Content:     openapi3.Content{"application/json": &openapi3.MediaType{Schema: &openapi3.SchemaRef{Value: schema}}},
		}})
	}
	if route.mode == ordinary {
		operation.Responses.Set("default", &openapi3.ResponseRef{Value: &openapi3.Response{
			Description: ptr("Handler-defined response"),
		}})
		return
	}
	response := &openapi3.Response{
		Description: ptr(getResponseDescription(route.status)),
		Content: openapi3.Content{
			"application/json": &openapi3.MediaType{Schema: &openapi3.SchemaRef{Value: createSchemaFromType(route.resultType)}},
		},
	}
	operation.Responses.Set(fmt.Sprintf("%d", route.status), &openapi3.ResponseRef{Value: response})
}

// getResponseDescription returns a default description for common HTTP status codes
func getResponseDescription(statusCode int) string {
	switch statusCode {
	case 200:
		return "Successful response"
	case 201:
		return "Resource created successfully"
	case 202:
		return "Request accepted"
	case 204:
		return "No content"
	case 400:
		return "Bad request"
	case 401:
		return "Unauthorized"
	case 403:
		return "Forbidden"
	case 404:
		return "Resource not found"
	case 409:
		return "Conflict"
	case 422:
		return "Unprocessable entity"
	case 500:
		return "Internal server error"
	default:
		return "Response"
	}
}

func (api *Api) ServeDocs(w http.ResponseWriter, r *http.Request) {
	data, err := api.Generate()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func (api *Api) ServeDocsHTML(w http.ResponseWriter, r *http.Request) {
	data, err := ui.ReadFile("static/scalar.html")
	if err != nil {
		http.Error(w, "Documentation not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.Write(data)
}
