# Background
This doc is only intended for Dagger SDK developers, not Dagger users. It's primarily intended to be a spec of how a given Dagger SDK should support extensions, with some background information as needed.
* "Spec" is strong word right now, it's more of a loose list of requirements, but should be tightened up as time goes on.

# Extensions
Extensions are a mechanism by which user-written code can become an invokable graphql schema merged into the base Dagger Core schema. The user written code has full access to the Dagger API, including APIs provided by other extensions.

Code implementing an extension executes in its own isolated container, but otherwise has no default restrictions on what it can do or call. It can use any libraries available in the language in addition to making Dagger API calls.

The end result is the ability for users to create re-usable abstractions over both the Dagger API and any arbitrary code in general. Extensions are invoked the same way Dagger invokes any Exec, so they benefit from all the same caching and other features provided by Dagger.

Importantly, because extensions are merged into the language-agnostic graphql API, they can be invoked across language boundaries. Go code can call a Python extension, which internally can call a Typescript extension, etc. The language implementing the extension is opaque to the caller.

## Extension Protocol Spec

An Extension Runtime is the bridge between the Dagger GraphQL server and executable programs implementing an extension. On a high-level, it is responsible for:

1. Determining the graphql schema of the user code, which is loaded into the graphql API of the session.
1. Receiving input from the Dagger server intended for resolving a field in a GraphQL query.
1. Based on that input, executing code that implements the resolver for that field.
1. Returning the output of that resolver back to the Dagger server

The above process (described in greater detail below) is what's called the "runtime protocol". It's what enables the otherwise highly-generic, language-agnostic Dagger server to dynamically plug in resolver implementations written in arbitrary languages and/or frameworks.

The protocol could thus be thought of as a way of "proxying" resolver calls out from the server to these dynamically loaded pieces of user code. It is optimized to maximize re-usability of BuildKit caching, with each resolver call being cached based exactly on its relevant inputs.

There are currently two runtime implementations:
1. Go
   * [Base runtime container definition](https://github.com/dagger/dagger/blob/7f020b797287e4d7501613bef03e3e4d6d35a880/core/goruntime.go)
   * [Runtime implementation](https://github.com/dagger/dagger/blob/7f020b797287e4d7501613bef03e3e4d6d35a880/sdk/go/server.go)
1. Python
   * [Base runtime container definition](https://github.com/dagger/dagger/blob/7f020b797287e4d7501613bef03e3e4d6d35a880/core/pythonruntime.go)
   * [Runtime implementation](https://github.com/dagger/dagger/tree/7f020b797287e4d7501613bef03e3e4d6d35a880/sdk/python/src/dagger/server)

### 1. Get Schema: Dagger Session <-> Runtime
The dagger session will first invoke the runtime's entrypoint with the `-schema` flag and the user code mounted at `/src`.

The runtime should convert the user code into a graphql schema file at write it to `/outputs/schema.graphql`.

If an error occurs, exit non-zero and write it to stderr.

### 2. Invoke: Dagger Session -> Runtime

When a resolver from an extension needs to be invoked:
1. It will execute `/entrypoint`, with no args, which is expected to either be the runtime executable or to otherwise something like a shell script that executes the runtime after some setup.
1. The user code will be mounted at `/src`.
1. A file `/inputs/dagger.json` will be mounted as read-only into the ExecOp, with the following (json-encoded) contents:
   - `resolver` - identifies the field that needs to be resolver, in the form of `<ObjectName>.<FieldName>`. For instance, if the `build` field of the `Alpine` object is being invoked, this will be set to `Alpine.build`
   - `args` - The args provided to the GraphQL resolver, as described [here](https://www.apollographql.com/docs/apollo-server/data/resolvers/#resolver-arguments).
   - `parent` - The result of the parent resolver to this field in the GraphQL query (if any), as described [here](https://www.apollographql.com/docs/apollo-server/data/resolvers/#resolver-arguments).
1. A directory `/outputs` will be mounted as read-write into the ExecOp. It is where the runtime will write output of the resolver (as described more below)
1. The `ExperimentalPrivilegedNesting` flag is set, enabling access back to the "parent" dagger session

### 3. Execute: Runtime <-> User Code

The runtime is expected to:

1. Read `/inputs/dagger.json`
   - If any of the types in the input are an "ID-able" dagger object (e.g. `File`, `Directory`, `Container`, etc.), then they will be be serialized as their ID string here and should be converted into the actual object when passed to the user code.
1. Use the `resolver` value to determine which code to execute
1. Execute that code, receive the result
1. JSON encode the result and write it to `/outputs/dagger.json`
   - If any of the types in the output are an "ID-able" dagger object (e.g. `File`, `Directory`, `Container`, etc.), then they should be serialized as their ID string here.
1. Exit 0 if successful. If an error occurs during any of the above steps, error details may be written to either stdout or stderr (which results in them appearing in the progress output) and the process must exit with non-zero code.

### 4. Return: Dagger Session <- Runtime

The Dagger session will submit the ExecOp to BuildKit and then use `ReadFile` (from BuildKit's Gateway API) to obtain the contents of `/outputs/dagger.json`.

The contents of the file will be unmarshalled into a generic json object (specifically, just directly into an `interface{}` in the Go code) and returned as the result of the resolver. At this point, the standard GraphQL execution process takes over again and continues evaluating the query.

# Command API
The goal of the Command API is to enable code written with one of the Dagger SDKs to be invokable with `dagger do` (or theoretically any other interface that can make dagger API calls).

Commands are really just a loosely-opinionated "view" of the underlying extension schema. They can be thought of as an initial, limited step towards exposing the full power of extensions to Dagger users.

## Command Structure
Commands can optionally be organized into hierarchies of parent and subcommands. The way this is structured in code is up to the SDK; the following only describes what resulting graphql schemas should look like.

### Flat
For simple sets of commands, it should be possible to just provide a flat set of commands with no hierarchy.

For example, in the CLI this might look like:
* `dagger do build --foo fooval`
* `dagger do test --bar barval`

In the graphql schema, this would look like:
```graphql
extend type Query {
    build(foo: String!) String!
    test(bar: String!) String!
}
```

### Hierarchical
It should also be possible to create parent commands with subcommands, e.g.
* `dagger do sdk:go:build --foo fooval --bar barval`
* `dagger do sdk:python:test --foo fooval --baz bazval`

In the graphql schema, this could look like:
```graphql
extend type Query {
    sdk(foo: String!) SDKTargets!
}

type SDKTargets {
    go: GoTargets!
    python: PythonTargets!
}

type GoTargets {
    build(bar: String!) String!
}

type PythonTargets {
    test(baz: String!) String!
}
```

The rest of the doc will use this terminology:
1. A "parent command" is one with subcommands (e.g. `sdk`, `go` and `python`)
1. A "leaf command" is one with no subcommands (e.g. `build` and `test`)

It's only valid to invoke a full hierarchy of commands to a leaf.
* e.g. if a user invokes `dagger do sdk:go` only the `--help` output will be shown, no commands actually executed

All arguments on every command in the hierarchy are coalesced together as flags available on the leaf.
* e.g. There is a `foo` arg on `sdk` and a `bar` arg on `build`, so `sdk:go:build` has flags for `--foo` and `--bar`
* As a result, there must be no overlap in names in a hierarchy. Doing so should result in an error.

Command hierarchies are helpful for organization, but they also enable common work and configuration to be down in parent commands and then passed down to subcommands, including leafs.
* This is just the graphql resolver model. More background and examples of the general idea are available in most graphql framework docs, e.g. [these Apollo docs](https://www.apollographql.com/docs/apollo-server/data/resolvers/#example)
* Parent commands pass information to subcommands by returning "structs" (or similar concept), which are then made available to the subcommands. More details in the Type restrictions section of Requirements below.
* Importantly, every execution of parent commands are cached individually, so any expensive work that runs as part of them can be cached even if the execution of the subcommands is not cached.

## Requirements
These are the requirements as of the writing of this doc. Many of the restrictions will be lifted in very near future along with the addition of new requirements.

### Type restrictions
Arguments can only be strings.
* It's fine for command code to accept types like e.g. `dagger.Context` args too if it makes sense in the language, but those should not become part of the graphql schema.

There must be one and only one return value.
* In e.g. Go, there is also an `error` return value, but that does not become part of the graphql schema.

For leaf commands, the currently supported return types are:
* `String`
* `dagger.File`
* `dagger.Directory`

For parent commands, the return type must be a json serializable "struct" (or equivalent in the SDK's language).
* If a field in the struct is a Dagger type (e.g. `File`, `Directory`, `Container`) it should be serialized as the ID string of that type.
* It is NOT required that every field in one of these structs show up in the graphql schema. It's valid to include struct fields that do not become graphql fields. This is useful for "intermediate" data that is useful for child commands but wouldn't make sense to show up in the `dagger do` CLI.

### Documentation
Commands should support optional doc string annotations, which will be displayed as the description of the command in the CLI when called with `--help`.

Each individual input to a command should also support an optional doc string annotation, which will be displayed as the description of the flag in the CLI when called with `--help`.

The way this is implemented is up to the SDK, e.g.:
* In Go, these are comments above the command
* In Python, type annotations are used
* For Typescript, TBD but jsdoc comments are one reasonable option

### Dagger Client
An already connected dagger client should be available to the command.
* In e.g. Go, this is currently provided via the `dagger.Context` arg, but each SDK can approach this in whatever way makes most the sense in that language.

### Forbidden Arg Names
A few names are reserved and can't be used as args:
* `help` (would overlap with the `--help` flag)
* `output` (would overlap with the `--output` flag)

If a user writes code with one of those names as args, an error should be returned.

