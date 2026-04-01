# Provenance

## Status: Designed (high level)

Depends on: [Artifacts](./artifacts.md)

## Summary

Workspace-origin metadata from `workspace.directory()` / `workspace.file()`
reads. Path and git filtering at query time. Entirely orthogonal to
collections and execution plans.

## Source of Provenance

V1 provenance comes from workspace API reads:
- `workspace.directory(...)`
- `workspace.file(...)`

This is intentionally narrow:
- not general lineage
- not git metadata itself
- not module ownership

Git- and path-based selection are derived by evaluating predicates against
stored provenance; they are not themselves provenance records.

## Provenance Predicates

Query-time predicates against stored provenance:
- "matches path `./docs`"
- "matches diff `HEAD~1..HEAD`"
- "overlaps this changed path set"
- "is entirely contained within this path set"

## Provenance Union

Provenance unions across fields and composed values:

```
provenance({foo: A, bar: B}) = union(provenance(A), provenance(B))
```

An artifact's effective provenance is the union of the provenance of all its
fields.

## Root-Path Provenance

Storing `Workspace` in a field is allowed, but it taints the object with
root-path provenance at `/`.

Consequences:
- the object matches all path and git filters
- it loses precise source filtering
- it should be treated as workspace-sensitive for caching

This is a deliberate tradeoff:
- **precise artifact:** materialize `Directory` / `File` inputs early →
  precise provenance, precise filtering
- **dynamic artifact:** store `Workspace` → matches everything, loses
  per-path precision

## Workspace Access Rule

Verb methods (`check`, `generate`, `ship`, `up`) must not accept `Workspace`
arguments.

Allowed:
- constructors and discovery helpers may accept `Workspace`
- non-verb helper methods may accept `Workspace`
- an object may store `Workspace` in a field

Forbidden:
- verb methods taking `Workspace`

This forces early materialization: by the time a verb runs, its inputs are
concrete directories and files with precise provenance, not a handle to the
whole workspace.

## Caching Semantics

Workspace-sensitive calls already have special cache behavior when `Workspace`
is injected as a function argument.

The same semantic taint extends to stored `Workspace` fields:
- if a function takes `Workspace`, it is workspace-sensitive
- if a function operates on an object that stores `Workspace`, it is also
  workspace-sensitive

This taint affects both:
- artifact filtering semantics (provenance at `/`)
- downstream cache sensitivity

Provenance is not a second invalidation system. It exists for UX and
orchestration, not to replace the engine's content-addressed execution model.

## Open Questions

1. Should runtime-discovered provenance become a second layer later?
   (`docusaurus`-style just-in-time workspace tracing suggests a real use
   case, but it is not a good foundation for v1 filtering.)
2. How far should provenance extend beyond source-backed workspace objects to
   built outputs (containers, packages, services)?
