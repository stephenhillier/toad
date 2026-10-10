package toad

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stephenhillier/toad/internal/schematest/first"
	"github.com/stephenhillier/toad/internal/schematest/second"
)

func validationModel(t reflect.Type, tag, jsonTag string) reflect.Type {
	return reflect.StructOf([]reflect.StructField{{Name: "Value", Type: t,
		Tag: reflect.StructTag(`json:"` + jsonTag + `" validate:"` + tag + `"`)}})
}

func validationTestSchema(t *testing.T, typ reflect.Type) *openapi3.Schema {
	t.Helper()
	r := newSchemaRegistry(make(openapi3.Schemas))
	ref, err := r.schemaIn(typ, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.finish(); err != nil {
		t.Fatal(err)
	}
	if err := ref.Value.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return ref.Value
}

// Compare the complete object schema to actual JSON decoding and tag validation,
// so an incorrect required-property inference cannot hide behind a field test.
func TestValidationSchemaMatchesRuntime(t *testing.T) {
	for _, tc := range []struct {
		name           string
		typ            reflect.Type
		tag            string
		valid, invalid []string
	}{
		{"string required", reflect.TypeFor[string](), "required", []string{`{"value":"a"}`}, []string{`{}`, `{"value":""}`}},
		{"unicode length", reflect.TypeFor[string](), "min=2,max=3", []string{`{"value":"é日"}`, `{"value":"abc"}`}, []string{`{}`, `{"value":"日"}`, `{"value":"abcd"}`}},
		{"length conjunction", reflect.TypeFor[string](), "min=1,min=2,max=4,len=3", []string{`{"value":"abc"}`}, []string{`{}`, `{"value":"ab"}`, `{"value":"abcd"}`}},
		{"optional string", reflect.TypeFor[string](), "omitempty,min=2", []string{`{}`, `{"value":""}`, `{"value":"ab"}`}, []string{`{"value":"a"}`}},
		{"ordered omission", reflect.TypeFor[string](), "min=2,omitempty,max=3", []string{`{"value":"ab"}`}, []string{`{}`, `{"value":""}`, `{"value":"abcd"}`}},
		{"required then omission", reflect.TypeFor[string](), "required,omitempty,min=2", []string{`{"value":"ab"}`}, []string{`{}`, `{"value":""}`, `{"value":"a"}`}},
		{"omission then required", reflect.TypeFor[string](), "omitempty,required,min=2", []string{`{}`, `{"value":""}`, `{"value":"ab"}`}, []string{`{"value":"a"}`}},
		{"omitnil scalar", reflect.TypeFor[string](), "omitnil,min=2", []string{`{"value":"ab"}`}, []string{`{}`, `{"value":""}`}},
		{"pointer string", reflect.TypeFor[*string](), "omitempty,min=2", []string{`{}`, `{"value":null}`, `{"value":"ab"}`}, []string{`{"value":""}`, `{"value":"a"}`}},
		{"pointer min", reflect.TypeFor[*int](), "min=0", []string{`{"value":0}`, `{"value":1}`}, []string{`{}`, `{"value":null}`, `{"value":-1}`}},
		{"pointer omitnil", reflect.TypeFor[*int](), "omitnil,gt=0", []string{`{}`, `{"value":null}`, `{"value":1}`}, []string{`{"value":0}`}},
		{"pointer boolean", reflect.TypeFor[*bool](), "required", []string{`{"value":false}`, `{"value":true}`}, []string{`{}`, `{"value":null}`}},
		{"pointer boolean omission", reflect.TypeFor[*bool](), "omitempty,required", []string{`{}`, `{"value":null}`, `{"value":false}`}, nil},
		{"boolean", reflect.TypeFor[bool](), "required", []string{`{"value":true}`}, []string{`{}`, `{"value":false}`}},
		{"boolean omission", reflect.TypeFor[bool](), "omitempty,required", []string{`{}`, `{"value":false}`, `{"value":true}`}, nil},
		{"required integer", reflect.TypeFor[int](), "required,min=-2,max=2", []string{`{"value":-2}`, `{"value":2}`}, []string{`{}`, `{"value":0}`, `{"value":3}`}},
		{"integer exclusive", reflect.TypeFor[int](), "gt=-2,lt=2", []string{`{}`, `{"value":-1}`, `{"value":1}`}, []string{`{"value":-2}`, `{"value":2}`}},
		{"float inclusive", reflect.TypeFor[float64](), "gte=0.5,lte=1.5", []string{`{"value":0.5}`, `{"value":1.5}`}, []string{`{}`, `{"value":0.49}`, `{"value":1.51}`}},
		{"exact float32 inclusive", reflect.TypeFor[float32](), "gte=0.5,lte=1.5", []string{`{"value":0.5}`, `{"value":1.5}`}, []string{`{}`, `{"value":0.49}`, `{"value":1.51}`}},
		{"float exclusive", reflect.TypeFor[float64](), "gt=0.5,lt=1.5", []string{`{"value":1}`}, []string{`{}`, `{"value":0.5}`, `{"value":1.5}`}},
		{"numeric len", reflect.TypeFor[float64](), "len=1.5", []string{`{"value":1.5}`}, []string{`{}`, `{"value":1}`, `{"value":2}`}},
		{"unsigned", reflect.TypeFor[uint64](), "min=0x2,max=4", []string{`{"value":2}`, `{"value":4}`}, []string{`{}`, `{"value":1}`, `{"value":5}`}},
		{"integer equality", reflect.TypeFor[int](), "len=-2", []string{`{"value":-2}`}, []string{`{}`, `{"value":-1}`}},
		{"numeric optional", reflect.TypeFor[int](), "omitempty,min=2", []string{`{}`, `{"value":0}`, `{"value":2}`}, []string{`{"value":1}`}},
		{"string choices", reflect.TypeFor[string](), "oneof=red 'dark blue' a0x2Cb x0x7Cy", []string{`{"value":"red"}`, `{"value":"dark blue"}`, `{"value":"a,b"}`, `{"value":"x|y"}`}, []string{`{}`, `{"value":"blue"}`}},
		{"empty choice", reflect.TypeFor[string](), "oneof='' red", []string{`{}`, `{"value":""}`, `{"value":"red"}`}, []string{`{"value":"blue"}`}},
		{"integer choices", reflect.TypeFor[int](), "oneof=-2 0 3", []string{`{}`, `{"value":-2}`, `{"value":3}`}, []string{`{"value":1}`}},
		{"unsigned choices", reflect.TypeFor[uint](), "oneof=2 3", []string{`{"value":2}`, `{"value":3}`}, []string{`{}`, `{"value":1}`}},
		{"required slice", reflect.TypeFor[[]int](), "required", []string{`{"value":[]}`, `{"value":[0]}`}, []string{`{}`, `{"value":null}`}},
		{"slice bounds", reflect.TypeFor[[]int](), "min=1,max=2", []string{`{"value":[0]}`, `{"value":[0,1]}`}, []string{`{}`, `{"value":null}`, `{"value":[]}`, `{"value":[0,1,2]}`}},
		{"slice nil length", reflect.TypeFor[[]int](), "len=0", []string{`{}`, `{"value":null}`, `{"value":[]}`}, []string{`{"value":[0]}`}},
		{"slice omitempty", reflect.TypeFor[[]int](), "omitempty,min=1,dive,gt=0", []string{`{}`, `{"value":null}`, `{"value":[1]}`}, []string{`{"value":[]}`, `{"value":[0]}`}},
		{"slice dive", reflect.TypeFor[[]int](), "dive,gt=0", []string{`{}`, `{"value":null}`, `{"value":[]}`, `{"value":[1]}`}, []string{`{"value":[0]}`}},
		{"array count", reflect.TypeFor[[2]int](), "len=2,dive,min=1", []string{`{"value":[1,2]}`}, []string{`{}`, `{"value":[0,2]}`}},
		{"map required", reflect.TypeFor[map[string]int](), "required", []string{`{"value":{}}`, `{"value":{"x":0}}`}, []string{`{}`, `{"value":null}`}},
		{"map count", reflect.TypeFor[map[string]int](), "min=1,max=2", []string{`{"value":{"x":0}}`, `{"value":{"x":0,"y":1}}`}, []string{`{}`, `{"value":{}}`, `{"value":{"x":0,"y":1,"z":2}}`}},
		{"map zero count", reflect.TypeFor[map[string]int](), "len=0", []string{`{}`, `{"value":null}`, `{"value":{}}`}, []string{`{"value":{"x":0}}`}},
		{"map values", reflect.TypeFor[map[string]string](), "omitnil,dive,min=2", []string{`{}`, `{"value":null}`, `{"value":{}}`, `{"value":{"x":"ab"}}`}, []string{`{"value":{"x":"a"}}`}},
		{"multiple dive", reflect.TypeFor[[][]*int](), "dive,dive,required,min=1", []string{`{}`, `{"value":null}`, `{"value":[null,[],[1]]}`}, []string{`{"value":[[null]]}`, `{"value":[[0]]}`}},
		{"pointer struct required", reflect.TypeFor[*struct{ Enabled bool }](), "required", []string{`{"value":{}}`, `{"value":{"Enabled":false}}`}, []string{`{}`, `{"value":null}`}},
		{"pointer nested struct", reflect.TypeFor[*validationChild](), "omitempty", []string{`{}`, `{"value":null}`, `{"value":{"name":"ab"}}`}, []string{`{"value":{}}`, `{"value":{"name":"a"}}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			typ := validationModel(tc.typ, tc.tag, "value")
			schema := validationTestSchema(t, typ)
			validator := newTagValidator()
			for _, group := range []struct {
				cases []string
				valid bool
			}{{tc.valid, true}, {tc.invalid, false}} {
				for _, data := range group.cases {
					body := reflect.New(typ)
					if err := json.Unmarshal([]byte(data), body.Interface()); err != nil {
						t.Fatal(err)
					}
					runtimeValid := validateTags(context.Background(), validator, body.Interface()) == nil
					schemaValid := schema.VisitJSON(jsonValue(t, []byte(data))) == nil
					if runtimeValid != group.valid || schemaValid != runtimeValid {
						t.Errorf("%s: runtime=%t schema=%t want=%t", data, runtimeValid, schemaValid, group.valid)
					}
				}
			}
		})
	}
}

type validationChild struct {
	Name string `json:"name" validate:"required,min=2"`
}
type validationTree struct {
	Child   validationChild            `json:"child"`
	Skipped validationChild            `json:"skipped" validate:"-"`
	Plain   []validationChild          `json:"plain"`
	Deep    []validationChild          `json:"deep" validate:"dive"`
	Map     map[string]validationChild `json:"map" validate:"dive"`
	Next    *validationTree            `json:"next"`
}

func TestValidationNestedTraversalAndRecursion(t *testing.T) {
	api := NewApi(http.NewServeMux())
	api.Route("POST /tree").Body(validationTree{}).Response(200, validationTree{}).
		HandlerFunc(func(_ *http.Request, b validationTree) (validationTree, error) { return b, nil })
	doc := generatedDocument(t, api)
	request := doc.Paths.Value("/tree").Post.RequestBody.Value.Content["application/json"].Schema.Value
	response := doc.Components.Schemas["validationTree"].Value
	validator := newTagValidator()
	for _, data := range []string{
		`{}`, `{"child":{}}`, `{"child":{"name":"ab"}}`,
		`{"child":{"name":"ab"},"skipped":{},"plain":[{}]}`,
		`{"child":{"name":"ab"},"deep":[{}]}`,
		`{"child":{"name":"ab"},"deep":[{"name":"ab"}],"map":{"x":{"name":"ab"}}}`,
		`{"child":{"name":"ab"},"map":{"x":{}}}`,
		`{"child":{"name":"ab"},"next":{}}`,
		`{"child":{"name":"ab"},"next":{"child":{"name":"ab"}}}`,
	} {
		var body validationTree
		if err := json.Unmarshal([]byte(data), &body); err != nil {
			t.Fatal(err)
		}
		runtimeValid := validateTags(context.Background(), validator, body) == nil
		if got := request.VisitJSON(jsonValue(t, []byte(data))) == nil; got != runtimeValid {
			t.Errorf("%s runtime=%t schema=%t", data, runtimeValid, got)
		}
		if err := response.VisitJSON(jsonValue(t, []byte(data))); err != nil {
			t.Fatalf("response constrained: %s: %v", data, err)
		}
	}
	if ref := request.Properties["next"].Value.AnyOf[0].Ref; ref != "#/components/schemas/validationTreeInput" {
		t.Fatalf("recursive request ref: %s", ref)
	}
}

func TestValidationUnsupportedAndPartialSchemas(t *testing.T) {
	for _, tc := range []struct {
		typ                reflect.Type
		tag, jsonTag       string
		unsupported        []string
		accepted, rejected string
	}{
		{reflect.TypeFor[string](), "min=2,custom,max=4", "value", []string{"custom"}, `{"value":"ab"}`, `{"value":"a"}`},
		{reflect.TypeFor[string](), "min=2|eq=0,max=4", "value", []string{"min=2|eq=0"}, `{"value":"a"}`, `{"value":"abcde"}`},
		{reflect.TypeFor[string](), "iscolor,min=2", "value", []string{"iscolor"}, `{"value":"ab"}`, `{"value":"a"}`},
		{reflect.TypeFor[string](), "required_if=Other yes,min=2", "value", []string{"required_if=Other yes"}, `{"value":"ab"}`, `{"value":"a"}`},
		{reflect.TypeFor[map[string]string](), "min=1,dive,keys,min=2,endkeys,min=2", "value", []string{"keys,min=2,endkeys"}, `{"value":{"x":"ab"}}`, `{"value":{"x":"a"}}`},
		{reflect.TypeFor[[]byte](), "required,min=2,dive,min=1", "value", []string{"min=2", "dive,min=1"}, `{"value":"AA=="}`, `{"value":null}`},
		{reflect.TypeFor[int](), "required,min=2", "value,string", []string{"required", "min=2"}, `{"value":"0"}`, `{}`},
		{reflect.TypeFor[validationChild](), "structonly", "value", []string{"structonly"}, `{"value":{}}`, ""},
		{reflect.TypeFor[validationChild](), "nostructlevel", "value", []string{"nostructlevel"}, `{"value":{}}`, ""},
		{reflect.TypeFor[validationChild](), "omitempty", "value", []string{"omitempty"}, `{"value":{}}`, ""},
		{reflect.TypeFor[validationChild](), "required", "value", []string{"required"}, `{"value":{"name":"ab"}}`, `{}`},
		{reflect.TypeFor[[2]int](), "required", "value", []string{"required"}, `{"value":[0,0]}`, `{}`},
		{reflect.TypeFor[string](), "min=1,omitzero,min=3", "value", []string{"omitzero,min=3"}, `{"value":"a"}`, `{"value":""}`},
		{reflect.TypeFor[string](), "required,min=2,custom", "value", []string{"custom"}, `{"value":"ab"}`, `{}`},
		{reflect.TypeFor[string](), "omitnil,required,custom", "value", []string{"custom"}, `{"value":"ab"}`, `{}`},
		{reflect.TypeFor[string](), "omitempty,required,custom", "value", []string{"custom"}, `{}`, ""},
		{reflect.TypeFor[float64](), "min=NaN", "value", []string{"min=NaN"}, `{"value":1}`, ""},
		{reflect.TypeFor[float32](), "gte=0.1,lte=0.2", "value", []string{"gte=0.1", "lte=0.2"}, `{"value":0.1}`, ""},
		{reflect.TypeFor[float32](), "len=0.1", "value", []string{"len=0.1"}, `{"value":0.1}`, ""},
		{reflect.TypeFor[int64](), "max=9007199254740993", "value", []string{"max=9007199254740993"}, `{"value":1}`, ""},
		{reflect.TypeFor[int64](), "oneof=9007199254740992", "value", []string{"oneof=9007199254740992"}, `{"value":1}`, ""},
		{reflect.TypeFor[*int](), "isdefault,min=2", "value", []string{"isdefault,min=2"}, `{}`, ""},
		{reflect.TypeFor[*int](), "isdefault,required,min=2", "value", []string{"isdefault,required,min=2"}, `{}`, ""},
		{reflect.TypeFor[uint64](), "oneof=18446744073709551615", "value", []string{"oneof=18446744073709551615"}, `{"value":1}`, ""},
		{reflect.TypeFor[int](), "oneof=01 2", "value", []string{"oneof=01 2"}, `{"value":1}`, ""},
		{reflect.TypeFor[int8](), "oneof=128 2", "value", []string{"oneof=128 2"}, `{"value":1}`, ""},
	} {
		t.Run(tc.tag+tc.typ.String(), func(t *testing.T) {
			s := validationTestSchema(t, validationModel(tc.typ, tc.tag, tc.jsonTag))
			property := s.Properties["value"].Value
			want := map[string]any{"tags": tc.tag, "unsupported": tc.unsupported}
			if !reflect.DeepEqual(property.Extensions[validationExtension], want) {
				t.Fatalf("extension: %#v want %#v", property.Extensions, want)
			}
			if err := s.VisitJSON(jsonValue(t, []byte(tc.accepted))); err != nil {
				t.Errorf("partial schema rejected %s: %v", tc.accepted, err)
			}
			if tc.rejected != "" && s.VisitJSON(jsonValue(t, []byte(tc.rejected))) == nil {
				t.Errorf("partial schema accepted %s", tc.rejected)
			}
			if !unconditionalRequired(tc.typ, mustParseValidation(t, tc.tag)) && len(s.Required) != 0 {
				t.Errorf("uncertain presence inferred: %v", s.Required)
			}
		})
	}
}

func mustParseValidation(t *testing.T, tag string) []validationRule {
	t.Helper()
	rules, err := parseValidation(tag)
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

func TestValidationMalformedTagsHaveContext(t *testing.T) {
	for _, tc := range []struct {
		typ reflect.Type
		tag string
	}{
		{reflect.TypeFor[string](), "min=oops"}, {reflect.TypeFor[string](), "max="},
		{reflect.TypeFor[int](), "gte"}, {reflect.TypeFor[int](), "lt=1.2"},
		{reflect.TypeFor[float64](), "len=oops"}, {reflect.TypeFor[string](), "required=yes"},
		{reflect.TypeFor[string](), "min=oops|email"}, {reflect.TypeFor[string](), "oneof="},
		{reflect.TypeFor[string](), "min=1,,max=2"}, {reflect.TypeFor[string](), "email|"},
		{reflect.TypeFor[string](), "dive,min=1"}, {reflect.TypeFor[string](), "omitzero,dive"},
		{reflect.TypeFor[string](), "omitzero,min=bad"},
		{reflect.TypeFor[[]int](), "dive,keys,required,endkeys"},
		{reflect.TypeFor[map[string]int](), "dive,keys,required"},
		{reflect.TypeFor[map[string]int](), "keys,required,endkeys"},
		{reflect.TypeFor[map[string]int](), "dive,endkeys"},
		{reflect.TypeFor[[]int](), "dive|required"},
		{reflect.TypeFor[string](), "omitempty|min=2"},
		{reflect.TypeFor[*string](), "required|omitnil"},
		{reflect.TypeFor[map[string]int](), "keys|required"},
		{reflect.TypeFor[map[string]int](), "required|endkeys"},
		{reflect.TypeFor[validationChild](), "structonly|required"},
		{reflect.TypeFor[validationChild](), "required|nostructlevel"},
		{reflect.TypeFor[string](), "omitzero|required"},
		{reflect.TypeFor[string](), "-,required"},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			typ := reflect.StructOf([]reflect.StructField{{Name: "Outer", Type: validationModel(tc.typ, tc.tag, "value")}})
			r := newSchemaRegistry(make(openapi3.Schemas))
			_, err := r.schemaIn(typ, true)
			if err == nil || !strings.Contains(err.Error(), "field Outer: field Value: validation rule") {
				t.Fatalf("context: %v", err)
			}
		})
	}
	type malformed struct {
		Value int `validate:"min=bad"`
	}
	for _, described := range []bool{false, true} {
		api := NewApi(http.NewServeMux())
		if described {
			api.Route("POST /broken").DescribeBody(malformed{}).HandlerFunc(func(http.ResponseWriter, *http.Request) {})
		} else {
			api.Route("POST /broken").Body(malformed{}).HandlerFunc(func(http.ResponseWriter, *http.Request, malformed) {})
		}
		_, err := api.Generate()
		if err == nil || !strings.Contains(err.Error(), `route "POST /broken"`) || !strings.Contains(err.Error(), `field Value: validation rule "min=bad"`) {
			t.Fatalf("route context: %v", err)
		}
	}
}

func TestValidationFloat32WireBoundary(t *testing.T) {
	type body struct {
		Value float32 `json:"value" validate:"gte=0.1,lte=0.2"`
	}
	mux := http.NewServeMux()
	api := NewApi(mux)
	api.Route("POST /float32").Body(body{}).Response(200, body{}).
		HandlerFunc(func(_ *http.Request, b body) (body, error) { return b, nil })
	doc := generatedDocument(t, api)
	schema := doc.Paths.Value("/float32").Post.RequestBody.Value.Content["application/json"].Schema.Value
	for _, value := range []string{`{"value":0.1}`, `{"value":0.2}`} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/float32", strings.NewReader(value))
		r.Header.Set("Content-Type", "application/json")
		mux.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: status=%d body=%s", value, w.Code, w.Body.String())
		}
		if err := schema.VisitJSON(jsonValue(t, []byte(value))); err != nil {
			t.Fatalf("schema rejects valid wire boundary %s: %v", value, err)
		}
	}
}

type validationPresenceA struct {
	B validationPresenceB `json:"b,omitzero"`
}
type validationPresenceB struct {
	A    *validationPresenceA `json:"a"`
	Name string               `json:"name,omitempty" validate:"min=2"`
}

func TestValidationPresenceAfterRecursiveDiscovery(t *testing.T) {
	build := func(reverse bool) []byte {
		api := NewApi(http.NewServeMux())
		a := func() {
			api.Route("POST /a").DescribeBody(validationPresenceA{}).HandlerFunc(func(http.ResponseWriter, *http.Request) {})
		}
		b := func() {
			api.Route("POST /b").DescribeBody(validationPresenceB{}).HandlerFunc(func(http.ResponseWriter, *http.Request) {})
		}
		if reverse {
			b()
			a()
		} else {
			a()
			b()
		}
		data, err := api.Generate()
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	a, b := build(false), build(true)
	if !bytes.Equal(a, b) {
		t.Fatal("recursive discovery changes presence")
	}
	doc, err := openapi3.NewLoader().LoadFromData(a)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string][]string{"validationPresenceAInput": {"b"}, "validationPresenceBInput": {"name"}} {
		if got := doc.Components.Schemas[name].Value.Required; !reflect.DeepEqual(got, want) {
			t.Errorf("%s required=%v want=%v", name, got, want)
		}
	}
}

func TestValidationWireEncodingAndAnnotations(t *testing.T) {
	type wire struct {
		Plain   int      `json:"plain,string"`
		Quoted  string   `json:"quoted,string"`
		Pointer *int     `json:"pointer,string" validate:"required,min=2"`
		Bytes   []byte   `json:"bytes" validate:"omitempty,len=2"`
		Email   string   `json:"email" validate:"email"`
		UUID    string   `json:"uuid" validate:"uuid"`
		Skipped chan int `json:"-" validate:"min=bad"`
		hidden  string   `validate:"min=bad"`
	}
	api := NewApi(http.NewServeMux())
	api.Route("POST /wire").DescribeBody(wire{}).DescribeResponse(200, wire{}).HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	doc := generatedDocument(t, api)
	s := doc.Components.Schemas["wireInput"].Value
	if !reflect.DeepEqual(s.Required, []string{"email", "pointer", "uuid"}) {
		t.Fatalf("presence on wire values: %v", s.Required)
	}
	for name, tag := range map[string]string{"pointer": "required,min=2", "bytes": "omitempty,len=2"} {
		ext, ok := s.Properties[name].Value.Extensions[validationExtension].(map[string]any)
		if !ok || ext["tags"] != tag {
			t.Errorf("wire extension %s: %#v", name, ext)
		}
	}
	if err := s.VisitJSON(map[string]any{"pointer": "0", "email": "anything", "uuid": "anything", "bytes": "AA=="}); err != nil {
		t.Fatal(err)
	}
	if err := doc.Components.Schemas["wire"].Value.VisitJSON(map[string]any{}); err != nil {
		t.Fatal(err)
	}
}

type validationAlias string
type validationReuse struct {
	Short validationAlias `json:"short" validate:"max=2"`
	Long  validationAlias `json:"long" validate:"min=3"`
	Email string          `json:"email" validate:"omitempty,email"`
	UUID  *string         `json:"uuid" validate:"required,uuid"`
}

func TestValidationDescribeBodyAndSharedTypes(t *testing.T) {
	mux := http.NewServeMux()
	api := NewApi(mux)
	api.Route("POST /described").DescribeBody(validationReuse{}).
		DescribeQuery(struct {
			Limit validationAlias `query:"limit" validate:"required,min=8"`
		}{}).
		DescribeResponse(200, validationReuse{}).
		HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) })
	api.Route("POST /typed").Body(validationReuse{}).Validator(func(*http.Request, validationReuse) error { return Invalid("short", "callback") }).
		Response(200, validationReuse{}).HandlerFunc(func(_ *http.Request, b validationReuse) (validationReuse, error) { return b, nil })
	doc := generatedDocument(t, api)
	described := doc.Paths.Value("/described").Post.RequestBody.Value.Content["application/json"].Schema
	typed := doc.Paths.Value("/typed").Post.RequestBody.Value.Content["application/json"].Schema
	if described.Ref != typed.Ref || described.Ref != "#/components/schemas/validationReuseInput" {
		t.Fatalf("input refs: %s %s", described.Ref, typed.Ref)
	}
	for _, input := range []string{`{"short":"ab","long":"abc","uuid":"anything"}`, `{"short":"ab","long":"abc","uuid":"anything","email":""}`} {
		if err := typed.Value.VisitJSON(jsonValue(t, []byte(input))); err != nil {
			t.Fatalf("format annotations/callbacks should not constrain shape: %v", err)
		}
	}
	for _, input := range []string{`{"short":"abc","long":"abc","uuid":"anything"}`, `{"short":"a","long":"ab","uuid":"anything"}`, `{"short":"a","long":"abc"}`} {
		if typed.Value.VisitJSON(jsonValue(t, []byte(input))) == nil {
			t.Errorf("accepted %s", input)
		}
	}
	ordinary := doc.Components.Schemas["validationReuse"].Value
	if err := ordinary.VisitJSON(map[string]any{}); err != nil {
		t.Fatalf("response was constrained: %v", err)
	}
	if len(doc.Components.Schemas["validationAliasInput"].Value.AllOf) != 0 {
		t.Fatal("shared scalar mutated")
	}
	parameter := doc.Paths.Value("/described").Post.Parameters[0].Value
	if parameter.Required || parameter.Schema.Ref != "#/components/schemas/validationAlias" || parameter.Schema.Value.VisitJSON("") != nil {
		t.Fatal("described parameter received body constraints")
	}
	var found []string
	var formats func(*openapi3.SchemaRef)
	formats = func(ref *openapi3.SchemaRef) {
		if ref.Value.Format != "" {
			found = append(found, ref.Value.Format)
		}
		for _, child := range ref.Value.AllOf {
			formats(child)
		}
		for _, child := range ref.Value.AnyOf {
			formats(child)
		}
	}
	formats(typed.Value.Properties["email"])
	formats(typed.Value.Properties["uuid"])
	if !reflect.DeepEqual(found, []string{"email", "uuid"}) {
		t.Fatalf("formats: %v", found)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/described", strings.NewReader(`{}`)))
	if w.Code != 202 {
		t.Fatalf("described handler validation changed: %d", w.Code)
	}
}

type collision struct {
	Value string `validate:"required"`
}
type collisionInput struct{ Value string }

func TestValidationComponentNamesDeterministic(t *testing.T) {
	build := func(reverse bool) []byte {
		api := NewApi(http.NewServeMux())
		registrations := []func(){
			func() {
				api.Route("POST /a").DescribeBody(first.Model{}).HandlerFunc(func(http.ResponseWriter, *http.Request) {})
			},
			func() {
				api.Route("POST /b").DescribeBody(second.Model{}).HandlerFunc(func(http.ResponseWriter, *http.Request) {})
			},
			func() {
				api.Route("POST /c").DescribeBody(collision{}).DescribeResponse(200, collisionInput{}).HandlerFunc(func(http.ResponseWriter, *http.Request) {})
			},
		}
		for i := range registrations {
			if reverse {
				registrations[len(registrations)-i-1]()
			} else {
				registrations[i]()
			}
		}
		data, err := api.Generate()
		if err != nil {
			t.Fatal(err)
		}
		again, err := api.Generate()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, again) {
			t.Fatal("repeated generation differs")
		}
		return data
	}
	a, b := build(false), build(true)
	if !bytes.Equal(a, b) {
		t.Fatal("registration order changes document")
	}
	doc, err := openapi3.NewLoader().LoadFromData(a)
	if err != nil {
		t.Fatal(err)
	}
	refs := make(map[string]bool)
	for _, path := range []string{"/a", "/b", "/c"} {
		ref := doc.Paths.Value(path).Post.RequestBody.Value.Content["application/json"].Schema.Ref
		if !strings.Contains(ref, "Input_") {
			t.Fatalf("collision name: %s", ref)
		}
		refs[ref] = true
	}
	ref := doc.Paths.Value("/c").Post.Responses.Status(200).Value.Content["application/json"].Schema.Ref
	if refs[ref] {
		t.Fatal("input suffix collided with ordinary model")
	}
}
