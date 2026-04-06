# Workspace Selection

## Status: Prototype Contract

This is the design reference for explicit workspace selection in the CLI.
If the current prototype diverges from this document, follow the document.

It covers:

- the user-visible CLI contract
- the CLI/engine ownership boundary
- which commands should accept `--workspace`
- the implementation shape to keep

## Problem

1. The branch needs an explicit way to select a workspace for both local and remote refs.
2. `--workdir` changes the real process cwd. Workspace selection should not depend on that behavior.
3. Positional workspace syntax is hard to discover and easy to misparse.
4. Local-vs-remote resolution already exists in the engine. Re-implementing it in the CLI creates drift.

## Decision

Use a first-class `--workspace` flag with shorthand `-W`. The flag selects a
workspace binding for the session. It accepts either a local path or a remote
git ref.

Keep hidden `--workdir` as a legacy cwd-only flag. Do not give `--workdir`
workspace semantics. Do not overload `-w`; it remains the shorthand for
`--web`.

Do not support positional workspace targets. Do not infer a workspace from the
first positional argument. The only explicit workspace-selection mechanism is
`--workspace`.

## CLI Contract

| Flag | Meaning |
| --- | --- |
| `--workspace`, `-W` | Select the workspace binding for the command session. |
| `--workdir` | Hidden legacy flag. Changes the native process cwd before execution. No workspace semantics. |
| `--web`, `-w` | Open the trace URL in a browser. Unchanged. |

Examples:

```bash
dagger -W ../repo functions
dagger -W github.com/acme/ws check go:lint
dagger --workdir ../repo -W . functions
```

When both flags are present:

1. `--workdir` applies first.
2. `--workspace` is then interpreted from that cwd if it is a relative local path.

## Selection Semantics

1. The CLI parses `--workspace` as an opaque string.
2. The CLI passes that string unchanged to `client.Params.Workspace`.
3. The engine resolves the declared workspace ref:
   - local path first, relative to the caller
   - remote git ref second
4. Workspace binding then drives `currentWorkspace()` resolution and workspace
   module loading.
5. `--workspace` does not call `os.Chdir`.

Implementation consequence:

- if the prototype still contains CLI-side local/remote heuristics, remove
  them
- the engine is the source of truth for declared-workspace resolution

## Command Policy

`--workspace` should be available only on commands that conceptually operate on
a workspace binding.

### Accept `--workspace`

- `call`
- `functions`
- `check`
- `generate`
- `workspace info`
- `workspace list`
- `init`
- `install`
- `update`
- `lock update`
- `workspace config`

### Reject `--workspace`

Reject in the CLI for commands that are module-centric or otherwise unrelated to
workspace selection:

- `config`
- `migrate`
- `module ...`
- `toolchain ...`

### Local-Only Mutations

Some workspace commands can target a selected workspace, but still require that
the bound workspace resolve to a local host path.

Examples:

- `workspace init`
- `install`
- `update`
- `lock update`
- `workspace config <key> <value>`

Important rule:

- the CLI must not classify `--workspace` as local or remote
- these commands should pass the workspace ref through normally
- if the selected workspace resolves to a remote workspace, the engine/schema
  should return the local-only error

This keeps one authority for local-vs-remote behavior.

## Unsupported Syntax

The following are explicitly out of scope:

- positional workspace target arguments
- implicit first-arg workspace inference
- split flags for local vs remote workspaces
- repurposing `--workdir` as workspace selection
- CLI-side local/remote workspace heuristics

## Implementation Shape

Keep this shape:

- define `--workspace/-W` in `cmd/dagger/main.go`
- keep `--workdir` hidden and cwd-only in `cmd/dagger/main.go`
- inject `workspaceRef` centrally in `cmd/dagger/engine.go`
- let `engine/server/session_workspaces.go` resolve declared workspace refs
- delete `workspace_target_args.go` and any positional-target parser tests
- regenerate the CLI reference after the command surface settles

Avoid this shape:

- per-command workspace parsing logic for `call`, `functions`, `check`, or
  `generate`
- CLI-side `isRemoteWorkspace(...)` helpers
- hidden alias flags that exist only to emulate a second shorthand

## Notes For Implementers

- The design is intentionally boring: one visible flag, one transport field,
  one engine resolver.
- If a command needs special policy, keep that policy at the command layer.
  Do not push command policy into workspace-ref parsing.
- If a command needs a local workspace, rely on the existing engine/schema
  local-only checks rather than re-implementing ref classification in Cobra.
