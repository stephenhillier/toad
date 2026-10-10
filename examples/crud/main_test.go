package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stephenhillier/toad"
)

func TestCRUDWorkflow(t *testing.T) {
	mux, _ := newAPI()
	request := func(method, path, body string, status int, want string) {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != status || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("%s %s: got %d %s; want %d containing %q", method, path, w.Code, w.Body.String(), status, want)
		}
	}
	project := `{"name":"Launch","owner":{"name":"Sam","email":"sam@example.com"},"labels":["demo"],"metadata":{"team":"platform"}}`
	task := `{"project_id":1,"title":"Write docs","priority":"high","estimate_hours":2.5,"assignee":{"name":"Sam","email":"sam@example.com"},"checklist":[{"text":"Review","done":false}]}`
	request("GET", "/projects", "", 200, `"items":[]`)
	request("GET", "/tasks", "", 200, `"items":[]`)
	request("POST", "/projects", project, 201, `"id":1`)
	request("POST", "/projects", project, 409, `"detail":"Project name already exists"`)
	request("GET", "/projects/1", "", 200, `"metadata":{"team":"platform"}`)
	request("PUT", "/projects/1", `{"name":"Release","owner":{"name":"Sam","email":"sam@example.com"}}`, 200, `"metadata":{}`)
	request("GET", "/projects", "", 200, `"total":1`)
	request("POST", "/tasks", task, 201, `"checklist":[{"text":"Review","done":false}]`)
	request("DELETE", "/projects/1", "", 409, "Delete this project's tasks")
	request("PATCH", "/tasks/1", `{"completed":true}`, 200, `"completed":true`)
	request("PATCH", "/tasks/1", `{"completed":false}`, 200, `"completed":false`)
	request("PATCH", "/tasks/1", `{}`, 422, `"field":"completed","code":"required"`)
	request("PATCH", "/tasks/1", `{"completed":null}`, 422, `"field":"completed","code":"required"`)
	request("PUT", "/tasks/1", `{"project_id":1,"title":"Publish","priority":"normal"}`, 200, `"assignee":null`)
	request("GET", "/tasks/1", "", 200, `"title":"Publish"`)
	request("GET", "/tasks", "", 200, `"total":1`)
	request("PUT", "/tasks/1", `{"project_id":99,"title":"Publish","priority":"normal"}`, 404, "Project not found")
	request("GET", "/tasks/1", "", 200, `"project_id":1`)
	request("DELETE", "/tasks/1", "", 200, `"deleted":true`)
	request("DELETE", "/projects/1", "", 200, `"deleted":true`)
	request("GET", "/tasks/1", "", 404, "Task not found")
	request("GET", "/projects/1", "", 404, "Project not found")
	request("GET", "/projects/invalid", "", 400, `"detail":"ID must be a positive integer"`)
	request("POST", "/projects", `{}`, 422, `"field":"name","code":"required"`)
	request("POST", "/tasks", `{"project_id":1,"title":"Bad","priority":"urgent"}`, 422, `"field":"priority","code":"oneof"`)
	request("POST", "/projects", `{`, 400, "Bad Request")
	request("POST", "/projects", `{"padding":"`+strings.Repeat("x", 16<<10)+`"}`, 413, "Request Entity Too Large")
	request("POST", "/projects", "", 415, "Unsupported Media Type")
}

func TestBodyValidation(t *testing.T) {
	project := `{"name":"Launch","owner":{"name":"Sam","email":"sam@example.com"}}`
	task := `{"project_id":1,"title":"Write docs","priority":"normal"}`
	for _, tc := range []struct {
		name, path, body, field, code, message string
	}{
		{"project name required", "/projects", `{"owner":{"name":"Sam","email":"sam@example.com"}}`, "name", "required", ""},
		{"project name blank", "/projects", `{"name":" \t","owner":{"name":"Sam","email":"sam@example.com"}}`, "name", "invalid", "Name must not be blank"},
		{"owner name required", "/projects", `{"name":"Launch","owner":{"email":"sam@example.com"}}`, "owner.name", "required", ""},
		{"owner email required", "/projects", `{"name":"Launch","owner":{"name":"Sam"}}`, "owner.email", "required", ""},
		{"owner name blank", "/projects", `{"name":"Launch","owner":{"name":" \t","email":"sam@example.com"}}`, "owner.name", "invalid", "Name must not be blank"},
		{"owner email blank", "/projects", `{"name":"Launch","owner":{"name":"Sam","email":" \t"}}`, "owner.email", "invalid", "Email must not be blank"},
		{"project ID positive", "/tasks", `{"title":"Write docs","priority":"normal"}`, "project_id", "gt", ""},
		{"task title required", "/tasks", `{"project_id":1,"priority":"normal"}`, "title", "required", ""},
		{"task title blank", "/tasks", `{"project_id":1,"title":" \t","priority":"normal"}`, "title", "invalid", "Title must not be blank"},
		{"priority required", "/tasks", `{"project_id":1,"title":"Write docs"}`, "priority", "oneof", ""},
		{"priority allowed values", "/tasks", `{"project_id":1,"title":"Write docs","priority":"urgent"}`, "priority", "oneof", ""},
		{"estimate nonnegative", "/tasks", `{"project_id":1,"title":"Write docs","priority":"normal","estimate_hours":-1}`, "estimate_hours", "gte", ""},
		{"assignee name required", "/tasks", `{"project_id":1,"title":"Write docs","priority":"normal","assignee":{"email":"sam@example.com"}}`, "assignee.name", "required", ""},
		{"assignee email required", "/tasks", `{"project_id":1,"title":"Write docs","priority":"normal","assignee":{"name":"Sam"}}`, "assignee.email", "required", ""},
		{"assignee name blank", "/tasks", `{"project_id":1,"title":"Write docs","priority":"normal","assignee":{"name":" \t","email":"sam@example.com"}}`, "assignee.name", "invalid", "Name must not be blank"},
		{"assignee email blank", "/tasks", `{"project_id":1,"title":"Write docs","priority":"normal","assignee":{"name":"Sam","email":" \t"}}`, "assignee.email", "invalid", "Email must not be blank"},
		{"checklist text required", "/tasks", `{"project_id":1,"title":"Write docs","priority":"normal","checklist":[{"text":"Review"},{}]}`, "checklist[1].text", "required", ""},
		{"checklist text blank", "/tasks", `{"project_id":1,"title":"Write docs","priority":"normal","checklist":[{"text":"Review"},{"text":" \t"}]}`, "checklist[1].text", "invalid", "Checklist text must not be blank"},
		{"completion required", "/tasks", `{}`, "completed", "required", ""},
		{"completion nonnull", "/tasks", `{"completed":null}`, "completed", "required", ""},
	} {
		methods := []string{http.MethodPost, http.MethodPut}
		if tc.field == "completed" {
			methods = []string{http.MethodPatch}
		}
		for _, method := range methods {
			t.Run(tc.name+"/"+method, func(t *testing.T) {
				mux, _ := newAPI()
				request := func(method, path, body string) *httptest.ResponseRecorder {
					t.Helper()
					r := httptest.NewRequest(method, path, strings.NewReader(body))
					if body != "" {
						r.Header.Set("Content-Type", "application/json")
					}
					w := httptest.NewRecorder()
					mux.ServeHTTP(w, r)
					return w
				}
				for _, seed := range []struct{ path, body string }{{"/projects", project}, {"/tasks", task}} {
					if w := request(http.MethodPost, seed.path, seed.body); w.Code != http.StatusCreated {
						t.Fatalf("seed %s: %d %s", seed.path, w.Code, w.Body.String())
					}
				}
				before := request(http.MethodGet, tc.path, "").Body.String()
				path := tc.path
				if method != http.MethodPost {
					path += "/1"
				}
				w := request(method, path, tc.body)
				if w.Code != http.StatusUnprocessableEntity {
					t.Fatalf("got %d %s, want 422", w.Code, w.Body.String())
				}
				var response toad.ValidationErrorResponse
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				want := []toad.FieldError{{Field: tc.field, Code: tc.code, Message: tc.message}}
				if response.Detail != "Unprocessable Entity" || !reflect.DeepEqual(response.Errors, want) {
					t.Fatalf("got %+v, want errors %+v", response, want)
				}
				if after := request(http.MethodGet, tc.path, "").Body.String(); after != before {
					t.Fatalf("invalid request changed stored data: before %s; after %s", before, after)
				}
			})
		}
	}
}

func TestDocumentation(t *testing.T) {
	mux, api := newAPI()
	data, err := api.Generate()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Info       struct{ Title, Version string }
		Paths      map[string]map[string]json.RawMessage
		Components struct{ Schemas map[string]json.RawMessage }
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Info.Title != "ProjectFrog Backend API" || doc.Info.Version != "1.0.0" {
		t.Fatalf("unexpected metadata: %+v", doc.Info)
	}
	count := 0
	for path, item := range doc.Paths {
		for method, raw := range item {
			if method == "description" {
				continue
			}
			var op struct {
				Summary     string
				Description string
				Responses   map[string]json.RawMessage
				RequestBody json.RawMessage
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				t.Fatal(err)
			}
			count++
			if op.Summary == "" || op.Description == "" || len(op.Responses["500"]) == 0 {
				t.Errorf("%s %s lacks managed documentation", method, path)
			}
			if method == "post" || method == "put" || method == "patch" {
				if len(op.RequestBody) == 0 {
					t.Errorf("%s %s missing body", method, path)
				}
				for _, status := range []string{"400", "413", "415", "422"} {
					if len(op.Responses[status]) == 0 {
						t.Errorf("%s %s missing %s", method, path, status)
					}
				}
			}
		}
	}
	if count != 11 {
		t.Fatalf("got %d operations, want 11", count)
	}
	for _, name := range []string{"Project", "ProjectInputInput", "Task", "TaskInputInput", "TaskPatchInput", "Owner", "OwnerInput", "ChecklistItem", "ChecklistItemInput", "ID", "Priority", "ProjectList", "TaskList", "Deleted", "ErrorResponse", "ValidationErrorResponse"} {
		if len(doc.Components.Schemas[name]) == 0 {
			t.Errorf("missing schema %s", name)
		}
	}
	openapi, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		schema, body string
		valid        bool
	}{
		{"ProjectInputInput", `{"name":"Launch","owner":{"name":"Sam","email":"sam@example.com"}}`, true},
		{"ProjectInputInput", `{"owner":{"name":"Sam","email":"sam@example.com"}}`, false},
		{"ProjectInputInput", `{"name":"Launch","owner":{"name":"Sam"}}`, false},
		{"TaskInputInput", `{"project_id":1,"title":"Write docs","priority":"normal","estimate_hours":0}`, true},
		{"TaskInputInput", `{"project_id":0,"title":"Write docs","priority":"normal"}`, false},
		{"TaskInputInput", `{"project_id":1,"title":"Write docs","priority":"urgent"}`, false},
		{"TaskInputInput", `{"project_id":1,"title":"Write docs","priority":"normal","estimate_hours":-1}`, false},
		{"TaskInputInput", `{"project_id":1,"title":"Write docs","priority":"normal","checklist":[{}]}`, false},
		{"TaskPatchInput", `{"completed":false}`, true},
		{"TaskPatchInput", `{}`, false},
		{"TaskPatchInput", `{"completed":null}`, false},
	} {
		var body any
		if err := json.Unmarshal([]byte(tc.body), &body); err != nil {
			t.Fatal(err)
		}
		err := openapi.Components.Schemas[tc.schema].Value.VisitJSON(body)
		if (err == nil) != tc.valid {
			t.Errorf("%s validating %s: got %v, want valid=%t", tc.schema, tc.body, err, tc.valid)
		}
	}
	for path, want := range map[string]string{"/docs": "text/html", "/openapi.json": "application/json"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != 200 || w.Header().Get("Content-Type") != want {
			t.Errorf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}
