// Command crud runs a documented, in-memory project and task API.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/stephenhillier/toad"
)

// Shared ID and priority types.
type ID uint64
type Priority string

type Owner struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type Project struct {
	ID          ID                `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Owner       Owner             `json:"owner"`
	Labels      []string          `json:"labels"`
	Metadata    map[string]string `json:"metadata"`
}

// Fields used to create or replace a project.
type ProjectInput struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Owner       Owner             `json:"owner"`
	Labels      []string          `json:"labels,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type Task struct {
	ID            ID              `json:"id"`
	ProjectID     ID              `json:"project_id"`
	Title         string          `json:"title"`
	Completed     bool            `json:"completed"`
	Priority      Priority        `json:"priority"`
	EstimateHours float64         `json:"estimate_hours"`
	Assignee      *Owner          `json:"assignee"`
	Checklist     []ChecklistItem `json:"checklist"`
}

type ChecklistItem struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type TaskInput struct {
	ProjectID     ID              `json:"project_id"`
	Title         string          `json:"title"`
	Completed     bool            `json:"completed"`
	Priority      Priority        `json:"priority"`
	EstimateHours float64         `json:"estimate_hours"`
	Assignee      *Owner          `json:"assignee,omitempty"`
	Checklist     []ChecklistItem `json:"checklist,omitempty"`
}

// A pointer lets us distinguish false from a missing value.
type TaskPatch struct {
	Completed *bool `json:"completed"`
}

type ProjectList struct {
	Items []Project `json:"items"`
	Total int       `json:"total"`
}

type TaskList struct {
	Items []Task `json:"items"`
	Total int    `json:"total"`
}

type Deleted struct {
	ID      ID   `json:"id"`
	Deleted bool `json:"deleted"`
}

var (
	errInvalidID      = errors.New("ID must be a positive integer")
	errProjectMissing = errors.New("Project not found")
	errTaskMissing    = errors.New("Task not found")
	errProjectInvalid = errors.New("Project requires a non-blank name and owner name and email")
	errTaskInvalid    = errors.New("Task requires a project_id, non-blank title, priority low/normal/high, non-negative estimate_hours, and non-blank checklist text; an assignee requires name and email")
	errNameTaken      = errors.New("Project name already exists")
	errProjectInUse   = errors.New("Delete this project's tasks before deleting the project")
	errPatchInvalid   = errors.New("completed must be provided as a boolean")
)

// A mutex protects the maps. Nested values stay unchanged after storage.
type store struct {
	mu                    sync.RWMutex
	nextProject, nextTask ID
	projects              map[ID]Project
	tasks                 map[ID]Task
}

func newAPI() (*http.ServeMux, *toad.Api) {
	mux := http.NewServeMux()
	api := toad.NewApi(mux).
		Title("ProjectFrog Backend API").
		Description("A simple API for an app for managing projects and tasks.").
		Version("1.0.0").Server("/").BodyLimit(16 << 10)
	s := &store{nextProject: 1, nextTask: 1, projects: make(map[ID]Project), tasks: make(map[ID]Task)}

	api.Route("GET /projects").Title("List projects").
		Description("Browse all projects, ordered by their ID.").
		Response(http.StatusOK, ProjectList{}).HandlerFunc(s.listProjects)

	api.Route("POST /projects").Title("Create a project").
		Description("Create a new project with an owner.").
		Body(ProjectInput{}).Response(http.StatusCreated, Project{}).
		Error(http.StatusUnprocessableEntity, errProjectInvalid).Error(http.StatusConflict, errNameTaken).HandlerFunc(s.createProject)

	api.Route("GET /projects/{id}").Title("Get a project").
		Description("Find a project using its ID.").
		Response(http.StatusOK, Project{}).Error(http.StatusBadRequest, errInvalidID).Error(http.StatusNotFound, errProjectMissing).HandlerFunc(s.getProject)

	api.Route("PUT /projects/{id}").Title("Replace a project").
		Description("Replace the details of an existing project.").
		Response(http.StatusOK, Project{}).Body(ProjectInput{}).
		Error(http.StatusBadRequest, errInvalidID).Error(http.StatusNotFound, errProjectMissing).
		Error(http.StatusUnprocessableEntity, errProjectInvalid).Error(http.StatusConflict, errNameTaken).HandlerFunc(s.replaceProject)

	api.Route("DELETE /projects/{id}").Title("Delete a project").
		Description("Remove a project once its tasks are deleted.").
		Response(http.StatusOK, Deleted{}).Error(http.StatusBadRequest, errInvalidID).
		Error(http.StatusNotFound, errProjectMissing).Error(http.StatusConflict, errProjectInUse).HandlerFunc(s.deleteProject)

	api.Route("GET /tasks").Title("List tasks").
		Description("Browse all tasks, ordered by their ID.").
		Response(http.StatusOK, TaskList{}).HandlerFunc(s.listTasks)

	api.Route("POST /tasks").Title("Create a task").
		Description("Add a new task to an existing project.").
		Body(TaskInput{}).Response(http.StatusCreated, Task{}).
		Error(http.StatusUnprocessableEntity, errTaskInvalid).Error(http.StatusNotFound, errProjectMissing).HandlerFunc(s.createTask)

	api.Route("GET /tasks/{id}").Title("Get a task").
		Description("Find a task using its ID.").
		Response(http.StatusOK, Task{}).Error(http.StatusBadRequest, errInvalidID).Error(http.StatusNotFound, errTaskMissing).HandlerFunc(s.getTask)

	api.Route("PUT /tasks/{id}").Title("Replace a task").
		Description("Replace the details of an existing task.").
		Response(http.StatusOK, Task{}).Body(TaskInput{}).Error(http.StatusBadRequest, errInvalidID).
		Error(http.StatusNotFound, errTaskMissing).Error(http.StatusNotFound, errProjectMissing).
		Error(http.StatusUnprocessableEntity, errTaskInvalid).HandlerFunc(s.replaceTask)

	api.Route("PATCH /tasks/{id}").Title("Set task completion").
		Description("Mark a task as complete or incomplete.").
		Body(TaskPatch{}).Response(http.StatusOK, Task{}).Error(http.StatusBadRequest, errInvalidID).
		Error(http.StatusNotFound, errTaskMissing).Error(http.StatusUnprocessableEntity, errPatchInvalid).HandlerFunc(s.patchTask)

	api.Route("DELETE /tasks/{id}").Title("Delete a task").
		Description("Remove a task from its project.").
		Response(http.StatusOK, Deleted{}).Error(http.StatusBadRequest, errInvalidID).Error(http.StatusNotFound, errTaskMissing).HandlerFunc(s.deleteTask)

	return mux, api
}

// Shared helpers

func compareID(a, b ID) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func pathID(r *http.Request) (ID, error) {
	n, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("parse path ID %q: %w", r.PathValue("id"), errInvalidID)
	}
	return ID(n), nil
}

func validOwner(o Owner) bool {
	return strings.TrimSpace(o.Name) != "" && strings.TrimSpace(o.Email) != ""
}

func (s *store) projectValue(id ID, in ProjectInput) (Project, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || !validOwner(in.Owner) {
		return Project{}, errProjectInvalid
	}
	for otherID, p := range s.projects {
		if otherID != id && strings.EqualFold(p.Name, in.Name) {
			return Project{}, fmt.Errorf("project uniqueness check: %w", errNameTaken)
		}
	}
	if in.Labels == nil {
		in.Labels = []string{}
	}
	if in.Metadata == nil {
		in.Metadata = map[string]string{}
	}
	return Project{id, in.Name, in.Description, in.Owner, in.Labels, in.Metadata}, nil
}

func (s *store) taskValue(id ID, in TaskInput) (Task, error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.ProjectID == 0 || in.Title == "" || (in.Priority != "low" && in.Priority != "normal" && in.Priority != "high") || in.EstimateHours < 0 || (in.Assignee != nil && !validOwner(*in.Assignee)) {
		return Task{}, errTaskInvalid
	}
	for _, item := range in.Checklist {
		if strings.TrimSpace(item.Text) == "" {
			return Task{}, errTaskInvalid
		}
	}
	if _, ok := s.projects[in.ProjectID]; !ok {
		return Task{}, fmt.Errorf("task project lookup: %w", errProjectMissing)
	}
	if in.Checklist == nil {
		in.Checklist = []ChecklistItem{}
	}
	return Task{id, in.ProjectID, in.Title, in.Completed, in.Priority, in.EstimateHours, in.Assignee, in.Checklist}, nil
}

// Project CRUD methods

func (s *store) listProjects(_ *http.Request) (ProjectList, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]Project, 0, len(s.projects))
	for _, p := range s.projects {
		items = append(items, p)
	}
	slices.SortFunc(items, func(a, b Project) int { return compareID(a.ID, b.ID) })
	return ProjectList{items, len(items)}, nil
}

func (s *store) createProject(_ *http.Request, in ProjectInput) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.projectValue(s.nextProject, in)
	if err != nil {
		return Project{}, err
	}
	s.projects[p.ID] = p
	s.nextProject++

	return p, nil
}

func (s *store) getProject(r *http.Request) (Project, error) {
	id, err := pathID(r)
	if err != nil {
		return Project{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	p, ok := s.projects[id]
	if !ok {
		return Project{}, errProjectMissing
	}

	return p, nil
}

func (s *store) replaceProject(r *http.Request, in ProjectInput) (Project, error) {
	id, err := pathID(r)
	if err != nil {
		return Project{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.projects[id]; !ok {
		return Project{}, errProjectMissing
	}
	p, err := s.projectValue(id, in)
	if err != nil {
		return Project{}, err
	}
	s.projects[id] = p

	return p, nil
}

func (s *store) deleteProject(r *http.Request) (Deleted, error) {
	id, err := pathID(r)
	if err != nil {
		return Deleted{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.projects[id]; !ok {
		return Deleted{}, errProjectMissing
	}
	for _, task := range s.tasks {
		if task.ProjectID == id {
			return Deleted{}, errProjectInUse
		}
	}
	delete(s.projects, id)
	return Deleted{id, true}, nil
}

// Task CRUD methods

func (s *store) listTasks(_ *http.Request) (TaskList, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		items = append(items, task)
	}
	slices.SortFunc(items, func(a, b Task) int { return compareID(a.ID, b.ID) })
	return TaskList{items, len(items)}, nil
}

func (s *store) createTask(_ *http.Request, in TaskInput) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, err := s.taskValue(s.nextTask, in)
	if err != nil {
		return Task{}, err
	}
	s.tasks[task.ID] = task
	s.nextTask++

	return task, nil
}

func (s *store) getTask(r *http.Request) (Task, error) {
	id, err := pathID(r)
	if err != nil {
		return Task{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, ok := s.tasks[id]
	if !ok {
		return Task{}, errTaskMissing
	}

	return task, nil
}

func (s *store) replaceTask(r *http.Request, in TaskInput) (Task, error) {
	id, err := pathID(r)
	if err != nil {
		return Task{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.tasks[id]; !ok {
		return Task{}, errTaskMissing
	}
	task, err := s.taskValue(id, in)
	if err != nil {
		return Task{}, err
	}
	s.tasks[id] = task

	return task, nil
}

func (s *store) patchTask(r *http.Request, in TaskPatch) (Task, error) {
	id, err := pathID(r)
	if err != nil {
		return Task{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[id]
	if !ok {
		return Task{}, errTaskMissing
	}
	if in.Completed == nil {
		return Task{}, errPatchInvalid
	}
	task.Completed = *in.Completed
	s.tasks[id] = task

	return task, nil
}

func (s *store) deleteTask(r *http.Request) (Deleted, error) {
	id, err := pathID(r)
	if err != nil {
		return Deleted{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.tasks[id]; !ok {
		return Deleted{}, errTaskMissing
	}
	delete(s.tasks, id)
	return Deleted{id, true}, nil
}

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()
	mux, api := newAPI()
	// Check the docs before starting the server.
	if _, err := api.Generate(); err != nil {
		log.Fatal(err)
	}
	_, port, err := net.SplitHostPort(*addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Listening on %s; docs: http://localhost:%s/docs; OpenAPI: http://localhost:%s/openapi.json", *addr, port, port)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
