# Provenance

## Status: Designed (high level)

Depends on: [Artifacts](./artifacts.md)

## Table of Contents

- [Summary](#summary)
- [Source of Provenance](#source-of-provenance)
- [Workspace Provenance](#workspace-provenance)
- [Workspace-Wide Provenance](#workspace-wide-provenance)
- [Tracking and Union](#tracking-and-union)
- [Matching](#matching)
- [Filtering Model](#filtering-model)
- [Schema](#schema)
- [Examples](#examples)
- [Implementation](#implementation)
- [Caching Semantics](#caching-semantics)
- [Open Questions](#open-questions)

## Summary

Provenance starts with `workspace.directory()` and `workspace.file()`. This
design adds a public `Artifact.provenance` value, plus two convenience filters
on `Artifacts`:

- `filterAffectedByPath(...)`
- `filterAffectedByDiff(...)`

These filters run on artifacts before plan compilation.

## Source of Provenance

This design tracks workspace reads only:

- `workspace.directory(...)`
- `workspace.file(...)`

It does not try to model:

- general lineage
- module ownership
- raw git metadata itself

When these APIs return a `Directory` or `File`, they attach a workspace
selector to that value.

## Workspace Provenance

Provenance is represented by `WorkspaceProvenance`.

It has two parts:

- `selectors`: the known workspace regions that were read
- `workspaceWide`: a broad flag meaning "this may be affected by any workspace
  path"

Empty provenance is:

```text
workspaceWide = false
selectors = []
```

`selectors` are returned in deterministic normalized order. They stay attached
even when `workspaceWide` is `true`.

In prose or debug output, `/` can be used as shorthand for
"workspace-wide provenance". In the API, the source of truth is
`workspaceWide: true`.

## Workspace-Wide Provenance

This is the simple name for the broad case.

An artifact becomes workspace-wide if either is true:

- it stores `Workspace` in a field
- it exposes any public function that accepts `Workspace`

This rule is conservative. It avoids false negatives.

Example:

```text
Artifact A
  ws = Workspace
```

Result:

```text
workspaceWide = true
selectors = []
```

Example:

```text
Artifact A
  check(ws: Workspace): Void
```

Result:

```text
workspaceWide = true
selectors = []
```

## Tracking and Union

Known selectors come from stored `Directory` and `File` values.

Artifact provenance is the union of all reachable stored field values. This
walk is transitive through nested stored objects.

`union(other)` follows these rules:

- `result.workspaceWide = left.workspaceWide OR right.workspaceWide`
- `result.selectors = semantic union of both selector lists`
- selectors are kept even when `result.workspaceWide = true`
- equivalent selectors may be deduplicated when practical

## Matching

Matching is exact.

`affectedByPath(path)` returns `true` if either is true:

- `workspaceWide = true`
- at least one selector's effective selected region overlaps `path`

`affectedByDiff(changes)` returns `true` if either is true:

- `workspaceWide = true`
- at least one selector's effective selected region overlaps the changed-path
  region from the core `Changeset`

The important rule is simple:

- match against the final selected file set
- not just against the selector root path

So `include` and `exclude` affect the result.

## Filtering Model

`Artifact.provenance` is the inspectable source of truth.

The two `Artifacts` filters are convenience wrappers:

- `filterAffectedByPath(path)` keeps rows where
  `artifact.provenance.affectedByPath(path)` is `true`
- `filterAffectedByDiff(changes)` keeps rows where
  `artifact.provenance.affectedByDiff(changes)` is `true`

These filters:

- work on any `Artifacts` scope
- preserve the current scope and `dimensions`
- narrow only the row set
- compose with other `Artifacts` filters by `AND`
- are order-independent with other `Artifacts` filters

The main model is still artifact filtering, not plan filtering. Plans compile
from the already selected artifact set.

## Schema

```graphql
"""
One workspace selector captured from a workspace read.
"""
type WorkspaceSelector {
  """
  Root path of this selector, in canonical workspace-relative form.
  """
  path: WorkspacePath!

  """
  Optional include globs. Empty means "include everything under path".
  """
  include: [String!]!

  """
  Optional exclude globs. Applied after include.
  """
  exclude: [String!]!
}

"""
Workspace provenance for one value or artifact.
"""
type WorkspaceProvenance {
  """
  True means this provenance may be affected by any workspace path.

  This is the source of truth for workspace-wide provenance. `/` may still be
  used as shorthand in prose or debug output.
  """
  workspaceWide: Boolean!

  """
  Known workspace selectors that contributed to this provenance.

  These selectors are kept even when `workspaceWide` is true. They are returned
  in deterministic normalized order.
  """
  selectors: [WorkspaceSelector!]!

  """
  Returns true if `workspaceWide` is true.

  Otherwise, returns true if any selector's effective selected region, after
  `include` and `exclude` are applied, overlaps the given path.
  """
  affectedByPath(path: WorkspacePath!): Boolean!

  """
  Returns true if `workspaceWide` is true.

  Otherwise, returns true if any selector's effective selected region, after
  `include` and `exclude` are applied, overlaps the changed-path region from
  the core `Changeset`.
  """
  affectedByDiff(changes: Changeset!): Boolean!

  """
  Returns the semantic union of this provenance and `other`.

  The result is workspace-wide if either input is workspace-wide. The result
  keeps selectors from both inputs.
  """
  union(other: WorkspaceProvenance!): WorkspaceProvenance!
}

extend type Artifact {
  """
  Non-null workspace provenance for this artifact.

  Empty provenance is `workspaceWide = false` and `selectors = []`.
  """
  provenance: WorkspaceProvenance!
}

extend type Artifacts {
  """
  Keeps rows where `artifact.provenance.affectedByPath(path)` is true.

  Preserves the current scope and dimensions. Narrows only the row set.
  Composes with other `Artifacts` filters by `AND`.
  """
  filterAffectedByPath(path: WorkspacePath!): Artifacts!

  """
  Keeps rows where `artifact.provenance.affectedByDiff(changes)` is true.

  Preserves the current scope and dimensions. Narrows only the row set.
  Composes with other `Artifacts` filters by `AND`.
  """
  filterAffectedByDiff(changes: Changeset!): Artifacts!
}
```

## Examples

### Precise Selector

Suppose an artifact has:

```text
workspaceWide = false
selectors = [
  { path = docs, include = **/*.md, exclude = docs/generated/** }
]
```

Then:

- `affectedByPath("docs")` is `true`
- `affectedByPath("docs/intro.md")` is `true`
- `affectedByPath("docs/intro.png")` is `false`
- `affectedByPath("docs/generated")` is `false`

If a core `Changeset` only contains paths under `docs/generated/**`, then
`affectedByDiff(changes)` is `false`.

### Transitive Aggregation

```text
Artifact A
  site = Site

Site
  src = ws.directory("docs")
  cfg = ws.file("config/site.yaml")
```

`Artifact A` gets both selectors, even though they are nested under `site`.

So:

- `affectedByPath("docs")` is `true`
- `affectedByPath("config")` is `true`
- `affectedByPath("scripts")` is `false`

### Workspace-Wide Provenance

```text
Artifact A
  check(ws: Workspace): Void
```

This artifact is workspace-wide:

```text
workspaceWide = true
selectors = []
```

So:

- `affectedByPath("docs")` is `true`
- `affectedByPath("src/api")` is `true`

### Workspace-Wide Provenance Keeps Selectors

```text
Artifact A
  src = ws.directory("docs")
  check(ws: Workspace): Void
```

This artifact is still workspace-wide:

```text
workspaceWide = true
selectors = [{ path = docs }]
```

So:

- `affectedByPath("docs")` is `true`
- `affectedByPath("src/api")` is `true`

The `docs` selector is still useful for inspection and debugging.

## Implementation

The initial implementation can be built in four steps.

### 1. Save selectors on workspace reads

When the engine returns a workspace-backed `Directory` or `File`, it stores a
`WorkspaceSelector` on that value.

```go
type WorkspaceSelector struct {
	Path    WorkspacePath
	Include []string
	Exclude []string
}

type WorkspaceProvenance struct {
	WorkspaceWide bool
	Selectors     []WorkspaceSelector
}

func (ws *Workspace) Directory(path WorkspacePath, opts DirOpts) *Directory {
	return &Directory{
		Provenance: WorkspaceProvenance{
			Selectors: []WorkspaceSelector{{
				Path:    path,
				Include: opts.Include,
				Exclude: opts.Exclude,
			}},
		},
	}
}

func (ws *Workspace) File(path WorkspacePath) *File {
	return &File{
		Provenance: WorkspaceProvenance{
			Selectors: []WorkspaceSelector{{
				Path: path,
			}},
		},
	}
}
```

### 2. Collect and union through stored fields

The engine walks stored fields. It also walks nested stored objects.

```go
func CollectStoredProvenance(v any) WorkspaceProvenance {
	switch x := v.(type) {
	case *Directory:
		return x.Provenance
	case *File:
		return x.Provenance
	case ObjectWithFields:
		var out WorkspaceProvenance
		for _, f := range x.MaterializedFields() {
			out = out.Union(CollectStoredProvenance(f.Value))
		}
		return out
	default:
		return WorkspaceProvenance{}
	}
}
```

### 3. Add workspace-wide provenance

After collecting precise selectors, the engine sets `workspaceWide = true` if
the artifact:

- stores `Workspace` in a field
- or exposes any public function that accepts `Workspace`

```go
func CollectArtifactProvenance(obj ObjectWithSchema) WorkspaceProvenance {
	prov := CollectStoredProvenance(obj)
	if obj.HasStoredWorkspaceField() || obj.HasPublicWorkspaceArgFunction() {
		prov.WorkspaceWide = true
	}
	return NormalizeProvenance(prov)
}
```

### 4. Implement the `Artifacts` filters in terms of `Artifact.provenance`

```go
func (s *Artifacts) FilterAffectedByPath(path WorkspacePath) *Artifacts {
	return s.FilterRows(func(a ArtifactRow) bool {
		return a.Provenance.AffectedByPath(path)
	})
}

func (s *Artifacts) FilterAffectedByDiff(changes Changeset) *Artifacts {
	return s.FilterRows(func(a ArtifactRow) bool {
		return a.Provenance.AffectedByDiff(changes)
	})
}
```

## Caching Semantics

Workspace-wide provenance and workspace-sensitive caching should line up.

If an artifact:

- stores `Workspace`
- or exposes a public `Workspace`-taking function

then it should also be treated as workspace-sensitive for caching.

Provenance does not replace the engine's content-addressed execution model. It
exists for selection, inspection, and UX.

## Open Questions

1. Should plans gain a second, narrower provenance layer later for late
   workspace reads?
2. Should built outputs like containers, packages, or services also carry
   `WorkspaceProvenance`, or should provenance stop at source-backed objects?
