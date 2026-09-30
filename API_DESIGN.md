# Buddy API design

Status: agreed target design, recorded 2026-09-29, updated 2026-09-30. The examples below describe the API to build; the current implementation does not yet expose these builders. See [tasks.md](tasks.md) for implementation scope and completion status.

## Goals

- Make the common REST/JSON endpoint straightforward: receive a typed body, return a typed model, and let Buddy handle HTTP encoding and documentation.
- Keep route configuration explicit and readable, with `Body` and `HandlerFunc` as consistent fluent methods.
- Use the same declarations for runtime behavior and OpenAPI so success status codes and response models do not drift between handler code and documentation.
- Check body parameters and simple managed return types at compilation, without reflective handler invocation.
- Preserve `net/http`, `http.ServeMux`, ordinary middleware, request contexts, and path parameters.
- Keep ordinary HTTP handlers available for direct response control.
- Provide an explicit documentation-only adoption path for existing `net/http` handlers, without changing their request decoding or response writing.
- Support unusual endpoints through an advanced response API with any number of registered outcomes.
- Deliver a solid minimal core and a compelling local example before broader framework features.

Go 1.27 is the target minimum because the fluent design uses generic methods. Schema generation may use reflection. Normal JSON encoding and standard error matching retain their standard-library behavior; the goal is to avoid custom reflective dispatch, not prohibit every internal use of reflection.

## API setup and documentation

Retain the existing setup shape:

```go
mux := http.NewServeMux()
api := buddy.NewApi(mux)
```

Retain OpenAPI 3.0 generation through `api.Generate()`, validation with kin-openapi, `GET /openapi.json`, and the embedded documentation UI at `GET /docs`.

Use short fluent configuration names consistently. API-level `Title(text)`, `Description(text)`, `Version(text)`, and `Server(url)` return the API for chaining and configure OpenAPI metadata. `Server(url)` sets a single server URL; configuring multiple servers is deferred. Do not force a localhost server URL when none is configured. Application route documentation excludes the built-in documentation endpoints by default.

Route-level `Title(text)` supplies the OpenAPI operation summary, and `Description(text)` supplies its longer description. These annotations do not affect handling. Request and response configuration uses `Body`, `Response`, `Status`, `Error`, and the planned `Responses`, `Query`, `Params`, and `Auth` methods. Documentation-only declarations use an explicit `Describe` prefix, as specified below.

## Fluent builder contract

`api.Route(pattern)` creates an unfinished route builder. `HandlerFunc(handler)` finalizes and registers the route with the mux and the documentation registry. It is the MVP's only finalization method; the documentation-only follow-up adds `Handler(http.Handler)` on ordinary builders. An unfinished chain registers nothing.

The concrete builder type determines the signature accepted by `HandlerFunc`. Adding a body or selecting a response mode changes that type. There is no reflective inspection of arbitrary handler signatures.

`Title` and `Description` are available throughout the chain. `Body` can appear before or after selecting a response mode; both orders produce the same handler contract. Body and response types must survive subsequent fluent configuration. `Error` configures managed response modes.

There is one response mode per route:

| Configuration | No body: handler signature | Body(B{}): handler signature |
| --- | --- | --- |
| No managed response declaration | `func(w http.ResponseWriter, r *http.Request)` | `func(w http.ResponseWriter, r *http.Request, body B)` |
| `Response(status, R{})` | `func(r *http.Request) (R, error)` | `func(r *http.Request, body B) (R, error)` |
| `Status(status)` | `func(r *http.Request) (R, error)`, inferred R | `func(r *http.Request, body B) (R, error)`, inferred R |
| `Responses(definitions...)` (advanced) | `func(r *http.Request) (buddy.Responder, error)` | `func(r *http.Request, body B) (buddy.Responder, error)` |

Managed handlers do not receive a response writer. Buddy owns their status selection and response serialization. Ordinary handlers own their writes and do not return an error to Buddy.

`Response` declares one managed success outcome. Repeating it to accumulate success-result slots is not supported; use the advanced mode for multiple outcomes. `Response`, `Status`, and `Responses` are alternative mode selectors, not independent response annotations to combine.

Reject invalid configuration clearly during setup, including conflicting response modes, duplicate success statuses, and repeated finalization. Completed route metadata must not change through subsequent mutation of an old builder. Register routes during startup; concurrent route reconfiguration is outside the MVP.

## Ordinary HTTP handlers

Without a managed response declaration, use the ordinary HTTP signature:

```go
api.Route("GET /health").
    Description("Check service health").
    HandlerFunc(healthHandler)

func healthHandler(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusNoContent)
}
```

Body decoding can also be used independently of managed responses:

```go
api.Route("POST /events").
    Body(EventRequest{}).
    HandlerFunc(eventHandler)

func eventHandler(w http.ResponseWriter, r *http.Request, body EventRequest) {
    // Buddy has decoded body. The handler writes its own response.
}
```

`Response` is a managed-response selector in the new API, replacing the old metadata-only response option. The planned `DescribeResponse` method below documents ordinary-handler responses without selecting managed behavior. Directly registering native handlers on the mux remains available.

## Documentation-only adoption path (planned follow-up)

Existing applications can register their ordinary handlers through Buddy and explicitly describe the request and response contracts. The handler keeps the standard `func(w http.ResponseWriter, r *http.Request)` signature and continues to own decoding, validation, status selection, serialization, and errors:

```go
api.Route("POST /users").
    Title("Create a user").
    Description("Create a user using the existing application handler").
    DescribeBody(CreateUserRequest{}).
    DescribeQuery(UserQuery{}).
    DescribeResponse(http.StatusCreated, User{}).
    DescribeResponse(http.StatusBadRequest, ExistingError{}).
    HandlerFunc(existingCreateUserHandler)
```

The structs identify documented types; their field values are not defaults or shared request state. These methods do not decode requests, supply typed handler arguments, inspect handler internals, validate payloads, map errors, or serialize responses. Buddy does not check at compile time or runtime that the handler's actual requests, responses, or statuses match these annotations. Ordinary Go handler-signature checks still apply, and schema generation and OpenAPI validation still report unsupported models or invalid documentation.

The planned methods are:

| Method | Documentation effect |
| --- | --- |
| `DescribeBody(B{})` | Describe a required JSON request body using B's schema; allow explicit requiredness and media-type options for existing contracts. |
| `DescribeQuery(Q{})` | Describe query parameters from Q's fields, without parsing or enforcing them. |
| `DescribeParams(P{})` | Describe named path parameters from P's fields, without decoding them; names must match the route and path parameters remain required. |
| `DescribeResponse(status, R{})` | Describe a response status and payload schema; allow explicit media-type options. Repeat for success and error outcomes. |
| `DescribeNoContent(status)` | Describe a response status with no body, such as 204, without requiring a model. |

`Title` and `Description` work on these routes as usual. A separate `DescribeError` method is unnecessary: `DescribeResponse` can document the application's own error model without suggesting Buddy performs sentinel matching or uses its managed error envelope. Field naming, parameter requiredness/serialization, and body/response option syntax must be recorded when this follow-up is implemented.

Keep this path on ordinary builders with no typed input decoding or managed response mode. Reject combinations with `Body`, `Query`, `Params`, `Response`, `Status`, `Responses`, or `Error`, regardless of configuration order, rather than silently replacing a runtime contract with documentation. Preserve documented types across subsequent `Title` and `Description` calls. Reject duplicate or conflicting declarations for the same body, parameter, or response status.

Document only the explicitly described outcomes; do not add Buddy decoding failures, mapped errors, or managed 500 responses to these routes. When no response outcome is described, retain the ordinary handler-defined OpenAPI `default` response. `DescribeParams` replaces the matching automatically documented string parameter with its explicit schema rather than creating a duplicate. The shared model registry supplies reusable schemas for this path too.

`HandlerFunc(existingHandler)` finalizes and registers the route once. This follow-up also adds `Handler(existingHTTPHandler)` for ordinary builders to accept an `http.Handler`, including an existing middleware wrapper, with the same finalization rules. Migration replaces the application's `mux.HandleFunc` or `mux.Handle` registration with the Buddy chain while preserving its handler and middleware. It does not register the same pattern twice or attach metadata to a route already registered on the mux; a separate annotation-only registry API is outside this follow-up.

This is an alternate adoption path after the MVP. Its documentation is an application assertion, not a runtime guarantee; applications can adopt typed inputs and managed responses incrementally by replacing the corresponding documentation-only route configuration. The package's stated Go minimum still applies to this path.

## Required request bodies

```go
Body(CreateUserRequest{})
```

The prototype identifies the concrete body type. It is not shared request state and does not supply field defaults. Each request receives a freshly decoded value. Initially support required, struct-valued JSON request bodies; optional and pointer-valued top-level bodies are deferred.

The MVP decoding policy is:

- Require `application/json`, accepting valid media-type parameters; missing or unsupported content types produce 415.
- Decode exactly one object; trailing whitespace is allowed.
- Empty bodies, `null`, malformed JSON, incompatible values, trailing JSON values, and trailing garbage produce 400.
- Enforce a finite documented body-size limit with an API-level override; oversized bodies produce 413. The default limit is an implementation decision still to be recorded.
- Accept unknown fields using the standard decoder's permissive behavior initially.
- Stop before invoking the handler on a decoding failure.

Missing individual fields and business rules remain the handler's responsibility in the MVP. A required body does not make every property required, and a struct's zero value is not validation failure by itself.

## Simple managed response: explicit model

```go
api.Route("POST /users").
    Description("Create a user").
    Body(CreateUserRequest{}).
    Response(http.StatusCreated, User{}).
    Error(http.StatusConflict, ErrAlreadyExists).
    HandlerFunc(createUserHandler)

func createUserHandler(r *http.Request, body CreateUserRequest) (User, error) {
    // Create the user or return an application error.
    return User{Name: body.Name, Email: body.Email}, nil
}
```

The registered model fixes the accepted return type. A handler returning a different model does not compile. When the error is nil, Buddy serializes the returned value as JSON and writes the configured status. The handler does not repeat the status or call a response constructor.

The prototype's field values are not output defaults. A zero-valued returned model is still a response. This design does not automatically validate business data or filter model fields beyond normal JSON encoding.

The initial managed success path uses JSON-bearing success statuses. Bodyless managed responses such as 204 are deferred; ordinary HTTP handlers can write them immediately.

## Simple managed response: inferred model

```go
api.Route("POST /users").
    Description("Create a user").
    Body(CreateUserRequest{}).
    Status(http.StatusCreated).
    Error(http.StatusConflict, ErrAlreadyExists).
    HandlerFunc(createUserHandler)
```

Use the same `(User, error)` handler as above. `Status` selects managed responses, and the terminal generic `HandlerFunc` infers the model from the handler's return type. The inferred type supplies the OpenAPI schema. No model prototype or runtime function inspection is needed.

An ordinary managed GET follows the same convention:

```go
api.Route("GET /users/{id}").
    Status(http.StatusOK).
    Error(http.StatusNotFound, ErrNotFound).
    HandlerFunc(getUserHandler)

func getUserHandler(r *http.Request) (User, error) {
    // Read r.PathValue("id") and look up the user.
    return user, nil
}
```

Examples are API sketches; omitted application variables and lookup logic must be supplied in the runnable example. A returned model alone does not encode its status. `Status` provides it explicitly; a chain with no response selector retains the ordinary writer-based signature.

Both explicit and inferred forms are supported. The inferred form is the shortest common path; the explicit form makes the model visible beside the route declaration. `Body` remains explicit in both forms.

## Returned errors and response writing

```go
Error(http.StatusNotFound, ErrNotFound)
Error(http.StatusTeapot, ErrTeapot)
```

Register application sentinel errors with their HTTP statuses. In managed modes:

- A non-nil returned error takes precedence over the success value. Zero-valued success results returned alongside an error are ignored.
- Match registered errors with `errors.Is`, including wrapped errors. If several registrations match, the first matching registration wins; document registration order.
- Matched errors use their configured status and the shared JSON error envelope.
- Several sentinels may map to the same status and share its documented error schema. Reject conflicting mappings for the same sentinel; merge documentation entries that use the common envelope.
- Unmatched errors produce a generic 500 and are logged. Internal and wrapped context is not automatically included in the public message.
- Document a framework 500 response for managed routes, along with registered error statuses and applicable decoding failures. Do not invent unrelated statuses on every route.

The exact public error envelope, stable codes, and mapping to public messages remain implementation decisions. Error mappings and the formatter must generate documentation matching the runtime envelope. Sentinel values alone do not define a JSON schema.

Encode a managed response successfully before committing its success headers so serialization failures can become a framework 500. After the HTTP response is committed, write failures cannot be replaced with another response; surface them through the framework's logging/error reporting boundary.

These rules resolve response ownership for returned errors: managed handlers do not write directly. Ordinary handlers continue to handle their own errors.

## Advanced managed responses (planned extension)

Use typed response definitions for endpoints with multiple success outcomes:

```go
created := buddy.JSONResponse[User](http.StatusCreated)
accepted := buddy.JSONResponse[InProgressResponse](http.StatusAccepted)

api.Route("POST /users").
    Body(CreateUserRequest{}).
    Responses(created, accepted).
    Error(http.StatusConflict, ErrAlreadyExists).
    HandlerFunc(createUserHandler)
```

The handler returns a single response instance plus an error:

```go
func createUserHandler(r *http.Request, body CreateUserRequest) (buddy.Responder, error) {
    // Choose one outcome:
    return created.Respond(User{Name: body.Name, Email: body.Email}), nil
    // Or: return accepted.Respond(InProgressResponse{...}), nil
    // Or: return nil, ErrAlreadyExists
}
```

Response definitions must be in scope for both registration and handlers, through package variables, closures, or injected application dependencies. The runnable examples will choose one of these arrangements explicitly.

`JSONResponse[T](status)` defines a status, payload type, schema, and stable definition identity. `definition.Respond(value)` accepts only T and constructs a response instance implementing the shared `Responder` interface. Domain model structs do not need HTTP methods.

`Responses` accepts any number of definitions. Definitions do not accumulate generic return slots; every advanced handler returns `(buddy.Responder, error)`.

Buddy checks that the returned instance belongs to a definition registered on the route, then writes its JSON using that definition's status. An unregistered definition or missing response with no error is a handler bug and produces a generic 500. Returning an error follows the same error-mapping rules as simple managed handlers.

Separate definitions can use the same Go model at different statuses. Selection uses definition identity, so it does not depend on guessing a status from a model's runtime type.

The shared interface's exact methods and support for user-defined implementations are not settled. An unrestricted `WriteTo(w)` method would allow arbitrary output; registration alone cannot prove its implementation matches a declared schema. The first advanced implementation should enforce the contract for Buddy's typed JSON instances.

Changing a simple route to advanced mode changes the handler's result from `(Model, error)` to `(buddy.Responder, error)`. A simple Go handler cannot sometimes return Model and sometimes a response object without changing its declared return type.

## Guarantees and limits

| Property | Enforcement |
| --- | --- |
| Declared body matches the handler parameter | Compile time |
| Explicit single response model matches the handler return type | Compile time |
| Inferred response schema follows the handler return type | Generic type inference and schema generation |
| Typed advanced response constructor receives the correct model | Compile time |
| Success status matches documentation | Shared response configuration used for both |
| Advanced response is registered on this particular route | Runtime definition-identity check |
| Returned sentinel error has a configured mapping | Runtime error matching, with 500 fallback |
| JSON can be encoded and written | Runtime encoding and I/O handling |
| Returned model contains correct business data | Application logic and tests |
| Documentation-only annotations match ordinary handler behavior (planned) | Application assertions and contract tests; Buddy does not enforce the match |

Avoid `reflect.Call`, inspection of arbitrary return-value lists, and intermediate maps for handler dispatch. Generic wrappers, interface methods, and response-definition identity are sufficient. This is not a promise of zero allocations or zero overhead.

OpenAPI schema generation can use registration/document-generation reflection. `errors.Is` includes a standard-library comparability check using internal reflection; keeping standard wrapped-error semantics is intentional.

Go return types do not provide FastAPI/Pydantic-style runtime validation or output filtering automatically. Custom JSON marshaling and unsupported schema shapes need explicit treatment. Contract tests remain useful for actual status codes and serialized bodies.

## OpenAPI and reusable schemas

Use one route registry as the source for documentation and runtime configuration. In simple mode, the explicit or inferred response model supplies the success schema. In advanced mode, each response definition supplies its status and schema. Error mappings and decoder behavior supply the corresponding error responses.

Named models are reusable components under `components.schemas`, referenced with `$ref`, for example `#/components/schemas/User`. A shared type registry supplies stable names, resolves naming collisions, and handles nested/recursive types. Request and response generation use the same registry.

Honor JSON field names and ignored fields. Handle supported pointers, optional fields, arrays/slices, maps, and scalars accurately. Do not treat `omitempty` as input validation. Return useful generation errors for unsupported models rather than silently describing arbitrary object schemas.

Ordinary handlers have no inferred response contract. Use an OpenAPI `default` response describing handler-defined output, rather than claiming a specific success status. Body-decoding failures can still be documented explicitly for `Body` routes. The planned `DescribeResponse` and `DescribeNoContent` annotations supply explicit outcomes for documentation-only routes without enforcing them.

Retain required string documentation for ordinary named path parameters. Handlers use `r.PathValue`; typed query/path decoding is outside the MVP.

## MVP and future scope

The MVP includes fluent registration, typed required bodies, ordinary handlers, explicit and inferred single managed responses, returned-error mappings, a consistent JSON error envelope, reusable schemas, documentation endpoints, and a working in-memory create/fetch example. Make the inferred simple path the main example and show the explicit and ordinary forms briefly.

The documentation-only adoption path and advanced response-definition API are agreed extensions, documented here for future implementation. Neither is a prerequisite for the first shareable demo. Implement the documentation-only path as the first adoption-focused follow-up. Generic tuple builders, fixed success-count limits, and reflective response-type selection are not planned.

Further extensions remain possible:

- `Query` / `Params`, with explicit decoding rules and a stable argument order when combined with bodies.
- `Auth` providers combining authentication middleware with OpenAPI security requirements.
- `DescribeBody`, `DescribeQuery`, `DescribeParams`, `DescribeResponse`, `DescribeNoContent`, and ordinary `Handler(http.Handler)` support for documentation-only adoption, as specified above.
- Existing tag-based validation libraries or custom hooks, with supported constraints reflected in documentation.
- Configurable error formatting, schema overrides, and richer response/header metadata.
- Bodyless managed responses such as 204, optional bodies, additional media types, and custom/streaming responders.
- Stable operation IDs and client-generation examples using the emitted OpenAPI document.

These features do not require changing the central separation between typed inputs, managed response contracts, and ordinary HTTP handlers.

## Implementation details still to settle

- Exact body-limit and logging configuration methods; API metadata uses `Title`, `Description`, `Version`, and `Server`.
- Documentation-only body/response options, parameter field naming, requiredness, and serialization rules.
- Default body-size limit and the public error envelope/codes/messages.
- Supported simple success-result shapes beyond the demo's struct values, including nil and nullable result policy.
- Component naming and treatment of embedded fields/custom JSON marshaling.
- The advanced Responder interface, custom implementation rules, and no-content response representation.

Record these choices here when implemented. They do not change the agreed handler modes or the distinction between MVP and planned extensions.

## References

- [Go generic method declarations](https://go.dev/ref/spec#Method_declarations) and [type inference](https://go.dev/ref/spec#Type_inference).
- [Go wrapped-error matching](https://pkg.go.dev/errors#Is).
- [FastAPI response models](https://fastapi.tiangolo.com/tutorial/response-model/) and [explicit responses](https://fastapi.tiangolo.com/advanced/response-directly/), as experience references; Buddy's Go guarantees are described above.
- [OpenAPI 3.0 reusable components](https://spec.openapis.org/oas/v3.0.3.html#components-object).
