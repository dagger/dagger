# DagQL Types: Nullables and Lists

This is a short note on the cache-relevant behavior of dagql nullables and lists.

## Nullables are views, not separate cached values

The important rule for nullables is that dagql does not create a separate underlying cached result for:

- "this value is non-null"
- "this value is nullable but currently set"

That would be pointless duplication.

Instead, nullable-vs-non-null is modeled as a view on top of the same `sharedResult`.

At the `Result` / `ObjectResult` layer, this is carried by small view flags:

- `nullableWrapped`
- `derefView`

So:

- `NullableWrapped()` returns a nullable view over the same shared result
- `DerefValue()` on a present nullable returns a dereferenced view over the same shared result
- loading by ID preserves the same underlying shared result and just changes the exposed type view

There are dedicated tests for this:

- `TestNullableDerefUsesSameSharedResult`
- `TestNullableWrappedUsesSameSharedResult`

That is the main thing to remember: nullable is not a separate cache node. It is a different lens on the same cache-backed payload.

## How nullable wrapping actually presents itself

When a result is viewed as nullable, `Result.Unwrap()` presents a `DynamicNullable` wrapper. When it is dereferenced, the wrapper is removed and the inner value is exposed directly.

But again, this is presentation behavior at the `Result` layer. The underlying `sharedResult` does not change.

That is why:

- nullable wrapping changes the exposed GraphQL type
- nullable wrapping changes the handle ID's type view
- nullable wrapping does not imply a different underlying cached object

## Lists are enumerable values with per-element call lineage

Lists in dagql implement `Enumerable`, which means they support:

- `Len()`
- `Nth(i)`
- `NthValue(i, call)`

The cache-relevant method is `NthValue`.

When you select an element from a cache-backed list result, dagql does not just throw away the list context. It derives a child result with:

- the parent call frame
- `Nth` set to the selected index
- the element type as the child type

So element selections have real call lineage rather than being anonymous extracted values.

## There are two important list cases

### Raw value arrays

For plain `Array[T]`, `NthValue(i, call)` creates a detached child result using the element value and a cloned call frame with the correct `Nth` and element type.

If the parent list itself is cache-backed, `Result.NthValue(ctx, i)` can then attach/cache that child call normally.

### Arrays of results

For `ResultArray[T]` and `ObjectResultArray[T]`, `NthValue` just returns the already-existing child result wrapper.

That matters because those children may already be attached cache-backed results with their own identity, dependencies, and lifecycle.

## Lists of object results preserve child dependencies

`ObjectResultArray[T]` implements `HasDependencyResults`.

That means when such an array is attached to the cache, each child object result is attached and recorded as an owned dependency of the array result.

This is the important retention behavior to be aware of:

- if a cache entry stores an array of child object results
- the array result keeps those child results live through normal dependency attachment

There is coverage for this in `TestCacheArrayResultsRetainChildResultsAcrossProducerSessionRelease`.

So arrays of object results are not just passive containers. They participate in normal dependency/liveness tracking.

## Practical mental model

The short mental model is:

- nullable/non-null is a view change over one shared cached payload
- list element selection gets its own child call lineage through `Nth`
- arrays of existing results can preserve and reuse child result identity directly
- arrays of object results attach those children as real dependencies

That is basically the cache-relevant part of the story.
