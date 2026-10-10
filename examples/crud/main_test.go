package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	request("PATCH", "/tasks/1", `{}`, 422, "completed must be provided")
	request("PATCH", "/tasks/1", `{"completed":null}`, 422, "completed must be provided")
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
	request("POST", "/projects", `{}`, 422, "Project requires")
	request("POST", "/tasks", `{"project_id":1,"title":"Bad","priority":"urgent"}`, 422, "Task requires")
	request("POST", "/projects", `{`, 400, "Bad Request")
	request("POST", "/projects", `{"padding":"`+strings.Repeat("x", 16<<10)+`"}`, 413, "Request Entity Too Large")
	request("POST", "/projects", "", 415, "Unsupported Media Type")
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
	for _, name := range []string{"Project", "ProjectInputInput", "Task", "TaskInputInput", "TaskPatchInput", "Owner", "ChecklistItem", "ID", "Priority", "ProjectList", "TaskList", "Deleted", "ErrorResponse"} {
		if len(doc.Components.Schemas[name]) == 0 {
			t.Errorf("missing schema %s", name)
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
