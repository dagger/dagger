# Source path filtering

## Problem

Dagger can select checks by artifact address and collection key. It cannot yet
select them reliably by the source paths they depend on. Users must maintain
custom rules or request unrelated checks. Even cached execution can still cost
preparation work and cloud launches.

## Solution

Add source path filters to **Artifacts**, starting with checks. Derive provenance
from normal Workspace operations and carry it through function results. Modules
keep their dynamic file access. No new input declarations are required.

Use the same selection for listing and execution. Keep a check selected when
its full set of possible inputs is unknown. Do not run its body just to answer
a filter query.

## Provenance

A **source path** is a path in the source repository that an artifact depends on.
Provenance records those dependencies as workspace identity, root path, and
include/exclude patterns. Record selectors, not just existing files, so additions
and deletions count too.

Combine provenance from arguments, receiver, module code, and inputs supplied
by the engine, including nested values. Discarding a value does not remove its
dependency.

Workspace gives code access to files it has not yet requested. An unrun function
has unknown reads. A completed call can have a narrower summary if the engine
has recorded all its access. Interrupted calls or missing nested metadata leave
coverage unknown. Stored fields are insufficient: configuration reads may have
become plain scalar values.

`WorkspacePath` remains a separate core scalar for path values and client display.
It does not carry provenance or cause file reads.

## Where laziness limits filtering

The current engine has three stages:

1. **Construct the receiver.** Constructors and collection discovery run as
   needed. They can already read files or execute commands.
2. **Construct the Check.** The check projection stores the receiver, arguments,
   and workspace context. It does not call the author's check body.
3. **Request the result.** `Check.sync` invokes the hidden check function. The
   body can read Workspace, force lazy values, and run commands.

Markdownlint is a concrete example. Its receiver stores tool settings.
`lint(ws)` calls `sandbox(ws)`, which calls `inputs(ws)`. Only inside that body
is `ws.directory("/", include: ["**/*.md", "**/.markdownlint*"])` requested.
The engine cannot see those patterns in the receiver before running the body.

The pinned pytest module instead captures a Directory in its constructor. Its
paths are known before the test body runs. But its default source is `/`, and
the captured source does not prove that there are no other body inputs.

Two current details prevent shortcuts:

- `workspace.directory` can copy and hash files when called. Executing helpers
  early is not necessarily cheap or lazy.
- A body can obtain `currentWorkspace` through its nested client. Having no
  Workspace argument does not prove that it has no future workspace reads.

Stopping at the first read gives an incomplete set: its result can determine
later reads. Supplying dummy results would change the code's behavior.

## Implementation

1. **Record normal calls.** At Workspace reads, record resolved paths and
   patterns. Carry the record through arguments, receivers, nested calls, and
   cache hits. Include module source. Keep discarded scalar reads and failed
   requests.
2. **Store completed summaries in dagql.** Use ordinary cache entries with the
   call's lifetime and access rules. Keep source origins separate when outputs
   have equal content. Missing coverage stays unknown.
3. **Validate before filtering.** Match code and other inputs. Then compare the
   recorded regions with the current snapshot. Use the same content hashes and
   metadata rules as File and Directory caching. A configuration change can
   cause new reads on the next execution.
4. **Filter through Artifacts.** Keep unknown inputs selected. Keep collection
   views, legacy batching, module load errors, and cloud dispatch in the existing
   selection system. `--source-path PATH` uses one workspace snapshot for
   filtering and execution. Paths start at the workspace root.

First-run skipping is not supported. A cache hit without a recorded summary
stays unknown. Filtering can construct receivers and discover collection keys;
it does not run the check body.

The prototype narrows File reads and filtered Directory copies from stable
workspaces. Unfiltered Directory values retain the full source tree, so they
retain a root dependency. Git ignore reads also retain a root dependency.
Symlink paths, live workspace access, and changing non-file inputs stay unknown.
Other Workspace operations currently stay unknown.

## Relationship to caching

Keep one execution cache. Dagql tracks call inputs and caches the hidden check
function. Its retained dependency graph does not record every nested
workspace read or scalar origin.

Object IDs remain opaque references, as in current content caching.

Read summaries belong with those calls. An old path list is not enough to skip:
a changed manifest can cause new reads. Validate configuration and discovery
inputs too, including queries for files that were absent. Do not add a separate
lockfile of check names and old path lists.

A requested path or diff does not prove an old summary is current. Validate
its recorded inputs too. Other arguments, code, workspace metadata, and cache
policy also matter.

For late-reading checks, this cache dependency work may be necessary for useful
early filtering. It is not just an optional performance improvement. A filter
still reports selection, not a new passing outcome for a skipped check.

## Guidelines for module developers

1. **Read narrowly.** Use Workspace.file for one file. Use Workspace.directory
   with filters for a set of files. An unfiltered Directory retains the full
   source tree. Include configuration, shared code, and test data.
2. **Keep work lazy.** Do only the work needed to construct values and discover
   keys. Run tools in check bodies. Do not move reads earlier just to help the
   filter; the engine must account for dynamic access.
3. **Respect selection.** Expose projects as collection items. Shared commands
   must use the selected keys rather than process every discovered project.

## Expected gains

No official workloads were timed. Validation still copies and hashes selected
inputs. These are the limits visible in current code:

| Module | What is known before the body | Limit on early filtering |
|---|---|---|
| Markdownlint | Settings; its Markdown read is inside `lint(ws)`. | Keep selected until a valid recorded read summary can establish its inputs. |
| Go / golangci-lint | Discovery already scans Go source across the workspace. Checks read Workspace later. | Broad discovery remains paid. Simple provenance rollup can retain the whole Go workload. |
| pytest / Ruff | Constructors capture source, default `/`; aggregate checks can also read Workspace later. | Captured inputs are broad. They do not prove complete body inputs. |
| ESLint | Project keys; source reads happen later and can cover a shared install root. | Project keys alone do not establish project-sized dependencies. |
| Helm / Shellcheck | Discovery can build chart or script values with source inputs. | Ordinary lists do not expose them as selectable artifact items. Aggregate checks cover the whole set. |

Pinned Go shares one test command across selected tests in a module. Latest Go
uses smaller units. Skipped checks therefore do not measure saved processes.
A warm cache may already save execution; fewer cloud launches can still help.
Filtering after discovery does not save discovery work.

The Artifacts API alone will not speed up current modules. Measure whether all
reads are recorded, discovery cost, commands avoided, and cloud launches. The
first useful result must come from automatic tracking and validation.

## Evidence

Reviewed on 2026-10-30: dagger/dagger at `ddee9399a`, with main fetched through
`f0a3d728c`.
The intervening changes do not alter these core mechanisms. The intent comes from [Modules v2](https://github.com/dagger/dagger/pull/12900).

- Engine: [Check construction](../../core/object.go),
  [body invocation](../../core/check_projection.go),
  [artifact evaluation and cloud dispatch](../../core/schema/artifacts.go),
  [nested workspace inheritance](../../engine/server/session_workspaces.go),
  [collection execution rules](../../core/artifact_legacy_batch.go),
  [filtered workspace identities](../../core/schema/workspace.go),
  [returned object content identities](../../core/modfunc.go),
  [cache dependency meaning](../../dagql/cache.go).
- [Markdownlint inputs and check](../../modules/markdownlint/markdownlint.dang),
  [pinned pytest source and test](https://github.com/dagger/pytest/blob/cdf6dc0bce0fe8fdf87c6b26ecd991e6f96200b0/main.dang#L19).
- Go: [pinned scanner](https://github.com/dagger/go/blob/3159d1fa20a86963dbf9b48f856daeec72be3893/gomod/main.dang#L249),
  [pinned shared test command](https://github.com/dagger/go/blob/3159d1fa20a86963dbf9b48f856daeec72be3893/go.dang#L1199),
  [latest test checks](https://github.com/dagger/go/blob/217ac6cff4b3b51e8b7339bc3f71c5ec25bc5ade/go.dang#L1377).
- Other official modules: [golangci-lint](https://github.com/dagger/golangci-lint/blob/043504934a2b7db24a55aa63d327910a907c20d1/golangci-lint.dang#L240),
  [pytest](https://github.com/dagger/pytest/blob/6cbe945cfcc133f7cb786c45e4b6011f3eb4e3c6/main.dang#L78),
  [Ruff](https://github.com/dagger/ruff/blob/241637949f14bb373a87a09d74d90613d72b0394/main.dang#L38),
  [ESLint](https://github.com/dagger/eslint/blob/8cb42f9ed25b78147c02d65d05529c7ef418b800/eslint.dang#L788),
  [Helm](https://github.com/dagger/helm/blob/8a886dd273e8fdaf855a2807313b9e5bd89cb16f/helm.dang#L21),
  [Shellcheck](https://github.com/dagger/shellcheck/blob/c549c12b6c7d962eacb9eb2dcda22aa940ac2ca2/shellcheck.dang#L26).
