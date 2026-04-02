# Provenance

## Status: Designed (high level)

Depends on: [Artifacts](./artifacts.md)

## Table of Contents

- [Summary](#summary)
- [Source of Provenance](#source-of-provenance)
- [Tracking](#tracking)
- [Provenance Predicates](#provenance-predicates)
- [Selector Shape](#selector-shape)
- [Provenance Union](#provenance-union)
- [Conservative Workspace-Sensitive Taint](#conservative-workspace-sensitive-taint)
- [Filtering Model](#filtering-model)
- [Caching Semantics](#caching-semantics)
- [Open Questions](#open-questions)

## Summary

Workspace-origin metadata from `workspace.directory()` / `workspace.file()`
reads. V1 path and git filtering is defined on [Artifacts](./artifacts.md):
query-time predicates select artifacts from their effective provenance before
verb execution. Collections remain orthogonal. Execution plans consume the
selected artifact set rather than replacing artifact-level provenance filters.

## Source of Provenance

V1 provenance comes from workspace API reads:
- `workspace.directory(...)`
- `workspace.file(...)`

These calls attach hidden workspace provenance to the returned `Directory` /
`File` values.

This is intentionally narrow:
- not general lineage
- not git metadata itself
- not module ownership

Git- and path-based selection are derived by evaluating predicates against
stored provenance; they are not themselves provenance records.

## Tracking

Provenance is tracked on values, then aggregated onto artifacts.

- `workspace.directory(...)` attaches provenance to the returned `Directory`
- `workspace.file(...)` attaches provenance to the returned `File`
- ordinary object composition carries that provenance forward
- an artifact's effective provenance is derived from the values reachable from
  its fields

V1 does not derive precise provenance from function bodies, function arguments,
or legacy path-defaulting conventions. It relies on value provenance plus a
conservative workspace-sensitive taint rule described below.

## Provenance Predicates

Query-time predicates against stored provenance:
- "matches path `./docs`"
- "matches diff `HEAD~1..HEAD`"
- "overlaps this changed path set"
- "is entirely contained within this path set"

## Selector Shape

Provenance is stored as one or more workspace selectors. At minimum, a
selector records the workspace path region that was read. Selectors may also
carry include/exclude constraints when the underlying workspace read is more
specific than a single path.

Root-path provenance at `/` is the coarse "matches any workspace path" case.

## Provenance Union

Provenance unions across fields and composed values:

```
provenance({foo: A, bar: B}) = union(provenance(A), provenance(B))
```

An artifact's effective provenance is the union of the provenance of all its
fields.

## Conservative Workspace-Sensitive Taint

Artifact filters must avoid false negatives, even when modules defer workspace
access until later. V1 therefore adds a conservative taint rule on top of
precise field provenance.

An artifact is tainted with root-path provenance at `/` if either is true:

- it stores `Workspace` in a field
- it exposes any function that accepts `Workspace`

This rule is intentionally broad:

- it applies to verb methods and non-verb helpers alike
- `Workspace` arguments remain allowed, but they trade provenance precision for
  conservative `/` taint at artifact-filtering time

Consequences:
- the object matches all path and git filters
- it loses precise source filtering
- it should be treated as workspace-sensitive for caching

This is a deliberate tradeoff:
- **precise artifact:** materialize `Directory` / `File` inputs early →
  precise provenance, precise filtering
- **workspace-sensitive artifact:** store `Workspace`, or expose a
  `Workspace`-taking function → matches everything, loses per-path precision

This keeps `Artifacts` as the primary provenance filter surface while allowing
lazy workspace access patterns to remain correct by over-approximating them.

Known limitation:
- sibling functions may inherit a false-positive `/` match because taint is
  applied at artifact granularity, not per function

Possible future refinement:
- execution plans may add a second, narrower provenance layer later if the
  false-positive rate becomes a practical problem

## Filtering Model

The primary model is artifact filtering, not plan filtering.

- `workspace.artifacts` and verb-scoped artifact views are filtered by
  effective artifact provenance
- path and git filters are query-time predicates over that artifact provenance
- `Artifacts.check`, `Artifacts.generate`, and other verbs compile plans from
  the already selected artifact set

This means provenance answers "which artifacts are relevant?" before plan
compilation. Plans may later refine execution inside the selected set, but
that is not required for v1 correctness.

## Caching Semantics

Workspace-sensitive calls already have special cache behavior when `Workspace`
is injected as a function argument.

The same semantic taint extends to stored `Workspace` fields and exposed
`Workspace`-taking functions:
- if a function takes `Workspace`, it is workspace-sensitive
- if a function operates on an object that stores `Workspace`, it is also
  workspace-sensitive

This taint affects both:
- artifact filtering semantics (provenance at `/`)
- downstream cache sensitivity

Provenance is not a second invalidation system. It exists for UX and
orchestration, not to replace the engine's content-addressed execution model.

## Open Questions

1. Should execution plans gain a second, narrower provenance layer later?
   (`docusaurus`-style just-in-time workspace tracing suggests a real use
   case, but it is not a good foundation for v1 artifact filtering.)
2. How far should provenance extend beyond source-backed workspace objects to
   built outputs (containers, packages, services)?
