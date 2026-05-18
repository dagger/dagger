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

The reference docs in `references/` are up to date and should be treated as current guidance for understanding the cache, persistence, pruning, lazy evaluation, session resources, filesync, typedefs, and e-graph behavior. The code remains the final source of truth, but the reference docs are expected to match the current implementation.

Always read the relevant reference docs for the cache area being debugged. For broad cache investigations, read all of them. `references/debugging.md` has critical instructions on running tests, replaying CI traces, and debugging generally.

## Scripts

- `scripts/dagql-cache-analyzer.go`
  Analyze `/debug/dagql/cache` snapshot dumps offline and summarize retained
  roots, result categories, and approximate cumulative closures.

  Usage:

  ```bash
  go run ./skills/cache-expert/scripts/dagql-cache-analyzer.go /tmp/dagql.cache.1
  ```
