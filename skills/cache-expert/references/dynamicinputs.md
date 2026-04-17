# Dynamic Inputs and Implicit Inputs

This is a short reference on two closely related dagql cache concepts:

- dynamic input functions
- implicit inputs

These are both about shaping call identity before the resolver actually runs.

## The big idea

Dagql does not treat cache identity as a single opaque digest that fields can tweak however they want. Instead, it models call identity as a structured `ResultCall`:

- explicit args
- implicit inputs
- receiver
- module provenance
- view
- nth selection
- extra digests

Dynamic inputs and implicit inputs are part of that structured model.

That is what makes cache identity more observable and debuggable than the old approach of just mutating the cache key digest in opaque ways.

## Dynamic input functions

The general mechanism is `FuncWithDynamicInputs` / `NodeFuncWithDynamicInputs`.

The typed callback looks like:

```go
type DynamicInputFunc[T Typed, A any] func(
	context.Context,
	ObjectResult[T],
	A,
	*CallRequest,
) error
```

This runs before cache lookup and before the field resolver body executes. It can mutate the `CallRequest`, which is the planning-time wrapper around the semantic `ResultCall` plus request-only cache policy:

- `Args`
- `ImplicitInputs`
- `ConcurrencyKey`
- `TTL`
- `DoNotCache`
- `IsPersistable`

So dynamic inputs are not limited to "rewrite an arg." They can also adjust request policy such as concurrency grouping, TTL, or do-not-cache behavior.

One important distinction:

- rewritten args and implicit inputs affect recipe identity
- request-policy fields like `TTL`, `DoNotCache`, `ConcurrencyKey`, and `IsPersistable` affect cache behavior without becoming part of persisted semantic provenance

## Implicit inputs

An implicit input is the simpler, more specialized mechanism.

It is declared with `Field.WithInput(...)` and has the form:

```go
type ImplicitInput struct {
	Name     string
	Resolver ImplicitInputResolver
}
```

The resolver computes an engine-side input value from:

- the current context
- the current explicit arg map

That input is then attached to the call identity as a hidden input.

Important properties:

- it is not a normal GraphQL arg
- it is not exposed in field argument definitions
- it still participates in recipe identity
- it is preserved explicitly as `ResultCall.ImplicitInputs`, not flattened away into an opaque digest mutation

This is the part that makes the model much easier to reason about. The client/session/schema scoping information is still visible in the structured call data.

## Execution order

The call planning order in `dagql/objects.go` is important:

1. Decode explicit GraphQL args into `inputArgs`.
2. Build `frameArgs` for the `ResultCall`.
3. Resolve implicit inputs from the current `inputArgs`.
4. Build the initial `CallRequest`.
5. Run the field's dynamic input hook, if any.
6. Re-sort and re-decode the request args from `req.Args`.
7. Recompute implicit inputs from the rewritten arg set.
8. Finalize the request and perform cache lookup / execution.

That recomputation step is a very important detail.

It means dynamic input hooks can rewrite explicit args, and any implicit inputs that depend on those args will then be recalculated from the final rewritten values.

There is a dedicated test for this behavior: `TestImplicitInputRecomputedAfterCacheConfigIDRewrite`.

## What implicit inputs are for

The most common use of implicit inputs is scoping.

For example:

- per client
- per session
- per call
- per schema
- per caller module

The important point is that the scope becomes part of the recipe as a real, named input. So the recipe digest changes, but it changes through structured data rather than through an invisible custom hash tweak.

## Built-in implicit inputs

Dagql ships a few standard ones in `dagql/cache_inputs.go`:

- `PerClientInput`
- `PerSessionInput`
- `PerCallInput`
- `PerSchemaInput`
- `CurrentSchemaInput`
- `RequestedCacheInput(argName)`

These cover most of the "boring but necessary" cases.

### `PerClientInput`

Adds the current client ID as an implicit input.

Use this when results must not cross clients even within the same session.

### `PerSessionInput`

Adds the current session ID as an implicit input.

Use this when reuse within a session is fine, but reuse across sessions is not.

### `PerCallInput`

Adds a random value as an implicit input.

This forces a unique identity per invocation, which is effectively "always miss the cache."

### `CurrentSchemaInput`

Adds the current dagql server schema digest as an implicit input.

This is important for fields whose meaning depends on the currently served schema. `currentTypeDefs` is the most obvious example.

### `RequestedCacheInput(argName)`

This is a neat helper for the common `noCache` pattern.

It uses the value of a boolean arg to choose between:

- per-client identity when false
- per-call identity when true

So the field still uses the cache model, but the caller can request "treat this like uncached" without inventing a completely separate execution path.

## Where implicit inputs live

Implicit inputs are first-class in the identity model.

They live in:

- `ResultCall.ImplicitInputs`
- `call.ID.ImplicitInputs()`
- the protobuf call encoding
- recipe ID reconstruction

When the recipe ID is rebuilt, implicit inputs are appended separately from normal args with:

- `call.WithArgs(...)`
- `call.WithImplicitInputs(...)`

They affect the digest, but they are still carried as separate structured inputs.

There is also a test showing two calls with different implicit inputs produce different digests: `TestImplicitInputsAffectDigest`.

One small nuance: implicit inputs are intentionally omitted from the human-readable display form of IDs for now, even though they do affect the digest.

## Dynamic input functions vs implicit inputs

The relationship is:

- dynamic input functions are the general hook
- implicit inputs are a simple declarative special case

If all you need is "scope this field by client/session/schema/caller module," implicit inputs are the right tool.

If you need to:

- rewrite args
- synthesize hidden internal args
- canonicalize an input object before hashing
- inject contextual defaults into the recipe
- change TTL / do-not-cache / concurrency behavior

then you want a dynamic input function.

## Representative dynamic-input uses

Here are the most useful examples in the current codebase.

### `currentModule`

`Query.currentModule` uses a dynamic input hook in `core/schema/module.go`.

It injects an internal `implementationScopedMod` arg when the caller did not provide one. That arg is derived from the current module, but specifically from its implementation-scoped identity rather than its caller-specific provenance.

Why this matters:

- the field is cacheable
- the cache identity tracks the actual module implementation being served
- it does not accidentally key off incidental caller/session provenance

This is a good example of dynamic input as "rewrite the recipe to the real identity you wanted all along."

### `cacheVolume`

`Query.cacheVolume` uses a dynamic input hook in `core/schema/cache.go`.

It does two interesting things:

1. If `namespace` was omitted, it derives a namespace from the current module and injects it as an internal arg.
2. If sharing mode is `PRIVATE`, it injects a random `privateNonce` arg so the result becomes unique.

So this hook both:

- canonicalizes omitted defaults into explicit structured args
- intentionally makes one policy mode non-reusable

### `withMountedCache`

`Container.withMountedCache` uses a dynamic input hook in `core/schema/container.go`.

It loads the referenced cache volume, merges any overriding `source`, `sharing`, and `owner` inputs, resolves ownership if needed, then rewrites the `cache` arg to the canonical resolved cache volume.

This is one of the clearest examples of why the hook exists:

- the caller provided a cache volume handle plus some overrides
- the actual semantic identity is the fully resolved cache volume after those overrides
- the dynamic hook rewrites the request to that canonical form before hashing

### Module function calls

`ModuleFunction.DynamicInputsForCall` is another important example.

It injects object-valued defaults that the caller did not explicitly provide, including:

- contextual args from `defaultPath`
- workspace args
- user defaults from `.env`

This is not just execution-time convenience. These values are pushed into `req.Args` before cache lookup, so they become part of the recipe identity too.

That means two calls that omit an arg but resolve to different contextual/default objects do not accidentally collide in cache.

## Representative implicit-input uses

### Session-scoping mutable image tags

`Container.from` uses a custom implicit input named `fromSessionScope`.

Its rule is:

- if the image address is digest-addressed, use an empty string
- if the image address is tag-based, use the current session ID

That is a nice example of an implicit input depending on explicit args. Digest-addressed refs are immutable and safe to share broadly. Tag-based refs are mutable, so they are only cached within a session.

### Host paths with `noCache`

Several host accessors use:

- `WithInput(dagql.RequestedCacheInput("noCache"))`

That gives them a simple "cache normally unless the caller asked not to" behavior without inventing opaque identity hacks.

### Host services

`Host.service` uses:

- `dagql.PerSessionInput`
- `core.CachePerCallerModule`

That combination means:

- host services do not cross sessions
- different function calls from the same module can still share the same host service identity

`CachePerCallerModule` is a custom implicit input that resolves to the caller module's implementation-scoped content digest, or `"mainClient"` when there is no current module.

This is a good example of implicit inputs being composed, not just used one at a time.

### Lots of "boring" scoping

There are many fields that just use the built-in helpers directly:

- `PerClientInput` for client-local results like host unix sockets, tunnels, workdir access, and similar client-bound views
- `PerSessionInput` for session-bound operations like some HTTP and LLM-related calls
- `PerCallInput` for one-shot behavior like SSH auth socket scoping or explicitly per-invocation operations
- `CurrentSchemaInput` for schema-shaped values like `currentTypeDefs`

These are boring in the best way: simple, declarative, and easy to audit.

## Observability and debuggability

The main design win here is that the extra cache scoping information is modeled explicitly.

Instead of "the digest changed for some mysterious custom reason," we can inspect:

- explicit args
- implicit inputs
- rewritten internal args

in the structured call frame.

That is much easier to debug than directly salting the digest with hidden state.

## Historical note

This is conceptually similar to the older custom cache-config hook, but the current model is better in an important way.

The old style could make the recipe digest change in ways that were effectively opaque. The current design still lets the engine compute extra information dynamically, but it models that information as:

- rewritten args
- explicit implicit inputs
- separate request-policy fields on `CallRequest` when the concern is cache behavior rather than recipe identity

So the cache behavior is still dynamic, but the data model stays understandable.

## Practical guidance

If you are adding or reviewing a field:

- use `WithInput(...)` when the only need is declarative scoping
- use a dynamic input hook when the field needs canonicalization or synthesized args before cache lookup
- prefer adding a named implicit input over mixing hidden state straight into the digest
- remember that dynamic input hooks run before execution and can change cache policy too, not just args
- remember that implicit inputs are recomputed after dynamic arg rewrites

That last rule is easy to miss, and it is one of the most important details in the implementation.
