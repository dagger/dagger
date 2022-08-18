# Extension Runtime Protocol

An Extension Runtime is the bridge between the Dagger GraphQL server and executable programs implementing an extension. On a high-level, it is responsible for:

1. Receiving input from the Dagger server intended for resolving a single field in a GraphQL query.
1. Based on that input, executing code that implements the resolver for that field.
1. Returning the output of that resolver back to the Dagger server

The above process (described in greater detail below) is what's called the "runtime protocol". It's what enables the otherwise highly-generic, language-agnostic Dagger server to dynamically plug in resolver implementations written in arbitrary languages and/or frameworks.

The protocol could thus be thought of as a way of "proxying" resolver calls out from the server to these dynamically loaded pieces of user code. It is optimized to maximize re-usability of BuildKit caching, with each resolver call being cached based exactly on its relevent inputs.

Possible examples of runtimes include:

1. A Go runtime may be a single binary that has been compiled with user-written extension functions, which it is instrumented to call via the Go SDK.
1. A TS runtime may by a TS script that dynamically loads user-written extension code (via `import()`) and executes it.
1. A Makefile runtime may be a binary that shells out `make` commands

## Spec

### 1. Invoke: Dagger Server -> Runtime

When an extension is installed into the Dagger server, resolvers for each of the fields in the schema are associated with a Filesystem that contains the runtime and any resources (user code, configuration files, etc.) needed to resolve field values.

- FIXME: document full extension load+install process (probably separately from the runtime protocol)

The Dagger server will invoke the runtime via an LLB ExecOp in BuildKit with the following configuration:

1. It will execute `/entrypoint`, with no args, which is expected to either be the runtime executable or to otherwise execute the runtime.
   - FIXME: instead, we should support standard image config variables associated with the Filesystem, which would make the entrypoint configurable in addition to supporting default envs.
1. A file `/inputs/dagger.json` will be mounted as read-only into the ExecOp, with the following (json-encoded) contents:
   - `resolver` - identifies the field that needs to be resolver, in the form of `<ObjectName>.<FieldName>`. For instance, if the `build` field of the `Alpine` object is being invoked, this will be set to `Alpine.build`
   - `args` - The args provided to the GraphQL resolver, as described [here](https://www.apollographql.com/docs/apollo-server/data/resolvers/#resolver-arguments).
   - `parent` - The result of the parent resolver to this field in the the GraphQL query (if any), as described [here](https://www.apollographql.com/docs/apollo-server/data/resolvers/#resolver-arguments).
1. A directory `/outputs` will be mounted as read-write into the ExecOp. It is where the runtime will write output of the resolver (as described more below)
1. A unix socket will be mounted at `/dagger.sock`. Connections initiated on this socket will be forwarded to the Dagger server, enabling code in the ExecOp to make Dagger API calls.
1. The root filesystem will be mounted as read-only
   - FIXME: this is a small optimization (buildkit won't try to cache a layer for the rootfs which would never be re-used), but can be lifted if users find it too restrictive
1. A tmpfs will be mounted at /tmp (useful for quick read-write scratch space when required by code in the Filesystem)

FIXME: document the "why" behind this approach (it's simple and ensures we get caching behavior we want)

### 2. Execute: Runtime <-> User Code

The runtime is expected to:

1. Read `/inputs/dagger.json`
1. Use the `resolver` value to determine which code to execute
1. Execute that code, receive the result
1. JSON encode the result and write it to `/outputs/dagger.json`
1. Exit 0 if successful. If an error occurs during any of the above steps, error details may be written to either stdout or stderr (which results in them appearing in the progress output) and the process must exit with non-zero code.
   - FIXME: the way errors are reported needs much more fleshing out. Current state at least results in the being visible, but they are often hard to read+interpret+handle.

### 3. Return: Dagger Server <- Runtime

The Dagger server will submit the ExecOp to BuildKit and then use `ReadFile` (from BuildKit's Gateway API) to obtain the contents of `/outputs/dagger.json`.

The contents of the file will be unmarshalled into a generic json object (specifically, just directly into an `interface{}` in the Go code) and returned as the result of the resolver. At this point, the standard GraphQL execution process takes over again and continues evaluating the query.
