---
name: cache-expert
description: Covers Dagger Engine caching internals including cache key derivation, invalidation, and the immutable DAG model. Use when debugging cache misses, unexpected invalidations, or implementing caching-related engine features.
---

# Cache Expert

## High-Level Architecture

The Dagger Engine serves a GraphQL-based API for building and executing DAG workflows.

Each operation takes immutable objects/scalar values as inputs and produces an immutable object/scalar value as output. "Mutability" is simulated as a DAG of these operations on immutable values, similar to functional programming.

This enables caching: since inputs are immutable and operations are deterministic, cache keys can be derived from the operation and its inputs.

**Key concepts:**
- **ID**: A scalar value that encapsulates the operation that created an object. Enables non-scalar values to be used as inputs to other operations, forming a DAG.
- **Digest**: A hash derived from an operation and its inputs, used for cache key computation.
- **Call Cache Key**: Determines whether an operation's result can be retrieved from cache.

## Core API Call Anatomy

Every API call has three typed components:

1. **Parent** - The object the operation is called on (e.g., `Container`)
2. **Arguments** - The operation's input arguments
3. **Return value** - The result (scalar or object with an ID)

### Cache Key Computation

By default, a call's cache key is a hash of:
- Operation name (e.g., `Container.withExec`)
- Parent's digest
- Arguments' digests

This matches the default ID digest. See [ids.md](references/ids.md) for details.

### Cache Key Customization

Cache keys can be scoped differently:
- **Per-client** - Cached per connected client
- **Per-session** - Cached for the duration of a session
- **Per-call** - Never cached (unique each invocation)
- **Custom** - Arbitrary cache key logic

### Object IDs vs Cache Keys

These are related but distinct:
- **Call cache key** - Used to look up cached results
- **Returned object ID** - May equal the cache key, or may be a separate operation
- **Object digest** - Usually matches call cache key, but can be customized (e.g., content-addressed)

## Quick Reference

Jump to the right doc for your task:

| Task | Read |
|------|------|
| Understand how IDs encode operations | [ids.md](references/ids.md) |
| Understand the GraphQL server implementation | [dagql-api-server.md](references/dagql-api-server.md) |
| Understand how results are cached | [cache-storage.md](references/cache-storage.md) |
| Understand BuildKit integration (being phased out) | [buildkit-dagop.md](references/buildkit-dagop.md) |
| Debug a cache miss | [debugging.md](references/debugging.md) |
| Test cache behavior | [testing.md](references/testing.md) |

## Core References

Read in order to build deep expertise:

1. **[ids.md](references/ids.md)** - How IDs encode operations and derive digests
2. **[dagql-api-server.md](references/dagql-api-server.md)** - The dagql GraphQL server implementation
3. **[cache-storage.md](references/cache-storage.md)** - How dagql results are cached
4. **[buildkit-dagop.md](references/buildkit-dagop.md)** - BuildKit integration and its phase-out status

## Optional References

Load on-demand for specific tasks:

- **[debugging.md](references/debugging.md)** - Techniques for diagnosing cache misses and unexpected invalidations
- **[testing.md](references/testing.md)** - How to test cache behavior in the engine

