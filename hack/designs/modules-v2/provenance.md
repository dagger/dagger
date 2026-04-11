# Provenance

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
- [Caching Semantics](#caching-semantics)
- [Open Questions](#open-questions)

## Summary

Provenance starts with `workspace.directory()` and `workspace.file()`. Each
artifact also carries provenance for the source files of the Dagger module that
defines it.

This design adds:

- `Artifact.provenance`
- `Artifact.affectedByPath(...)`
- `Artifact.affectedByDiff(...)`
- `Artifacts.filterAffectedByPath(...)`
- `Artifacts.filterAffectedByDiff(...)`

By default, matching uses all provenance kinds. Callers can opt in to narrower
matching by passing `kinds`.

## Source of Provenance

This design tracks artifact inputs that can be expressed as workspace path
selectors:

- `workspace.directory(...)`
- `workspace.file(...)`
- the source files of the Dagger module that defines the artifact

It does not try to model:

- general lineage
- ownership beyond the defining module
- raw git metadata itself

When `workspace.directory()` or `workspace.file()` returns a `Directory` or
`File`, it attaches a selector with kind `WORKSPACE_READ`.

When artifact provenance is collected, the defining module adds selectors with
kind `MODULE_SOURCE`.

Implementation sketch:

When the engine returns a workspace-backed `Directory` or `File`, it stores an
exact `WorkspaceSelector` on that value.

```go
type WorkspaceProvenanceKind string

const (
	WorkspaceRead WorkspaceProvenanceKind = "WORKSPACE_READ"
	ModuleSource  WorkspaceProvenanceKind = "MODULE_SOURCE"
)

type WorkspaceSelector struct {
	Path         WorkspacePath
	Include      []string
	Exclude      []string
	Conservative bool
	Kind         WorkspaceProvenanceKind
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
				Kind:         WorkspaceRead,
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
				Kind:         WorkspaceRead,
			}},
		},
	}
}
```

## Workspace Provenance

Provenance is represented by `WorkspaceProvenance`.

It stores a list of `WorkspaceSelector` values.

Each selector has:

- a root `path`
- optional `include`
- optional `exclude`
- a `conservative` flag
- a `kind`

Empty provenance is:

```text
selectors = []
```

Selectors are returned in deterministic normalized order.

`kind` tells why the selector exists:

- `WORKSPACE_READ`: came from `workspace.directory()`, `workspace.file()`, or
  conservative workspace access on the artifact
- `MODULE_SOURCE`: came from the source files of the Dagger module that defines
  the artifact

`Directory` and `File` values only carry `WORKSPACE_READ` selectors.
`MODULE_SOURCE` selectors are added when artifact provenance is collected.

In the initial implementation, `MODULE_SOURCE` selectors are exact.

If `conservative = false`, the selector is exact.

If `conservative = true`, the selector is a safe upper bound. It means:

- the artifact may be affected by files in this region
- the exact smaller set may only be known later

## Workspace-Wide Provenance

A selector rooted at `/` with kind `WORKSPACE_READ` means workspace-wide
provenance.

Simple case:

```text
{ path = /, include = [], exclude = [], conservative = true, kind = WORKSPACE_READ }
```

This means:

- any workspace path may affect this artifact

But workspace-wide provenance can still be narrowed.

Example:

```text
{ path = /, include = [], exclude = [third_party/**, docs/**], conservative = true, kind = WORKSPACE_READ }
```

This means:

- the artifact may be affected by many workspace paths
- but definitely not by files under `third_party/**` or `docs/**`

That is useful in large monorepos. It lets broad provenance still avoid many
irrelevant changes.

An artifact becomes workspace-wide if either is true:

- it stores `Workspace` in a field
- it exposes any public function that accepts `Workspace`

The baseline representation is a conservative `WORKSPACE_READ` selector rooted
at `/`. Later analysis may narrow it with `include` and `exclude`.

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
  { path = docs, include = [], exclude = [], conservative = false, kind = WORKSPACE_READ },
  { path = /, include = [], exclude = [], conservative = true, kind = WORKSPACE_READ }
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
{ path = /, include = [apps/**], exclude = [apps/legacy/**], conservative = true, kind = WORKSPACE_READ }
```

But the exact author-facing or runtime mechanism for producing that narrower
conservative selector is not yet defined in this design.

Implementation sketch:

After collecting exact selectors, the engine adds a conservative selector when
the artifact:

- stores `Workspace` in a field
- or exposes any public function that accepts `Workspace`

The baseline selector is rooted at `/` and has kind `WORKSPACE_READ`.

```go
func CollectArtifactProvenance(obj ObjectWithSchema) WorkspaceProvenance {
	prov := CollectStoredProvenance(obj)
	prov = prov.Union(CollectModuleSourceProvenance(obj.DefiningModule()))
	if obj.HasStoredWorkspaceField() || obj.HasPublicWorkspaceArgFunction() {
		prov = prov.Union(WorkspaceProvenance{
			Selectors: []WorkspaceSelector{{
				Path:         "/",
				Conservative: true,
				Kind:         WorkspaceRead,
			}},
		})
	}
	return NormalizeProvenance(prov)
}
```

In the initial implementation, `CollectModuleSourceProvenance` can return one
exact `MODULE_SOURCE` selector rooted at the defining module's source
directory.

Later implementations may narrow that conservative selector with `include` and
`exclude`.

## Tracking and Union

Known selectors come from:

- stored `Directory` and `File` values
- the source files of the Dagger module that defines the artifact

Artifact provenance is the union of all reachable stored field values. This
walk is transitive through nested stored objects.

`union(other)` follows these rules:

- result selectors are the semantic union of both selector lists
- selectors keep their `kind`
- exact selectors stay exact
- conservative selectors stay conservative
- equivalent selectors with the same `kind` may be deduplicated when practical

Implementation sketch:

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

## Matching

Matching is exact with respect to the selector model.

Selector filtering uses the same `include` / `exclude` semantics as existing
`CopyFilter` APIs such as `Workspace.directory(...)`, `Host.directory(...)`,
and `Directory.filter(...)`.

The rules are:

- `include` and `exclude` are evaluated relative to selector `path`
- empty `include` means "everything under `path`"
- `exclude` is applied after `include`
- if both match, `exclude` wins
- `affectedByPath(path, kinds = [])` uses subtree overlap, not exact string
  equality
- `affectedByDiff(changes, kinds = [])` uses the same overlap rule over the
  changed paths in the core `Changeset`
- if `kinds` is empty or omitted, all kinds are used

Examples:

- selector `{ path = docs, include = [], exclude = [] }` matches
  `affectedByPath("docs")` and `affectedByPath("docs/intro.md")`
- selector `{ path = docs/intro.md, include = [], exclude = [] }` matches
  `affectedByPath("docs")` and `affectedByPath("docs/intro.md")`, but not
  `affectedByPath("docs/other.md")`
- selector `{ path = docs, include = ["**/*.md"], exclude = ["generated/**"] }`
  matches `affectedByPath("docs/intro.md")`, but not
  `affectedByPath("docs/generated/api.md")`

`conservative` does not change the matching rule. It only changes the meaning of
the selector:

- exact selector: "this region definitely contributes"
- conservative selector: "this region may contribute; we will know more later"

## Filtering Model

`Artifact.provenance` is the inspectable source of truth.

`Artifact.affectedByPath(...)` and `Artifact.affectedByDiff(...)` are the main
matching surface. By default, they match across all selector kinds.

The two `Artifacts` filters are convenience wrappers:

- `filterAffectedByPath(path, kinds)` keeps rows where
  `artifact.affectedByPath(path, kinds)` is `true`
- `filterAffectedByDiff(changes, kinds)` keeps rows where
  `artifact.affectedByDiff(changes, kinds)` is `true`

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

Implementation sketch:

The `Artifacts` filters lower directly to `Artifact.affectedBy...`.

```go
func (s *Artifacts) FilterAffectedByPath(path WorkspacePath, kinds []WorkspaceProvenanceKind) *Artifacts {
	return s.FilterRows(func(a ArtifactRow) bool {
		return a.AffectedByPath(path, kinds)
	})
}

func (s *Artifacts) FilterAffectedByDiff(changes Changeset, kinds []WorkspaceProvenanceKind) *Artifacts {
	return s.FilterRows(func(a ArtifactRow) bool {
		return a.AffectedByDiff(changes, kinds)
	})
}
```

## Lockfile Acceleration

Provenance filtering can be expensive if provenance must be discovered by
loading artifacts first.

That creates a bad shape:

- load artifact
- compute provenance
- then decide the artifact was irrelevant

In large workspaces, this can waste a lot of time.

An optional optimization is to keep a lockfile-backed provenance index.

Terms:

- actual provenance: the files that really affect the artifact
- recorded provenance: the `WorkspaceProvenance` recorded by the engine when the
  artifact is loaded
- persisted provenance: a cached copy of recorded provenance from an earlier run

The idea is:

- store the last known `WorkspaceProvenance` for an artifact
- use it only for negative pruning
- if the stored provenance says the artifact is definitely unaffected, skip
  loading it
- otherwise load it normally

This index is advisory. It is not the source of truth.

Safety rules:

- every artifact that is actually loaded must recompute its provenance and
  rewrite its stored entry
- any construction-time input that can change whether an artifact is affected
  must become a selector in recorded provenance
- if exact recording is not possible, the engine must record a conservative
  selector instead

By default, cached matching uses all selector kinds. That includes
`WORKSPACE_READ` and `MODULE_SOURCE`.

If those rules hold:

- recorded provenance is correct when it is written
- persisted provenance is correct until it is invalidated

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

Module source is covered by the same lookup. If the defining Dagger module
changed, its `MODULE_SOURCE` selectors should match and the artifact should
load.

This does not replace artifact enumeration.

If a Dagger module changed, the engine must still load that module enough to
enumerate its current artifacts. Cached provenance can skip constructing known
unaffected artifacts. It cannot be the only source of truth for which artifacts
exist.

Persisted provenance also needs a semantics version.

If provenance recording or matching logic changes, all persisted entries from
the older semantics version must be treated as invalid.

This optimization is most useful in CI.

Local development can be simpler:

- ignore this optimization
- or always refresh entries whenever artifacts are loaded

This topic is about artifact provenance only. If plans later gain their own
provenance layer, the same idea can be extended to actions.

Implementation sketch:

```go
const ProvenanceSemanticsVersion = 1

type PersistedArtifactProvenance struct {
	SemanticsVersion int
	Provenance       WorkspaceProvenance
}

func ShouldLoadArtifact(a ArtifactKey, changes Changeset) bool {
	if cached, ok := LoadCachedProvenance(a); ok &&
		cached.SemanticsVersion == ProvenanceSemanticsVersion &&
		!cached.Provenance.AffectedByDiff(changes, nil) {
		return false
	}
	return true
}

func OnArtifactLoaded(a ArtifactKey, obj ObjectWithSchema) {
	StoreCachedProvenance(a, PersistedArtifactProvenance{
		SemanticsVersion: ProvenanceSemanticsVersion,
		Provenance:       CollectArtifactProvenance(obj),
	})
}
```

This cache is advisory. Any artifact that is actually loaded must refresh its
entry.

## Schema

```graphql
enum WorkspaceProvenanceKind {
  """Selector came from `workspace.directory()` or `workspace.file()`."""
  WORKSPACE_READ

  """Selector came from the source files of the Dagger module that defines the artifact."""
  MODULE_SOURCE
}

"""
One provenance selector over workspace paths.
"""
type WorkspaceSelector {
  """
  Root path of this selector, in canonical workspace-relative form.
  """
  path: WorkspacePath!

  """
  Optional include globs.

  Uses the same semantics as existing `CopyFilter` APIs.
  Patterns are evaluated relative to `path`.
  Empty means "include everything under path".
  """
  include: [String!]!

  """
  Optional exclude globs.

  Uses the same semantics as existing `CopyFilter` APIs.
  Patterns are evaluated relative to `path`.
  Applied after `include`. If both match, `exclude` wins.
  """
  exclude: [String!]!

  """
  False means this selector is exact.

  True means this selector is conservative. It is a safe upper bound. The
  actual smaller file set may only be known later.
  """
  conservative: Boolean!

  """
  Why this selector exists.
  """
  kind: WorkspaceProvenanceKind!
}

"""
Provenance over workspace paths for one value or artifact.
"""
type WorkspaceProvenance {
  """
  Known workspace selectors that contributed to this provenance.

  A selector rooted at `/` with kind `WORKSPACE_READ` represents workspace-wide
  provenance. It may still be narrowed by `include` and `exclude`.
  """
  selectors: [WorkspaceSelector!]!

  """
  Returns true if any selector in the chosen kinds, after `include` and
  `exclude` are applied, overlaps the given path.

  Matching uses subtree overlap, not exact string equality.

  Empty `kinds` means all kinds.
  """
  affectedByPath(
    path: WorkspacePath!
    kinds: [WorkspaceProvenanceKind!] = []
  ): Boolean!

  """
  Returns true if any selector in the chosen kinds, after `include` and
  `exclude` are applied, overlaps the changed-path region from the core
  `Changeset`.

  Uses the same overlap rule as `affectedByPath`.

  Empty `kinds` means all kinds.
  """
  affectedByDiff(
    changes: Changeset!
    kinds: [WorkspaceProvenanceKind!] = []
  ): Boolean!

  """
  Returns the semantic union of this provenance and `other`.
  """
  union(other: WorkspaceProvenance!): WorkspaceProvenance!
}

extend type Artifact {
  """
  Non-null provenance for this artifact.

  Empty provenance is `selectors = []`.
  """
  provenance: WorkspaceProvenance!

  """
  Returns true if this artifact is affected by the given path.

  Empty `kinds` means all kinds.
  """
  affectedByPath(
    path: WorkspacePath!
    kinds: [WorkspaceProvenanceKind!] = []
  ): Boolean!

  """
  Returns true if this artifact is affected by the given diff.

  Empty `kinds` means all kinds.
  """
  affectedByDiff(
    changes: Changeset!
    kinds: [WorkspaceProvenanceKind!] = []
  ): Boolean!
}

extend type Artifacts {
  """
  Keeps rows where `artifact.affectedByPath(path, kinds)` is true.

  Preserves the current scope and dimensions. Narrows only the row set.
  Composes with other `Artifacts` filters by `AND`.
  """
  filterAffectedByPath(
    path: WorkspacePath!
    kinds: [WorkspaceProvenanceKind!] = []
  ): Artifacts!

  """
  Keeps rows where `artifact.affectedByDiff(changes, kinds)` is true.

  Preserves the current scope and dimensions. Narrows only the row set.
  Composes with other `Artifacts` filters by `AND`.
  """
  filterAffectedByDiff(
    changes: Changeset!
    kinds: [WorkspaceProvenanceKind!] = []
  ): Artifacts!
}
```

## Examples

### Precise Selector

Suppose an artifact has:

```text
selectors = [
  { path = docs, include = **/*.md, exclude = docs/generated/**, conservative = false, kind = WORKSPACE_READ }
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

### Module Source Selector

```text
Artifact A
  defined by module at ci/go-toolchain
  src = ws.directory("docs")
```

This artifact may have both:

```text
selectors = [
  { path = docs, include = [], exclude = [], conservative = false, kind = WORKSPACE_READ },
  { path = ci/go-toolchain, include = [], exclude = [], conservative = false, kind = MODULE_SOURCE }
]
```

Then:

- `affectedByPath("ci/go-toolchain")` is `true`
- `affectedByPath("ci/go-toolchain", kinds = [WORKSPACE_READ])` is `false`
- `affectedByPath("ci/go-toolchain", kinds = [MODULE_SOURCE])` is `true`

### Simple Workspace-Wide Provenance

```text
Artifact A
  check(ws: Workspace): Void
```

This artifact may be affected by any workspace path:

```text
selectors = [
  { path = /, include = [], exclude = [], conservative = true, kind = WORKSPACE_READ }
]
```

So:

- `affectedByPath("docs")` is `true`
- `affectedByPath("src/api")` is `true`

### Narrowed Workspace-Wide Provenance

```text
selectors = [
  { path = /, include = [], exclude = [third_party/**, docs/**], conservative = true, kind = WORKSPACE_READ }
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
  { path = docs, include = [], exclude = [], conservative = false, kind = WORKSPACE_READ },
  { path = /, include = [], exclude = [third_party/**], conservative = true, kind = WORKSPACE_READ }
]
```

The exact selector is still useful for inspection and debugging. The
conservative selector is still useful for broad pruning.

## Caching Semantics

Conservative `WORKSPACE_READ` selectors and workspace-sensitive caching should
line up.

If an artifact has a conservative `WORKSPACE_READ` selector, caching should
treat that artifact as workspace-sensitive within that selector's region.

`MODULE_SOURCE` selectors should invalidate the artifact when the defining
module's source changes.

Provenance does not replace the engine's content-addressed execution model. It
exists for selection, inspection, and UX.

## Open Questions

1. Should actions gain a second, narrower provenance layer later for late
   workspace reads?
2. Should built outputs like containers, packages, or services also carry
   `WorkspaceProvenance`, or should provenance stop at source-backed objects?
