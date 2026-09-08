# SDK review fixes

created: 2026-09-08
status: implemented; final test command stopped at user request

Fixes 1–8 are approved. Keep one commit per fix. Topic 9 is resolved: keep the
core scope name and the current SDK interface.

| Issue | Change |
| --- | --- |
| 1 | Keep Workspace through SDK generation and export. |
| 2 | Prevent and reconcile equivalent scope records. |
| 3 | Remove only the selected client entry. |
| 4 | Separate parent flags from SDK setting flags by command position. |
| 5 | Use one scope-generation function. |
| 6 | Remove unused generator state. |
| 7 | Remove obsolete ModuleManifest API definitions. |
| 8 | Accept source references only as client targets. |
| 9 | Resolved: keep the core scope name and the current SDK interface. |

## 1. Workspace generation and export

The SDK graph returns a `Workspace`. Keep it through export. Convert to
`Changeset` only when an operation requires it, such as conflict checks when
combining SDK and regular generators. Use the command directory for scope
selection. Permit writes above that directory within the workspace.

## 2. Equivalent scope records

Require one record per SDK and resolved workspace path. Shared lookup and
validation apply to configuration reads and writes. Reuse the original key
and complete record when one match exists.

Combine compatible duplicate records before SDK loading or generation:

- Combine client lists and remove exact duplicates. Preserve target strings.
- Keep matching settings and settings present in only one record.
- Keep a name present in only one record.
- Reject different nonempty names, different values for the same setting,
  or different module flags. Report the SDK, keys, and conflicting fields.

Report each reconciliation. Use the combined record immediately. Save it
through the next normal configuration write. A read does not write files.

## 3. Client removal

`module client rm` removes only the selected client. Keep the scope record,
name, module status, settings, and other clients. Keep an empty scope.
Regenerate the scope to remove the selected client's generated files, subject
to the invalid-target repair rule in issue 8.

## 4. Flag position

For `dagger module init --path=src custom --path=assets`, `src` is the module
path and `assets` is the SDK setting. Apply the same rule during early global
flag parsing. Preserve inherited flags when no SDK setting has that name.

If trailing context flags require SDK discovery again, allow one further
discovery. The SDK setting names and types must remain the same. Otherwise,
report an error and show how to put context flags before SDK selection.
Resolve relative working directories from the original command directory.

## 5. Shared generation

The graph calls `generateSDKModuleScope` for each scope. Keep dependency
ordering and scope-specific errors in the graph. Pass the returned Workspace
to the next scope.

## 6. Generator state

Remove the unused `Generator.OriginalModuleResult` field and its persistence
code. Keep `Node.OriginalModule` and its existing persistence.

## 7. Obsolete API definitions

Remove `ModuleManifest`, `ModuleManifestID`, and `loadModuleManifestFromID`
from the saved base schema and current generated files. Update the test SDK
to use Workspace operations. Keep historical documentation.

## 8. Client source references

Accept explicit local paths or module addresses. Reject installed module
names. A bare `foo` never means `./foo`. Relative paths must use `./`, `../`,
`.` or `..`. Preserve explicit path markers and declared remote versions.
A remote address must not become a local path after a failed remote lookup.

Apply shared validation to add, generation, and update. Keep path-only fields
separate from module references. Reject equivalent source references within
one scope. Do not add remote module identity comparison.

Break compatibility with old installed names and unmarked local targets.
Do not migrate or rewrite them automatically.

`list` shows every saved target. `rm` accepts the exact listed target without
loading it. If other invalid targets remain in the selected scope, save the
removal, skip generation, and report those targets with repair instructions.
Generate normally once the remaining references are valid. Do not suppress
unrelated SDK, runtime, or network errors.

## 9. Scope names

Keep the optional core scope name and the current `generateScope()` interface.
Module init saves only an explicit name and preserves any existing saved name.
Before generation, use the saved name or infer one for a module scope. A local
entrypoint installation that targets the scope supplies its name. Otherwise,
use the scope directory name, with the config-parent or workspace name plus
`-dev` as the fallback at the workspace root. Multiple matching entrypoint names
require an explicit scope name. Use the same lookup for SDK module listings.
Do not write an inferred name into the scope. A scope that contains only clients
can retain an unused name or leave it empty.

Changing the SDK interface would require another update in each SDK
repository. The benefit does not justify that cost. This topic is closed.
