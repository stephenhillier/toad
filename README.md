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
is called. Invalid configuration and repeated finalization panic during setup.

```go
mux := http.NewServeMux()
api := buddy.NewApi(mux)
api.Route("GET /health").
    Title("Health check").
    HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusNoContent)
    })
```

Task 1b provides typed ordinary, explicit-response, and inferred-response builders.
Required JSON body validation, limits, the managed JSON error envelope, and the
complete OpenAPI response policy are planned in subsequent milestones; see
[the implementation plan](plans/01-poc.md) and [API design](plans/API_DESIGN.md).
