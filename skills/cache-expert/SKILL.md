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
| Understand how IDs encode operations | [ids.md](references/ids.md) |
| Understand the GraphQL server implementation | [dagql-api-server.md](references/dagql-api-server.md) |
| Understand how results are cached | [cache-storage.md](references/cache-storage.md) |
| Debug a cache miss | [debugging.md](references/debugging.md) |

## Core References

To build deep experitise, read these in order:

1. **[ids.md](references/ids.md)** - How IDs encode operations and derive digests
2. **[dagql-api-server.md](references/dagql-api-server.md)** - The dagql GraphQL server implementation
3. **[cache-storage.md](references/cache-storage.md)** - How dagql results are cached

## Optional References

Load on-demand for specific tasks:

- **[debugging.md](references/debugging.md)** - Techniques for diagnosing cache misses and unexpected invalidations
