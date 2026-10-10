# Examples

Examples of how to use the Toad http framework.

- **basic/**: setting up a new API with typed request bodies and response models, automatic validation tags, and a typed validator callback.
- **[crud/](crud/README.md)**: runnable project and task CRUD API using managed handlers, in-memory storage, comprehensive endpoint documentation, and typed OpenAPI schemas. Run `go run ./examples/crud` from the repository root, then visit http://localhost:8080/docs.
- **stdlib/**: an example of using Toad to set up OpenAPI docs for stdlib handlers, without request or response models. This may be an easier path to adding OpenAPI documentation to an existing API.
