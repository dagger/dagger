# TypeDefs and the DagQL Cache

This is a focused note on the cache-relevant parts of module typedefs, not a full design doc for the typedef system itself.

## Why typedefs matter so much

Typedefs are on a very hot path.

They are queried and traversed heavily whenever clients inspect the currently served module API, and the CLI does that routinely on connect. In practice, `currentTypeDefs` is one of the heaviest schema/introspection-style queries the engine serves.

That makes typedef representation a real cache and memory problem, not just a schema-modeling detail.

As of this branch, memory usage is much better than it used to be, but typedef-related work is still a major source of overhead. Under `test modules`, mutex profiles are still largely dominated by typedef operations. So this area is improved, but not "done."

## The core rule: always use `dagql.ObjectResult[...]`

The most important implementation rule is:

- do not treat typedefs as a graph of raw pointers passed around directly
- treat them as dagql object results whenever possible

That applies at every layer:

- `Module` stores `ObjectDefs`, `InterfaceDefs`, and `EnumDefs` as `dagql.ObjectResultArray[*TypeDef]`
- `Function.ReturnType` is `dagql.ObjectResult[*TypeDef]`
- `FunctionArg.TypeDef` is `dagql.ObjectResult[*TypeDef]`
- `FieldTypeDef.TypeDef` is `dagql.ObjectResult[*TypeDef]`
- `ListTypeDef.ElementTypeDef` is `dagql.ObjectResult[*TypeDef]`
- nested typedef payloads like `TypeDef.AsObject`, `TypeDef.AsList`, `ObjectTypeDef.Fields`, `ObjectTypeDef.Functions`, `InputTypeDef.Fields`, and `EnumTypeDef.Members` all use object-result wrappers too

This is what keeps typedef identity flowing through the dagql cache instead of bypassing it.

## Why that rule matters

Using attached object results buys several things at once:

- canonical cache entries for equivalent typedef nodes
- lower memory usage from reusing shared results instead of repeatedly rebuilding parallel pointer trees
- less duplicate work when the same typedef fragments are revisited many times
- stable identity via attached result IDs for downstream logic that wants memoization or deduplication

If code falls back to raw detached pointers too early, all of those wins disappear.

## How typedefs become canonical cached results

Typedef construction is intentionally routed through dagql selectors rather than ad hoc allocation at the call sites that matter.

There is a small family of builder fields rooted on `Query`, including:

- `typeDef`
- `function`
- `__functionArg`
- `__functionArgExact`
- `__fieldTypeDef`
- `__fieldTypeDefExact`
- `__listTypeDef`
- `__objectTypeDef`
- `__interfaceTypeDef`
- `__inputTypeDef`
- `__scalarTypeDef`
- `__enumTypeDef`
- `__enumMemberTypeDef`

The normal pattern is:

1. select a base typedef or typedef sub-object through dagql
2. apply `with...` / `__with...` selectors to produce the next immutable shape
3. keep carrying the resulting `dagql.ObjectResult[...]`

Examples:

- `core.SelectTypeDefWithServer(...)`
- `PrimitiveType.TypeDef(...)`
- `ListType.TypeDef(...)`
- `NullableType.TypeDef(...)`
- the various schema helpers in `core/schema/module.go`

Because these go through normal dagql selection and normal cache lookup, typedef fragments get canonical cache-backed result identities instead of proliferating detached copies.

## Canonical names are part of the model

`TypeDef.Name` is the canonical non-optional name of the type. That is not just presentation metadata; it is part of how higher-level typedef queries deduplicate and rebind references.

The `TypeDef` mutators all call `syncName()` so that when a typedef changes shape:

- `Kind`
- `Optional`
- `AsList`
- `AsObject`
- `AsInterface`
- `AsInput`
- `AsScalar`
- `AsEnum`

its canonical name stays synchronized with the actual structure.

This is especially important for:

- list typedefs, whose names encode the element type
- optional typedefs, whose canonical output handling needs a stable non-optional name for deduplication

## `currentTypeDefs` is the hot path

`currentTypeDefs` is the main engine entry point for serving typedefs to clients.

Some important cache-facing properties of that field:

- it is registered with `dagql.CurrentSchemaInput`, so its cache key is tied to the current served schema
- it loads typedefs from `CurrentServedDeps`
- it synthesizes the live `Query` typedef from the actual current dagql schema and splices that into the result set
- it can optionally expand from top-level served types to the full referenced typedef closure with `returnAllTypes: true`

That schema-sensitive cache key is important. Clients call this query frequently, but the result must still invalidate when the served schema changes.

## `returnAllTypes: true` does more than just recurse

The `returnAllTypes` path is not a dumb tree walk. It does a normalization and deduplication pass that matters a lot for downstream memory and correctness.

It:

- normalizes optional typedefs to their non-optional canonical form before closure expansion
- deduplicates by `TypeDef.Name`
- prefers a fuller typedef over a stub typedef when both share the same canonical name
- returns a stable ordered list of canonical typedef objects

The closure walk then follows:

- list element types
- object fields
- object functions
- object constructor args and return types
- interface functions
- input fields

That gives the CLI and SDKs a canonical typedef set instead of a bag of shallow duplicate references.

## Why the CLI cares

The CLI's module inspection flow depends directly on `currentTypeDefs`, and on the canonical names returned by `currentTypeDefs(returnAllTypes: true)`.

The relevant behavior is:

- load the typedef list from `currentTypeDefs`
- index it by canonical type name
- rebind shallow typedef refs onto the canonical full typedef entries

That is why the engine side is careful to return:

- non-nil typedefs
- canonical names
- deduplicated closure entries

Without that, the CLI would keep rebuilding and traversing redundant typedef graphs on its side too.

## Canonical wrappers are preserved through the public schema

One subtle but important improvement in this branch is that typedef sub-objects are consistently exposed through `dagql.ObjectResult[...]` in the schema instead of being unwrapped into plain pointers too early.

`core/schema/module_typedef_canonical.go` is basically the expression of that rule. Resolvers like:

- `functionReturnType`
- `functionArgTypeDef`
- `fieldTypeDefTypeDef`
- `objectTypeDefFields`
- `objectTypeDefFunctions`
- `objectTypeDefConstructor`
- `interfaceTypeDefFunctions`
- `listElementTypeDef`
- `inputTypeDefFields`
- `enumTypeDefMembers`

all return the stored object-result wrappers directly.

That keeps schema traversal on the canonical cache-backed identities all the way through.

## Attached-result identity is used downstream

The benefits are not only theoretical.

Module validation and related typedef lookups explicitly memoize by attached result identity when available:

- `moduleValidationState.validatedAttached`
- `moduleValidationState.modTypeAttached`

Those maps are keyed by `EngineResultID`.

Only when a typedef is detached does the code fall back to plain pointer identity.

That is a good concise summary of the broader design:

- attached typedef results get strong canonical identity
- detached typedef pointers are second-class fallback identity

## Typedefs participate in normal dagql dependency attachment

Typedef objects also implement `dagql.HasDependencyResults`, which means their nested typedef/function/field/member references are normalized onto attached results when they enter the cache.

That applies to:

- `TypeDef`
- `Function`
- `FunctionArg`
- `ObjectTypeDef`
- `FieldTypeDef`
- `InterfaceTypeDef`
- `ListTypeDef`
- `InputTypeDef`
- `EnumTypeDef`
- `EnumMemberTypeDef`

So the canonical object-result model is not just a calling convention. It is part of how dagql attaches, tracks, persists, and deduplicates the typedef graph.

## Simple leaf fields are intentionally cheap

Many leaf metadata fields on typedef-related objects are tagged as simple `doNotCache` field selections, for example:

- names
- descriptions
- kind
- optional flags

That keeps the cache focused on the structural typedef objects themselves rather than filling up with lots of trivial scalar leaf entries.

The expensive part is the typedef graph. That graph is what we want canonicalized.

## Current state

The practical state of things on this branch is:

- the engine now does a much better job of routing typedefs through canonical dagql object results
- this materially improved memory usage
- `currentTypeDefs` and typedef-building operations are still a significant performance hotspot
- telemetry treats many of these builder paths as introspection noise because they are so chatty and so frequent

So the current design is a meaningful improvement, but typedef handling remains an area with real room for future optimization.
