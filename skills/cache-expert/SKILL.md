---
name: cache-expert
description: Covers Dagger Engine caching internals including cache key derivation, invalidation, and the immutable DAG model. Use when debugging cache misses, unexpected invalidations, or implementing caching-related engine features.
---

# Cache Expert

## High-Level Architecture

The Dagger Engine serves a GraphQL-based API for building and executing DAG workflows.

Each operation takes immutable objects/scalar values as inputs and produces an immutable object/scalar value as output. "Mutability" is simulated as a DAG of these operations on immutable values, similar to functional programming.

This enables caching: since inputs are immutable and operations are deterministic, cache keys can be derived from the operation and its inputs.

DAGs of operations can be serialized as IDs, which have associated digests that serve as the operations' cache keys.

## Quick Reference

Jump to the right doc for your task:

| Task | Read |
|------|------|
| Understand how IDs encode operations and digests | [ids.md](references/ids.md) |
| Understand cache-relevant dagql execution flow | [dagql-api-server.md](references/dagql-api-server.md) |
| Understand base/session cache storage and lifecycle | [cache-storage.md](references/cache-storage.md) |
| Debug cache misses and cache behavior regressions | [debugging.md](references/debugging.md) |
| Understand filesync cache behavior | [filesync.md](references/filesync.md) |

## Core References

To build cache expertise, read these in order:

1. **[ids.md](references/ids.md)** - How IDs and digests define cache identity
2. **[dagql-api-server.md](references/dagql-api-server.md)** - How `Select`/`preselect`/`call` drive cache usage
3. **[cache-storage.md](references/cache-storage.md)** - How results are stored, indexed, released, and persisted

## Optional References

Load on-demand for specific tasks:

- **[debugging.md](references/debugging.md)** - Practical debugging loop and instrumentation points
- **[filesync.md](references/filesync.md)** - Host filesystem sync internals and filesync cache model
