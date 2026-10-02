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
failure stops before the application handler runs and returns the JSON error
envelope described below.

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

Managed handlers return `(Model, error)` and let Buddy write JSON at the selected
status. `Response(status, Model{})` fixes the model explicitly; `Status(status)`
infers it from the handler:

```go
api.Route("GET /users/{id}").Status(http.StatusOK).
    Error(http.StatusNotFound, ErrNotFound).
    HandlerFunc(func(r *http.Request) (User, error) {
        return lookupUser(r.Context(), r.PathValue("id"))
    })
```

This sketch assumes application-defined `User`, `ErrNotFound`, and `lookupUser`.
Managed results currently require a struct value, including a valid zero value.
Top-level pointers, interfaces, collections, scalars, and custom JSON/text
marshalers are rejected during setup; nil results are therefore unsupported.
Nested model/schema support is being completed in Task 6. Success statuses are
200–299 except 204 and 205; use an ordinary handler for bodyless responses.

A returned error takes precedence over the model. `Error(status, sentinel)`
accepts statuses 400–599 and non-nil, comparable sentinels. Matching uses
`errors.Is`, including wrapped errors, in registration order. Multiple sentinels
can share a status; repeating the same mapping is harmless, but mapping the same
sentinel to different statuses panics.

Decoder and managed errors use the public `buddy.ErrorResponse` envelope:

```json
{"code":"application_error","message":"Not Found"}
```

Codes are `invalid_body` (400), `body_too_large` (413),
`unsupported_media_type` (415), `application_error` (mapped status), and
`internal_error` (unmatched errors or serialization failures, 500). Messages use
HTTP status text, or "Request failed" for an unrecognized status. Sentinel text
and wrapped context are never exposed. OpenAPI merges codes sharing a status
into one envelope schema. The remaining response policy work is in Task 5.

Buddy finishes encoding before committing success headers. Unmatched errors,
encoding failures, and post-commit write failures are logged through the default
`log/slog` logger; write failures never trigger a second response. Mapped errors
are expected application outcomes and are not logged automatically.

Ordinary handlers may use `buddy.JSON(w, status, value)` to encode before writing
JSON. It returns encoding or write errors to the caller and does not automatically
log or emit an error response. Validation/encoding errors leave the writer
untouched; write errors occur after commitment. Ordinary handlers retain response
ownership. See [the implementation plan](plans/01-poc.md) and
[API design](plans/API_DESIGN.md) for scope and deferred features.

The end-to-end test in `managed_e2e_test.go` exercises a `Body` / `Response` /
`Error` route over HTTP and validates the served OpenAPI document and response
payloads. Run it and its request-handling benchmark with:

```sh
go test -run '^TestCreateUserEndToEnd$' ./...
mise run bench
```

The benchmark covers successful creation. It excludes route setup, OpenAPI
generation, and network transport; it includes routing, body decoding,
application logic, JSON encoding/writing, and lightweight buffer resets.
`Buddy/Created` is compared with `Stdlib/Created` through the same harness. The
baseline in `stdlib_baseline_test.go` uses only standard-library request/response
handling, including JSON decoding and encoding in its handler. Both implementations use
the same application function, payloads, 128-byte body limit, decoding rules,
error envelopes, and encode-before-commit behavior. A parity test checks their
status codes, headers, and payloads across successful and invalid requests.

To run only the stdlib baseline:

```sh
go test -run '^$' -bench '^BenchmarkCreateUserRequestHandling$/Stdlib' -benchmem -count=5
```
