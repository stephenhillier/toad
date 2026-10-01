# Buddy

Buddy registers HTTP handlers with a fluent, typed route builder and generates
OpenAPI 3.0 documentation. The builder requires **Go 1.27 or newer** for generic
methods.

Build and check the package and example:

```sh
go test ./...
go build ./...
go run ./example
```

The example serves `/docs` and `/openapi.json` at `http://localhost:8080`.
Register routes during startup; each chain registers only when `HandlerFunc`
is called. Finish configuration before serving requests or generating documentation;
concurrent route reconfiguration is unsupported. Invalid configuration and repeated finalization panic during setup.

```go
mux := http.NewServeMux()
api := buddy.NewApi(mux)
api.Route("GET /health").
    Title("Health check").
    HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusNoContent)
    })
```

Typed ordinary, explicit-response, and inferred-response builders support required
struct-valued bodies through `Body(Model{})`. Each request receives a fresh value;
prototype fields are not defaults. Requests must use `application/json` (valid
media-type parameters are accepted) and contain exactly one JSON object. Empty,
null, malformed, incompatible, or trailing input returns 400; missing or
unsupported content types return 415. Unknown fields are accepted. Missing
individual fields and business validation remain the handler's responsibility.

Bodies are limited to **1 MiB (1,048,576 bytes)** by default. Oversized bodies
return 413, including when Content-Length is absent. All bytes count toward the
limit, including whitespace. Override the limit during startup, before serving:

```go
api := buddy.NewApi(mux).BodyLimit(2 << 20) // 2 MiB
```

`BodyLimit` requires a positive byte count and applies to all typed-body routes,
including routes already registered. Invalid content types are rejected before
reading; otherwise the size limit is checked before decoding. Every decoding
failure stops before the application handler runs and currently returns generic
HTTP status text. The shared managed JSON error envelope and complete OpenAPI
response policy remain planned; see [the implementation plan](plans/01-poc.md)
and [API design](plans/API_DESIGN.md).

Buddy uses the supplied `http.ServeMux`, so external middleware can wrap it as
usual (`http.ListenAndServe(":8080", middleware(mux))`). Request contexts,
cancellation, and `r.PathValue("id")` remain available in every handler mode.
Handlers registered directly on the mux are served but are not documented.

Patterns require an explicit uppercase method and an absolute path, for example
`GET /users/{id}`. Supported methods are GET, HEAD, POST, PUT, PATCH, DELETE,
OPTIONS, and TRACE. Different methods can share a documented path; parameter
names must agree. GET also matches HEAD unless an explicit HEAD handler exists.
The root `/` retains ServeMux's fallback behavior. Host-qualified patterns,
catch-alls, end anchors, trailing-slash subtrees, and normalized/escaped pattern
paths are rejected during setup. See the [pattern contract](plans/API_DESIGN.md#registration-decisions-task-1a-settled-2026-09-30)
for the full subset.
