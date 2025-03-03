Do not generate a full program. Just generate a single function. Do not show me how to use the function.

Example:

```go
func DoThing(ctx context.Context) (string, error) {
    return dag.Container().
        WithExec([]string{"echo", "Hello, world!"]).
        Stdout(ctx)
}
```

All queries chain from the global `dag` variable, which is analogous to `Query`.

Example:

```go
// GraphQL: { container { ... } }
dag.Container()
```

Assume `dag` is available globally.

Object types directly translate to struct types, and have methods for each field.

```go
dag.Container(). // *Container
    WithExec([]string{"sh", "-c", "echo hey > ./some-file"}). // *Container
    File("./some-file") // *File
```

Calling a method that returns an object type is always lazy, and never returns
an error:

```go
myFile := dag.Container(). // *Container
    WithExec([]string{"sh", "-c", "echo hey > ./some-file"}). // *Container
    File("./some-file") // *File
```

Calling a method that returns a scalar or list takes a `context.Context`
argument and returns an `error`:

```go
stdout, err := dag.Container().
    WithExec([]string{"echo", "Hello, world!"]).
    Stdout(ctx)
```

Calling a field that returns `Void` just returns `error` instead of `(Void, error)`:

```go
err := service.Stop(ctx)
```

When a field's argument is non-null (`String!`) and does not have a default
(`String! = ...`), it is a REQUIRED argument. These are passed as regular
method arguments:

```go
dag.Container().
    WithExec([]string{"echo", "hey"}). // args: [String!]!
    File("./some-file") // path: String!
```

When a field's argument is nullable (`String`, `[String!]`) or has a default
(`String! = "foo"`), it is an OPTIONAL argument. These are passed in an `Opts`
struct named after the receiving type (`Container`) and the field (`withExec`):

```go
dag.Container().
    WithExec([]string{"start"}, dagger.ContainerWithExecOpts{
        UseEntrypoint: true, // useEntrypoint: Boolean = false
    })
```

When a field ONLY has optional arguments, just pass the `Opts` struct:

```go
dag.Container().
    WithExec([]string{"run"}).
    AsService(dagger.ContainerAsServiceOpts{
        ExperimentalPrivilegedNesting: true,
    })
```

Take special care with argument types to determine optionality:

```go
// Required arguments (marked with ! in schema) are passed directly:
// GraphQL: withExec(args: [String!]!)
container.WithExec([]string{"echo", "hi"})

// Optional arguments use an Opts struct:
// GraphQL: container(packages: [String!])  // Note: no ! at the end
wolfi.Container(dagger.WolfiContainerOpts{
    Packages: []string{"git"},
})
```

Pay close attention to where the `!` is:

* `foo: String!` -> REQUIRED
* `foo: [String!]!` -> REQUIRED
* `foo: [String!]` -> OPTIONAL
* `foo: String` -> OPTIONAL
* `foo: String = "hey"` -> OPTIONAL
* `foo: String! = "hey"` -> OPTIONAL

When a field accepts an ID argument (`DirectoryID`, `FooID`), pass an object
instead - the SDK handles converting this to an ID on its own:

```go
myDir := dag.Directory().WithNewFile("some-file", "contents")

dag.Container().
    WithDirectory("/dir", myDir)
    WithExec([]string{"find", "/dir"})
```

Built-in scalar types translate to their analogous Go primitive types. Custom
scalar types translate to `string` types:

* `String!` -> `string`
* `Int!` -> `int`
* `Boolean!` -> `bool`
* `Float!` -> `float64`
* `JSON` -> `type JSON string`
* `Platform` -> `type Platform string`
