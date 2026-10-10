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
	"time"

	"github.com/getkin/kin-openapi/openapi3"
)

type timestampAlias = time.Time

func TestTimestampTopLevelBodyRejected(t *testing.T) {
	api := NewApi(http.NewServeMux())
	route := api.Route("POST /time")
	requireRoutePanic(t, "POST /time", "JSON object model", func() { route.Body(timestampAlias{}) })
	// A failed selection must leave the builder usable for an object model.
	route.Body(struct{ At time.Time }{}).HandlerFunc(func(http.ResponseWriter, *http.Request, struct{ At time.Time }) {})
	generatedDocument(t, api)
}

type timestampModel struct {
	At       time.Time             `json:"at"`
	Optional *time.Time            `json:"optional,omitempty"`
	Alias    timestampAlias        `json:"alias"`
	Times    []time.Time           `json:"times"`
	Array    [1]time.Time          `json:"array"`
	Lookup   map[string]*time.Time `json:"lookup"`
	Nested   struct {
		At time.Time `json:"at,string,omitzero"`
	} `json:"nested"`
}

func TestTimestampSchemasAndRoundTrip(t *testing.T) {
	mux := http.NewServeMux()
	api := NewApi(mux)
	api.Route("POST /times").Body(timestampModel{}).Response(200, timestampModel{}).
		HandlerFunc(func(_ *http.Request, body timestampModel) (timestampModel, error) { return body, nil })
	api.Route("POST /described").DescribeBody(timestampModel{}).DescribeResponse(200, timestampModel{}).
		HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	doc := generatedDocument(t, api)
	op := doc.Paths.Value("/times").Post
	input := op.RequestBody.Value.Content["application/json"].Schema
	output := op.Responses.Status(200).Value.Content["application/json"].Schema
	described := doc.Paths.Value("/described").Post
	if input.Ref != described.RequestBody.Value.Content["application/json"].Schema.Ref ||
		output.Ref != described.Responses.Status(200).Value.Content["application/json"].Schema.Ref {
		t.Fatal("described and typed timestamp models do not share schemas")
	}
	for _, model := range []*openapi3.Schema{input.Value, output.Value} {
		for _, ref := range []*openapi3.SchemaRef{
			model.Properties["at"], model.Properties["alias"],
			model.Properties["optional"].Value.AnyOf[0],
			model.Properties["times"].Value.Items,
			model.Properties["array"].Value.Items,
			model.Properties["lookup"].Value.AdditionalProperties.Schema.Value.AnyOf[0],
			model.Properties["nested"].Value.Properties["at"],
		} {
			if !ref.Value.Type.Is("string") || ref.Value.Format != "date-time" || ref.Value.Nullable {
				t.Fatalf("timestamp schema: %#v", ref.Value)
			}
		}
		if model.Properties["optional"].Value.VisitJSON(nil) != nil {
			t.Fatal("timestamp pointer must accept null")
		}
		if model.Properties["at"].Value.VisitJSON(123.0) == nil {
			t.Fatal("timestamp schema accepted a numeric value")
		}
	}
	at := time.Date(2026, 10, 9, 12, 34, 56, 123456789, time.FixedZone("offset", -7*60*60))
	full := timestampModel{At: at, Optional: &at, Alias: at, Times: []time.Time{at}, Array: [1]time.Time{at}, Lookup: map[string]*time.Time{"at": &at, "nil": nil}}
	full.Nested.At = at
	for _, value := range []timestampModel{{}, full} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := input.Value.VisitJSON(jsonValue(t, data)); err != nil {
			t.Fatalf("request schema rejected %s: %v", data, err)
		}
		r := httptest.NewRequest("POST", "/times", bytes.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) {
			t.Fatalf("timestamp round trip: status=%d body=%s want=%s", w.Code, w.Body.Bytes(), data)
		}
		if err := output.Value.VisitJSON(jsonValue(t, w.Body.Bytes())); err != nil {
			t.Fatalf("response violates schema: %v", err)
		}
	}
}

type timestampValidationModel struct {
	Start time.Time  `json:"start" validate:"required"`
	End   time.Time  `json:"end" validate:"gtfield=Start"`
	Next  *time.Time `json:"next" validate:"omitempty,gt"`
}

func TestTimestampBodyValidation(t *testing.T) {
	mux := http.NewServeMux()
	api := NewApi(mux)
	called := false
	api.Route("POST /times").Body(timestampValidationModel{}).Response(200, timestampValidationModel{}).
		HandlerFunc(func(_ *http.Request, body timestampValidationModel) (timestampValidationModel, error) {
			called = true
			return body, nil
		})
	doc := generatedDocument(t, api)
	op := doc.Paths.Value("/times").Post
	input := op.RequestBody.Value.Content["application/json"].Schema.Value
	if !reflect.DeepEqual(input.Required, []string{"start"}) {
		t.Fatalf("required timestamp fields: %v", input.Required)
	}
	for field, tag := range map[string]string{"start": "required", "end": "gtfield=Start", "next": "omitempty,gt"} {
		ext, ok := input.Properties[field].Value.Extensions[validationExtension].(map[string]any)
		if !ok || ext["tags"] != tag {
			t.Fatalf("missing timestamp validation annotation for %s: %#v", field, ext)
		}
		if op.Responses.Status(200).Value.Content["application/json"].Schema.Value.Properties[field].Value.Extensions[validationExtension] != nil {
			t.Fatalf("request validation constrained response field %s", field)
		}
	}
	for _, tc := range []struct {
		name, body, field, code string
		status                  int
	}{
		{"valid", `{"start":"2000-01-01T00:00:00Z","end":"2000-01-02T00:00:00Z","next":"9000-01-01T00:00:00Z"}`, "", "", 200},
		{"optional null", `{"start":"2000-01-01T00:00:00Z","end":"2000-01-02T00:00:00Z","next":null}`, "", "", 200},
		{"missing start", `{"end":"2000-01-02T00:00:00Z"}`, "start", "required", 422},
		{"null start", `{"start":null,"end":"2000-01-02T00:00:00Z"}`, "start", "required", 422},
		{"zero start", `{"start":"0001-01-01T00:00:00Z","end":"2000-01-02T00:00:00Z"}`, "start", "required", 422},
		{"end before start", `{"start":"2000-01-02T00:00:00Z","end":"2000-01-01T00:00:00Z"}`, "end", "gtfield", 422},
		{"next in past", `{"start":"2000-01-01T00:00:00Z","end":"2000-01-02T00:00:00Z","next":"2000-01-01T00:00:00Z"}`, "next", "gt", 422},
		{"invalid timestamp", `{"start":"invalid"}`, "", "", 400},
		{"date only", `{"start":"2026-10-09"}`, "", "", 400},
		{"numeric timestamp", `{"start":123}`, "", "", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/times", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			mux.ServeHTTP(w, r)
			if w.Code != tc.status || called != (tc.status == 200) {
				t.Fatalf("status=%d handler called=%t body=%s", w.Code, called, w.Body.String())
			}
			if tc.status == 422 {
				var response ValidationErrorResponse
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if len(response.Errors) != 1 || response.Errors[0].Field != tc.field || response.Errors[0].Code != tc.code {
					t.Fatalf("validation errors: %#v", response.Errors)
				}
			}
			if tc.status == 200 && input.VisitJSON(jsonValue(t, []byte(tc.body))) != nil {
				t.Fatal("schema rejected valid timestamp request")
			}
			if err := op.Responses.Status(tc.status).Value.Content["application/json"].Schema.Value.VisitJSON(jsonValue(t, w.Body.Bytes())); err != nil {
				t.Fatalf("response violates schema: %v", err)
			}
		})
	}
}

func TestTimestampComparisonValidation(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeFor[time.Time](), reflect.TypeFor[*time.Time]()} {
		for _, tag := range []string{"gt", "gte", "lt", "lte", "min", "max", "gt=ignored"} {
			t.Run(typ.String()+"/"+tag, func(t *testing.T) {
				model := validationModel(typ, tag, "value")
				schema := validationTestSchema(t, model)
				validator := newTagValidator()
				futureValid := tag == "gt" || tag == "gte" || tag == "min" || tag == "gt=ignored"
				for _, tc := range []struct {
					value string
					valid bool
				}{
					{`{"value":"2000-01-01T00:00:00Z"}`, !futureValid},
					{`{"value":"9000-01-01T00:00:00Z"}`, futureValid},
				} {
					body := reflect.New(model)
					if err := json.Unmarshal([]byte(tc.value), body.Interface()); err != nil {
						t.Fatal(err)
					}
					if got := validateTags(context.Background(), validator, body.Interface()) == nil; got != tc.valid {
						t.Fatalf("%s: runtime valid=%t want=%t", tc.value, got, tc.valid)
					}
					// Temporal constraints remain runtime checks; generated schemas
					// describe the timestamp shape without baking in today's date.
					if err := schema.VisitJSON(jsonValue(t, []byte(tc.value))); err != nil {
						t.Fatalf("partial schema rejected timestamp: %v", err)
					}
				}
			})
		}
	}
}
