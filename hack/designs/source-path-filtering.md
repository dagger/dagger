# Source path filtering

## Problem

Users need to select artifacts by the source paths they depend on. Today, they
must run unrelated checks or maintain separate path rules. They also need to see
why a path selects an artifact.

## Solution

Derive provenance from current inputs. Expose it through `Artifact.provenance`
and `Artifacts.provenance`. Use that same data for path filtering and explanations.
Inspecting provenance must not run artifact bodies or require an earlier run.

## Correctness contract

Provenance is the record of source dependencies. Selection may include extra
artifacts. It must not omit an artifact whose prepared inputs, source queries,
module code, or available Workspace access overlap the requested paths.

Omit an artifact only when all relevant dependencies are known and do not overlap
the requested paths. Missing records are not proof of no dependencies. A known
dependency outside the inspected workspace adds no path in that workspace.
An unresolved dependency that could refer to it keeps the artifact selected.
An empty requested path set selects nothing in either case.

Inspection and selection must use the same dependency record. An unrelated call
must not change that record, even if the two calls return equal contents.
Correctness includes both required matches and unrelated-path exclusion.

## Dependencies

A source path is a path in the source repository that an artifact depends on.
A selector describes a path and its include/exclude patterns. Patterns cover
possible files, so additions and deletions count too.

| Input | Source dependency |
|---|---|
| File read from Workspace | Its source file. |
| Filtered Directory | The region covered by its filters. |
| Generated File or Directory | The origins of the inputs and queries that produced it. These can contain no workspace paths. |
| Workspace retained for evaluation | Every path in that Workspace. |

Each region needs a source identity and a mapping into the inspected workspace.
A client ID routes a request; it is not sufficient proof of source identity.
Preserve or explicitly map source coordinates across workspace views, sessions,
persistence, and transfer. A different client ID does not prove that a source
belongs to another workspace. A missing mapping must remain unresolved.

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

## Source boundary

Record source queries at the Workspace API boundary. `Workspace.file(path)`
records the source file. `Workspace.directory(path, include, exclude)` records
the queried region and patterns, even when the result is empty.
The Host operations that implement these queries must not add another source
dependency. Keep the Workspace record when the backend returns equal contents.

Store the logical source boundary on the Workspace producer. The boundary names
its source, independently of shared payload contents and the client used to
route a call. A native Workspace view carries its input boundary. Preserve that
boundary through IDs, persistence, and transfer. An unresolved boundary keeps
its possible source scope conservative.

Host accesses the calling client's filesystem. It does not always refer to the
outer client's workspace. Classify an actual operation and its client before
assigning a source dependency:

| Operation | Source rule |
|---|---|
| Host read inside a Workspace query | Use the enclosing Workspace query. |
| Host read in a module runtime | Follow the runtime and function input origins. |
| Client Host.findUp(name) | Record the name in the selected Workspace current directory and each parent through the Workspace root. |
| Client Host file or directory read | Use a proven workspace mapping, or report an unresolved origin if it can overlap the workspace. |
| Host socket or service setup | Follow its actual inputs. The Host type alone does not imply a workspace file read. |

Do not remove all Host dependencies to improve selection. Do not convert all
Host ancestry into a root dependency either. Module-generated bindings exclude
Host, and ordinary direct module Host queries are rejected on this branch.

`Host.findUp` has an explicit rule for path filtering. From `src/pkg`, a search
for `.env` records `src/pkg/.env`, `src/.env`, and `.env`, including missing files.
It excludes descendants and locations above the Workspace root. This rule does
not change the Host search or DagQL caching. Store the search name, Workspace
boundary, and current directory with the producing call. Preserve them when a
stored value becomes an input to another call. Set a new binding only for an
actual search call or reuse of a call that performed the search. Null and
discarded results must retain the same record.
Internal engine calls still require the client and operation checks above.

A source mapping must follow the backend's symbolic-link and mount rules.
Record both the link and target dependencies when both can affect a read.
If the mapping cannot establish those dependencies, report the unresolved scope.
Lexical path containment alone is not proof of a mapping.

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
that introduced that dependency. Distinguish three cases: known regions, a
complete analysis with no workspace regions, and incomplete analysis.
An incomplete analysis retains a root selector when the missing origin could
overlap this workspace. State the operation, client scope, and missing mapping
in the issue. A known broad query is a dependency, not a missing-origin issue.

## DagQL caching

Keep one execution cache. Selected checks still use normal DagQL caching.
Selection does not itself invalidate a cached result or report a passing check.
Path matches are conservative. Comparing query results, such as an unchanged
Git dirty-state Boolean, belongs in DagQL caching, not a second validation layer
inside path filtering.

Keep two concepts separate. A **producer** is the call result with its call
frame, source facts, and coverage state. A **payload** is its stored value.
Coverage states whether the source analysis is complete. Equal File or Directory
payloads can be shared. Their producers must not exchange or combine origins.
An empty Git directory must not add a Git dependency to every empty directory.

An ordinary result ID identifies a producer. Argument conversion, call-frame
lookup, and value loading must preserve that producer. This applies to complete
coverage with zero facts and incomplete coverage with zero facts too. Do not
make identity depend on whether the fact list is nonempty.

Exact producer lookup must still enforce session resource access and dependency
attachment checks. Schema recovery may explicitly select an equivalent module
when the recorded module cannot be used. Keep that operation separate from
ordinary producer lookup. A guard in a shared equivalence helper is not enough
to define both behaviors.

SDK conversion must read composite stored fields through their exact attached
owner. Collections can hide those fields from the public schema. Internal
stored-field reads preserve the declared field path and exact schema module.
They complete source capture before applying a child view. These reads keep the
public schema unchanged and do not require a module file source.

Keep four records separate:

- **Prepared origins** come from the current arguments, receiver, engine inputs,
  and module runtime. Preserve the records from completed runtime setup.
- **Body source regions** record source queries made while preparing a value.
  Bind each region to a named input position, including nested fields and list
  items. These positions describe inputs; they are not cached result IDs.
- **Consumed context origins** record implicit inputs that native code uses
  during execution. For example, container execution uses the current module to
  set service search domains. Record that exact module when it is consumed.
  Inspecting an existing value does not create a new consumption record.
- **Retained capabilities** record where the returned value stores Workspace
  access and which input position supplied it. That access remains a wildcard
  when the value becomes an evaluation receiver.

An exact cache hit has the same call recipe and producing inputs. Keep its stored
prepared origins, body regions, consumed context origins, retained capabilities,
boundary, and coverage.
Runtime setup does not run again, so its discarded reads cannot be reconstructed
from the returned value. Refresh only the caller binding of body Host.findUp
rules. Pass the preserved records to the parent call as well as the returned value.

On a structural cache hit, use the current prepared origins. Map the stored body
regions and retained capabilities through the current named input positions. Equal
contents do not transfer absolute origins: a call with `/right/input.txt` must
not inherit `/left/input.txt` from an earlier call. Ordinary structural cache
hits remain complete when all input bindings are known. Structural means that
inputs have the same value shape and equal contents.

Keep consumed context origins on both exact and structural cache hits. They
belong to the call that consumed the input. A later caller cannot change them
without executing that operation again. Pass these records to the enclosing
call even when it discards the child result.

A retained Workspace can have more than one possible input position. If those
positions cannot be distinguished, keep all possible source scopes and report
incomplete coverage. Do the same for a missing binding or an unknown retained
capability. Ambiguity must come from the actual input bindings; a cache hit alone
does not make coverage incomplete.

Generic DagQL cache hooks capture and rebind these records. The cache keeps the
record contents opaque: core code defines their source meaning. The records are
metadata, not executable queries. Do not replay source queries or run an artifact
body to repair provenance. Capture records after every successful resolver return,
including uncached calls and null results.

Preserve producer identity, facts, coverage, and source mappings through IDs,
collections, cached nulls, persistence, and transfer. These records do not become
a second execution cache or require a previous check run. Source facts do not
change payload equality or execution cache keys.

Older values without complete origin data remain conservative and report the
missing coverage. A new fact cannot prove that all older origins are known.
A custom SDK can load types before it prepares its runtime. Until that runtime
has inspectable inputs, keep the artifact selected and report this limit.

## CLI

`--path PATH` selects a source file or subtree. Repeat it to combine paths.
Relative paths start at cwd; `/` starts at the workspace root.
Treat a literal path as a possible subtree without consulting the current
filesystem. This includes paths that do not exist yet.

`--commit COMMIT` supplies paths changed by that commit against its first parent.
A root commit uses an empty tree. `--uncommitted` supplies current changes from
HEAD, including non-ignored untracked files. Repeated commits and uncommitted
changes combine their path sets.
Diff file paths identify exact files. A rename supplies both the old and new
paths. Removed directories must cover their removed contents. Thus a literal
`--path` can select more artifacts than a diff for the same spelling.

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

## Implementation order and merge conditions

1. **Preserve producers.** Add session-checked exact lookup for ordinary result
   IDs. Keep schema recovery explicit. Test nonempty facts, complete empty
   facts, incomplete empty facts, conflicting equal producers, resource checks,
   attachment failure, restart, and transfer.
2. **Preserve read bindings.** Test the same preparation function with equal
   contents from different source paths. Check current origins and discarded
   reads. Cached facts must not introduce an unrelated producer's paths.
3. **Classify Host operations.** Identify the failing operation and its client.
   Verify Workspace backend calls, runtime file reads, and socket setup
   separately. Keep unresolved reads conservative until their mapping is proven.
4. **Verify selection.** Pass the existing artifact input tests. Preserve all
   positive `defaultPath` assertions. A separate raw Host read with equal contents
   must leave an existing artifact's provenance unchanged. Also test an artifact
   that actually receives an unresolved Host input; it must remain selected.
5. **Measure only after correctness passes.** Compare the exact unfiltered URI
   set, then a clean diff and a README-only diff. Reject load-failure artifacts.
   Report source revisions, selection counts, issues, and whether bodies ran.

The acceptance cases must cover:

| Case | Required result |
|---|---|
| Explicit and `defaultPath` File inputs | Both select the required checks and exclude unrelated paths. |
| Empty filtered Directory | New matching files select it; excluded files do not. |
| Constructor reads discarded or converted to scalars | Reads remain on the returned producer, including cached null calls. |
| Workspace used only during preparation | Keep queried regions; do not add a wildcard for mere access. |
| Workspace retained in nested receiver fields | Match the whole corresponding workspace. |
| Equal payloads with different source origins | Preserve each producer's origins in either call order. |
| Changed source client or transferred result | Preserve a proven mapping, or report incomplete coverage. |
| Additions, deletions, renames, and symbolic links | Use all affected source paths under the backend's read rules. |
| Git flags with path limits | Limit changed paths before dependency matching. |
| Root commits, merge commits, and repeated commits | Use the specified base and combine the changed paths. |
| Untracked files | Include non-ignored files in `--uncommitted`; exclude ignored files. |
| Module code, configuration, and defaults search paths | Select dependent artifacts, including newly added defaults files. |
| Listing and inspection | Leave artifact bodies unrun. |

## Measurements and limits

Filtering can avoid commands and cloud launches for checks with narrow inputs.
It cannot exclude checks that retain Workspace access for evaluation. A broad
source query in a constructor or discovery call also gives its returned objects
broad dependencies. Filtering after discovery does not avoid discovery work.

A small-repository test passed on 2026-11-12 with production source
`3daa65918602993543af216cc8e1aaaffeeea5ce`. The fixture revision was
`5c8dc00927d60455c2eb3369fc135da699418595`, with Go module
`dade46ab46d7b9fff51898016830e770b7d4caff`. It contains one Go test and one
generator. The run used `engine-dev without-image-entries`.

| Selection | Selected |
| --- | ---: |
| Unfiltered | 2 |
| Clean `--uncommitted` | 0 |
| README newline, `--uncommitted` | 0 |
| README newline, `--uncommitted --path /README.md` | 0 |
| Go source comment, `--uncommitted` | 2 |
| `--path /value.go` | 2 |

The separate raw `--path /README.md` control selected two checks under the
existing subtree contract. Both public source records had zero issues and zero
conservative selectors. Listing ran preparation commands and no check bodies.
This proves useful filtering for this fixture. The larger repository and full
test suite still need validation before merge.

The following historical measurement used engine
`98b2fdbdaaa003707c5b0f506ad79d9aeeb9bd93`. It predates the current binding model.
The workspace base was `7e9eaacc376e5c3bed3efdca9e1ba750bc8d9233`.
The Go module was `dade46ab46d7b9fff51898016830e770b7d4caff`; its test and package
generation inputs were prepared before evaluation.

| Selection | Selected | Skipped |
|---|---:|---:|
| Unfiltered | 845 | 0 |
| Clean `--uncommitted` | 0 | 845 |
| README newline, `--uncommitted` | 843 | 2 |
| README newline, `--path /README.md` | 844 | 1 |

These were list-only runs. No check bodies ran. All 757 Go checks remained
selected. Two inspected Go checks reported unresolved Host origins. Their exact
Host operations and clients were not established by the measurement.

These counts do not validate the current implementation or execution savings.
At that historical revision, the artifact input test missed required
`defaultPath` inputs. Later producer and binding changes need separate runtime
validation. This measurement is not a passing result for the current artifact
tests.

The original intent is described in [Modules v2](https://github.com/dagger/dagger/pull/12900).
