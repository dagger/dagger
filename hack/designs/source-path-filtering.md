# Source path filtering

## Problem

Users need to select artifacts by the source paths they depend on. Today, they
must run unrelated checks or maintain separate path rules. They also need to see
why a path selects an artifact.

## Solution

Derive provenance from current inputs. Expose it through `Artifact.provenance`
and `Artifacts.provenance`. Use that same data for path filtering and explanations.
Inspecting provenance must not run artifact bodies or require an earlier run.

## Dependencies

A source path is a path in the source repository that an artifact depends on.
A selector describes a path and its include/exclude patterns. Patterns cover
possible files, so additions and deletions count too.

| Input | Source dependency |
|---|---|
| File | Its source file. |
| Filtered Directory | The region covered by its filters. |
| Workspace retained for evaluation | Every path in that Workspace. |

Combine dependencies from prepared arguments, the receiver, engine inputs, and
module code. Record the Workspace queries used to prepare them. A returned
object retains those queries, including reads discarded or converted to scalars.

Passing a Workspace gives access; it does not read every file. For example,
`makeTool(ws)` can query one File and return an object containing it. If the
object retains no Workspace, that input is bounded to the queried file.

**Bounded evaluation** means preparing source inputs first, then evaluating
without Workspace access. A Workspace argument or a stored Workspace anywhere
in the evaluation receiver is a wildcard: the unrun function can query any path.
Engine context used to route an Artifact or Check is not an author input.
Runtime inputs count too. An entrypoint without `sources()` gets Workspace
implicitly. An entrypoint with `sources()` depends on its prepared Directory.

Source regions cover queries, not just the files they returned. A glob must
cover new matches. Git dirty-state queries have a repository-wide scope. Their
Boolean results remain ordinary computation data, not reference values stored
in provenance. Missing source origins must have an explicit explanation.
`WorkspacePath` is a scalar for path coordinates and client display. It does not
carry provenance.

## Inspection and matching

Prepare the artifact's current arguments and receiver with the normal DagQL
planning path. This resolves defaults and engine inputs. Constructors and
collection discovery can run; their source queries become dependencies of the
prepared values. Input preparation errors still fail the request.
For example, a missing required File fails before path matching. The artifact
body stays unrun.

`Artifact.provenance` returns selectors, their input origins, and any unresolved
origin issues. `Artifacts.provenance` combines the dependencies of the selected
artifacts. `items { provenance }` gives the detail for each artifact.

Match requested paths against those selectors. One match selects the artifact.
An empty path set selects nothing. Include module source and configuration paths,
including the `.env` search paths that can supply module defaults.
An explanation must show the input path, the matching selector, and the input
that introduced that dependency. Unresolved origins retain a root selector with
a stated issue.

## DagQL caching

Keep one execution cache. Selected checks still use normal DagQL caching.
Selection does not itself invalidate a cached result or report a passing check.
Path matches are conservative. Comparing query results, such as an unchanged
Git dirty-state Boolean, belongs in DagQL caching, not a second validation layer
inside path filtering.

DagQL can reuse equal File or Directory contents from different source paths.
The engine must preserve those origins when it reuses values. Origins must stay
with the value through collection, persistence, and transfer. Losing an origin
must never cause a false exclusion.

Store source-origin facts with cached values. These facts include the queries
made by the calls that prepared the inputs; they do not require a previous
check run. Calls that reuse a cached result retain its facts. They do not change
execution cache keys. Combine origins when the engine shares a value across
producers. This can give a wider dependency set than one producer alone.

Older values without complete origin data remain conservative and report the
missing coverage. A new fact cannot prove that all older origins are known.
A custom SDK can load types before it prepares its runtime. Until that runtime
has inspectable inputs, keep the artifact selected and report this limit.

## CLI

`--path PATH` selects a source file or subtree. Repeat it to combine paths.
Relative paths start at cwd; `/` starts at the workspace root.

`--commit COMMIT` supplies paths changed by that commit against its first parent.
A root commit uses an empty tree. `--uncommitted` supplies current changes from
HEAD, including non-ignored untracked files. Repeated commits and uncommitted
changes combine their path sets.

When Git flags and `--path` are combined, limit the changed paths before matching
artifact dependencies. Listing and execution use one workspace snapshot. An empty
selection succeeds.

The API has two selection fields: `filterAffectedByPath(paths)` and
`filterAffectedByDiff(changes, paths)`. The diff field's optional paths limit the
changes before matching. Git supplies paths; it adds no provenance rule.

## Guidelines for module developers

1. **Limit inputs.** Query Workspace while preparing inputs. Pass File or filtered
   Directory views to evaluation. A Workspace retained in its arguments or
   receiver matches every path, even if the body later reads only Markdown.
2. **Delay execution.** Keep tools in check bodies. Constructors can form inputs
   and discover keys. Do not run tools early to help filtering.
3. **Respect selection.** Expose independent work as collection items. Shared
   commands must process the selected keys.

## Expected gains

Filtering can avoid commands and cloud launches for checks with narrow inputs.
It cannot exclude checks that retain Workspace access for evaluation. A broad
source query in a constructor or discovery call also gives its returned objects
broad dependencies. Filtering after discovery does not avoid discovery work.

A [Cloud run of the earlier input-only rule](https://dagger.cloud/dagger/traces/d8a4016fb68e8ccfbbb9070f8649ada2)
used all 845 expanded checks in `dagger/dagger` at `7e9eaacc37`.

| Edit | Selected | Omitted |
|---|---:|---|
| `README.md` | 844 / 845 | `docs/check` |
| Docs introduction page | 845 / 845 | None |
| Python SDK `_utils.py` | 844 / 845 | `docs/check` |

`--path` and `--uncommitted` returned identical sets for each edit. A clean
checkout selected zero checks. A Python edit limited to `/docs` also selected
zero checks.

The earlier rule treated any ancestor Workspace argument as a wildcard. The
results above do not measure bounded evaluation. Go collections account for 757
checks and still take Workspace at evaluation. They need prepared source views
to become narrow. SDK checks that build the engine can retain a real broad
dependency through Git dirty-state queries.

The first unfiltered list took 77.7 seconds. Subsequent `--uncommitted` selection
for the three edits took 6.7–7.7 seconds in this sequential run. These times
include loading and input preparation. Check bodies were not run.

The original intent is described in [Modules v2](https://github.com/dagger/dagger/pull/12900).
