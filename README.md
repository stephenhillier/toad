# Toad

Toad is a micro-framework that makes it easy to create JSON-based HTTP APIs with OpenAPI documentation using the Go programming language.

Toad allows you to register endpoints using ServeMux-style patterns, add documentation to them, and attach either a Toad http handler with typed request (Body, Query) and response models, or a normal stdlib http handler.

Toad requires Go 1.27.

## Usage

### Registering endpoints

```go
mux := http.NewServeMux()
api := toad.NewApi(mux)
api.Route("GET /products").
    Title("List products").
    Description("List all products")
    Body(http.StatusOK, []models.Product{})
    HandlerFunc(func(w http.ResponseWriter, r *http.Request) ([]models.Product, error) {
      return []models.Product{
        {Id: 1, Name: "widget"},
        {Id: 2, Name: "whirlygig"},
        {Id: 3, Name: "doodad"},
      }, nil
    })
```

Toad's managed handlers return `(Model, error)`, and Toad writes the JSON response.
By using `Response(status, Model{})` while registering an endpoint, Toad knows
to expect a managed handler instead of an stdlib handler.

> [!TIP]
> **Why should I use a non-stdlib handler?**
>
> Toad handlers help prevent drift between documentation and actual behavior. It's also convenient:
>
> - Return typed models from handlers (Toad handles JSON responses).
> - Map Go errors to HTTP status codes and error messages.

```go
api.Route("GET /users/{id}").
    Response(http.StatusOK, User{}).
    Error(http.StatusNotFound, ErrNotFound).
    HandlerFunc(func(r *http.Request) (User, error) {
        // lookupUser should return a User{} and a nil error, or an ErrNotFound.
        // ErrNotFound maps to a 404 response.
        return lookupUser(r.Context(), r.PathValue("id"))
    })
```

### Error responses

Register a public error message with `Error()`:

```go
var ErrNameTaken = errors.New("That name is already in use")

api.Route("POST /users").
    Response(http.StatusCreated, User{}).
    Error(http.StatusConflict, ErrNameTaken).
    HandlerFunc(createUser)
```

When you return a registered error from a handler, the response will show the error message:

```go

return User{}, ErrNameTaken

// or...
return User{}, fmt.Errorf("insert user failed: %w", ErrNameTaken)

// Response: { "detail": "That name is already in use" }
```

You can wrap your registered error in internal errors and only the registered error will be displayed (see wrapped version in example above).

Note: Unmapped error types (with no registered error in the chain) return a generic 500.

### Adopt Toad in an existing service

Toad can be gradually adopted in an existing stdlib-based project, making it easier to add OpenAPI documentation without moving all of your handlers to a new framework.
Methods beginning with Describe add information to docs without altering handler behavior.

```go
// Before: mux.Handle("PUT /users/{id}", middleware(http.HandlerFunc(updateUser)))
api.Route("PUT /users/{id}").
    DescribeBody(UpdateUser{}).
    DescribeParams(UserParams{}).
    DescribeQuery(UserQuery{}).
    DescribeResponse(http.StatusOK, User{}).
    DescribeResponse(http.StatusBadRequest, ExistingError{}).
    Handler(middleware(http.HandlerFunc(updateUser)))
```

## Benchmark

```sh
go test -run '^$' -bench '^BenchmarkCreateUserRequestHandling$/Stdlib' -benchmem -count=5
```
