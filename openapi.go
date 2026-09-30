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

func (api *Api) Generate() ([]byte, error) {
	// Create a new OpenAPI 3.0 document
	doc := &openapi3.T{
		OpenAPI: "3.0.0",
		Info: &openapi3.Info{
			Title:   "API Documentation",
			Version: "1.0.0",
		},
		Paths: &openapi3.Paths{},
		Components: &openapi3.Components{
			Schemas: make(openapi3.Schemas),
		},
		Servers: []*openapi3.Server{
			{
				URL:         "http://localhost:8080",
				Description: "Development server",
			},
		},
	}

	// Convert routes to OpenAPI paths
	for _, route := range api.Routes {
		// Get or create path item
		pathItem := doc.Paths.Find(route.Path)
		if pathItem == nil {
			pathItem = &openapi3.PathItem{
				Description: "path item",
			}
			doc.Paths.Set(route.Path, pathItem)
		}

		// Create operation
		operation := &openapi3.Operation{
			Summary:     route.Description,
			Description: route.Description,
			Responses:   openapi3.NewResponses(),
		}

		// Add request body if Body field is present
		if route.Body != nil {
			bodySchema := createSchemaFromValue(route.Body)
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
		pathParams := extractPathParameters(route.Path)
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

		// Add responses (custom first, then standard)
		addResponses(operation, route)
		pathItem.SetOperation(route.Method, operation)
	}

	// Validate the document
	if err := doc.Validate(openapi3.NewLoader().Context); err != nil {
		return nil, err
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

// createSchemaFromValue creates an OpenAPI schema from a Go value
func createSchemaFromValue(value any) *openapi3.Schema {
	if value == nil {
		return &openapi3.Schema{
			Type: &openapi3.Types{"object"},
		}
	}

	t := reflect.TypeOf(value)
	if t.Kind() == reflect.Ptr {
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
	if t.Kind() == reflect.Ptr {
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

// addResponses adds custom and standard HTTP responses to an operation
func addResponses(operation *openapi3.Operation, route Route) {
	// First add custom responses from the route
	addCustomResponses(operation, route.Responses)
}

// addCustomResponses adds custom response schemas from Route.Responses
func addCustomResponses(operation *openapi3.Operation, responses map[int]any) {
	if responses == nil {
		return
	}

	for statusCode, schema := range responses {
		response := &openapi3.Response{
			Description: ptr(getResponseDescription(statusCode)),
			Content: openapi3.Content{
				"application/json": &openapi3.MediaType{
					Schema: &openapi3.SchemaRef{
						Value: createSchemaFromValue(schema),
					},
				},
			},
		}
		operation.Responses.Set(fmt.Sprintf("%d", statusCode), &openapi3.ResponseRef{Value: response})
	}
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
