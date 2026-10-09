// openapi.go contains functions for generating OpenAPI specifications.
package toad

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

	registry := newSchemaRegistry(doc.Components.Schemas)
	// Convert finalized types once, using one registry for the entire document.
	for _, route := range api.routes {
		models := make([]*openapi3.SchemaRef, 2)
		for i, model := range []struct {
			name string
			typ  reflect.Type
		}{
			{"request body", route.bodyType}, {"response", route.resultType},
		} {
			if model.typ != nil {
				var err error
				models[i], err = registry.schema(model.typ)
				if err != nil {
					return nil, fmt.Errorf("toad: route %q %s model %s: %w", route.method+" "+route.path, model.name, model.typ, err)
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
			bodySchema := models[0]
			operation.RequestBody = &openapi3.RequestBodyRef{
				Value: &openapi3.RequestBody{
					Description: "Request body",
					Required:    true,
					Content: openapi3.Content{
						"application/json": &openapi3.MediaType{
							Schema: bodySchema,
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
		addResponses(operation, route, registry, models[1])
		if err := addDescriptions(operation, route, registry); err != nil {
			return nil, err
		}
		pathItem.SetOperation(route.method, operation)
	}

	if err := registry.finish(); err != nil {
		return nil, err
	}

	// Validate the document
	if err := doc.Validate(openapi3.NewLoader().Context); err != nil {
		return nil, fmt.Errorf("toad: validate OpenAPI document: %w", err)
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

// addResponses documents only the route contract, using the runtime error envelope.
func addResponses(operation *openapi3.Operation, route routeRecord, registry *schemaRegistry, success *openapi3.SchemaRef) {
	details := make(map[int][]any)
	addDetail := func(status int, detail string) {
		for _, existing := range details[status] {
			if existing == detail {
				return
			}
		}
		details[status] = append(details[status], detail)
	}
	if route.bodyType != nil {
		for _, status := range []int{400, 413, 415} {
			addDetail(status, publicError(status).Detail)
		}
	}
	if route.mode != ordinary {
		addDetail(500, publicError(500).Detail)
		for _, mapping := range route.errors {
			addDetail(mapping.status, mapping.detail)
		}
	}
	for status, values := range details {
		base, _ := registry.schema(reflect.TypeFor[ErrorResponse]()) // fixed, supported framework model
		detail := publicError(status).Detail
		schema := openapi3.NewObjectSchema()
		schema.AllOf = openapi3.SchemaRefs{base}
		schema.Properties = openapi3.Schemas{
			"detail": {Value: openapi3.NewStringSchema().WithEnum(values...)},
		}
		operation.Responses.Set(fmt.Sprintf("%d", status), &openapi3.ResponseRef{Value: &openapi3.Response{
			Description: ptr(detail),
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
			"application/json": &openapi3.MediaType{Schema: success},
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
