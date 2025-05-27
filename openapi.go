// openapi.go contains functions for generating OpenAPI specifications.
package buddy

import (
	"embed"
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/getkin/kin-openapi/openapi3"
)

//go:embed docs.html
var ui embed.FS

func (api *Api) generate() ([]byte, error) {
	// Create a new OpenAPI 3.0 document
	doc := &openapi3.T{
		OpenAPI: "3.0.0",
		Info: &openapi3.Info{
			Title:   "API Documentation",
			Version: "1.0.0",
		},
		Paths: &openapi3.Paths{},
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

		// Add 200 response
		response := &openapi3.Response{
			Description: stringPtr("Successful response"),
			Content: openapi3.Content{
				"application/json": &openapi3.MediaType{
					Schema: &openapi3.SchemaRef{
						Value: &openapi3.Schema{
							Type: &openapi3.Types{"object"},
						},
					},
				},
			},
		}
		operation.Responses.Set("200", &openapi3.ResponseRef{Value: response})
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

// stringPtr returns a pointer to the given string
func stringPtr(s string) *string {
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

func (api *Api) ServeDocs(w http.ResponseWriter, r *http.Request) {
	data, err := api.generate()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func (api *Api) ServeDocsHTML(w http.ResponseWriter, r *http.Request) {
	data, err := ui.ReadFile("docs.html")
	if err != nil {
		http.Error(w, "Documentation not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.Write(data)
}
