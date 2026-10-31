# Smart checks

## Decision

Put workspace path filtering on **Artifacts**. Start with checks. Give each check
an input description that can be read before its body runs. Use core Workspace
rules to describe paths. Use dagql to carry input metadata across calls and
cached results. Keep one execution cache. Matching belongs in selection, so lists
and execution use the same narrowed set.

## What a path means

A check is affected when a requested workspace path overlaps any of its inputs.
An input has a workspace identity, a path from its root, and include/exclude
patterns.
Record the selector, not just existing files: a new matching file must count.
A diff supplies changed paths, including additions, deletions, and renames.

The default propagation rule should combine provenance—the paths that can
affect a result—from its receiver, arguments, module implementation, and
inputs supplied by the engine. This includes nested values. Discarding an input
does not remove its provenance.

**Workspace is the important boundary.** A plain Workspace argument gives a
function access to the tree. Before that function runs, its reads are unknown.
A whole-tree dependency is safe. Core reads such as
`workspace.directory(path, include, exclude)` know the selected region.
Arbitrary module functions must run or declare their inputs to give that detail.

Stored fields can omit a manifest used to choose a command. They cannot replace
input provenance. But combining all inputs carries a broad Workspace input
through every result. A later subdirectory lookup cannot undo that history.

## How to ship

Use an explicit **input contract** for checks whose reads happen late.
The module supplies an input function that describes the paths the check body
may read. Describe paths before loading their files. Use the same description
for filtering and for the eventual workspace read. Include configuration and
discovery inputs that affect execution. It is trusted module code: an incomplete
declaration can omit a needed check. Unknown coverage keeps the check selected.

Markdownlint already has `inputs(ws)` selecting Markdown and configuration
files. Expose its patterns without calling `workspace.directory` first. A Go-only
change could then omit markdownlint without loading Markdown files. This contract
replaces a whole-Workspace dependency with the declared input set. Syntax is not
settled here.

The engine should:

1. Discover current checks and collection keys. Describe inputs using the same
   arguments, workspace state, and collection view as execution. Include local
   module/SDK source inputs and workspace configuration used to build the check.
2. Apply path matching through `Artifacts`. Preserve unknown checks and module
   load failures. Feed retained keys into the existing collection-view and
   execution rules, including legacy batching.
3. Construct and dispatch only retained checks. Filter before cloud dispatch.
   A valid path filter with no matches is a successful empty selection.

Discovery still runs to find current checks. A complete per-check declaration
can separate its inputs from the execution inputs of each check. Combining all
provenance through a shared discovery result cannot infer that distinction.

Preparation belongs beside the existing deferred Check projection. Artifacts
keeps listing and execution consistent. Dagql carries generic metadata.
Automatic read capture could replace some declarations later; it is not needed
for this first release.

## Guidelines for module developers

1. **Describe before reading.** Keep constructors and discovery cheap. Use one
   path description for filtering and loading. Treat `workspace.directory` as
   a read: it can copy and hash files. Read configuration first only when needed
   to determine the paths.
2. **Read only needed inputs.** Read each check's inputs directly from Workspace
   with narrow filters. Include all dependencies, such as configuration,
   lockfiles, shared code, and test data. Avoid giving every check all projects'
   source files.
3. **Run only selected checks.** Expose projects as collection items. Build
   sandboxes and run tools after selection. Shared commands must use only the
   selected collection keys.

## Relationship to caching

Dagql asks whether it can reuse a computation. Smart checks asks which checks
the user requested by path. A skipped check has no new outcome. It is not a
cache hit or proof that the check passed.

The cache already tracks calls and input identities. Filtered workspace copies
and some returned module objects have content identities. But cache dependency
edges do not map every workspace read. Scalars can lose their path origin.
Equal content can also come from different paths.

Use existing cache records to retain input metadata where needed. Keep path
origins distinct when equal content shares execution. Do not build a second
lockfile mapping check names to previous paths and outcomes. Prepare the current
input description; selected checks use ordinary dagql caching.

A future bridge could use the same descriptions to validate cache reuse against
only the needed workspace regions. Changed configuration and discovery must
still invalidate that decision. Extend existing cache lookup and dynamic inputs.

One current detail matters: an unfiltered subdirectory can still be a view into
its original root. A filtered copy has a narrower content identity. Path
metadata must respect that distinction; a path prefix alone does not prove a
value has no wider inputs.

## Expected gains from current modules

These estimates assume complete prepared inputs. Receiver inspection alone
cannot resolve late Workspace reads. No workloads were timed.

| Module | Current input shape | Likely useful boundary |
|---|---|---|
| Markdownlint | Late reads of `**/*.md` and `**/.markdownlint*`. | Skip on changes outside those patterns. |
| Go | Shared scanner over all Go source and `go.mod` files. Checks read Workspace late. | Skip unrelated file types. A Go edit can retain the whole Go workload under simple propagation. Package-level gains need a per-check input contract. |
| golangci-lint | Shared Go scan plus workspace-wide lint configuration. | Similar to Go; a lint config change can affect many modules. |
| pytest / Ruff | Constructors capture `sourcePath`, default `/`. Aggregate checks build separate project sources. | All-input propagation stays broad. Complete input contracts can use project subtrees, but skipping individual projects needs separate check units. |
| ESLint | Project collection, but execution mounts the dependency-install root, sometimes the whole workspace. | Install-root boundaries; not necessarily individual projects. |
| Helm / Shellcheck | Chart subtrees / individual script files, discovered through ordinary lists. | Natural small input sets. Leaf methods exist, but ordinary lists do not make them selectable artifacts. Expose collection items first. |

Pinned Go shares one test command across selected tests in a module. Latest Go
uses smaller package/test units. Skipping many checks need not skip many
processes. Shared inputs and commands limit the gain.

An unrelated Markdown-only edit could omit Go checks. But Go can embed Markdown
or include it as extra source, so file extensions alone are insufficient. A warm
cache may already reuse execution; fewer requests and cloud launches can still
help. Preparation before filtering is still paid. Cold caches offer more
execution savings. Total time falls only if skipped work delayed the run or
competed for resources.

My expectation is useful savings between tools, with limited gains inside the
current Go workload. Ship one narrow official check and one project collection.
Measure preparation time, check bodies reached, actual processes, cloud
launches, and total time with warm and cold caches. Do not promise a percentage
from source inspection alone.

## Evidence

Reviewed on 2026-10-30: dagger/dagger at `ddee9399a`, with main fetched through
`f0a3d728c`.
The intervening changes do not alter these core mechanisms. This proposal
supersedes the prototype brief. The intent comes from [Modules v2](https://github.com/dagger/dagger/pull/12900).

- Engine: [check projection and cloud dispatch](../../core/schema/artifacts.go),
  [collection execution rules](../../core/artifact_legacy_batch.go),
  [filtered workspace identities](../../core/schema/workspace.go),
  [returned object content identities](../../core/modfunc.go),
  [cache dependency meaning](../../dagql/cache.go).
- [Markdownlint inputs and check](../../modules/markdownlint/markdownlint.dang).
- Go: [pinned scanner](https://github.com/dagger/go/blob/3159d1fa20a86963dbf9b48f856daeec72be3893/gomod/main.dang#L249),
  [pinned shared test command](https://github.com/dagger/go/blob/3159d1fa20a86963dbf9b48f856daeec72be3893/go.dang#L1199),
  [latest test checks](https://github.com/dagger/go/blob/217ac6cff4b3b51e8b7339bc3f71c5ec25bc5ade/go.dang#L1377).
- Other official modules: [golangci-lint](https://github.com/dagger/golangci-lint/blob/043504934a2b7db24a55aa63d327910a907c20d1/golangci-lint.dang#L240),
  [pytest](https://github.com/dagger/pytest/blob/6cbe945cfcc133f7cb786c45e4b6011f3eb4e3c6/main.dang#L78),
  [Ruff](https://github.com/dagger/ruff/blob/241637949f14bb373a87a09d74d90613d72b0394/main.dang#L38),
  [ESLint](https://github.com/dagger/eslint/blob/8cb42f9ed25b78147c02d65d05529c7ef418b800/eslint.dang#L788),
  [Helm](https://github.com/dagger/helm/blob/8a886dd273e8fdaf855a2807313b9e5bd89cb16f/helm.dang#L21),
  [Shellcheck](https://github.com/dagger/shellcheck/blob/c549c12b6c7d962eacb9eb2dcda22aa940ac2ca2/shellcheck.dang#L26).
