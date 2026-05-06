# Single-Query Workspace Module Loading

## Table of Contents

- [Problem](#problem)
- [Solution](#solution)
- [Client Contract](#client-contract)
- [Request Peeking](#request-peeking)
- [Module Planning](#module-planning)
- [CLI](#cli)
- [Correctness](#correctness)
- [Implementation](#implementation)
- [Status](#status)

## Problem

Workspace module loading is now centralized in the engine. This keeps clients simple and gives every client the same schema, but it can be wasteful for one-shot clients.

1. **Full workspace cost** - A workspace with many modules loads every configured module even when the query only touches one.
2. **One-shot queries know their workload** - Commands such as `dagger query` know the GraphQL request they will send, but the engine currently loads modules before using that request.
3. **Hints duplicate intent** - A separate "modules to load" hint would repeat information already present in the GraphQL query.

## Solution

Add a `SingleQuery` client metadata flag. When set, the engine may inspect the first `/query` request body before workspace module loading, derive the top-level GraphQL fields, and load only the workspace modules needed for that request. If the request cannot be safely classified, the engine falls back to current behavior and loads all workspace modules.

## Client Contract

`SingleQuery` is a session contract, not a module-loading flag.

```go
type ClientMetadata struct {
    LoadWorkspaceModules bool `json:"load_workspace_modules,omitempty"`

    // SingleQuery declares that this client will issue at most one GraphQL
    // /query request before disconnecting. The engine may use that request body
    // to specialize session setup.
    SingleQuery bool `json:"single_query,omitempty"`
}
```

Semantics:

| LoadWorkspaceModules | SingleQuery | Behavior |
| --- | --- | --- |
| false | false/true | Load no workspace modules. |
| true | false | Load all workspace modules. |
| true | true | Inspect the first request and load a subset if safe; otherwise load all. |

If a `SingleQuery` client sends a second `/query` request, the engine should return a clear error. This avoids silently serving a schema that was narrowed for a previous request.

## Request Peeking

`dagql` owns GraphQL-over-HTTP parsing and leaves the request usable for the real handler.

```go
func PeekRootFields(r *http.Request) (ok bool, fields []string, err error)
```

Responsibilities:

1. Decode the GraphQL HTTP request envelope.
2. Parse the selected GraphQL operation using the existing `gqlparser` parser.
3. Return actual top-level field names, ignoring aliases.
4. Expand top-level fragments when this can be done syntactically.
5. Restore `r.Body` before returning.
6. Return `ok=false` when it cannot safely produce a complete field list.

`dagql` does not interpret field names. It does not know about workspaces, modules, `currentTypeDefs`, or any other Dagger-specific field.

## Module Planning

The engine calls `dagql.PeekRootFields` before module loading, then applies Dagger workspace rules.

```go
ok, fields, err := dagql.PeekRootFields(r)
if err != nil || !ok || requiresFullWorkspaceSchema(fields) {
    loadAllWorkspaceModules()
} else {
    loadWorkspaceModulesForRootFields(fields)
}
```

Full workspace loading is required when root fields include schema-wide Dagger or GraphQL fields:

```go
__schema
__type
currentTypeDefs
```

For ordinary root fields:

1. If a field matches a workspace module constructor, load that module.
2. If a field does not match a constructor but there is exactly one workspace entrypoint, load the entrypoint module.
3. If multiple workspace entrypoints or other ambiguity exists, load all modules.
4. If all fields are core-only, load no workspace modules.

The query is still validated and executed by the normal GraphQL handler after planning.

## CLI

`dagger query` is the first target.

```bash
$ dagger query --doc query.graphql
```

It should connect with:

```go
client.Params{
    LoadWorkspaceModules: true,
    SingleQuery:          true,
}
```

Before this can be set honestly, `dagger query` must avoid preflight GraphQL calls that would violate the single-query contract. Today it uses the generic optional module wrapper, which may issue module/config checks before the user query.

## Correctness

The optimization must be conservative.

1. **Fallback preserves behavior** - Parser failure, unsupported transport shape, ambiguous operation selection, unsupported fragment shape, or uncertain module mapping loads all workspace modules.
2. **Request body is preserved** - Peeking must restore `r.Body` so the real GraphQL handler receives the original request.
3. **No schema validation during peek** - The peek is syntax-only. Validation still happens against the final schema.
4. **No Dagger semantics in dagql** - `dagql` returns root fields; the engine decides what those fields mean.
5. **Single-query enforcement** - A narrowed schema is only safe if the client cannot later ask for a different workspace module.

## Implementation

Likely changes:

1. Add `SingleQuery` to `engine.ClientMetadata`, engine client params, CLI session forwarding, and SDK connection config where needed.
2. Add `dagql.PeekRootFields(*http.Request)`.
3. In `engine/server.serveQuery`, call the peek helper for `LoadWorkspaceModules && SingleQuery` before `ensureModulesLoaded`.
4. After `ensureWorkspaceLoaded` gathers `client.pendingModules`, filter those pending modules from the peeked fields.
5. Track whether a `SingleQuery` client has already served a `/query` request and reject subsequent requests.
6. Refactor `dagger query` so it can set `SingleQuery` without issuing preflight GraphQL calls.
7. Add tests for request body restoration, root-field extraction, fallback cases, workspace constructor matching, entrypoint fallback, and second-query rejection.

## Status

Draft proposal.
