package buddy

import (
	"fmt"
	"go/token"
	"net/http"
	"reflect"
	"strings"
	"unicode"
)

// Api registers handlers and generates their OpenAPI documentation. Configure
// routes during startup; concurrent configuration is not supported.
type Api struct {
	mux    *http.ServeMux
	routes []routeRecord
}

type responseMode uint8

const (
	ordinary responseMode = iota
	explicitResponse
	inferredResponse
)

// Records retain types rather than prototypes, independently of generic builders.
// Only successfully finalized routes enter the registry.
type routeRecord struct {
	method, path, title, description string
	bodyType, resultType             reflect.Type
	mode                             responseMode
	status                           int
	errors                           []errorMapping
}
type errorMapping struct {
	status   int
	sentinel error
}
type routeConfig struct {
	api       *Api
	pattern   string
	record    routeRecord
	finalized bool
}

// Every transition shares one configuration but captures its expected typed
// state. A retained builder cannot finalize a newer body/response state.
type builderState struct {
	config               *routeConfig
	bodyType, resultType reflect.Type
	mode                 responseMode
}

func NewApi(mux *http.ServeMux) *Api {
	if mux == nil {
		panic("buddy: NewApi requires a non-nil ServeMux")
	}
	api := &Api{mux: mux}
	api.mux.HandleFunc("GET /openapi.json", api.ServeDocs)
	api.mux.HandleFunc("GET /docs", api.ServeDocsHTML)
	return api
}

// Route begins an unfinished route. HandlerFunc is the only registration step.
func (api *Api) Route(pattern string) *RouteBuilder {
	method, path := parsePattern(pattern)
	c := &routeConfig{api: api, pattern: pattern, record: routeRecord{method: method, path: path}}
	return &RouteBuilder{builderState: builderState{config: c}}
}
func (c *routeConfig) fail(rule string) { panic(fmt.Sprintf("buddy: route %q: %s", c.pattern, rule)) }
func (s builderState) active() {
	if s.config.finalized {
		s.config.fail("route has already been finalized")
	}
}
func (s builderState) title(text string)       { s.active(); s.config.record.title = text }
func (s builderState) description(text string) { s.active(); s.config.record.description = text }
func (s builderState) body(t reflect.Type) builderState {
	s.active()
	if s.config.record.bodyType != nil {
		s.config.fail("Body may only be selected once")
	}
	if t.Kind() != reflect.Struct {
		s.config.fail("Body requires a struct-valued JSON model")
	}
	s.current()
	s.config.record.bodyType = t
	s.bodyType = t
	return s
}
func (s builderState) response(mode responseMode, status int, t reflect.Type) builderState {
	s.active()
	if s.config.record.mode != ordinary {
		s.config.fail("Response and Status are single-use, alternative managed selectors")
	}
	if status < 200 || status >= 300 || status == http.StatusNoContent || status == http.StatusResetContent {
		s.config.fail("managed responses require a JSON-bearing success status")
	}
	s.current()
	s.config.record.mode, s.config.record.status, s.config.record.resultType = mode, status, t
	s.mode, s.resultType = mode, t
	return s
}
func (s builderState) addError(status int, sentinel error) {
	s.current()
	if status < 400 || status > 599 {
		s.config.fail("Error requires an HTTP error status (400-599)")
	}
	if sentinel == nil {
		s.config.fail("Error requires a non-nil sentinel")
	}
	v := reflect.ValueOf(sentinel)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if v.IsNil() {
			s.config.fail("Error requires a non-nil sentinel")
		}
	}
	for _, mapping := range s.config.record.errors {
		if v.Comparable() && reflect.ValueOf(mapping.sentinel).Comparable() && reflect.TypeOf(mapping.sentinel) == v.Type() && mapping.sentinel == sentinel {
			if mapping.status != status {
				s.config.fail("the same error sentinel cannot map to conflicting statuses")
			}
			return
		}
	}
	s.config.record.errors = append(s.config.record.errors, errorMapping{status, sentinel})
}
func (s builderState) current() {
	s.active()
	r := s.config.record
	if s.bodyType != r.bodyType || s.mode != r.mode || s.resultType != r.resultType {
		s.config.fail("obsolete builder does not match the selected body/response state")
	}
}
func (s builderState) finalize(handler http.Handler, nilHandler bool, resultType reflect.Type) {
	s.current()
	c := s.config
	if nilHandler {
		c.fail("HandlerFunc requires a non-nil handler")
	}
	if c.api == nil || c.api.mux == nil {
		c.fail("route requires an API with a non-nil ServeMux")
	}
	parsePattern(c.pattern)
	r := c.record
	if r.mode == inferredResponse {
		r.resultType = resultType
	}
	r.errors = append([]errorMapping(nil), r.errors...)
	for _, registered := range c.api.routes {
		if pathTemplate(registered.path) == pathTemplate(r.path) && registered.path != r.path {
			c.fail("parameter names must agree across registrations of the same path template")
		}
	}
	// ServeMux can panic for conflicts. Add context without swallowing the failure
	// or publishing metadata. The route remains unfinished if registration fails.
	func() {
		defer func() {
			if failure := recover(); failure != nil {
				c.fail(fmt.Sprintf("ServeMux registration failed: %v", failure))
			}
		}()
		c.api.mux.Handle(c.pattern, handler)
	}()
	c.api.routes = append(c.api.routes, r)
	c.finalized = true
}
func parsePattern(pattern string) (string, string) {
	fail := func(rule string) { panic(fmt.Sprintf("buddy: route %q: %s", pattern, rule)) }
	if strings.Count(pattern, " ") != 1 || strings.IndexFunc(pattern, func(r rune) bool { return unicode.IsSpace(r) && r != ' ' }) >= 0 {
		fail("pattern must be exactly METHOD /path with one ASCII space")
	}
	method, path, _ := strings.Cut(pattern, " ")
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE":
	default:
		fail("unsupported method; use an explicit uppercase OpenAPI HTTP method")
	}
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#%") {
		fail("path must be absolute, without hosts, queries, fragments, or percent escapes")
	}
	if path == "/" {
		return method, path
	}
	names := make(map[string]bool)
	for _, segment := range strings.Split(path[1:], "/") {
		if segment == "" || segment == "." || segment == ".." {
			fail("empty, dot, and trailing-slash path segments are unsupported")
		}
		if strings.ContainsAny(segment, "{}") {
			if !strings.HasPrefix(segment, "{") || !strings.HasSuffix(segment, "}") {
				fail("parameters must occupy a whole path segment")
			}
			name := segment[1 : len(segment)-1]
			if name == "_" || !token.IsIdentifier(name) || names[name] {
				fail("parameter names must be unique Go identifiers other than _; catch-alls and anchors are unsupported")
			}
			names[name] = true
		}
	}
	return method, path
}
func pathTemplate(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if strings.HasPrefix(segment, "{") {
			segments[i] = "{}"
		}
	}
	return strings.Join(segments, "/")
}
