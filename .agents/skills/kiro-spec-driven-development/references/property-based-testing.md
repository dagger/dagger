# Property-based testing (PBT) integration

Spec-driven development treats correctness formally: the design names a set of
**correctness properties**, and the implementation must provide evidence — via
property-based tests — that the software obeys them.

The three artifacts you are steering the user toward:
1. A specification including correctness properties (the design).
2. An implementation that conforms to them.
3. A test suite that gives evidence of conformance (the PBTs).

## What a property is

A property is a statement that should hold across **all** valid executions, not one
example. It is universally quantified over an input space:

> *For any* {generated inputs}, {the system} SHALL {invariant / observable behaviour}.

PBT then generates many random inputs from that space, runs the system, and checks the
property holds — shrinking any counterexample to a minimal failing case.

## Deriving properties from requirements

1. Group related acceptance criteria. A cluster of criteria about one state machine,
   one round-trip, or one precedence rule usually becomes a single property.
2. Express the cluster as one universally quantified statement.
3. Prefer a **reference model**: a simple, obviously-correct re-implementation of the
   expected behaviour that the test compares the real system against. This is far
   stronger than spot-checking outputs.
4. Deduplicate. Each property must add unique validation value; fold overlapping ones
   together.
5. Cite the requirements each property covers:
   `**Validates: Requirements X.Y, X.Z**`.

## Property archetypes (use these as a checklist)

- **State-machine / reference-model:** a sequence of operations evolves observable state
  exactly as a reference model predicts (CRUD, routing config, metadata maps).
- **Round-trip:** encode→decode, persist→reload, author→replay yields an equal value
  (serialization, restart recovery, history replay).
- **Invariant under rejection:** any rejected request leaves durable state byte-identical
  ("no mutation on rejection").
- **Determinism / precedence:** a pure decision function is deterministic and follows a
  defined precedence order (routing, effective-value selection).
- **Idempotence:** repeating an operation with the same idempotency key is a no-op.
- **Concurrency / CAS:** among concurrent writers with the same expected token, at most
  one succeeds.
- **Pagination:** paging a set yields each element exactly once, no duplicates or
  omissions, with a terminal empty token.

## Writing the PBT tasks

For every `Property N` in the design, `tasks.md` gets a required task that:
- implements it as a property-based test using the **workspace-standard** PBT library
  (do not hand-roll a generator framework);
- runs a minimum iteration count (e.g. ≥100);
- carries a tag identifying the property, e.g.
  `// Feature: {feature}, Property N: {short text}`;
- lives in the module the design's Testing Strategy assigns (pure logic near the core;
  round-trips near storage/replay; projection near the read layer);
- cites the same requirements as the property.

## What PBT does *not* cover

Fixed, non-input-varying facts are example-based unit tests, not properties:
- exact error messages / status codes for a specific malformed input;
- a specific "unsupported" response matching the targeted system;
- single-namespace "not found" and similar one-shot cases.

State these explicitly in the Testing Strategy so the property set stays focused and the
edge cases are still covered.

## Specification is iterative

Specs are hard; expect to refine properties as implementation reveals nuance. When a
property turns out to be wrong (over- or under-constrained), fix the property and its
backing requirement together — keep the requirement → property → test chain consistent.
A failing PBT is information: either the implementation is wrong, or the property is
wrong. Verify which against ground truth before "fixing" either.
