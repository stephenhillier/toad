package toad

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

type adoptionModel struct {
	Name string `json:"name"`
}
type adoptionID int

func TestDescriptionsPreserveHandlerAndGenerate(t *testing.T) {
	mux := http.NewServeMux()
	api := NewApi(mux).BodyLimit(1)
	var received string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bytes, _ := io.ReadAll(r.Body)
		received = string(bytes)
		if r.PathValue("id") != "abc" || r.URL.RawQuery != "limit=invalid" {
			t.Error("request changed")
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(418)
		io.WriteString(w, "existing error\n")
	})
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Middleware", "yes")
		handler.ServeHTTP(w, r)
	})
	b := api.Route("POST /legacy/{id}").
		DescribeBody(adoptionModel{Name: "not a default"}).
		DescribeQuery(struct {
			Limit   int      `query:"limit"`
			Search  string   `query:"q"`
			Ignored chan int `query:"-"`
		}{}).
		DescribeParams(struct {
			ID adoptionID `path:"id"`
		}{}).
		DescribeResponse(201, adoptionModel{}).
		DescribeResponse(400, adoptionModel{}).
		DescribeResponse(204, nil).Title("Legacy").Description("Unchanged handler")
	b.Handler(wrapped)
	raw := "not JSON and larger than the limit"
	req := httptest.NewRequest("POST", "/legacy/abc?limit=invalid", strings.NewReader(raw))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if received != raw || w.Code != 418 || w.Body.String() != "existing error\n" || w.Header().Get("X-Middleware") != "yes" || w.Header().Get("Content-Type") != "text/plain" {
		t.Fatalf("handler changed: %q %+v", received, w)
	}
	data, err := api.Generate()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	op := doc.Paths.Value("/legacy/{id}").Post
	if op.Summary != "Legacy" || op.Description != "Unchanged handler" {
		t.Fatal("metadata lost")
	}
	body := op.RequestBody.Value
	if !body.Required || body.Content["application/json"].Schema.Ref == "" {
		t.Fatal("body options/schema lost")
	}
	if op.Responses.Len() != 3 || op.Responses.Default() != nil || op.Responses.Status(500) != nil {
		t.Fatal("invented responses")
	}
	if len(op.Responses.Status(204).Value.Content) != 0 || op.Responses.Status(400).Value.Content["application/json"] == nil {
		t.Fatal("response options lost")
	}
	if body.Content["application/json"].Schema.Ref != op.Responses.Status(201).Value.Content["application/json"].Schema.Ref+"Input" {
		t.Fatal("request and response contexts lost")
	}
	if len(op.Parameters) != 3 {
		t.Fatal("duplicated parameters")
	}
	for _, ref := range op.Parameters {
		p := ref.Value
		switch p.Name {
		case "id":
			if p.In != "path" || !p.Required || p.Style != "simple" || *p.Explode || !p.Schema.Value.Type.Is("integer") {
				t.Fatalf("path: %+v", p)
			}
		case "limit":
			if p.Required || p.Style != "form" || !*p.Explode {
				t.Fatalf("query: %+v", p)
			}
		case "q":
			if p.Required {
				t.Fatal("optional query required")
			}
		default:
			t.Fatal(p.Name)
		}
	}
	before := string(data)
	mustDescriptionPanic(t, func() { b.DescribeResponse(202, nil) })
	mustDescriptionPanic(t, func() { b.Title("changed") })
	mustDescriptionPanic(t, func() { b.Handler(handler) })
	after, _ := api.Generate()
	if before != string(after) {
		t.Fatal("finalized metadata mutated")
	}
}

func mustDescriptionPanic(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Error("expected setup panic")
		}
	}()
	f()
}

func TestDescriptionConfiguration(t *testing.T) {
	annotations := []func(*RouteBuilder){
		func(b *RouteBuilder) { b.DescribeBody(adoptionModel{}) },
		func(b *RouteBuilder) { b.DescribeQuery(struct{ Q string }{}) },
		func(b *RouteBuilder) {
			b.DescribeParams(struct {
				ID int `path:"id"`
			}{})
		},
		func(b *RouteBuilder) { b.DescribeResponse(200, adoptionModel{}) },
		func(b *RouteBuilder) { b.DescribeResponse(204, nil) },
	}
	selectors := []func(*RouteBuilder){
		func(b *RouteBuilder) { b.Body(adoptionModel{}) },
		func(b *RouteBuilder) { b.Response(200, adoptionModel{}) },
	}
	for _, describe := range annotations {
		for _, selectMode := range selectors {
			for _, reverse := range []bool{false, true} {
				b := NewApi(http.NewServeMux()).Route("POST /users/{id}")
				mustDescriptionPanic(t, func() {
					if reverse {
						selectMode(b)
						describe(b)
					} else {
						describe(b)
						selectMode(b)
					}
				})
			}
		}
	}
	invalid := []func(*RouteBuilder){
		func(b *RouteBuilder) { b.DescribeBody("").DescribeBody("") },
		func(b *RouteBuilder) { b.DescribeQuery(struct{}{}).DescribeQuery(struct{}{}) },
		func(b *RouteBuilder) {
			b.DescribeParams(struct {
				ID int `path:"id"`
			}{}).DescribeParams(struct {
				ID int `path:"id"`
			}{})
		},
		func(b *RouteBuilder) { b.DescribeResponse(200, "").DescribeResponse(200, nil) },
		func(b *RouteBuilder) { b.DescribeResponse(200, nil).DescribeResponse(200, "") },
		func(b *RouteBuilder) { b.DescribeParams(struct{ Other string }{}) },
		func(b *RouteBuilder) { b.DescribeParams(struct{}{}) },
		func(b *RouteBuilder) {
			b.DescribeQuery(struct {
				A string `query:"x"`
				B int    `query:"x"`
			}{})
		},
		func(b *RouteBuilder) {
			b.DescribeQuery(struct {
				A string `query:"x,optional"`
			}{})
		},
		func(b *RouteBuilder) { b.DescribeQuery("") },
		func(b *RouteBuilder) { b.DescribeResponse(204, "") },
		func(b *RouteBuilder) { b.DescribeResponse(600, "") },
		func(b *RouteBuilder) { b.DescribeResponse(99, nil) },
		func(b *RouteBuilder) { b.Handler(nil) },
		func(b *RouteBuilder) { var h http.HandlerFunc; b.Handler(h) },
	}
	for i, f := range invalid {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			api := NewApi(http.NewServeMux())
			b := api.Route("POST /users/{id}")
			mustDescriptionPanic(t, func() { f(b) })
			if len(api.routes) != 0 {
				t.Fatal("failed configuration published")
			}
		})
	}
}

func TestDescriptionDefaultsAndUnsupportedSchemas(t *testing.T) {
	api := NewApi(http.NewServeMux())
	api.Route("POST /default").DescribeBody(adoptionModel{}).HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	data, err := api.Generate()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	op := doc.Paths.Value("/default").Post
	if !op.RequestBody.Value.Required || op.RequestBody.Value.Content["application/json"] == nil || op.Responses.Len() != 1 || op.Responses.Default() == nil {
		t.Fatal("incorrect defaults")
	}
	for _, configure := range []func(*RouteBuilder){
		func(b *RouteBuilder) { b.DescribeBody(make(chan int)) },
		func(b *RouteBuilder) { b.DescribeResponse(200, make(chan int)) },
		func(b *RouteBuilder) { b.DescribeQuery(struct{ Q []string }{}) },
	} {
		api := NewApi(http.NewServeMux())
		b := api.Route("GET /invalid")
		configure(b)
		b.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
		if _, err := api.Generate(); err == nil || !strings.Contains(err.Error(), "GET /invalid") {
			t.Fatalf("missing contextual error: %v", err)
		}
	}
	// API stays confined to ordinary builders; reverse chains fail at compile time.
	for _, typ := range []reflect.Type{reflect.TypeFor[*BodyBuilder[adoptionModel]](), reflect.TypeFor[*ResponseBuilder[adoptionModel]]()} {
		if _, ok := typ.MethodByName("DescribeResponse"); ok {
			t.Fatal("descriptions exposed on typed/managed builder")
		}
	}
}

func TestDescribeResponseNilModel(t *testing.T) {
	api := NewApi(http.NewServeMux())
	api.Route("GET /nil").
		DescribeResponse(200, (*adoptionModel)(nil)).
		DescribeResponse(204, nil).
		DescribeResponse(304, nil).
		HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	data, err := api.Generate()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	responses := doc.Paths.Value("/nil").Get.Responses
	nullableModel := responses.Status(200).Value.Content["application/json"].Schema.Value
	if err := nullableModel.VisitJSON(nil); err != nil {
		t.Fatalf("typed nil lost nullable schema: %v", err)
	}
	for _, status := range []int{204, 304} {
		if len(responses.Status(status).Value.Content) != 0 {
			t.Fatalf("status %d has content", status)
		}
	}
	model := doc.Components.Schemas["adoptionModel"].Value
	if len(model.Required) != 0 {
		t.Fatal("model fields marked required")
	}
}
