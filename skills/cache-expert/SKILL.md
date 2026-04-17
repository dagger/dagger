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

THERE ARE NO REFERENCES RIGHT NOW BECAUSE THESE DOCS ARE VERY OLD. THE SOURCE OF TRUTH IS THE CODE.

The only exception to this is references/debugging.md, which HAS CRITICAL INSTRUCTIONS on running tests and debugging generally.

## Scripts

- `scripts/dagql-cache-analyzer.go`
  Analyze `/debug/dagql/cache` snapshot dumps offline and summarize retained
  roots, result categories, and approximate cumulative closures.

  Usage:

  ```bash
  go run ./skills/cache-expert/scripts/dagql-cache-analyzer.go /tmp/dagql.cache.1
  ```
