# Which calls make a check depend on a workspace path?

This is a draft for review. Read the **required effect** column as a specification. Read the **current rule** column as a description of the implementation in this PR. A broad rule can keep a check safe to select and still make filtering useless.

A **selector** names a workspace path and optional include and exclude patterns. A check matches a changed path if the path matches one of its selectors. With the default selection of all input kinds, a check also matches if an input could come from this workspace and the tracker cannot identify its paths. A known empty set of changes selects nothing.

## Example of loading a module

Suppose the root contains `README.md` and a module with only the older `tools/lint/dagger.json` manifest. A caller asks for `ws.moduleSource("/tools/lint").sourceSubpath`.

1. The engine looks for a module manifest in `tools/lint`. The engine tests `dagger-module.toml` first, then finds `dagger.json`. Editing `dagger.json` or adding `dagger-module.toml` can affect the answer.
2. If the engine finds a manifest, it reads it. It prepares the configured source directory and loads declared dependencies. An entrypoint can request other files. These steps have their own inputs.
3. The result holds a directory. The next call, `sourceSubpath`, returns a string already on the result. Another caller could ask for `directory(".")` and take files from that directory.
4. The directory-backed loader records each manifest name it tests. For a root copy with literal includes, the tracker can record those includes and `**/.gitignore`. On this fixture, a change to the root README file matches neither rule.

Holding a reference and using it have different effects. The review question is whether **any step in this call chain can depend on this path**. A manifest, a declared dependency, a directory filter, or an unrun check with access to the whole workspace can each supply a real reason to select the check.

## Workspace calls

The table covers all 81 field names in the native `Workspace` classifier, including internal names and version aliases. The engine-generated `id` field follows the ID rule below. Paths start at the workspace root unless a row says otherwise. These rules also apply to calls made by engine code.

| Calls | Required effect | Current rule |
|---|---|---|
| `file`, `directory` | Resolve the path against the workspace working directory. Track the file or the directory region. Keep include and exclude patterns, even for an empty result. Changes to ignore rules, symbolic links, or mounted inputs can affect the result. | Records the path and directory filters. With `gitignore: true`, an unmounted directory snapshot can keep literal root includes and `**/.gitignore` after the tracker checks for symbolic links. Other cases keep all paths. A literal subtree query can still match a possible ignore file below it. For non-root paths, an unverified link or mount mapping keeps the check selected. |
| `glob` | Track the pattern from the workspace root. A new matching file counts. | Records a root selector with the pattern as an include. Check that mount origins are covered when a mount supplies a match. |
| `findUp` | Track the candidate at the start directory and at each ancestor through the workspace root. Include missing candidates. | Uses that bound for a literal name other than `.`. The special name `.` selects all paths. Invalid names can produce an issue. Check the candidate mapping when a mount supplies the result. |
| `search`, `findRoots` | Track the search area, including possible new matches. | Records all paths. |
| `artifacts`, `resolve` | Track the inputs used to discover artifacts or resolve the address. Include reads made during that work. | Collects inputs and completed nested calls. A returned object can still give an unrun function workspace access. |
| `moduleSource` | Track manifest lookup and source loading. Preserve the origin of the directory for subsequent calls. | Depends on workspace type. See below. |
| `address`, `cwd`, `path`, `configPath`, `configFile`, `hasConfig`, `initialized`, `__workspaceModule`, `__workspaceSDK` | Returning stored metadata adds no file read by itself. Keep the inputs that established the value. | No automatic root selector. In particular, `__workspaceSDK` constructs an installed SDK descriptor from its arguments. |
| `snapshot`, `git`, `withWorkdir`, `withConfigPaths`, `withConfigEnvironment`, `__withUserConfigOverlay` | Preserve which workspace a later read refers to. Taking a snapshot can copy files. Passing the snapshot alone gives no evidence that a check uses every file. | Treated as workspace views. The tracker carries the source identity and inputs. |
| `withNewFile`, `withFile`, `withNewDirectory`, `withDirectory`, `withoutFile`, `withoutFiles`, `withoutDirectory`, `withMountedDirectory`, `withMountedFile`, `withoutMount` | Preserve the input origins across an edit or mount. A later read must account for content supplied or hidden by the edit. | Treated as views. Precise mapping through all edits and mounts needs review. |
| `withChanges`, `withPatchFile` | Track the patch or changeset inputs, plus the base files used when the change is applied. Preserve the output workspace identity. | Observes the call and its inputs. These two fields are absent from the special list of workspace views. Check the resulting identity on reuse. |
| `withInitialized`, `migrate`, `migrateModule`, `detectScope`, `__doctor` | Track files examined by initialization, migration, or discovery. | Records all paths. |
| `compareCommitsFrom`, `withCommitsFrom`, `__pullDirectory`, `__pullRepository`, `__saveDirectory`, `withCommit`, `withReset`, `changes`, `export` | Include the trees or changes the operation inspects, and any supplied source inputs. A whole-tree operation can require all paths. | Records all paths on the receiver. The direct rule does not visit the other arguments. See the two-workspace case below. |
| `withUserConfig` | Keep the workspace inputs. The caller’s external user config can change the value without changing any workspace file. | Records all paths on the receiver. |
| `withConfigValue`, `withoutConfigValue`, `withConfigEnv`, `withoutConfigEnv`, `configRead`, `envList` | Track config lookup, config contents, includes and other inputs consumed. | Records all paths. |
| `withModule`, `withoutModule`, `withSdk`, `withSDK`, `withoutSdk`, `withoutSDK`, `withMain`, `withEntrypoint`, `withoutMain`, `withoutEntrypoint`, `withInitModule`, `withClient`, `withoutClient`, `withUpdatedClients`, `withUpdatedLock`, `__withUpdatedLock`, `__lockEntries`, `withUpdatedModules`, `sdks`, `sdk`, `main`, `entrypoint`, `modules`, `module` | Track the config, lockfile, and module inputs that each operation uses. | Records all paths. The list includes names from older schema versions. |
| Any new native Workspace field | Establish its effect before using it to exclude a check. | Reports an unclassified operation. |

For example, `a.changes(from: b)` compares two workspaces. The tracker must keep inputs from both. Recording every path on `a` does not cover an edit to `b`. Nested calls can report a missing input. Check each side separately, including when DagQL serves a cached result. The direct rule alone does not establish the outcome.

[Workspace schema](../../core/schema/workspace.go) · [Workspace field classifier](../../core/workspace_input_operations.go) · [Path collection](../../core/workspace_inputs.go) · [Workspace views](../../core/workspace_input_reads.go)

## What happens inside Workspace.moduleSource?

| Step | Required effect | Current rule |
|---|---|---|
| Choose the backing source | Keep the identity of the workspace being inspected. | A local workspace and a Git workspace with a repository URL use the workspace loader. Other sources take the directory branch. The directory branch records the names it tests on the original directory. |
| Locate the manifest | Track every candidate tested in the module directory, including a missing higher-priority candidate. | In the directory branch, `Directory.asModuleSource` uses `moduleConfigInDir` to test names directly on the directory. It records each candidate before the test. An uncertain symbolic link or mount keeps a root selector. |
| Load a configured module | Include the selected manifest, configured sources, and any other inputs used to load dependencies, check ignore rules, evaluate entrypoint sources, or load defaults. | `loadConfiguredModuleSource` does this work before returning. Workspace-bound file and directory loads can already produce narrower selectors. |
| Return a source when the manifest is missing | Preserve the lookup and the origin of the context made available for later use. | In the directory branch, `allowNotExists: true` can return the original root directory as the context. The tracker records all paths for the missing-manifest branch. A later consumer of the full context can depend on all paths. |
| Use the result | Account for what the caller does with it. | `ModuleSource.sourceSubpath` returns a stored string. `ModuleSource.directory` selects from the context. `ModuleSource.digest` computes the implementation digest. These calls inherit the producer's recorded inputs, including any root selector. |

A change to this rule needs two independent checks on the same fixture. With a module confined to `tools/lint`, a root README edit must leave its check out. A change to a manifest candidate or a source that the module actually uses must select it. Also exercise the missing-manifest case with a consumer that requests the full context.

[Workspace loader](../../core/schema/workspace_module.go) · [Directory loader and ModuleSource calls](../../core/schema/modulesource.go) · [Source file and directory access](../../core/modulesource.go)

## Calls on other object types

The general rule is to keep the paths used to produce the receiver and arguments, then add reads performed by the call. A native helper that reads a filesystem directly must account for that read. The observer sees DagQL calls. It cannot infer which paths an arbitrary Go filesystem call inspected.

These rows describe the current implementation. An input can already carry a source path from the call that made it. For example, after `Workspace.file("/go.mod").contents`, the read of `/go.mod` comes from `Workspace.file`. The `File.contents` field has no separate workspace path rule.

| Call | Effect on workspace paths |
| --- | --- |
| `Query.currentWorkspace` | Return a workspace handle. Preserve the source of a workspace already bound to the call. Add no path for the handle itself. |
| `Directory.asWorkspace`, `GitRef.asWorkspace`, `GitRepository.asWorkspace` | Make a workspace view. Preserve the inputs that made the view. Add no path just for making it. |
| `Query.currentModule` | Use the prepared inputs of this metadata call. Add no path for the field itself. |
| Any `id` field, `Query.node`, `Query.currentFunctionCall`, native calls on or returning `FunctionCall` or `FunctionCallArgValue` (including lists of them) | Skip the call at the observer. Passing a value by ID can still carry paths from its producer. |
| `WorkspaceGit.head`, `directory`, `uncommitted`, `__checkout`, `__repository` | Add `/` on the workspace that owns the Git handle. The current rule applies to every field on this type after the exclusions above. A dirty-tree query can depend on any working-tree path. Check the separate case of `head`. Changing an uncommitted file alone leaves HEAD at the same commit. |
| `Host.file`, `directory`, `__gitDir`, `findUp` in the executing module's runtime | Use the prepared inputs of the runtime when the effective client is the executing module client and its inputs are available. Add no workspace path for the host path itself. |
| `Host.findUp(name)` outside that runtime rule | Add the exact basename at the caller's workspace working directory and each ancestor through the workspace root. From `/sdk/go`, `.env` selects `/sdk/go/.env`, `/sdk/.env`, and `/.env`. An invalid basename or unavailable caller workspace produces an issue. |
| `Host.file`, `directory`, `__gitDir` outside that runtime rule | Record an issue. The tracker has no mapping from the host path to a workspace path. |
| `Host.unixSocket`, `_sshAuthSocket`, `service`, `tunnel`, `containerImage` | Keep input paths. Add no path for the external socket, service, or image. An unclassified Host field records an issue. |
| `WorkspaceSDK.scopeSetting`, `WorkspaceModule.settings`, `WorkspaceModule.functions` | Reading a setting or loading module metadata can read live workspace config and included files. These methods use the general observation rule. Verify that their producers and nested calls cover direct config reads. |
| All other native fields, including fields on `ModuleSource` | Inherit paths from the receiver and arguments. Collect observed nested calls. The receiver type alone adds no direct read rule. Inside a classified source call or workspace view, the enclosing call owns the native effects. Engine code can also report a read explicitly. |

Implementation and schema declarations. [Call observer](../../core/workspace_inputs.go), [workspace views](../../core/workspace_input_reads.go), [Host rules](../../core/workspace_input_host.go), [ancestor search](../../core/workspace_input_findup.go), [WorkspaceGit fields](../../core/schema/workspace.go), [SDK setting lookup](../../core/schema/workspace_sdk.go), [module settings and functions](../../core/schema/workspace_module.go).

## Module functions and their inputs

| Input or operation | Effect on workspace paths |
| --- | --- |
| Module function call | Inspect the receiver, explicit arguments, engine-supplied arguments and called module. Collect calls from the function body when it runs. For an artifact whose body has yet to run, inspect its prepared inputs and the access those inputs still permit. |
| Module source | Add the workspace config and lock files if set, and `.env` at the workspace working directory and each ancestor. Add the mapped module source subtree and its extra include patterns. Repeat for module dependencies and a module-backed legacy runtime implementation. A directory source without a direct mapping can contribute paths through its directory producer. Artifact inspection also includes the ancestor `.env` search from the host working directory when that directory is inside the local workspace. |
| Native built-in legacy runtime | Use the module source regions, declared client targets (including clients of clients) and entrypoint inputs when present. For other modules, inspect the module producer, runtime, definition, and definition runtime when present. A required module-backed legacy runtime with unprepared inputs produces an issue during inspection. |
| Entrypoint with a `sources` function | Follow the returned source directory and the workspace reads captured while constructing it. Require a matching capture for that workspace. A missing capture produces an issue. |
| Entrypoint without a `sources` function | Add `/` with origin `entrypoint workspace`. This is an explicit rule even if no individual file read was observed. |
| Omitted module argument with `defaultPath` | Resolve the default to an actual argument before inspection. For a workspace-backed file or directory, follow the selected path and any directory filters. A Git default follows its Git producer. An optional Git default can be absent. A supplied argument uses its own producer. |
| Runtime preparation before the body starts | Record the inputs used during preparation. Include the runtime container's producer for a container runtime outside the built-in shortcut. These paths remain distinct from reads made by the function body. |
| Stored module object field | A verified trivial field or an engine-generated projection of that field can use its prepared inputs. The tracker checks the exact owner and field. An ordinary module method requires captured body reads. |
| Indexed list item | Follow the producer of the list and keep its diagnostics. An item can therefore inherit paths shared by the list. |

Implementation. [Module source regions and artifact preparation](../../core/workspace_provenance.go), [module and field rules](../../core/workspace_inputs.go), [declared clients](../../core/workspace_input_clients.go), [entrypoint capture](../../core/workspace_input_entrypoint.go), [argument defaults](../../core/modfunc.go), [context files and directories](../../core/modulesource.go).

## Values kept for a later call

A **retained workspace** is a workspace handle that an input still contains when the tracker inspects a function before it runs. The function can read any path through that handle. The tracker therefore adds `/` on that specific workspace. It can do this even when creation of the handle added no read.

| Value kept by an input | What the tracker follows |
| --- | --- |
| `Workspace`, `WorkspaceGit`, `WorkspaceSDK`, `WorkspaceModule`, `ModuleSource`, `Address` | The workspace itself, or the workspace the value owns or binds, if present. For a `WorkspaceModule`, resolve the owner from the producer. For `ModuleSource`, inspect its workspace field. |
| Module object, list, nested fields | Follow the object's module, stored fields, collection base, artifact-view sources, and list items as applicable. A complete stored record of its retained access can supply this information. |
| `Check`, `Generator`, `Expertise`, `Artifact`, `Artifacts` | Follow the relevant receiver, nested checks or generators, deferred call and arguments, collection entries and roots, or bound workspace. A check's workspace sets the context for this walk. |
| `Host`, `Agent`, `LLM` | An unrun function that retains a Host handle produces an issue. An Agent produces an issue for live conversation state. For an LLM, follow its workspace, expertise, and bound tool objects or IDs. A tool with no known input scope produces an issue. |

Implementation. [Retained values](../../core/workspace_inputs.go), [workspace ownership and stored fields](../../core/workspace_input_bindings.go).

## Inspecting and matching a check

`Artifact.provenance` prepares the check and reports its selectors and issues. `Artifact.affectedByPath` and `Artifact.affectedByDiff` match that record. `Artifacts.provenance` combines records. `Artifacts.filterAffectedByPath` and `Artifacts.filterAffectedByDiff` select members. `WorkspaceProvenance.affectedByPath` and `WorkspaceProvenance.affectedByDiff` match an existing record. A path query treats its argument as a possible subtree. A diff tests each changed file as a single file, including additions and deletions. An issue adds a conservative `WORKSPACE_READ` selector. A caller that requests only `MODULE_SOURCE` excludes that selector. Inspection leaves the check body unrun.

The same inputs must give the same selection on a fresh call and on a cache hit. Include a changed source that must select the check, an unrelated change that must exclude it, and an incomplete record that must keep it with the default selection of kinds. When a check matches every path, identify which rule supplied the match. A recorded root read, an unrun function with a workspace handle, and a missing source mapping require different corrections.

[Provenance API](../../core/schema/workspace_provenance.go) · [Artifact preparation](../../core/workspace_provenance.go) · [Design contract](source-path-filtering.md)
