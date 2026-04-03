# Provenance

## Status: Designed (high level)

Depends on: [Artifacts](./artifacts.md)

## Table of Contents

- [Summary](#summary)
- [Source of Provenance](#source-of-provenance)
- [Tracking](#tracking)
- [Provenance Predicates](#provenance-predicates)
- [Selector Shape](#selector-shape)
- [Path Filter Examples](#path-filter-examples)
- [Provenance Union](#provenance-union)
- [Transitive Aggregation Examples](#transitive-aggregation-examples)
- [Conservative Workspace-Sensitive Taint](#conservative-workspace-sensitive-taint)
- [Filtering Model](#filtering-model)
- [Artifacts Expansion](#artifacts-expansion)
- [Implementation](#implementation)
- [Caching Semantics](#caching-semantics)
- [Open Questions](#open-questions)

## Summary

Provenance starts with `workspace.directory()` and `workspace.file()`. V1 adds
path and changeset filtering to [Artifacts](./artifacts.md). These filters run
before verbs. Collections are separate. Execution plans use the already
selected artifact set.

## Source of Provenance

V1 provenance comes from these workspace API reads:
- `workspace.directory(...)`
- `workspace.file(...)`

These calls store workspace provenance on the returned `Directory` / `File`
values.

This is intentionally narrow:
- not general lineage
- not git metadata itself
- not module ownership

Path- and changeset-based selection is derived from stored provenance. These
filters are not provenance records themselves.

## Tracking

Provenance is stored on values, then collected onto artifacts.

- `workspace.directory(...)` attaches provenance to the returned `Directory`
- `workspace.file(...)` attaches provenance to the returned `File`
- ordinary object composition carries that provenance forward
- an artifact's provenance is derived from the values reachable from its fields

V1 gets precise provenance from stored values. It also adds a coarse
workspace-sensitive rule described below.

## Provenance Predicates

Public filters over stored provenance:
- "matches path `./docs`"
- "matches diff `HEAD~1..HEAD`"
- "overlaps this changed path set"

V1 uses overlap semantics for both public provenance filters:

- `filterAffectedByPath(path)` matches when effective artifact provenance
  overlaps the selected path region
- `filterAffectedByDiff(changes)` matches when effective artifact provenance
  overlaps the changed-path region derived from the `Changeset`

## Selector Shape

A provenance record is one or more workspace selectors. At minimum, each
selector stores the path region that was read. It may also store `include` and
`exclude` constraints.

Root-path provenance at `/` is the canonical coarse provenance region meaning
"matches all workspace paths".

`filterAffectedByPath(path)` checks overlap against what the selector really
includes
after `include` / `exclude` are applied, not just against its root path.

## Path Filter Examples

Suppose an artifact carries this provenance selector:

```text
path = docs
include = **/*.md
exclude = docs/generated/**
```

Then:

- `filterAffectedByPath("docs")` matches
- `filterAffectedByPath("docs/intro.md")` matches
- `filterAffectedByPath("docs/intro.png")` does not match
- `filterAffectedByPath("docs/generated")` does not match
- `filterAffectedByPath("docs/generated/api.md")` does not match

The important rule is simple: overlap is checked against the final selected
file set, not just against the selector root.

Changeset overlap follows the same rule. If a `Changeset` contains only paths
under `docs/generated/**`, the selector above does not match
`filterAffectedByDiff(changes)` because that region is excluded from the
effective selected file set.

## Provenance Union

Provenance unions across fields and nested objects:

```
provenance({foo: A, bar: B}) = union(provenance(A), provenance(B))
```

An artifact's provenance is the union of all reachable stored field values,
walking through nested objects too.

## Transitive Aggregation Examples

An artifact can match a path filter in two different ways:

- **precise match** — from stored `Directory` / `File` values
- **coarse match** — from stored `Workspace` or exposed `Workspace`-taking
  functions, which taint the artifact with `/`

Precise direct fields:

```text
Artifact A
  src    = Directory(path=docs, include=**/*.md, exclude=docs/generated/**)
  config = File(path=config/site.yaml)
```

Effective artifact provenance:

- `docs/**/*.md`, excluding `docs/generated/**`
- `config/site.yaml`

So:

- `filterAffectedByPath("docs")` matches
- `filterAffectedByPath("config")` matches
- `filterAffectedByPath("docs/generated")` does not match
- `filterAffectedByPath("scripts")` does not match

Precise nested fields:

```text
Artifact A
  site = Site

Site
  src    = Directory(path=docs, include=**/*.md, exclude=docs/generated/**)
  config = File(path=config/site.yaml)
```

`Artifact A` still gets the same provenance, even though the values are nested
under `site`.

This rule matters because nesting objects must not break provenance. The engine
should walk all reachable stored field values, not only direct top-level
fields.

Coarse match from a stored `Workspace` field:

```text
Artifact A
  ws = Workspace
```

Result:

- no precise path provenance is known
- the artifact is tainted with `/`
- `filterAffectedByPath("docs")` matches
- `filterAffectedByPath("src/api")` matches

Coarse match from an exposed `Workspace`-taking function:

```text
Artifact A
  check(ws: Workspace): Void
```

Result:

- no precise path provenance is known from fields
- the artifact is tainted with `/`
- `filterAffectedByPath("docs")` matches
- `filterAffectedByPath("src/api")` matches

## Conservative Workspace-Sensitive Taint

V1 must avoid false negatives. So it adds one coarse rule on top of precise
field provenance.

An artifact is tainted with root-path provenance at `/` if either is true:

- it stores `Workspace` in a field
- it exposes any public function that accepts `Workspace`

This rule is intentionally broad:

- it applies to public verb methods and other public helpers alike
- `Workspace` arguments remain allowed, but they trade precision for coarse `/`
  taint during artifact filtering
- taint is derived from the exposed schema surface, not from private
  implementation details that introspection cannot see

Consequences:
- the object matches all path and git filters
- it loses precise source filtering
- it should be treated as workspace-sensitive for caching

This is a deliberate tradeoff:
- **precise artifact:** materialize `Directory` / `File` inputs early →
  precise provenance, precise filtering
- **workspace-sensitive artifact:** store `Workspace`, or expose a
  `Workspace`-taking function → matches everything, loses per-path precision

This keeps `Artifacts` as the main filter surface while staying safe for lazy
workspace access.

Known limitation:
- sibling functions may inherit a false-positive `/` match because taint is
  applied at artifact granularity, not per function

Example:

```text
Artifact A
  check(ws: Workspace): Void
  generate(): Void
```

In v1, the artifact gets `/` as a whole. We do not try to say that `check`
has `/` but `generate` does not. So both commands may match the artifact:

```text
dagger check --path=docs
dagger generate --path=docs
```

Possible future refinement:
- execution plans may add a second, narrower provenance layer later if the
  false-positive rate becomes a practical problem

## Filtering Model

The main model is artifact filtering, not plan filtering.

- `workspace.artifacts` and verb-scoped artifact views are filtered by artifact
  provenance
- `filterAffectedByPath` and `filterAffectedByDiff(changes: Changeset!)` are
  dedicated
  provenance predicates on `Artifacts`; they are not selector dimensions
- provenance filters preserve the current `Artifacts` scope and header row;
  they narrow only the matching artifact rows
- `Artifacts.check`, `Artifacts.generate`, and other verbs compile plans from
  the already selected artifact set

So provenance answers "which artifacts matter?" before plan compilation. Plans
may refine this later, but v1 does not need that.

These predicates work on any `Artifacts` scope, not only the root scope.

Examples:

```text
workspace.artifacts.filterAffectedByPath("docs")
workspace.artifacts.filterVerb(CHECK).filterAffectedByPath("docs")
workspace.artifacts.filterVerb(GENERATE).filterAffectedByDiff(changes)
```

## Artifacts Expansion

Provenance extends the base `Artifacts` surface from
[artifacts.md](./artifacts.md) with two dedicated filters.

```graphql
extend type Artifacts {
  """
  Narrow by workspace path provenance. Matches artifacts whose effective
  provenance overlaps the given path selector.
  """
  filterAffectedByPath(path: WorkspacePath!): Artifacts!

  """
  Narrow by source changes. Matches artifacts whose effective provenance
  overlaps the given changeset. This is a provenance predicate, not a selector
  dimension.
  """
  filterAffectedByDiff(changes: Changeset!): Artifacts!
}
```

CLI lowering examples:

```text
dagger check --path=./docs      → workspace.artifacts.filterVerb(CHECK).filterAffectedByPath("./docs")
dagger check --affected-by=...  → workspace.artifacts.filterVerb(CHECK).filterAffectedByDiff(...)
```

## Implementation

V1 can be built in four simple steps.

### 1. Save provenance when reading the workspace

When the engine returns a workspace-backed `Directory` or `File`, it stores the
workspace selector on that value.

```go
type WorkspaceSelector struct {
	Path    string
	Include []string
	Exclude []string
}

type Directory struct {
	Provenance []WorkspaceSelector
}

type File struct {
	Provenance []WorkspaceSelector
}

func (ws *Workspace) Directory(path WorkspacePath, opts DirOpts) *Directory {
	return &Directory{
		Provenance: []WorkspaceSelector{{
			Path:    string(path),
			Include: opts.Include,
			Exclude: opts.Exclude,
		}},
	}
}

func (ws *Workspace) File(path WorkspacePath) *File {
	return &File{
		Provenance: []WorkspaceSelector{{
			Path: string(path),
		}},
	}
}
```

Example:

```text
src = ws.directory("docs", include="**/*.md", exclude="docs/generated/**")
```

That stored `Directory` now carries:

```text
path = docs
include = **/*.md
exclude = docs/generated/**
```

### 2. Union provenance through stored fields

The engine walks stored fields. It also walks nested stored objects.

```go
func CollectProvenance(v any) []WorkspaceSelector {
	switch x := v.(type) {
	case *Directory:
		return x.Provenance
	case *File:
		return x.Provenance
	case ObjectWithFields:
		var out []WorkspaceSelector
		for _, f := range x.MaterializedFields() {
			out = UnionSelectors(out, CollectProvenance(f.Value))
		}
		return out
	default:
		return nil
	}
}
```

Example:

```text
Artifact A
  site = Site

Site
  src = ws.directory("docs")
  cfg = ws.file("config/site.yaml")
```

The artifact gets both regions:

```text
docs
config/site.yaml
```

### 3. Add coarse `/` taint

After collecting precise field provenance, the engine adds `/` if the artifact
stores `Workspace` or exposes a public `Workspace`-taking function.

```go
func ApplyWorkspaceTaint(obj ObjectWithSchema, prov []WorkspaceSelector) []WorkspaceSelector {
	if obj.HasStoredWorkspaceField() || obj.HasPublicWorkspaceArgFunction() {
		return UnionSelectors(prov, []WorkspaceSelector{{Path: "/"}})
	}
	return prov
}
```

Example:

```text
Artifact A
  check(ws: Workspace): Void
  generate(): Void
```

The artifact gets:

```text
/
```

That means both commands may match it:

```text
dagger check --path=docs
dagger generate --path=docs
```

### 4. Implement the two Artifacts filters

`filterAffectedByPath` overlaps the stored selectors with the requested path
region. `filterAffectedByDiff` does the same thing, but uses the changed paths
from the `Changeset`.

```go
func (s *Artifacts) FilterAffectedByPath(path WorkspacePath) *Artifacts {
	return s.FilterRows(func(a ArtifactRow) bool {
		return OverlapsPath(a.Provenance, string(path))
	})
}

func (s *Artifacts) FilterAffectedByDiff(changes Changeset) *Artifacts {
	paths := changes.Paths()
	return s.FilterRows(func(a ArtifactRow) bool {
		return OverlapsAnyPath(a.Provenance, paths)
	})
}
```

Examples:

```text
workspace.artifacts.filterAffectedByPath("docs")
workspace.artifacts.filterVerb(CHECK).filterAffectedByPath("docs")
workspace.artifacts.filterVerb(GENERATE).filterAffectedByDiff(changes)
```

In all three cases:

- the current scope stays the same
- the current `dimensions` stay the same
- only the matching rows remain

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

Provenance is not a second invalidation system. It exists for selection and UX.
It does not replace the engine's content-addressed execution model.

## Open Questions

1. Should execution plans gain a second, narrower provenance layer later?
   (`docusaurus`-style just-in-time workspace tracing suggests a real use
   case, but it is not a good foundation for v1 artifact filtering.)
2. How far should provenance extend beyond source-backed workspace objects to
   built outputs (containers, packages, services)?
