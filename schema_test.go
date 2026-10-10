package toad

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stephenhillier/toad/internal/schematest/first"
	"github.com/stephenhillier/toad/internal/schematest/second"
)

type schemaNode struct {
	Name     string                 `json:"name"`
	Next     *schemaNode            `json:"next,omitempty"`
	Children []schemaNode           `json:"children"`
	Lookup   map[string]*schemaNode `json:"lookup"`
}

func TestSchemaReuseAndRecursion(t *testing.T) {
	api := NewApi(http.NewServeMux())
	api.Route("POST /nodes").Body(schemaNode{}).Response(201, schemaNode{}).
		HandlerFunc(func(_ *http.Request, body schemaNode) (schemaNode, error) { return body, nil })
	api.Route("GET /nodes").Response(200, schemaNode{}).HandlerFunc(func(*http.Request) (schemaNode, error) { return schemaNode{}, nil })
	doc := generatedDocument(t, api)
	if len(doc.Components.Schemas) != 5 {
		t.Fatalf("components: %v", doc.Components.Schemas)
	}
	ref := "#/components/schemas/schemaNode"
	post := doc.Paths.Value("/nodes").Post
	if post.RequestBody.Value.Content["application/json"].Schema.Ref != ref+"Input" {
		t.Fatal("request context lost")
	}
	for _, s := range []*openapi3.SchemaRef{
		post.Responses.Value("201").Value.Content["application/json"].Schema,
		doc.Paths.Value("/nodes").Get.Responses.Value("200").Value.Content["application/json"].Schema} {
		if s.Ref != ref {
			t.Fatalf("model not reused: %s", s.Ref)
		}
	}
	node := doc.Components.Schemas["schemaNode"].Value
	if node.Properties["next"].Value.AnyOf[0].Ref != ref || node.Properties["children"].Value.Items.Ref != ref || node.Properties["lookup"].Value.AdditionalProperties.Schema.Value.AnyOf[0].Ref != ref {
		t.Fatal("recursive references lost")
	}
	for _, value := range []schemaNode{{}, {Name: "root", Next: &schemaNode{Name: "child"}, Children: []schemaNode{{Name: "leaf"}}}} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := node.VisitJSON(jsonValue(t, data)); err != nil {
			t.Fatalf("%s: %v", data, err)
		}
	}
	if err := node.VisitJSON(map[string]any{"next": 12.0}); err == nil {
		t.Fatal("nullable reference accepted a number")
	}
	if err := node.VisitJSON(nil); err == nil {
		t.Fatal("pointer use made the value model nullable")
	}
	errorBase := doc.Components.Schemas["ErrorResponse"].Value
	if !reflect.DeepEqual(errorBase.Required, []string{"detail"}) || len(errorBase.Properties) != 1 || len(errorBase.Properties["detail"].Value.Enum) != 0 {
		t.Fatal("response constraints mutated shared error model")
	}
	for _, status := range []string{"400", "413", "415", "500"} {
		schema := post.Responses.Value(status).Value.Content["application/json"].Schema.Value
		if len(schema.AllOf) != 1 || schema.AllOf[0].Ref != "#/components/schemas/ErrorResponse" {
			t.Fatal("error component not reused")
		}
		if err := schema.VisitJSON(map[string]any{}); err == nil {
			t.Fatal("missing envelope field accepted")
		}
	}
	a, err := api.Generate()
	if err != nil {
		t.Fatal(err)
	}
	b, err := api.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("generation is not deterministic")
	}
}

type schemaTags struct {
	Renamed string   `json:"renamed"`
	Default string   `json:",omitempty"`
	Zero    int      `json:",omitzero"`
	Ignored chan int `json:"-"`
	hidden  chan int
	Dash    string                 `json:"-,omitempty"`
	Invalid string                 `json:"bad\\name"`
	Count   int                    `json:"count,string"`
	Quoted  *bool                  `json:"quoted,string,omitempty"`
	Bytes   []byte                 `json:"bytes"`
	Array   [2]uint                `json:"array"`
	Values  map[string][]float64   `json:"values"`
	Nested  struct{ Enabled bool } `json:"nested"`
}

func TestSchemaJSONFieldsAndNullability(t *testing.T) {
	api := NewApi(http.NewServeMux())
	api.Route("POST /tags").Body(schemaTags{}).Response(200, schemaTags{}).HandlerFunc(func(_ *http.Request, b schemaTags) (schemaTags, error) { return b, nil })
	doc := generatedDocument(t, api)
	schema := doc.Components.Schemas["schemaTags"].Value
	want := []string{"renamed", "Default", "Zero", "-", "Invalid", "count", "quoted", "bytes", "array", "values", "nested"}
	if len(schema.Properties) != len(want) {
		t.Fatalf("properties: %v", schema.Properties)
	}
	for _, name := range want {
		if schema.Properties[name] == nil {
			t.Errorf("missing %s", name)
		}
	}
	if len(schema.Required) != 0 {
		t.Fatal("field presence incorrectly treated as validation")
	}
	if err := schema.VisitJSON(map[string]any{}); err != nil {
		t.Fatal(err)
	}
	yes := true
	for _, value := range []schemaTags{{}, {Quoted: &yes, Bytes: []byte{1, 2}, Values: map[string][]float64{"x": nil}}} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := schema.VisitJSON(jsonValue(t, data)); err != nil {
			t.Fatalf("%s: %v", data, err)
		}
	}
	for _, name := range []string{"quoted", "bytes", "values"} {
		if err := schema.Properties[name].Value.VisitJSON(nil); err != nil {
			t.Fatalf("%s should be nullable: %v", name, err)
		}
	}
	if err := schema.Properties["array"].Value.VisitJSON(nil); err == nil {
		t.Fatal("array should not be nullable")
	}
	if !schema.Properties["count"].Value.Type.Is("string") || schema.Properties["bytes"].Value.Format != "byte" {
		t.Fatal("JSON representation differs")
	}
}

func TestSchemaComponentNameCollisions(t *testing.T) {
	build := func(reverse bool) *openapi3.T {
		api := NewApi(http.NewServeMux())
		a := func() {
			api.Route("GET /a").Response(200, first.Model{}).HandlerFunc(func(*http.Request) (first.Model, error) { return first.Model{}, nil })
		}
		b := func() {
			api.Route("GET /b").Response(200, second.Model{}).HandlerFunc(func(*http.Request) (second.Model, error) { return second.Model{}, nil })
		}
		if reverse {
			b()
			a()
		} else {
			a()
			b()
		}
		return generatedDocument(t, api)
	}
	a, b := build(false), build(true)
	refs := make(map[string]bool)
	for _, path := range []string{"/a", "/b"} {
		x := a.Paths.Value(path).Get.Responses.Value("200").Value.Content["application/json"].Schema
		y := b.Paths.Value(path).Get.Responses.Value("200").Value.Content["application/json"].Schema
		if x.Ref != y.Ref || !strings.HasPrefix(x.Ref, "#/components/schemas/Model_") {
			t.Fatalf("unstable collision name: %s %s", x.Ref, y.Ref)
		}
		refs[x.Ref] = true
	}
	if len(refs) != 2 || len(a.Components.Schemas) != 3 {
		t.Fatal("distinct Go types conflated")
	}
	if a.Paths.Value("/a").Get.Responses.Value("200").Value.Content["application/json"].Schema.Value.Properties["First"] == nil {
		t.Fatal("wrong model resolved")
	}
}

type schemaCustom string

func (*schemaCustom) UnmarshalJSON([]byte) error { return nil }

type schemaText string

func (schemaText) MarshalText() ([]byte, error) { return nil, nil }

func TestSchemaUnsupportedShapes(t *testing.T) {
	for _, tc := range []struct {
		value  any
		detail string
	}{
		{struct{ Value time.Time }{}, "field Value: custom"},
		{struct{ Value schemaCustom }{}, "field Value: custom"},
		{struct{ Value map[schemaText]string }{}, "map key: custom"},
		{struct{ User }{}, "field User: embedded"},
		{struct{ Value any }{}, "field Value: unsupported"},
		{struct{ Value complex64 }{}, "field Value: unsupported"},
		{reflect.New(reflect.StructOf([]reflect.StructField{
			{Name: "A", Type: reflect.TypeFor[string](), Tag: `json:"same"`},
			{Name: "B", Type: reflect.TypeFor[string](), Tag: `json:"same"`},
		})).Elem().Interface(), "duplicate JSON name"},
	} {
		t.Run(reflect.TypeOf(tc.value).String(), func(t *testing.T) {
			registry := newSchemaRegistry(make(openapi3.Schemas))
			_, err := registry.schema(reflect.TypeOf(tc.value))
			if err == nil || !strings.Contains(err.Error(), tc.detail) {
				t.Fatalf("expected %q, got %v", tc.detail, err)
			}
		})
	}
}

type schemaList []schemaList
type schemaMap map[string]schemaMap
type schemaScalar uint64
type schemaMutualA struct{ B *schemaMutualB }
type schemaMutualB struct{ A *schemaMutualA }

func TestSchemaNamedContainersAndMutualRecursion(t *testing.T) {
	type model struct {
		List   schemaList
		Map    schemaMap
		Scalar schemaScalar
		A      schemaMutualA
	}
	api := NewApi(http.NewServeMux())
	api.Route("GET /models").Response(200, model{}).HandlerFunc(func(*http.Request) (model, error) { return model{}, nil })
	doc := generatedDocument(t, api)
	schema := doc.Components.Schemas["model"].Value
	for _, value := range []model{{}, {List: schemaList{nil, {}}, Map: schemaMap{"key": nil}, Scalar: 12}} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := schema.VisitJSON(jsonValue(t, data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := doc.Components.Schemas["schemaScalar"].Value.VisitJSON(-1.0); err == nil {
		t.Fatal("unsigned scalar accepted a negative value")
	}
}
