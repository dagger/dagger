# Workspace provenance prototype

## Purpose

Select artifacts affected by workspace file changes. Keep affected checks even
when their inputs cannot be determined precisely. Expose the reason for each
match. This prototype adapts the core model from PR #12900 to the current engine.

## Current code

Base: ddee9399a, current main when this work started.

- `core/artifacts.go` stores deferred artifact addresses and module tree nodes.
  An artifact is now a selected field, rather than only a top-level object or
  collection member from the earlier spec.
- `core/artifact_collection.go` expands collection keys and retains immutable
  collection views. Filters must preserve these views and keyed exclusions.
- `core/schema/artifacts.go` exposes discovery, selection, and typed conversions.
  `asChecks` uses `EvaluationItems`, including legacy batch behavior.
- `core/checks.go` represents deferred checks. Reading outcomes runs a check.
  Provenance inspection must never evaluate the artifact leaf.
- `core/schema/workspace.go` resolves relative reads from `Workspace.Cwd` and
  absolute reads from the workspace boundary. Workspace backing sources also
  include Git trees, directories, mounts, and pending changes.
- The result system distinguishes local handles from portable construction
  recipes. A recipe records receiver, explicit inputs, and implicit inputs.
  Persisted artifact selections already retain their module trees and workspace.

## Model and prototype boundary

A provenance selector has a workspace-root-relative path, include and exclude
patterns, a kind, and a conservative flag. `WORKSPACE_READ` describes workspace
inputs. `MODULE_SOURCE` describes module implementation inputs. An exact selector
is exact for its input region, not a claim that every byte was consumed.

This first prototype derives a safe upper bound from construction recipes of
an artifact's receiver. It does not run the artifact field or its check body.
Recipes preserve construction inputs that a stored-field-only walk would miss,
including configuration read to decide which files to load. A direct workspace
file or directory read with an absolute path can supply a narrow selector.
Relative reads require workspace Cwd resolution; until that mapping is proven,
they supply a conservative root selector. Passing a Workspace to arbitrary
module code supplies a conservative root selector. This includes constructors:
they can consume scalar discovery inputs that do not survive in stored fields.

This has lower precision than complete runtime input tracking. That cost is
explicit. Most current functions accepting Workspace will initially remain
selected. Future work can record exact reads during module construction and
retain those inputs across serialization and content equivalence.

Unknown roots, missing module metadata, load failures, core receivers, and
bound artifacts use conservative root selectors. Missing provenance must not
be interpreted as an empty dependency set. Module sources use a conservative
root MODULE_SOURCE selector in this prototype: SDK compiler inputs, local
replacements, imported dependencies, and remote source pins cannot safely be
mapped from SourceRootSubpath alone. Narrow module invalidation requires that
complete mapping. Default matching includes both kinds. Callers may select
WORKSPACE_READ alone for inspection, but that loses module-change protection.

## First implementation result

This branch is an API and inspection prototype. Every artifact currently has
conservative root selectors for both kinds. Thus every nonempty diff retains
every artifact under default matching. It does not yet reduce CI work. Narrow
read selectors remain visible alongside those roots to inspect what recipes
can recover. Removing either root requires complete runtime read tracking and
compiler input mapping. That is the main implementation requirement exposed by
this prototype.

## API

Expose `Artifact.provenance`, `Artifact.affectedByPath`, and
`Artifact.affectedByDiff`. Expose the same predicates on WorkspaceProvenance.
Expose `Artifacts.filterAffectedByPath` and `filterAffectedByDiff`.

Paths use canonical workspace-root coordinates. Reject parent traversal.
Diff matching uses core Changeset paths, including both sides of renames and
all removed paths. It does not infer a Git baseline. Callers supply a Changeset.
Kinds are validated. Empty kinds means all kinds. Selectors have deterministic
order and no duplicates.

Filtering first expands the existing selection. It computes provenance without
running leaf values. It then removes unaffected complete artifact addresses
through the existing exclusion API. Thus collection view, type filters, and
legacy batching remain owned by the current selection system. No separate plan
or orchestration layer is introduced. Filtering is eager and can construct
receivers and enumerate collections. It is not an early discovery optimization.

## Matching

File paths match by subtree overlap. Include and exclude patterns are relative
to the selector root. Exclude wins. Directory overlap that cannot be proved
disjoint remains selected. This makes broad directory queries conservative,
while individual changed file paths can use precise glob matching. Invalid
patterns and paths return errors.

## Cache and persistence

Provenance objects contain scalar metadata only and need a persisted JSON
encoding. Artifact provenance resolvers and filters must use the current
workspace state; use uncached resolver behavior for this prototype. They compute
from retained receiver recipes on each request. Existing artifact persistence
retains selected addresses and exclusions. No persisted pruning index is added.
No content digest or execution cache key is changed. Provenance is selection
metadata, not a substitute for normal execution caching.

## Validation

Unit tests cover path validation, include/exclude rules, overlap, normalization,
kind selection, recipe input traversal, absolute reads, opaque workspace access,
and conservative fallback. Compile the affected core and schema packages for
Linux, since engine code requires Linux facilities. Check formatting and diff.
Record any environment limits and unexecuted integration cases here.

## Required design for useful pruning

The next implementation must record reads where the engine resolves them:

1. At `workspaceSchema.directoryAt` and `fileAt`, record the resolved path,
   include/exclude patterns, and backing workspace identity. Record the request,
   rather than only files present today, so newly added matching files are inputs.
2. Carry these regions on File and Directory results through lazy operations,
   persistence, remote transfer, and content equivalence. Union inputs for copy,
   merge, and generated values. Keep the original workspace coordinates when
   values move to another directory. A transformation can narrow a region only
   when its relation to the original paths is known.
3. Associate reads with the active module FunctionCall. Workspace reads that
   produce scalars, such as glob results and manifest contents, must contribute
   construction dependencies even when the returned object stores no File.
   Nested calls must contribute their reads to the caller. Request completion
   must wait for this recording before caching its returned value.
4. Include provenance in the equivalence policy for module values. Two equal
   payloads with different dependency regions cannot silently share the narrower
   region. Either retain separate metadata or combine their dependency regions.
5. Gather materialized stored values with cycle detection. Do not call public
   methods while walking fields. A late Workspace argument or stored Workspace
   remains conservative until a separate action-level mechanism accounts for it.
6. Map module source, SDK source, manifest, generated bindings, local compiler
   replacements, and module wiring configuration to workspace selectors. Preserve
   the conservative module root when any compiler input has an unknown mapping.
7. Distinguish the workspace boundary from Cwd and the backing repository. A
   path from another workspace cannot be treated as a path in this workspace.
   Changeset filtering needs an explicit coordinate contract or validated origin.
8. Store selection predicates through collection expansion and legacy batching.
   Tests must demonstrate that re-expansion cannot restore a dropped key, and
   that a batch executes precisely the surviving subset.

Only after these rules hold can the prototype remove its conservative roots.

## Follow-up

Record workspace reads with workspace identity and resolved Cwd in the result
graph. Preserve metadata through transformations, imported objects, persistence,
and content-equivalent values. Map complete module and SDK compiler inputs.
Then add a CI pruning index with semantics versioning and mandatory invalidation
of discovery inputs. Add CLI Git-baseline convenience after the API is proven.

## Example query

Use the existing query client with this engine schema:

```graphql
query {
  currentWorkspace {
    artifacts {
      filterAffectedByPath(path: "/src") {
        items {
          uri
          provenance {
            selectors { path include exclude conservative kind }
          }
        }
      }
    }
  }
}
```

To filter by a diff, obtain a Changeset through the existing workspace or
Directory APIs. Pass its ID as the `changes` argument to
`filterAffectedByDiff`. The diff must use workspace-boundary-relative paths.
This prototype does not verify that coordinate relationship automatically.

## Validation results

Passed:

- `go test ./core/provenance -count=1` on the host.
- Linux core tests: `TestWorkspaceProvenanceRecipe`,
  `TestWorkspaceProvenanceUnknownArtifact`, and
  `TestWorkspaceProvenancePersistence`.
- Linux schema tests: `TestWorkspaceProvenanceSchema` and
  `TestCoreSchemaObjectsHavePersistedFamilies`.
- `git diff --check` and Go formatting.

Core and schema test binaries were compiled with `GOOS=linux GOARCH=amd64`
and executed in a Linux Docker container. Native macOS core compilation is
unsupported by existing Linux mount code. The schema test also executes path
matching through actual dagql selections and checks the current `ID!` argument
shape. These tests do not start a complete Dagger engine or exercise a live
module runtime, cloud transfer, or CI workload.
