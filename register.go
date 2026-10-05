package toad

import (
	"encoding"
	"encoding/json"
	"fmt"
	"go/token"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"unicode"
)

// Api registers handlers and generates their OpenAPI documentation. Configure
// routes during startup; concurrent configuration is not supported.
type Api struct {
	mux       *http.ServeMux
	routes    []routeRecord
	bodyLimit int64

	title, description, version, server string
}

// DefaultBodyLimit is the maximum JSON request body size in bytes (1 MiB).
const DefaultBodyLimit int64 = 1 << 20

// BodyLimit sets the maximum size in bytes for all typed JSON request bodies.
// Configure it during startup, before serving requests. It applies to routes
// registered both before and after this call. Non-positive limits panic.
func (api *Api) BodyLimit(bytes int64) *Api {
	if bytes <= 0 {
		panic("toad: BodyLimit requires a positive byte limit")
	}
	api.bodyLimit = bytes
	return api
}

// Title sets the OpenAPI title. Blank titles panic during startup.
func (api *Api) Title(text string) *Api {
	if strings.TrimSpace(text) == "" {
		panic("toad: Title requires non-blank text")
	}
	api.title = text
	return api
}

// Description sets the OpenAPI description. Empty text clears it.
func (api *Api) Description(text string) *Api {
	api.description = text
	return api
}

// Version sets the OpenAPI version. Blank versions panic during startup.
func (api *Api) Version(text string) *Api {
	if strings.TrimSpace(text) == "" {
		panic("toad: Version requires non-blank text")
	}
	api.version = text
	return api
}

// Server replaces the single OpenAPI server URL. Relative URLs are supported;
// an empty string clears the server. Configure metadata before serving requests.
func (api *Api) Server(serverURL string) *Api {
	if strings.IndexFunc(serverURL, unicode.IsSpace) >= 0 {
		panic("toad: Server requires a URL without whitespace")
	}
	if _, err := url.Parse(serverURL); err != nil {
		panic(fmt.Sprintf("toad: Server requires a valid URL: %v", err))
	}
	api.server = serverURL
	return api
}

type responseMode uint8

const (
	ordinary responseMode = iota
	explicitResponse
)

// Records retain types rather than prototypes, independently of generic builders.
// Only successfully finalized routes enter the registry.
type routeRecord struct {
	method, path, title, description string
	bodyType, resultType             reflect.Type
	mode                             responseMode
	status                           int
	errors                           []errorMapping
	descriptions                     routeDescriptions
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

// NewApi creates a new Api ready for route registration.
// It also registers GET /openapi.json and GET /docs on mux.
func NewApi(mux *http.ServeMux) *Api {
	if mux == nil {
		panic("toad: NewApi requires a non-nil ServeMux")
	}
	api := &Api{mux: mux, bodyLimit: DefaultBodyLimit, title: "API Documentation", version: "1.0.0"}
	api.mux.HandleFunc("GET /openapi.json", api.ServeDocs)
	api.mux.HandleFunc("GET /docs", api.ServeDocsHTML)
	return api
}

// Route begins an unfinished route. HandlerFunc or Handler completes registration.
func (api *Api) Route(pattern string) *RouteBuilder {
	method, path := parsePattern(pattern)
	c := &routeConfig{api: api, pattern: pattern, record: routeRecord{method: method, path: path}}
	return &RouteBuilder{builderState: builderState{config: c}}
}
func (c *routeConfig) fail(rule string) { panic(fmt.Sprintf("toad: route %q: %s", c.pattern, rule)) }
func (s builderState) active() {
	if s.config.finalized {
		s.config.fail("route has already been finalized")
	}
}
func (s builderState) title(text string)       { s.active(); s.config.record.title = text }
func (s builderState) description(text string) { s.active(); s.config.record.description = text }
func (s builderState) body(t reflect.Type) builderState {
	s.active()
	s.rejectDescriptions()
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
func (s builderState) response(status int, t reflect.Type) builderState {
	s.active()
	s.rejectDescriptions()
	if s.config.record.mode != ordinary {
		s.config.fail("Response may only be selected once")
	}
	if status < 200 || status >= 300 || status == http.StatusNoContent || status == http.StatusResetContent {
		s.config.fail("managed responses require a JSON-bearing success status")
	}
	s.current()
	s.config.validateResult(t)
	s.config.record.mode, s.config.record.status, s.config.record.resultType = explicitResponse, status, t
	s.mode, s.resultType = explicitResponse, t
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
	if !v.Comparable() {
		s.config.fail("Error requires a comparable sentinel for stable mapping identity")
	}
	for _, mapping := range s.config.record.errors {
		if mapping.sentinel == sentinel {
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
func (s builderState) finalize(makeHandler func(routeRecord) http.Handler, nilHandler bool) {
	s.current()
	c := s.config
	if nilHandler {
		c.fail("HandlerFunc/Handler requires a non-nil handler")
	}
	if c.api == nil || c.api.mux == nil {
		c.fail("route requires an API with a non-nil ServeMux")
	}
	parsePattern(c.pattern)
	r := c.record
	if r.mode != ordinary {
		c.validateResult(r.resultType)
	}
	r.errors = append([]errorMapping(nil), r.errors...)
	r.descriptions.parameters = append([]describedParameter(nil), r.descriptions.parameters...)
	r.descriptions.responses = append([]describedPayload(nil), r.descriptions.responses...)
	if r.descriptions.body != nil {
		body := *r.descriptions.body
		r.descriptions.body = &body
	}
	for _, registered := range c.api.routes {
		if pathTemplate(registered.path) == pathTemplate(r.path) && registered.path != r.path {
			c.fail("parameter names must agree across registrations of the same path template")
		}
	}
	// Build the runtime adapter from the same snapshot published to Generate.
	handler := makeHandler(r)
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
func (c *routeConfig) validateResult(t reflect.Type) {
	if t == nil || t.Kind() != reflect.Struct {
		c.fail("managed responses require a struct-valued JSON model; nullable top-level results are unsupported")
	}
	for _, custom := range []reflect.Type{reflect.TypeFor[json.Marshaler](), reflect.TypeFor[encoding.TextMarshaler]()} {
		if t.Implements(custom) || reflect.PointerTo(t).Implements(custom) {
			c.fail("managed response models with custom top-level marshaling are unsupported")
		}
	}
}
func parsePattern(pattern string) (string, string) {
	fail := func(rule string) { panic(fmt.Sprintf("toad: route %q: %s", pattern, rule)) }
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
