# WHITEBOARD

## Agreement

## TODO
* Assess changeset merge decision to always use git path (removed `conflicts.IsEmpty()` no-git fast path), with specific focus on performance impact
   * Compare runtime/cost of old no-git path vs current always-git path in no-conflict workloads
   * Confirm whether correctness/cohesion benefits outweigh any measured regression and document outcome
* Remove internal `__immutableRef` schema API once and for all
   * Replace remaining stable-ID use cases with a cleaner non-internal API pattern in dagql/core
* Review the new HTTP implementation for clarity/cohesion
   * Current implementation is functional but confusing; do a low-priority cleanup pass
* Fix `query.__schemaJSONFile` implementation to avoid embedding megabytes of file contents in query args
   * Build/write via ref/snapshot path directly instead of passing huge inline string payloads through select args
* Clean up `cloneContainerForTerminal` usage
   * Find a cleaner container-child pattern for terminal/service callsites instead of special clone helper
* replacing CurrentOpOpts CauseCtx with trace.SpanContextFromContext seems sus, needs checking
* Reassess file mutator parent-passing + lazy-init shape (`WithName`/`WithTimestamps`/`Chown`/`WithReplaced`)
   * Current implementation passes parent object results through schema into core and appears correct in tests, but may not be the most cohesive long-term model.
   * Follow-up: revisit whether lazy-init/parent snapshot modeling can eliminate this explicit parent threading while preserving correctness for service-backed files.
* Assess whether we dropped any git lazyness (especially tree) and whether we should restore it
* Assess whether we really want persistent cache for every schema json file, that's probably a lot of files that are actually kinda sizable!
* Find a way to enable pruning of filesync mirror snapshots
   * Pretty sure filesync mirrors are currently not accounted for by dagql prune/usage accounting.

## Notes
* **THE DAGQL CACHE IS A SINGLETON CREATED ONCE AT ENGINE START AND IT LIVES FOR THE ENTIRE LIFETIME OF THE ENGINE.**
  * There is not a second DAGQL cache.
  * There is not a per-session DAGQL cache.
  * Result-call planning/runtime code should not be written as if cache identity were ambiguous.
  * If a code path needs the DAGQL cache, it should explicitly use or fetch the singleton cache rather than storing mutable cache backpointers on frame/helper structs.
* For persistence, it's basically like an export. Don't try to store in-engine numeric ids or something, it's the whole DAG persisted in single-engine-agnostic manner. When loading from persisted disk, you are importing it (including e-graph union and stuff)
  * But also for now, let's be biased towards keeping everything in memory rather than trying to do fancy page out to disk

* **CRITICAL CACHE MODEL RULE: OVERLAPPING DIGESTS MEAN EQUALITY AND FULL INTERCHANGEABILITY.**
  * If two values share any digest / end up in the same digest-equivalence set, that is not merely "evidence" or "similarity"; it means they are the same value for dagql cache purposes and may be reused interchangeably.

* A lot of eval'ing of lazy stuff is just triggered inline now; would be nice if dagql cache scheduler knew about these and could do that in parallel for ya
   * This is partially a pre-existing condition though, so not a big deal yet. But will probably make a great optimization in the near-ish future
* Moved the GC + ref counting + dependency tracking refactor notes to `scratch/GC-refactor.md` so this file can stay focused on the next design/debugging pass.
* Moved the Laziness Refactor notes to `scratch/laziness.md` so this file can stay focused on prune.
* Moved the Prune Refactor notes to `scratch/prunerefactor.md` so this file can stay focused on typedef performance.

# Making TypeDefs Highly Performant

## Design

### Goal

The typedef family should stop storing nested typedef/function/source-map graphs as
bare nested structs and start storing canonical attached dagql object results for
those nested children.

The intended hard cut is:

* nested ideable typedef children are canonicalized at construction time
* typedef family objects store those canonical children as `dagql.ObjectResult`
  or `dagql.ObjectResultArray`
* typedef family objects declare those embedded canonical children through
  `AttachDependencyResults`
* read paths reuse the already-canonical child result refs directly
* persistence stores child result IDs instead of recursively embedding the full
  nested payload graph
* post-construction passes like namespacing and patching must stop mutating
  shared nested children in place

This is explicitly a write-time canonicalization model, not a read-time
re-canonicalization model.

### Why the current design is expensive

Today the typedef family stores nested ideable children as bare pointers and
slices of bare pointers.

That creates three large costs.

1. Deep cloning.

   `Clone()` on the typedef family recursively duplicates nested typedef,
   function, field, enum, and source-map graphs. This creates memory churn and
   duplicate object graphs even when the nested child values are already
   semantically canonical.

2. Read-time re-canonicalization.

   `core/schema/module_typedef_canonical.go` repeatedly walks those nested bare
   graphs and reconstructs canonical dagql objects on demand. That means field
   accessors like `function.returnType`, `function.args`, `field.typeDef`,
   `object.functions`, `enum.members`, and the `currentTypeDefs` tree all pay a
   recursive canonicalization tax on read.

3. Recursive inline persistence.

   The typedef family persisted payloads currently embed nested typedef/function
   graphs recursively rather than storing references to already-canonical child
   results. That multiplies the amount of serialization work and prevents us
   from reusing the canonical attached children the cache already knows about.

The result is that the same conceptual typedef graph is repeatedly:

* cloned
* recursively walked
* recursively canonicalized
* recursively persisted

instead of being built once and then referenced.

### Full inventory of affected bare ideable fields

These are the typedef-related struct fields that currently store bare ideable
children and need to change.

`Function`
* `Args []*FunctionArg`
* `ReturnType *TypeDef`
* `SourceMap dagql.Nullable[*SourceMap]`

`FunctionArg`
* `SourceMap dagql.Nullable[*SourceMap]`
* `TypeDef *TypeDef`

`TypeDef`
* `AsList dagql.Nullable[*ListTypeDef]`
* `AsObject dagql.Nullable[*ObjectTypeDef]`
* `AsInterface dagql.Nullable[*InterfaceTypeDef]`
* `AsInput dagql.Nullable[*InputTypeDef]`
* `AsScalar dagql.Nullable[*ScalarTypeDef]`
* `AsEnum dagql.Nullable[*EnumTypeDef]`

`ObjectTypeDef`
* `SourceMap dagql.Nullable[*SourceMap]`
* `Fields []*FieldTypeDef`
* `Functions []*Function`
* `Constructor dagql.Nullable[*Function]`

`FieldTypeDef`
* `TypeDef *TypeDef`
* `SourceMap dagql.Nullable[*SourceMap]`

`InterfaceTypeDef`
* `SourceMap dagql.Nullable[*SourceMap]`
* `Functions []*Function`

`ListTypeDef`
* `ElementTypeDef *TypeDef`

`InputTypeDef`
* `Fields []*FieldTypeDef`

`EnumTypeDef`
* `Members []*EnumMemberTypeDef`
* `SourceMap dagql.Nullable[*SourceMap]`

`EnumMemberTypeDef`
* `SourceMap dagql.Nullable[*SourceMap]`

These are the fields this refactor is about. We are not including unrelated
structures like `FunctionCall` in this pass.

### Target field shapes

The end state should be:

`Function`
* `Args dagql.ObjectResultArray[*FunctionArg]`
* `ReturnType dagql.ObjectResult[*TypeDef]`
* `SourceMap dagql.Nullable[dagql.ObjectResult[*SourceMap]]`

`FunctionArg`
* `SourceMap dagql.Nullable[dagql.ObjectResult[*SourceMap]]`
* `TypeDef dagql.ObjectResult[*TypeDef]`

`TypeDef`
* `AsList dagql.Nullable[dagql.ObjectResult[*ListTypeDef]]`
* `AsObject dagql.Nullable[dagql.ObjectResult[*ObjectTypeDef]]`
* `AsInterface dagql.Nullable[dagql.ObjectResult[*InterfaceTypeDef]]`
* `AsInput dagql.Nullable[dagql.ObjectResult[*InputTypeDef]]`
* `AsScalar dagql.Nullable[dagql.ObjectResult[*ScalarTypeDef]]`
* `AsEnum dagql.Nullable[dagql.ObjectResult[*EnumTypeDef]]`

`ObjectTypeDef`
* `SourceMap dagql.Nullable[dagql.ObjectResult[*SourceMap]]`
* `Fields dagql.ObjectResultArray[*FieldTypeDef]`
* `Functions dagql.ObjectResultArray[*Function]`
* `Constructor dagql.Nullable[dagql.ObjectResult[*Function]]`

`FieldTypeDef`
* `TypeDef dagql.ObjectResult[*TypeDef]`
* `SourceMap dagql.Nullable[dagql.ObjectResult[*SourceMap]]`

`InterfaceTypeDef`
* `SourceMap dagql.Nullable[dagql.ObjectResult[*SourceMap]]`
* `Functions dagql.ObjectResultArray[*Function]`

`ListTypeDef`
* `ElementTypeDef dagql.ObjectResult[*TypeDef]`

`InputTypeDef`
* `Fields dagql.ObjectResultArray[*FieldTypeDef]`

`EnumTypeDef`
* `Members dagql.ObjectResultArray[*EnumMemberTypeDef]`
* `SourceMap dagql.Nullable[dagql.ObjectResult[*SourceMap]]`

`EnumMemberTypeDef`
* `SourceMap dagql.Nullable[dagql.ObjectResult[*SourceMap]]`

That means nested typedef-family relationships become attached reference
relationships rather than embedded bare-struct ownership.

### Construction-time canonicalization

Canonicalization should move to the write path.

Specifically:

* schema constructors and mutators should load child IDs as attached object
  results
* those attached child results should be stored directly in the parent typedef
  struct fields
* top-level typedef creation and mutation APIs should become the point where
  nested children are normalized into canonical refs

This means that functions like:

* `typeDefWithListOf`
* `typeDefWithObjectField`
* `typeDefWithFunction`
* `typeDefWithObjectConstructor`
* `function`
* `functionWithArg`
* `functionWithSourceMap`

must stop passing bare `.Self()` values into core and must instead pass the
attached object results themselves.

The corresponding core mutators should then accept and store those attached
results directly.

After that change, nested children inside a typedef family object are already
canonical when the object is created. Later field selections should not need to
canonicalize them again.

### Internal constructor surface

Once nested typedef-family children are stored as attached object results, the
schema needs an internal constructor surface that can build those child objects
directly as canonical dagql results without exposing any new public API.

These constructors should use internal `__*` field names on `Query` and should
exist specifically so schema write paths can produce canonical child object
results at construction time.

At minimum, the design expects internal constructors for:

* `Query.__functionArg(...) -> FunctionArg`
* `Query.__fieldTypeDef(...) -> FieldTypeDef`
* `Query.__enumMemberTypeDef(...) -> EnumMemberTypeDef`
* `Query.__listTypeDef(...) -> ListTypeDef`
* `Query.__objectTypeDef(...) -> ObjectTypeDef`
* `Query.__interfaceTypeDef(...) -> InterfaceTypeDef`
* `Query.__inputTypeDef(...) -> InputTypeDef`
* `Query.__scalarTypeDef(...) -> ScalarTypeDef`
* `Query.__enumTypeDef(...) -> EnumTypeDef`

The exact argument lists for those constructors should mirror the fields needed
to create each leaf/subtype object in its canonical form.

Examples:

* `__functionArg` should accept the canonical child refs it stores:
  * `name`
  * `description`
  * `typeDef`
  * `defaultValue`
  * `defaultPath`
  * `defaultAddress`
  * `ignore`
  * `sourceMap`
  * `deprecated`
* `__fieldTypeDef` should accept:
  * `name`
  * `description`
  * `typeDef`
  * `sourceMap`
  * `deprecated`
* `__enumMemberTypeDef` should accept:
  * `name`
  * `value`
  * `description`
  * `sourceMap`
  * `deprecated`
* `__listTypeDef` should accept:
  * `elementTypeDef`
* `__objectTypeDef` should accept:
  * `name`
  * `description`
  * `sourceMap`
  * `deprecated`
  * `sourceModuleName`
* `__interfaceTypeDef` should accept:
  * `name`
  * `description`
  * `sourceMap`
  * `sourceModuleName`
* `__inputTypeDef` should at minimum accept:
  * `name`
* `__scalarTypeDef` should accept:
  * `name`
  * `description`
  * `sourceModuleName`
* `__enumTypeDef` should accept:
  * `name`
  * `description`
  * `sourceMap`
  * `sourceModuleName`

These are internal implementation constructors. They are not intended to become
part of the public schema surface.

### Internal subtype mutator surface

Internal constructors alone are not sufficient.

Once `TypeDef.AsObject`, `AsInterface`, `AsInput`, `AsEnum`, and `AsList` store
attached child results, mutating those nested subtype `.Self()` values in place
becomes incoherent because those children are now shared canonical results.

So the schema also needs internal subtype mutators that are:

* copy-on-write
* return a new subtype result
* never mutate a previously-shared canonical child in place

At minimum, the design expects:

* `ObjectTypeDef.__withField(...) -> ObjectTypeDef`
* `ObjectTypeDef.__withFunction(...) -> ObjectTypeDef`
* `ObjectTypeDef.__withConstructor(...) -> ObjectTypeDef`
* `InterfaceTypeDef.__withFunction(...) -> InterfaceTypeDef`
* `InputTypeDef.__withField(...) -> InputTypeDef`
* `EnumTypeDef.__withMember(...) -> EnumTypeDef`
* `Function.__withArg(...) -> Function`
* `FunctionArg.__withTypeDef(...) -> FunctionArg`
* `FunctionArg.__withDefaultValue(...) -> FunctionArg`
* `FunctionArg.__withDefaultPath(...) -> FunctionArg`
* `FunctionArg.__withDefaultAddress(...) -> FunctionArg`
* `FunctionArg.__withIgnore(...) -> FunctionArg`

The subtype mutators should accept canonical attached child refs, not bare
children.

Examples:

* `ObjectTypeDef.__withField` should take a canonical `FieldTypeDef` result
* `ObjectTypeDef.__withFunction` should take a canonical `Function` result
* `ObjectTypeDef.__withConstructor` should take a canonical `Function` result
* `InterfaceTypeDef.__withFunction` should take a canonical `Function` result
* `InputTypeDef.__withField` should take a canonical `FieldTypeDef` result
* `EnumTypeDef.__withMember` should take a canonical `EnumMemberTypeDef` result
* `Function.__withArg` should take a canonical `FunctionArg` result and return
  a new canonical `Function` result containing that arg
* `FunctionArg.__withTypeDef` should take a canonical `TypeDef` result and
  return a new canonical `FunctionArg` result with the updated type ref
* `FunctionArg.__withDefaultValue` should take the new default JSON payload and
  return a new canonical `FunctionArg` result with that default value
* `FunctionArg.__withDefaultPath` should take the new contextual default path
  and return a new canonical `FunctionArg` result with that path
* `FunctionArg.__withDefaultAddress` should take the new contextual default
  address and return a new canonical `FunctionArg` result with that address
* `FunctionArg.__withIgnore` should take the new ignore-pattern list and return
  a new canonical `FunctionArg` result with those patterns

Then the parent `TypeDef`-level public mutators can be implemented coherently as
follows:

1. Build the canonical leaf or subtype child result using the internal
   constructor surface.
2. If a nested subtype already exists, call the internal subtype mutator to get
   a new canonical subtype result that includes the change.
3. Replace the parent `TypeDef.As*` child ref with that new canonical subtype
   result.

This gives us:

* write-time canonicalization
* no in-place mutation of shared nested child refs
* a cohesive path for namespacing/patch-style transformations later

The same copy-on-write rule also applies to later function metadata rewrites.

There are internal flows, especially module user-default handling, that need to
make function arguments optional or inject default values so those defaults are
visible in typedef introspection.

That used to work by mutating nested bare structs in place:

* mutating `FunctionArg.TypeDef.Optional`
* mutating `FunctionArg.DefaultValue`
* mutating `FunctionArg.DefaultPath`
* mutating `FunctionArg.DefaultAddress`
* mutating `FunctionArg.Ignore`
* mutating a `Function`'s `Args` slice in place

After the hard cut, that is no longer coherent because `Function.Args`,
`FunctionArg.TypeDef`, and nested typedef children are all canonical shared
refs.

So the design rule is:

* post-construction function metadata rewrites must also be expressed as
  copy-on-write transformations over canonical refs

Concretely, user-default visibility in typedef introspection should be
implemented by:

1. Building a new canonical optional typedef result when an arg type needs to
   become optional, e.g. through the existing `withOptional(true)` path.
2. Building a new canonical `FunctionArg` result through
   `FunctionArg.__withTypeDef(...)` and/or
   `FunctionArg.__withDefaultValue(...)` and/or
   `FunctionArg.__withDefaultPath(...)` and/or
   `FunctionArg.__withDefaultAddress(...)` and/or
   `FunctionArg.__withIgnore(...)`.
3. Building a new canonical `Function` result through `Function.__withArg(...)`
   that replaces the old arg result with the updated one.
4. Replacing the owning object/interface constructor or function list entry with
   that new canonical function result rather than mutating the previous shared
   child in place.

This is the same design family as namespacing and patching:

* no post-publication mutation of shared child `.Self()` values
* every later metadata rewrite must become a pure transformation that returns
  new canonical parent results while reusing unchanged child refs

### Broader internal mutator surface for module-level rewrites

The first wave of internal mutators is still not sufficient for the full hard
cut.

Once `core/module.go` stops treating nested typedef-family children as embedded
mutable structs, we also need low-level copy-on-write primitives for the kinds
of edits that namespacing, ownership stamping, patching, and source-map rebasing
actually perform.

The important design rule is:

* `core/module.go` should own the *policy* of module-local rewrites
* typedef-family objects should expose only the *copy-on-write edit
  primitives* needed to carry out those rewrites coherently

So the next required internal mutator surface should include at minimum:

* `ObjectTypeDef.__withName(...) -> ObjectTypeDef`
* `ObjectTypeDef.__withSourceMap(...) -> ObjectTypeDef`
* `ObjectTypeDef.__withSourceModuleName(...) -> ObjectTypeDef`
* `FieldTypeDef.__withTypeDef(...) -> FieldTypeDef`
* `FieldTypeDef.__withSourceMap(...) -> FieldTypeDef`
* `Function.__withReturnType(...) -> Function`
* `Function.__withSourceMap(...) -> Function`
* `Function.__withArg(...) -> Function`
* `FunctionArg.__withTypeDef(...) -> FunctionArg`
* `FunctionArg.__withSourceMap(...) -> FunctionArg`
* `FunctionArg.__withDefaultValue(...) -> FunctionArg`
* `InterfaceTypeDef.__withName(...) -> InterfaceTypeDef`
* `InterfaceTypeDef.__withSourceMap(...) -> InterfaceTypeDef`
* `InterfaceTypeDef.__withSourceModuleName(...) -> InterfaceTypeDef`
* `InterfaceTypeDef.__withFunction(...) -> InterfaceTypeDef`
* `ListTypeDef.__withElementTypeDef(...) -> ListTypeDef`
* `EnumTypeDef.__withName(...) -> EnumTypeDef`
* `EnumTypeDef.__withSourceMap(...) -> EnumTypeDef`
* `EnumTypeDef.__withSourceModuleName(...) -> EnumTypeDef`
* `EnumTypeDef.__withMember(...) -> EnumTypeDef`
* `EnumMemberTypeDef.__withName(...) -> EnumMemberTypeDef`
* `EnumMemberTypeDef.__withSourceMap(...) -> EnumMemberTypeDef`

These should all remain internal schema-only mutators.

They are not intended to become public API; they exist so module-level rewrite
passes can stay purely transformational without mutating shared child `.Self()`
values in place.

#### SourceMap treatment

`SourceMap` is also a shared child ref after the cut, so source-map rebasing
cannot mutate the existing `SourceMap` payload in place either.

The cleaner design is:

* rebuild transformed source maps through the constructor path
* either reuse the existing public `sourceMap(...)` constructor or introduce a
  private `__sourceMap(...)` if we want to keep this rewrite surface fully
  internal

The main rule matters more than the exact field name:

* source-map rewrites should produce a new canonical `SourceMap` result when
  any field changes
* unchanged source-map results should be reused as-is

#### `TypeDef` itself also needs internal child-ref replacement mutators

There is one more crucial implication for module-level pure transformations.

Once nested `TypeDef` refs are themselves shared canonical `ObjectResult`s,
module-level rewrites cannot coherently rebuild a transformed shared `TypeDef`
result if the only available API is the current public constructor-style
surface:

* `withListOf(elementType: ID)`
* `withObject(name, description, ...)`
* `withInterface(name, description, ...)`
* `withEnum(name, description, ...)`

Those public APIs are fine for ordinary write-time construction, but they are
not sufficient for pure rewrite passes that already have a transformed canonical
child subtype result in hand and need to replace the existing `As*` ref without:

* mutating `typeDef.Self().As*` in place, or
* replaying a lossy reconstruction of the subtype from raw scalar fields

So the design also needs internal `TypeDef` child-ref replacement mutators such
as:

* `TypeDef.__withListTypeDef(...) -> TypeDef`
* `TypeDef.__withObjectTypeDef(...) -> TypeDef`
* `TypeDef.__withInterfaceTypeDef(...) -> TypeDef`
* `TypeDef.__withInputTypeDef(...) -> TypeDef`
* `TypeDef.__withScalarTypeDef(...) -> TypeDef`
* `TypeDef.__withEnumTypeDef(...) -> TypeDef`

These are distinct from the existing public constructor-style mutators.

Their role is:

* accept an already-canonical child subtype result
* return a new canonical `TypeDef` result whose `As*` ref points at that child
* preserve the rest of the `TypeDef` state such as `Kind` and `Optional`

This is what will let module-level pure transformation passes update nested
shared `TypeDef` refs coherently when a child subtype result has changed.

### Public mutators become wrappers over internal constructors/mutators

The existing public `TypeDef`/`Function` schema mutators should remain the
public API, but their implementation model changes.

Instead of directly constructing or mutating nested bare structs, they should:

* load attached child refs from IDs
* call the internal `__*` constructors and subtype mutators
* store the returned canonical child refs on the parent object

Examples:

* `typeDefWithListOf` should build a canonical `ListTypeDef` result via
  `Query.__listTypeDef` and store it in `TypeDef.AsList`
* `typeDefWithObject` should build a canonical `ObjectTypeDef` result via
  `Query.__objectTypeDef` and store it in `TypeDef.AsObject`
* `typeDefWithObjectField` should:
  * build a canonical `FieldTypeDef` result via `Query.__fieldTypeDef`
  * call `ObjectTypeDef.__withField`
  * replace `TypeDef.AsObject` with the returned canonical `ObjectTypeDef`
* `typeDefWithFunction` should:
  * call `ObjectTypeDef.__withFunction` or `InterfaceTypeDef.__withFunction`
  * replace the relevant `TypeDef.As*` child ref
* `typeDefWithObjectConstructor` should:
  * canonicalize the constructor function result
  * call `ObjectTypeDef.__withConstructor`
  * replace `TypeDef.AsObject`
* `typeDefWithEnumMember` should:
  * build a canonical `EnumMemberTypeDef` via `Query.__enumMemberTypeDef`
  * call `EnumTypeDef.__withMember`
  * replace `TypeDef.AsEnum`
* `functionWithArg` should build a canonical `FunctionArg` via `Query.__functionArg`
  and append it through a copy-on-write `Function` mutator rather than embedding
  a bare struct

The exact same principle applies to source-map-bearing objects and any other
typedef-family child that becomes result-backed.

### Dependency attachment contract

Once typedef family objects embed canonical child result refs, they must declare
those refs as dependency results.

Every typedef-family object that stores child `ObjectResult` or
`ObjectResultArray` fields should implement `AttachDependencyResults`.

That includes:

* `Function`
* `FunctionArg`
* `TypeDef`
* `ObjectTypeDef`
* `FieldTypeDef`
* `InterfaceTypeDef`
* `ListTypeDef`
* `InputTypeDef`
* `EnumTypeDef`
* `EnumMemberTypeDef`

Those implementations should:

* attach/normalize the embedded child results in place
* rewrite the struct fields with the attached versions
* return the full set of embedded child refs

This is what makes the embedding relationship honest to dagql cache:

* these typedef objects now literally store result refs in their payload
* therefore cache needs to know those are dependency results

### Clone semantics after the cut

`Clone()` on the typedef family should stop recursively cloning nested ideable
subgraphs.

After the cut, clone should mostly do this:

* copy the outer struct
* clone slices of object results where needed
* preserve the nested child object results themselves as-is

For example:

* `Function.Clone` should stop cloning `ReturnType` and each `FunctionArg`
  recursively; it should copy the `ObjectResultArray[*FunctionArg]` and the
  `ObjectResult[*TypeDef]`
* `TypeDef.Clone` should stop recursively cloning `AsList/AsObject/...`
  payloads; it should preserve the child object result wrappers
* `ObjectTypeDef.Clone` should stop recursively cloning nested functions, fields,
  constructor, and source map payloads; it should copy their result wrapper
  slices/values only

This is one of the primary performance wins. It prevents exponential-looking
object duplication when module typedef graphs are copied around.

### Persistence model

The current typedef persistence is deeply recursive. That should be removed.

The typedef family should persist nested ideable children exactly the same way
other core objects already persist attached child refs:

* encode child object-result references as persisted result IDs
* decode those result IDs back into attached object results via
  `loadPersistedObjectResultByResultID`

So instead of recursive persisted payload structs like:

* `persistedFunctionArg{ TypeDef *persistedTypeDef }`
* `persistedFunction{ ReturnType *persistedTypeDef, Args []*persistedFunctionArg }`
* `persistedTypeDef{ AsObject *persistedObjectTypeDef, ... }`

we want result-ID based payload fields like:

* `TypeDefResultID uint64`
* `ReturnTypeResultID uint64`
* `ArgResultIDs []uint64`
* `AsObjectResultID uint64`
* `FieldResultIDs []uint64`
* `FunctionResultIDs []uint64`
* `ConstructorResultID uint64`
* `SourceMapResultID uint64`
* `MemberResultIDs []uint64`

This should use the same persistence helpers and conventions already used by:

* `Module`
* `ModuleSource`
* `Container`
* `GitRepository`
* `GitRef`

The typedef family should not maintain its own special recursive persistence
subsystem once nested children are attached results.

### Read-path simplification in `module_typedef_canonical.go`

After the cut, `core/schema/module_typedef_canonical.go` should stop
re-canonicalizing nested children on read.

Field accessors that currently rebuild child refs should instead just reuse the
stored attached object results directly.

Examples:

* `functionReturnType` should become `return fn.ReturnType, nil`
* `functionArgs` should become `return fn.Args, nil`
* `fieldTypeDefTypeDef` should become `return field.TypeDef, nil`
* `objectTypeDefFields` should become `return obj.Fields, nil`
* `objectTypeDefFunctions` should become `return obj.Functions, nil`
* `objectTypeDefConstructor` should become `return obj.Constructor, nil`
* `interfaceTypeDefFunctions` should become `return iface.Functions, nil`
* `listElementTypeDef` should become `return list.ElementTypeDef, nil`
* `inputTypeDefFields` should become `return input.Fields, nil`
* `enumTypeDefMembers` should become `return enum.Members, nil`
* `typeDefAsList/AsObject/AsInterface/AsInput/AsScalar/AsEnum` should return
  the stored nullable child result directly

The existing recursive helpers like `canonicalTypeDefRef` and `canonicalFunction`
should shrink dramatically and should stop being used in ordinary nested read
paths.

The intended model is:

* top-level canonicalization may still exist where truly needed
* nested field access should never recursively rebuild what was already
  canonicalized and stored earlier

### Namespacing and patching must stop mutating shared children in place

This is the most important behavioral constraint of the refactor.

Today, `namespaceTypeDef` and `Patch` recursively walk bare nested children and
mutate them in place.

That becomes incoherent once nested children are canonical attached results,
because then mutating `child.Self()` mutates a shared canonical child reference
that may be reused from multiple parents.

So yes: `namespaceTypeDef` and `Patch` should be rethought as cohesive,
cache-friendly transformation APIs rather than in-place recursive mutation over
shared children.

The rule after the cut should be:

* no post-construction pass may recursively mutate nested shared child refs in
  place

Instead, normalization should happen in one of two ways.

1. Preferably, before publication.

   The module typedef graph should be normalized into its final namespaced,
   module-owned form before it is stored as the canonical nested-child graph.

2. If a later transformation is still necessary, it must be expressed as a pure
   copy-on-write API.

   That API should:
   * take a top-level typedef family object
   * return a new top-level typedef family object
   * reuse existing child refs where unchanged
   * create new canonical child refs only where some actual field changed
   * never mutate previously-shared child `.Self()` values in place

This could take the form of new top-level normalization helpers on the typedef
family, but the core design rule matters more than the exact method name.

The point is that namespacing, ownership stamping, source-map rebasing, and enum
patching must become pure transformations over canonical refs, not imperative
mutation passes over a shared nested object graph.

The same rule applies to later function metadata rewrites such as:

* user-default introspection visibility
* optionalizing object args for user defaults
* setting or changing `FunctionArg.DefaultValue`
* setting or changing `FunctionArg.DefaultPath`
* setting or changing `FunctionArg.DefaultAddress`
* setting or changing `FunctionArg.Ignore`

Those too must become copy-on-write canonical transformations rather than
in-place mutation of shared child refs.

The same rule also applies to module-source and toolchain argument
customizations.

In particular, helpers like `applyArgumentConfigToFunction` in
`core/schema/modulesource.go` must stop mutating:

* `FunctionArg.DefaultValue`
* `FunctionArg.DefaultPath`
* `FunctionArg.DefaultAddress`
* `FunctionArg.Ignore`

on shared canonical child refs in place.

Instead they must:

1. operate on canonical `Function` / `FunctionArg` results,
2. build updated arg results through the internal arg mutators above,
3. rebuild the owning function through `Function.__withArg(...)`,
4. rebuild any owning object typedefs or top-level typedef refs through the
   corresponding internal mutators,
5. and reuse unchanged refs as-is.

### Module-level pure transformation pipeline

The important consequence for `core/module.go` is that it should stop behaving
like an imperative nested-typedef editor.

Instead, it should become an orchestrator of pure copy-on-write
transformations over canonical typedef refs.

The intended shape is:

* `core/module.go` owns the high-level module policy:
  * namespacing
  * ownership stamping
  * source-map rebasing
  * enum-default patching
  * user-default introspection visibility
* low-level copy-on-write edits are delegated to the internal mutator surface
  described above

That means the next generation of module helpers should look more like:

* `stampOwnedTypeRefs(ctx, dag, dagql.ObjectResult[*TypeDef]) (dagql.ObjectResult[*TypeDef], error)`
* `namespaceTypeDef(ctx, dag, modPath, dagql.ObjectResult[*TypeDef]) (dagql.ObjectResult[*TypeDef], error)`
* `patchTypeDef(ctx, dag, dagql.ObjectResult[*TypeDef]) (dagql.ObjectResult[*TypeDef], error)`
* `namespaceSourceMap(ctx, dag, modPath, dagql.ObjectResult[*SourceMap]) (dagql.ObjectResult[*SourceMap], error)`

And then narrower subtype-specific helpers beneath those:

* `namespaceObjectTypeDef`
* `namespaceInterfaceTypeDef`
* `namespaceEnumTypeDef`
* `namespaceFunction`
* `namespaceFunctionArg`
* `namespaceFieldTypeDef`
* `patchFunction`
* `patchFunctionArg`

The contract for every such helper is:

1. Recurse through child refs first.
2. Determine whether anything semantically changed.
3. If nothing changed, return the original canonical result unchanged.
4. If anything changed, build a new canonical result by applying only the
   smallest necessary internal mutators.
5. Reuse unchanged child refs exactly as-is.

That "return the original result unchanged if nothing changed" rule is
important for performance and identity stability.

### High-level transformation responsibilities

#### Ownership stamping

Ownership stamping should:

* only stamp `SourceModuleName` on locally-owned object/interface/enum defs
* avoid mutating dependency/core types
* recurse through child refs only so that nested local typedefs can be stamped
  coherently too

#### Namespacing

Namespacing should:

* only rename locally-owned object/interface/enum defs
* only rebase source maps on locally-owned nodes
* recursively update:
  * list element typedef refs
  * field typedef refs
  * function return typedef refs
  * function arg typedef refs
  * source-map refs on object/interface/function/arg/field/enum/member
* leave dependency/core child refs untouched when they are already canonical

#### Patching

`Patch` should become another pure transformation pass.

For now its main behavioral responsibility is enum-default normalization.

That means:

* walk object/interface functions
* if a function arg has an enum default encoded in original-name form, rewrite
  it to the canonical member `Name`
* do that by:
  * creating a new arg via `FunctionArg.__withDefaultValue`
  * creating a new function via `Function.__withArg`
  * creating a new owning subtype via `ObjectTypeDef.__withFunction` or
    `InterfaceTypeDef.__withFunction`
* never mutate `arg.DefaultValue` in place

#### User-default introspection visibility

User-default introspection visibility in `core/modfunc.go` is the same kind of
transformation as patching.

It should:

* optionalize arg typedefs through the canonical `withOptional(true)` path
* create updated arg results through `FunctionArg.__withTypeDef` and/or
  `FunctionArg.__withDefaultValue`
* rebuild the owning function via `Function.__withArg`
* never mutate `FunctionArg.TypeDef.Optional` or `FunctionArg.DefaultValue`
  directly

### Module-level behavior after the cut

`Module.TypeDefs`, validation, namespacing, and patching will all need to
traverse through attached child refs.

That means code in `core/module.go` that currently does things like:

* recurse into `field.TypeDef`
* recurse into `fn.ReturnType`
* recurse into `arg.TypeDef`
* recurse into `typeDef.AsList.Value.ElementTypeDef`
* recurse into `obj.Functions`, `obj.Fields`, `obj.Constructor.Value`
* recurse into `iface.Functions`
* recurse into `input.Fields`
* recurse into `enum.Members`

must instead dereference through the stored result refs and operate on the
result-backed children.

The important design consequence is:

* module-level consumers must treat typedef-family children as references, not
  embedded subtrees

That is the core model shift.

### `ModType.TypeDef` becomes result-backed

The `ModType` interface should stop returning bare transient typedef graphs.

The current shape:

* `TypeDef() *TypeDef`

is not coherent once subtype slots like `AsObject`, `AsEnum`, `AsList`, and
`AsInterface` require attached object results.

Several internal `ModType` implementations currently synthesize transient
typedefs outside the schema write path, for example:

* primitive/list/nullable wrapper types
* core object and core enum types
* module enum/object/interface wrappers

Once subtype slots are result-backed, those transient builders need access to
the dagql server so they can construct canonical subtype refs through the new
internal `__*` constructor surface.

So the design should hard-cut the `ModType` contract to:

* `TypeDef(ctx context.Context) (dagql.ObjectResult[*TypeDef], error)`

This is more coherent than either:

* keeping `TypeDef() *TypeDef`, or
* changing only to `TypeDef(ctx) *TypeDef`

because it keeps the contract fully in the canonical result-backed world.

The consequences are:

* `PrimitiveType`, `ListType`, `NullableType`, `ModuleEnumType`,
  `CoreModObject`, `CoreModEnum`, `InterfaceType`, and any other `ModType`
  implementation must build and return a canonical `ObjectResult[*TypeDef]`
* any caller that only needs the struct can call `.Self()`
* any caller that needs canonical identity, IDs, or nested child refs already
  has the canonical typedef result
* if the current dagql server is missing from context, that should surface as an
  explicit error rather than silently synthesizing a fake bare subtype payload

This also implies that helper structs like `NullableType` should stop storing
bare typedef metadata such as `InnerDef *TypeDef` and should instead store the
canonical inner typedef result, for example:

* `InnerDef dagql.ObjectResult[*TypeDef]`

Then `NullableType.TypeDef(ctx)` can derive the optional wrapper as a canonical
typedef result rather than cloning a bare inner typedef.

### Top-level module typedef storage must become result-backed too

There is one more crucial consequence of the hard cut:

it is not sufficient to make only the nested typedef-family children
result-backed while keeping the module's top-level typedef collections as bare
`[]*TypeDef`.

If top-level storage remains bare, then any API that wants canonical typedef
results still has to fabricate wrappers after the fact. That just moves the
problem around and reintroduces detached-result invention in another place.

So the coherent cut is:

* `Module.ObjectDefs` becomes `dagql.ObjectResultArray[*TypeDef]`
* `Module.InterfaceDefs` becomes `dagql.ObjectResultArray[*TypeDef]`
* `Module.EnumDefs` becomes `dagql.ObjectResultArray[*TypeDef]`

And correspondingly:

* `Mod.TypeDefs(ctx, dag)` becomes
  `func TypeDefs(ctx context.Context, dag *dagql.Server) (dagql.ObjectResultArray[*TypeDef], error)`

This is the correct foundation because:

* top-level typedefs already have canonical result identity at storage time
* `TypeDefs` can return those refs directly
* `currentTypeDefs` no longer needs to fabricate or re-canonicalize top-level
  typedef wrappers
* module-level transformation passes can operate directly on the real stored
  canonical refs
* we do not need `dagql.NewObjectResultForCurrentCall(...)` hacks inside
  `TypeDefs` just to run rewrite logic

#### Consequences of the top-level storage hard cut

Once the module stores top-level typedefs as canonical results, the next
implementation steps become:

1. `core/module.go`
   * `ObjectDefs`, `InterfaceDefs`, and `EnumDefs` become
     `dagql.ObjectResultArray[*TypeDef]`
2. `core/module.go`
   * `WithObject`, `WithInterface`, and `WithEnum` accept
     `dagql.ObjectResult[*TypeDef]` instead of bare `*TypeDef`
   * they validate / namespace / patch by transforming those attached results
     and then store the returned canonical results
3. `core/module.go`
   * `TypeDefs(ctx, dag)` returns `dagql.ObjectResultArray[*TypeDef]`
   * in the steady state it should mostly just concatenate and/or return the
     stored top-level result arrays
4. `core/moddeps.go`
   * `ModDeps.TypeDefs` becomes result-backed too
5. `core/schema/module.go`
   * `moduleWithObject`, `moduleWithInterface`, and `moduleWithEnum` stop doing
     `.Self()` on the loaded typedef
   * they pass the attached `dagql.ObjectResult[*TypeDef]` through
6. `core/schema/module_typedef_canonical.go`
   * `currentTypeDefs` becomes much simpler
   * it should no longer canonicalize top-level typedefs on read
   * it should reuse the stored top-level result arrays directly
7. `core/module.go` persistence
   * module top-level typedef storage must persist as result refs, not recursive
     bare payloads
8. `core/schema/coremod.go`
   * coremod typedef collection must also return canonical result-backed typedefs
     rather than bare ones

The key rule is:

* do not invent new current-call or detached typedef wrappers in `TypeDefs`
  just to feed later logic

If a typedef is a real stored top-level module typedef, it should already be a
canonical result before `TypeDefs` ever returns it.

### Scope of expected code movement

This is not just a `core/typedef.go` edit.

The design expects coordinated changes across at least:

* `core/typedef.go`
* `core/module.go`
* `core/schema/module.go`
* `core/schema/module_typedef_canonical.go`
* `core/moddeps.go`
* `core/modtypes.go`
* `core/object.go`
* `core/interface.go`
* `core/enum.go`
* `core/persisted_object.go` usage sites

The first four are the core of the refactor. The others are the downstream
plumbing sweep that must be updated to consume result-backed nested typedef
children coherently.

### Implementation sequencing for the module-level rewrite

The implementation order should be:

1. Finish the low-level internal mutator surface in `core/typedef.go` and the
   internal schema fields in `core/schema/module.go`.
2. Rewrite `core/module.go` so:
   * `TypeDefs`
   * `validateTypeDef`
   * `validateObjectTypeDef`
   * `validateInterfaceTypeDef`
   * `namespaceTypeDef`
   * `Patch`
   all consume result-backed children and transform by returning new canonical
   refs instead of mutating nested `.Self()` values.
3. Update `core/modfunc.go` fully onto the copy-on-write function/arg rewrite
   model for user-default introspection visibility.
4. Sweep downstream readers and bridges:
   * `core/moddeps.go`
   * `core/object.go`
   * `core/interface.go`
   * `core/enum.go`
   * `core/modtree.go`
5. Only after the runtime model is stable, finish the typedef persistence hard
   cut in `core/typedef.go`.

The important point is that `core/module.go` is not a late cleanup.

It is the first place where the old imperative mutation model must be fully
replaced rather than patched around.

### Non-goals

This refactor does not need to:

* change unrelated `FunctionCall` payload fields
* redesign module result storage wholesale
* introduce backward compatibility for mixed old/new nested typedef payloads
* preserve the current recursive persisted typedef payload encoding

This should be a hard cut.

### Acceptance criteria

We are done only when all of the following are true.

#### Field conversion is complete

Every typedef-family bare ideable child field listed above has been converted to
an attached object result or object-result array.

There are no remaining typedef-family fields storing bare nested ideable
children except where the field is intentionally top-level and outside the scope
listed in this design.

#### Construction-time canonicalization is complete

Every schema/core construction path that creates or mutates typedef-family
objects now installs canonical attached child refs at write time.

In particular, paths like:

* `function`
* `functionWithArg`
* `functionWithSourceMap`
* `typeDefWithListOf`
* `typeDefWithObjectField`
* `typeDefWithFunction`
* `typeDefWithObjectConstructor`
* enum/object/interface/source-map mutators

must no longer pass `.Self()` bare children into core when the intent is to
store canonical child identity.

#### Dependency attachment is complete

Every typedef-family object that embeds child object results implements
`AttachDependencyResults` correctly.

Attaching a typedef-family parent result must attach and normalize all embedded
child refs and must establish explicit parent dependency edges to those child
results.

#### Read-time re-canonicalization is gone

Ordinary nested read paths in `core/schema/module_typedef_canonical.go` no
longer recursively call canonicalization helpers for fields that are already
stored as canonical child refs.

Nested field resolvers must directly reuse the stored child object results.

#### Recursive deep clone behavior is gone

`Clone()` on the typedef family no longer recursively clones nested ideable
children.

Cloning a typedef-family object may copy outer structs and slices, but it must
not rebuild the full nested typedef graph.

#### Recursive persisted payload encoding is gone

Typedef-family persistence no longer recursively embeds nested typedef/function
payloads.

Instead, nested ideable child refs are encoded as persisted result IDs and
decoded back into attached object results.

#### Namespacing/patch are coherent with shared refs

There is no remaining code path that recursively mutates shared nested typedef
child `.Self()` values in place after canonicalization.

Namespacing and patching must either:

* happen before canonical child refs are published, or
* be implemented as pure copy-on-write transformations that return new top-level
  typedef-family objects without mutating previously-shared nested children

In addition:

* `core/module.go` must be using the pure transformation pipeline described
  above, not a disguised imperative walker that mutates nested `.Self()` values
  while traversing
* unchanged canonical child refs must be returned untouched
* changed nodes must be rebuilt only through the internal mutator surface

#### Function metadata rewrites are coherent with shared refs

Any post-construction rewrite of function metadata must be copy-on-write.

That includes at minimum:

* user-default visibility in typedef introspection
* optionalizing object args for user defaults
* setting or changing `FunctionArg.DefaultValue`

The implementation is only done when:

* no codepath mutates `Function.Args`, `FunctionArg.TypeDef.Optional`, or
  `FunctionArg.DefaultValue` directly on shared canonical children
* internal mutators such as `Function.__withArg`,
  `FunctionArg.__withTypeDef`, and `FunctionArg.__withDefaultValue` are used to
  produce new canonical results instead
* unchanged arg/type child refs are reused as-is
* the resulting typedef introspection still reflects user defaults correctly

#### Module-level rewrite responsibilities are fully covered

The refactor is not done until the module-level rewrite pipeline covers all of:

* ownership stamping
* namespacing
* source-map rebasing
* enum-default patching
* user-default introspection visibility

And each of those responsibilities must be implemented as a pure
copy-on-write transformation over canonical typedef refs.

#### Module consumers are updated

Module validation, namespacing, patching, mod-type resolution, object/interface
bridges, and enum helpers all correctly recurse through the result-backed child
refs.

There must be no lingering assumptions in those paths that nested typedef
children are embedded bare pointers.

#### Canonical reuse is observable

When a client selects nested typedef-family fields from `currentTypeDefs` and
related schema APIs, the system reuses the already-stored canonical child refs
instead of rebuilding them on read.

Operationally, the code should make this true by construction, not by hopeful
caching side effects.

#### Tests prove the hard cut

Tests should explicitly cover:

* persistence round-trip for typedef-family objects with nested child refs
* dependency attachment for typedef-family parents embedding child refs
* `currentTypeDefs` nested selections reusing canonical child refs rather than
  rebuilding them
* namespacing/patch behavior not mutating shared nested children in place
* clone behavior no longer deep-copying nested typedef graphs

We should consider the refactor incomplete until those behaviors are locked down
by tests.

# Make Core Mod Not Fucking Suck

## Design

### Problem statement

`CoreMod` is currently carrying around a `*dagql.Server` and using that to do
several conceptually different jobs:

* identify the active `View`
* answer core mod-type lookups
* produce core typedefs
* serve as the install target for core schema
* act as the thing we awkwardly copy when we want the same core schema with a
  different `View`

That is not a good model.

It makes `CoreMod` look like some kind of server owner, even though most of the
time the server it holds is not actually the one we conceptually care about. It
also means we keep paying for:

* repeated core schema installation onto fresh dagql servers
* repeated core typedef generation
* weird ad hoc dag copies just to change `View`
* repeated rebuilding of the same core-only schema state for every client and
  every dependency-set server

This memoization surface is too small and its lifetime is too short. We already
improved the inner `CoreMod` typedef cache and its name indexes, but the
surrounding lifetime model is still wrong, so we are still throwing away too
much work.

The hard cut we want is:

* `CoreMod` should no longer store a live dagql server
* there should be one engine-wide core-only base server
* core schema should install once onto that base server
* core typedefs should be cached once per `View`
* other servers should be forked from that base rather than rebuilt from
  nothing and reinstalled every time

### New foundational model

The new foundational model is:

* a singleton core schema base exists for the whole engine
* `CoreMod` becomes a lightweight view-bound handle into that base
* non-core session or dependency servers are forked from the core schema base
  rather than re-installing core from scratch
* all core typedef generation and core mod-type lookup is driven from cached
  per-view state owned by that singleton base

The singleton object should **not** be called `CoreRuntime`.

For this design, the proposed name is:

* `CoreSchemaBase`

That name is intentionally boring and descriptive:

* it is specifically about schema/server state
* it is clearly the base from which others are derived
* it avoids colliding with the already-loaded meaning of "runtime"

### `CoreSchemaBase` responsibilities

`CoreSchemaBase` should own:

* the installed base dagql server containing only core schema
* the base root object used for that server
* per-view cached state derived from that base

Concretely, it should conceptually look like:

* `base *dagql.Server`
* `baseRoot *core.Query`
* `views map[call.View]*coreSchemaViewState`
* `mu sync.Mutex`

And each `coreSchemaViewState` should own:

* `server *dagql.Server`
  a core-only fork of the base server with the target `View`
* `typedefs dagql.ObjectResultArray[*core.TypeDef]`
  the canonical top-level core typedefs for that `View`
* `objectsByName map[string]dagql.ObjectResult[*core.TypeDef]`
* `scalarsByName map[string]dagql.ObjectResult[*core.TypeDef]`
* `enumsByName map[string]dagql.ObjectResult[*core.TypeDef]`

The key is that the singleton cache is **not** just a global typedef slice.

It is:

* one installed base core server
* plus one cached core-only view state for each `call.View`

That is the right lifetime and the right cache boundary.

### Why the cache key must include `View`

`View` is real schema state. Today we already contort `CoreMod.Dag` or copy
servers just to alter `View`.

So if we made core schema installation singleton but did **not** partition the
cached state by `View`, we would just be reintroducing the same bug in a new
place.

The cache key for core typedefs and core server state must be:

* `call.View`

No weaker cache key is coherent.

### `CoreMod` after the cut

After this refactor, `CoreMod` should stop carrying `Dag *dagql.Server`.

Instead it should become a thin handle with only:

* `base *CoreSchemaBase`
* `view call.View`

That means:

* `CoreMod.View()` returns `view`
* `CoreMod.TypeDefs(ctx, dag)` uses `base` + `view`
* `CoreMod.ModTypeFor(ctx, typeDef)` uses `base` + `view`
* `CoreMod` no longer pretends to own the dagql server it is installed into

This is the key hard cut. As long as `CoreMod` still owns a live server, the
model remains confused.

### What should happen when we need an actual server

There are two different server uses in the current system:

1. a core-only schema server used to answer core schema questions
2. a live session or dependency server that includes core plus extra schema

The new model should make those two cases explicit.

For case 1:

* use the cached per-view core-only server from `CoreSchemaBase`

For case 2:

* fork a fresh server from the appropriate core-only base-view server
* then install only the extra schema/modules that are needed on top

That means we stop:

* starting from `dagql.NewServer(...)` for every dependency-set load
* re-running `CoreMod.Install(...)` every time

### The current weirdness that this removes

Today there is already a codepath that effectively admits the current design is
bad:

* in `core/schema/modulesource.go`, we shallow-copy `coreMod.Dag`
* then overwrite only `dag.View`
* then wrap that in a new `CoreMod`

That is a symptom of the design being wrong.

The new model should replace that whole pattern with:

* `coreMod.WithView(view)` or equivalent lightweight construction around
  `CoreSchemaBase`

No shallow copy of a whole server just to change `View`.

### The major tripwire: installed schema captures install-time server state

This is the first big blocker and it must be treated as a real design concern,
not an implementation nuisance.

Today some installed schema fields/hooks capture the install-time server:

* `dagql.PerSchemaInput(srv)` usage in:
  * `core/schema/query.go`
  * `core/schema/env.go`
  * `core/schema/module.go`
* schema structs carrying `dag *dagql.Server`, notably:
  * `environmentSchema`
  * `llmSchema`
* install hooks carrying a specific server reference, especially:
  * `EnvHook{Server: srv}`

That means a reusable base server cannot simply be cloned and blindly reused if
the installed closures and hooks are still bound to the original install-time
server.

So the first prerequisite is:

* remove or rebind install-time server capture in core schema

This is not optional. If we skip it, the base/fork model will be subtly wrong.

### Prerequisite 1: stop capturing the install-time dagql server where we
actually want the current server

The schema pieces that currently capture `srv` or `dag` need to be audited and
changed so they use the current server at call time when that is the true
intent.

That includes at minimum:

* `core/schema/query.go`
* `core/schema/env.go`
* `core/schema/module.go`
* `core/schema/llm.go`
* `core/schema/coremod.go`

The desired rule is:

* if a field/input/helper wants "the server that is executing this call", it
  must derive that from the call context, not from install-time closure capture

In practice, this means:

* replacing `PerSchemaInput(srv)` style server capture with a current-server
  path
* removing install-time `dag *dagql.Server` fields from schema structs where
  those fields exist only to service call-time behavior

We should only keep an install-time server reference where the server itself is
truly part of the long-lived object identity, and for core schema that should
be very close to nowhere.

### Prerequisite 2: make install hooks fork-safe

The next tripwire is install hooks.

If a base server has install hooks that hold server-specific state, a fork
cannot safely share them as-is.

The main concrete case here is:

* `EnvHook{Server: srv}`

So we need a coherent hook-fork model.

The design should be:

* add a fork/rebind contract for install hooks that carry server-specific state
* when a server is forked, each install hook must either:
  * produce a clone bound to the new server, or
  * fail fast because it is not safe to reuse in a forked server

This should be explicit. No silent sharing of server-bound hooks across forks.

### Prerequisite 3: make object type installation fork-safe

The next major mutation hazard is `dagql.Class[T]`.

Installed object types are not immutable descriptors. They carry mutable field
tables and server-bound schema cache invalidation behavior.

That means:

* shallow-copying the object-type map from one server to another is not safe

Why:

* `Class.Install(...)`, `Class.Extend(...)`, and `Class.ExtendLoadByID(...)`
  mutate class field tables in place
* user module loading and interface extension mutate these tables after initial
  install
* sharing those class instances across servers would leak extensions and schema
  mutations between otherwise-independent dags

So the server fork model must include:

* object types are cloned for the new server
* the clone must get:
  * fresh field maps/slices
  * fresh locks
  * a schema-cache invalidation callback bound to the new server

This is another mandatory prerequisite.

### Required dagql primitive: `Server.Fork(...)`

The dagql layer should grow a real fork primitive rather than relying on ad hoc
copying.

Conceptually:

* `func (s *Server) Fork[T dagql.Typed](root T) (*Server, error)`

This should:

* create a new server
* install a fresh root value
* copy or clone server state carefully rather than by blunt struct copy

The fork operation should produce:

* fresh:
  * locks
  * schema caches
  * once guards
  * root object state
* shared or copied as appropriate:
  * directives
  * scalar registrations
  * type definitions that are immutable descriptors
* cloned:
  * object types
  * install hooks that are server-bound

The point is not to create some magic deep clone of all possible server state.

The point is:

* clone exactly the mutable and server-bound pieces
* share only the parts that are truly immutable and safe

### Object-type cloning contract

To make `Server.Fork(...)` honest, object types need a cloning contract.

The clean design is:

* object types installed in a server must be forkable into a new server

In practice, that likely means:

* a new interface on object types for cloning/rebinding to a target server

For `dagql.Class[T]`, the fork implementation must:

* shallow-copy the class struct itself
* deep-copy field maps and field slices
* allocate a fresh mutex
* rebind schema-cache invalidation to the new server

If some object type cannot satisfy this contract:

* server fork should fail fast rather than sharing it unsafely

### Root object handling in forks

The forked server must not reuse the base server's root object result directly.

Instead it should:

* install the cloned root class
* construct a fresh root object result for the new root value passed to
  `Fork(...)`

This avoids leaking session/client-specific root object state through the base.

### What `CoreSchemaBase` should do with forks

Once `Server.Fork(...)` exists and the capture issues are fixed, the singleton
should operate like this:

1. Create one base server with a base `core.Query` root.
2. Install core schema onto that base server exactly once.
3. For each `View` on demand:
   * fork the base server
   * set the target `View`
   * cache that core-only view server
   * generate and cache the core typedefs and lookup indexes once for that
     view
4. For actual client or dependency servers:
   * fork from the appropriate cached core-only view server
   * then install only the non-core modules/schema on top

This is the entire heart of the refactor.

### `CoreMod.TypeDefs` and `CoreMod.ModTypeFor` after the cut

After the refactor:

* `CoreMod.TypeDefs(ctx, dag)` should not regenerate typedefs by walking a
  fresh server
* it should simply consult the per-view cached state from `CoreSchemaBase`
* `CoreMod.ModTypeFor(ctx, typeDef)` should use those same cached per-view
  lookup indexes

So the effective behavior becomes:

* core typedef generation happens once per view
* core object/scalar/enum lookup is O(1) against that cached per-view state

### Engine session construction after the cut

Today the engine session path creates a new server and installs core schema on
it directly.

After the refactor it should instead:

* obtain the engine-wide `CoreSchemaBase`
* fork a fresh session dag from the base-view server for the session's view
* create a lightweight `CoreMod` pointing at the same `CoreSchemaBase` and that
  view
* use that in the session deps

That means:

* no per-session `CoreMod.Install(...)`
* no per-session rebuilding of core-only schema state

### `ModDeps.lazilyLoadSchema` after the cut

This is another major win surface.

Today it starts from:

* `dagql.NewServer(...)`
* then reinstalls all modules, including core

After the cut it should:

* determine the dependency-set `View`
* fork a server from `CoreSchemaBase` for that `View`
* install only non-core mods onto that fork
* then run the later object/interface extension phase as it does today

This avoids paying the core installation cost every time a dependency-set
schema is materialized.

### `modulesource` after the cut

The weird `CoreMod.Dag` shallow-copy path should disappear entirely.

Instead:

* if `modulesource` needs the same core mod in another view, it should create a
  new lightweight `CoreMod` handle around the same `CoreSchemaBase` with the
  alternate `view`

That should be a cheap operation.

No copying of a whole dagql server struct just to swap `View`.

### Scope of expected code movement

This is a broad but coherent change. The main files expected to move are:

* `dagql/server.go`
* `dagql/objects.go`
* `core/schema/coremod.go`
* `engine/server/session.go`
* `core/moddeps.go`
* `core/schema/modulesource.go`
* `core/schema/query.go`
* `core/schema/env.go`
* `core/schema/module.go`
* `core/schema/llm.go`
* `cmd/introspect/introspect.go`

The first five are the structural center of the refactor. The schema files are
the closure-capture cleanup. The others are the callsites that must stop
assuming `CoreMod` owns a dagql server.

### Detailed implementation sequence

The implementation should proceed in this order.

#### 1. Remove install-time server capture from core schema

Audit and rewrite the core schema pieces that capture `srv` / `dag` when they
really want the executing server at call time.

This includes:

* `PerSchemaInput(srv)` callsites in:
  * `core/schema/query.go`
  * `core/schema/env.go`
  * `core/schema/module.go`
* install-time schema structs that hold the server only to answer later calls
  in:
  * `core/schema/coremod.go`
  * `core/schema/env.go`
  * `core/schema/llm.go`

The acceptance criterion for this step is:

* the core schema no longer depends on install-time closure capture of a server
  for ordinary call execution behavior

#### 2. Add fork-safe contracts for install hooks and object types

Introduce the minimal new dagql contracts needed so a server can be forked
safely.

This includes:

* an install-hook rebinding/fork contract
* an object-type cloning/fork contract
* `dagql.Class[T]` support for that contract

The acceptance criterion for this step is:

* a fork can clone all core-installed object types and hooks without sharing
  mutable field tables or stale server references

#### 3. Implement `dagql.Server.Fork(...)`

Once the contracts above exist, implement the real fork primitive in
`dagql/server.go`.

The fork operation must:

* create fresh mutable server state
* clone object types
* rebind server-bound hooks
* install a fresh root
* preserve the already-installed immutable core schema definitions

The acceptance criterion for this step is:

* we can fork a core-only installed server into a new independent server
  without reinstalling core and without cross-server schema mutation leakage

#### 4. Introduce `CoreSchemaBase`

Add the singleton `CoreSchemaBase` and the per-view cached state in
`core/schema/coremod.go` or a nearby dedicated file.

This step should:

* build the base server once
* install core schema once
* lazily build per-view core-only forks
* lazily build per-view typedef/index caches

The acceptance criterion for this step is:

* the base object exists independently of any particular client or dependency
  server
* per-view core typedef generation happens at most once per process lifetime
  until invalidated by code changes/restart

#### 5. Remove `Dag` from `CoreMod`

Refactor `CoreMod` into a lightweight handle:

* `base *CoreSchemaBase`
* `view call.View`

Update:

* `CoreMod.TypeDefs`
* `CoreMod.ModTypeFor`
* `CoreMod.View`
* `CoreModScalar/Object/Enum` helpers

so they all work through `CoreSchemaBase` state instead of a stored server.

The acceptance criterion for this step is:

* `CoreMod` no longer stores `*dagql.Server`
* no codepath relies on `CoreMod.Dag`

#### 6. Switch engine session creation to fork from the core base

Update `engine/server/session.go` so client dag construction starts from the
singleton `CoreSchemaBase` rather than from a fresh bare dag plus a full core
install.

The acceptance criterion for this step is:

* starting a new session no longer reruns `CoreMod.Install(...)`

#### 7. Switch `ModDeps.lazilyLoadSchema` to fork from the core base

Update `core/moddeps.go` so dependency-set schema construction starts from the
core base for the target view, then installs only non-core mods.

The acceptance criterion for this step is:

* materializing a dependency-set schema no longer reinstalls core schema
  repeatedly

#### 8. Remove the remaining ad hoc `CoreMod` server hacks

Clean up:

* `core/schema/modulesource.go`
* `cmd/introspect/introspect.go`
* any tests or helpers still creating `CoreMod{Dag: ...}`

The acceptance criterion for this step is:

* there is no remaining codepath that copies a `CoreMod` server or constructs a
  `CoreMod` around an arbitrary live dagql server

### Additional design constraints

The implementation must preserve these constraints.

#### View isolation

Different views must remain isolated.

No cached typedefs, mod-type lookup indexes, or view-specific server state may
leak across views.

#### Server mutation isolation

Forked servers must remain independently mutable for:

* object/interface extension
* load-by-id extension
* module installation
* schema-cache invalidation

The base server must not observe later client/module schema mutation.

#### No fake wrapper reconstruction

This refactor is specifically trying to avoid rebuilding/reinstalling core
schema over and over.

So the implementation must not just move that work to some later point by
fabricating new wrapper servers, shallow-copying classes, or rerunning typedef
generation on demand.

If the work is still being redone repeatedly, the design has not been fully
realized.

### Non-goals

This refactor does not need to:

* redesign non-core module installation semantics beyond changing the base
  server they start from
* introduce compatibility with the old `CoreMod{Dag: ...}` shape
* preserve weird server-copy hacks that exist only because `CoreMod` currently
  owns a server

This should be a hard cut.

### Acceptance criteria

We are done only when all of the following are true.

#### `CoreMod` no longer owns a dagql server

There is no `Dag *dagql.Server` field on `CoreMod`, and no codepath depends on
such a field existing.

#### Core schema install is singleton

Core schema installation happens once onto a base server for the engine process,
not once per client session or once per dependency-set server.

#### Core typedef generation is singleton per view

For any given `call.View`, the core typedef graph is generated once and reused
for later calls rather than rebuilt repeatedly through fresh `CoreMod`
instances.

#### Session/dependency servers fork from the base

Per-session and per-dependency-set servers are derived from the appropriate
base-view core server, not built from scratch and reinstalled.

#### Install-time server capture is gone where inappropriate

Core schema field/input resolution no longer relies on stale install-time server
capture for behavior that should depend on the current executing server.

#### Forked servers are safely isolated

Forked servers can be independently extended and mutated without sharing mutable
object type tables, install hooks, or schema-cache invalidation state with the
base or with sibling forks.

#### `modulesource` no longer copies a server to change view

There is no remaining shallow-copy-of-`CoreMod.Dag` path used only to change
`View`.

#### Profiles show the intended win

On representative module-heavy workloads, profiles should show that:

* repeated `CoreMod.Install(...)` cost is gone or dramatically reduced
* repeated `CoreMod.TypeDefs(...)` / `CoreMod.typedefs(...)` rebuild cost is
  gone or dramatically reduced
* `Module.validateObjectTypeDef` no longer spends a large fraction of its time
  forcing rebuilds of the same core-only schema state

#### Tests and empirical validation cover the new model

We should not consider this done until we have verified at minimum:

* new sessions reuse the singleton core schema base rather than reinstalling
  core
* dependency-set schema materialization reuses the core base
* different views still see the correct core typedef state
* forked servers can still accept later module/interface extensions safely
* modulesource/introspection flows that previously depended on `CoreMod.Dag`
  still behave correctly under the new base/fork model
