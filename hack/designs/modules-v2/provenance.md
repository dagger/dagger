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
- [Lockfile Acceleration](#lockfile-acceleration)
- [Schema](#schema)
- [Examples](#examples)
- [Implementation Notes](#implementation-notes)
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

It stores a list of `WorkspaceSelector` values.

Each selector has:

- a root `path`
- optional `include`
- optional `exclude`
- a `conservative` flag

Empty provenance is:

```text
selectors = []
```

Selectors are returned in deterministic normalized order.

If `conservative = false`, the selector is exact.

If `conservative = true`, the selector is a safe upper bound. It means:

- the artifact may be affected by files in this region
- the exact smaller set may only be known later

## Workspace-Wide Provenance

A selector rooted at `/` means workspace-wide provenance.

Simple case:

```text
{ path = /, include = [], exclude = [], conservative = true }
```

This means:

- any workspace path may affect this artifact

But workspace-wide provenance can still be narrowed.

Example:

```text
{ path = /, include = [], exclude = [third_party/**, docs/**], conservative = true }
```

This means:

- the artifact may be affected by many workspace paths
- but definitely not by files under `third_party/**` or `docs/**`

That is useful in large monorepos. It lets broad provenance still avoid many
irrelevant changes.

An artifact becomes workspace-wide if either is true:

- it stores `Workspace` in a field
- it exposes any public function that accepts `Workspace`

The baseline representation is a conservative selector rooted at `/`. Later
analysis may narrow it with `include` and `exclude`.

This tradeoff is intentional.

A public function that accepts `Workspace` can read workspace files later, after
artifact provenance has already been inspected.

If artifact provenance ignored that function, filtering could skip an artifact
that is actually affected. That would be a false negative.

So this design chooses a conservative result instead:

- no false negatives at the artifact layer
- possible false positives
- lower precision for some artifacts

Example:

```text
Artifact A
  src = ws.directory("docs")
  check(ws: Workspace): Void
```

This artifact may have both:

```text
selectors = [
  { path = docs, include = [], exclude = [], conservative = false },
  { path = /, include = [], exclude = [], conservative = true }
]
```

The exact selector is still useful for inspection. The conservative selector
keeps filtering safe.

A later action-layer provenance pass can recover precision here. It can track a
specific action or function call, instead of widening the whole artifact.

The internal selector model already supports this. A conservative selector can
be narrower than the whole repo.

Example:

```text
{ path = /, include = [apps/**], exclude = [apps/legacy/**], conservative = true }
```

But the exact author-facing or runtime mechanism for producing that narrower
conservative selector is not yet defined in this design.

## Tracking and Union

Known selectors come from stored `Directory` and `File` values.

Artifact provenance is the union of all reachable stored field values. This
walk is transitive through nested stored objects.

`union(other)` follows these rules:

- result selectors are the semantic union of both selector lists
- exact selectors stay exact
- conservative selectors stay conservative
- equivalent selectors may be deduplicated when practical

## Matching

Matching is exact with respect to the selector model.

`affectedByPath(path)` returns `true` if any selector's effective selected
region overlaps `path`.

`affectedByDiff(changes)` returns `true` if any selector's effective selected
region overlaps the changed-path region from the core `Changeset`.

The important rule is simple:

- match against the final selected region
- not just against the selector root path

So `include` and `exclude` affect the result.

`conservative` does not change the matching rule. It only changes the meaning of
the selector:

- exact selector: "this region definitely contributes"
- conservative selector: "this region may contribute; we will know more later"

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

Later, the engine may add a second provenance layer on actions. That would make
some currently conservative artifact matches narrower at execution time, without
changing the artifact-level no-false-negative rule.

## Lockfile Acceleration

Provenance filtering can be expensive if provenance must be discovered by
loading artifacts first.

That creates a bad shape:

- load artifact
- compute provenance
- then decide the artifact was irrelevant

In large workspaces, this can waste a lot of time.

An optional optimization is to keep a lockfile-backed provenance index.

The idea is:

- store the last known `WorkspaceProvenance` for an artifact
- use it only for negative pruning
- if the stored provenance says the artifact is definitely unaffected, skip
  loading it
- otherwise load it normally

This index is advisory. It is not the source of truth.

Safety rule:

- every artifact that is actually loaded must recompute its provenance and
  rewrite its stored entry

This creates a self-healing loop.

Example:

1. a Node artifact currently depends on `package.json`
2. `package.json` changes to add a new dependency in another part of the repo
3. the artifact is still selected, because `package.json` was already in its
   old provenance
4. the artifact loads
5. its provenance is recomputed
6. the lockfile entry is rewritten and now includes the new dependency path
7. later changes to that new dependency are now caught by pruning

This is safe only if discovery inputs are themselves part of provenance.

In the example above:

- if changing `package.json` can cause new paths to be read later
- then `package.json` must already be in the old provenance

If that rule does not hold, stale provenance entries can cause false
negatives.

This optimization is most useful in CI.

Local development can be simpler:

- ignore this optimization
- or always refresh entries whenever artifacts are loaded

This topic is about artifact provenance only. If plans later gain their own
provenance layer, the same idea can be extended to actions.

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

  """
  False means this selector is exact.

  True means this selector is conservative. It is a safe upper bound. The
  actual smaller file set may only be known later.
  """
  conservative: Boolean!
}

"""
Workspace provenance for one value or artifact.
"""
type WorkspaceProvenance {
  """
  Known workspace selectors that contributed to this provenance.

  A selector rooted at `/` represents workspace-wide provenance. It may still
  be narrowed by `include` and `exclude`.
  """
  selectors: [WorkspaceSelector!]!

  """
  Returns true if any selector's effective selected region, after `include` and
  `exclude` are applied, overlaps the given path.
  """
  affectedByPath(path: WorkspacePath!): Boolean!

  """
  Returns true if any selector's effective selected region, after `include` and
  `exclude` are applied, overlaps the changed-path region from the core
  `Changeset`.
  """
  affectedByDiff(changes: Changeset!): Boolean!

  """
  Returns the semantic union of this provenance and `other`.
  """
  union(other: WorkspaceProvenance!): WorkspaceProvenance!
}

extend type Artifact {
  """
  Non-null workspace provenance for this artifact.

  Empty provenance is `selectors = []`.
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
selectors = [
  { path = docs, include = **/*.md, exclude = docs/generated/**, conservative = false }
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

### Simple Workspace-Wide Provenance

```text
Artifact A
  check(ws: Workspace): Void
```

This artifact may be affected by any workspace path:

```text
selectors = [
  { path = /, include = [], exclude = [], conservative = true }
]
```

So:

- `affectedByPath("docs")` is `true`
- `affectedByPath("src/api")` is `true`

### Narrowed Workspace-Wide Provenance

```text
selectors = [
  { path = /, include = [], exclude = [third_party/**, docs/**], conservative = true }
]
```

Then:

- `affectedByPath("apps/web")` is `true`
- `affectedByPath("docs")` is `false`
- `affectedByPath("third_party/libfoo")` is `false`

### Workspace-Wide Provenance Keeps Exact Selectors

```text
Artifact A
  src = ws.directory("docs")
  check(ws: Workspace): Void
```

This artifact may have both:

```text
selectors = [
  { path = docs, include = [], exclude = [], conservative = false },
  { path = /, include = [], exclude = [third_party/**], conservative = true }
]
```

The exact selector is still useful for inspection and debugging. The
conservative selector is still useful for broad pruning.

## Implementation Notes

The initial implementation can be built in five steps.

### 1. Save exact selectors on workspace reads

When the engine returns a workspace-backed `Directory` or `File`, it stores an
exact `WorkspaceSelector` on that value.

```go
type WorkspaceSelector struct {
	Path         WorkspacePath
	Include      []string
	Exclude      []string
	Conservative bool
}

type WorkspaceProvenance struct {
	Selectors []WorkspaceSelector
}

func (ws *Workspace) Directory(path WorkspacePath, opts DirOpts) *Directory {
	return &Directory{
		Provenance: WorkspaceProvenance{
			Selectors: []WorkspaceSelector{{
				Path:         path,
				Include:      opts.Include,
				Exclude:      opts.Exclude,
				Conservative: false,
			}},
		},
	}
}

func (ws *Workspace) File(path WorkspacePath) *File {
	return &File{
		Provenance: WorkspaceProvenance{
			Selectors: []WorkspaceSelector{{
				Path:         path,
				Conservative: false,
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

### 3. Add conservative selectors

After collecting exact selectors, the engine adds a conservative selector when
the artifact:

- stores `Workspace` in a field
- or exposes any public function that accepts `Workspace`

The baseline selector is rooted at `/`.

```go
func CollectArtifactProvenance(obj ObjectWithSchema) WorkspaceProvenance {
	prov := CollectStoredProvenance(obj)
	if obj.HasStoredWorkspaceField() || obj.HasPublicWorkspaceArgFunction() {
		prov = prov.Union(WorkspaceProvenance{
			Selectors: []WorkspaceSelector{{
				Path:         "/",
				Conservative: true,
			}},
		})
	}
	return NormalizeProvenance(prov)
}
```

Later implementations may narrow that conservative selector with `include` and
`exclude`.

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

### 5. Optionally prune with a lockfile-backed provenance index

```go
func ShouldLoadArtifact(a ArtifactKey, changes Changeset) bool {
	if cached, ok := LoadCachedProvenance(a); ok && !cached.AffectedByDiff(changes) {
		return false
	}
	return true
}

func OnArtifactLoaded(a ArtifactKey, obj ObjectWithSchema) {
	StoreCachedProvenance(a, CollectArtifactProvenance(obj))
}
```

This cache is advisory. Any artifact that is actually loaded must refresh its
entry.

## Caching Semantics

Conservative provenance and workspace-sensitive caching should line up.

If an artifact has a conservative selector, caching should treat that artifact as
workspace-sensitive within that selector's region.

Provenance does not replace the engine's content-addressed execution model. It
exists for selection, inspection, and UX.

## Open Questions

1. Should actions gain a second, narrower provenance layer later for late
   workspace reads?
2. Should built outputs like containers, packages, or services also carry
   `WorkspaceProvenance`, or should provenance stop at source-backed objects?
