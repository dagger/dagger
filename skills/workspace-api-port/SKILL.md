---
name: workspace-api-port
description: "Port Dagger toolchain modules from deprecated +defaultPath/+ignore annotations to the Workspace API. The Workspace API (PR #11812) deprecates the separate 'toolchain' concept: every module can now receive a *dagger.Workspace argument and dynamically access project files, replacing static +defaultPath pragmas with code-level ws.Directory()/ws.File() calls. Triggers on: port to workspace API, migrate defaultPath, workspace migration, replace defaultPath with workspace, port toolchain to workspace, port module to workspace."
---

# Port Toolchain Modules to the Workspace API

The Workspace API (dagger/dagger#11812) deprecates `+defaultPath`/`+ignore` annotations and the separate "toolchain" concept. Instead of static pragmas that magically inject host directories, modules explicitly receive a `*dagger.Workspace` and call `ws.Directory()`/`ws.File()` to access project files. This makes every module a potential toolchain.

## Scope: toolchain modules only

`+defaultPath` means two different things depending on how a module is used:

- **Toolchain modules** (installed into a workspace, receive host project files): `+defaultPath` tells the engine which host directory to inject. This is what the Workspace API replaces — the module should receive `*dagger.Workspace` instead and explicitly access project files via `ws.Directory()`/`ws.File()`.
- **Regular dependency modules** (called by other modules, receive directories programmatically): `+defaultPath` provides a default value for a directory argument in the module-to-module call. This is **not** affected by the Workspace API and should be left alone.

Only port modules that are used as toolchains (i.e., installed into a workspace to operate on the host project). Do not port regular dependency modules.

## Workflow

### 1. Identify targets

Find all `+defaultPath` usages in **toolchain** modules:

```
grep -rn '+defaultPath' <module-dir>/**/*.go
```

Skip modules that are regular dependencies (not installed as toolchains into a workspace).

Also check the root `dagger.json` for `customizations` with `ignore` patterns referencing the same arguments.

### 2. Classify each usage

Each `+defaultPath` maps to a transformation pattern. Read [references/patterns.md](references/patterns.md) for detailed before/after examples.

| Situation | Pattern |
|-----------|---------|
| `+defaultPath` points to a subdirectory | **Pattern A**: path string arg with `+default` |
| `+defaultPath="/"` (whole root) with `+ignore` | **Pattern B**: `include`/`exclude` slice args |
| `+defaultPath` points to a specific file | **Pattern C**: path string arg + `ws.File()` |
| Multiple `+defaultPath` args in one function | **Pattern D**: one `ws`, multiple path args |
| `+defaultPath` on a non-constructor method | **Pattern E**: add `ws` param to that method |

### 3. Apply transformation

For each usage:

1. Remove the `*dagger.Directory` or `*dagger.File` arg with `+defaultPath` (and `+optional`, `+ignore`)
2. Add `ws *dagger.Workspace` as a parameter (once per function, even if multiple args are replaced)
3. Add path/include/exclude args as appropriate for the pattern
4. In the function body, derive the directory/file: `ws.Directory(path)` or `ws.File(path)`
5. Store the derived `*dagger.Directory`/`*dagger.File` (not the Workspace) in struct fields
6. Remove any `source == nil` fallbacks — workspace is always injected

### 4. Clean up dagger.json

Remove `customizations` entries from the root `dagger.json` that referenced the old argument. Those `ignore` patterns move to workspace config (`config.exclude = [...]` in `.dagger/config.toml`).

## Key constraints

- `*dagger.Workspace` **cannot be a struct field** — only a function argument
- `ws.Directory()` and `ws.File()` are lazy (no `ctx` needed)
- Paths are relative to workspace root; strip leading `/` from old `+defaultPath` values
- The engine auto-injects workspace args — callers don't pass them explicitly
