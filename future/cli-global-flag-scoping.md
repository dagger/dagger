# Future CLI Global Flag Scoping

author: shykes
created: 2026-09-01
status: design draft
related: https://github.com/dagger/dagger/issues/14024

## Context

The Dagger CLI defines many flags as root-persistent flags. Cobra therefore
shows them in the help for every command, including commands for which the
flags have no useful effect.

This design separates the current global flags into four groups:

1. Stable workspace settings move to `dagger.toml`. A generic configuration
   override can change these settings for one invocation.
2. Command-specific flags are available only when a command declares the
   required capability.
3. Bootstrap and framework flags stay global.
4. Development controls stay available but hidden.

Capability scoping controls flag validation and help output. It does not have
to prevent a flag from appearing before the subcommand. For example,
`dagger -W ./workspace check` can remain valid when `check` declares
`MaySelectWorkspace`.

## Command Capabilities

| Capability | Meaning |
|---|---|
| `MayCallEngine` | The command can connect to and call the engine. |
| `MaySelectWorkspace` | The command can use `-W`, `--workspace` to select or resolve a workspace. |
| `MayReadWorkspaceConfig` | The command can use `--env` when reading workspace configuration. |
| `MayWriteWorkspaceConfig` | The command can use `--env` when writing workspace configuration. |
| `MayRenderPipeline` | The command can show a user-facing pipeline trace. |
| `MayProduceOutput` | The command can produce an output whose disposition can require user input, such as review before a side effect or selection of a local output path. |

The capabilities are independent. A command can declare more than one.

- A normal pipeline command usually declares `MayCallEngine`,
  `MaySelectWorkspace`, `MayReadWorkspaceConfig`, and `MayRenderPipeline`.
- `dagger trace` declares `MayRenderPipeline` but does not call the engine.
- A configuration command can call the engine without rendering its internal
  trace to the user.
- `dagger activity`, `dagger cloud rerun`, and `dagger workspace remote`
  declare `MaySelectWorkspace` without declaring `MayCallEngine`.
- `dagger check` renders pass or failure status, but does not produce an
  output. `dagger generate` and `dagger api call` can produce outputs.

An output is a returned value whose disposition the CLI must decide. The CLI
can print, export, apply, save, discard, or request approval for the output.
The capability does not depend on the current output type.

`--verbose` remains global as a diagnostic escape hatch. When a command emits
a trace, `--verbose` prints a final trace even if the command does not declare
`MayRenderPipeline`. It does not enable the live TUI or make `--no-exit`,
`--web`, or `--interactive` applicable.

`--debug` enables engine and trace diagnostics. It is available when a command
declares `MayCallEngine OR MayRenderPipeline`. Engine commands request debug
logs from the engine. Rendering commands expose internal trace details. No
current command outside this capability union implements debug behavior.

### Workspace environment selection

`--env` selects an `env.<name>` overlay in `dagger.toml`. For a read, the
command uses the environment-applied configuration. For a write, the command
targets that environment overlay. The flag is available when a command
declares `MayReadWorkspaceConfig OR MayWriteWorkspaceConfig`.

`dagger module init` and `dagger api client init` declare both capabilities.
They resolve the SDK from the environment-applied configuration and record the
new module or client in that environment overlay. Generated files are still
exported to the workspace filesystem. Their dynamic SDK command registration
also applies the selected environment.

An environment SDK role replaces the base module's `as-sdk` value. An init
command starts with the effective role, adds or replaces the requested entry,
and writes the complete result under `env.<name>.modules.<sdk>.as-sdk`. This
keeps the base role unchanged and gives the environment a consistent module and
client ownership list.

`dagger setup` remains base-only. It declares neither workspace configuration
capability and does not accept `--env`.

### Output disposition

`--auto-apply` is available on commands that declare `MayProduceOutput`. It
applies an output without interactive review when the output handler supports
application.

`-o`, `--output` is also scoped by `MayProduceOutput`, but it remains a
command-local flag on function-call commands. It selects a local destination
when the output handler supports saving the result. Other output-producing
commands do not expose it until they define how an output path affects them.

## Workspace Configuration

The proposed configuration shape is:

```toml
[cloud.traces]
enabled = true
org = "my-org"

[cloud.engines]
enabled = true
scale-out = false
```

`cloud.<product>.enabled` enables client-side use for the current machine or
workspace. It does not enable the product for the Cloud organization. The
organization must enable and authorize the product separately.

Cloud Checks configuration is not part of `dagger.toml`. It is server-side
configuration managed through the Dagger Cloud API.

`cloud.engines.scale-out` is independent of `cloud.engines.enabled`. A command
can use a local primary engine and use Cloud Engines only for scale-out work.

## Global Flag Disposition

The table includes every current root-persistent flag. A dash in the `Change`
column means that no change is proposed.

| Change | Flag | Current | Capability or `dagger.toml` field |
|---|---|---|---|
| Move to configuration | `--org` | Visible | `cloud.traces.org` |
| Move to configuration | `--cloud` | Hidden | `cloud.engines.enabled` |
| Move to configuration | `--scale-out` | Hidden | `cloud.engines.scale-out` |
| Scope by capability | `-W`, `--workspace` | Visible | `MaySelectWorkspace` |
| Scope by capability | `--env` | Visible | `MayReadWorkspaceConfig OR MayWriteWorkspaceConfig` |
| Scope by capability | `-q`, `--quiet` | Visible | `MayRenderPipeline` |
| Scope by capability | `-s`, `--silent` | Visible | `MayRenderPipeline` |
| Scope by capability | `-d`, `--debug` | Visible | `MayCallEngine OR MayRenderPipeline` |
| Scope by capability | `--progress` | Visible | `MayRenderPipeline` |
| Scope by capability | `-i`, `--interactive` | Visible | `MayCallEngine AND MayRenderPipeline` |
| Scope by capability | `-w`, `--web` | Visible | `MayRenderPipeline` |
| Scope by capability | `-E`, `--no-exit` | Visible | `MayRenderPipeline` |
| Scope by capability | `-y`, `--auto-apply` | Visible | `MayProduceOutput` |
| Scope by capability | `--profile` | Hidden | `MayCallEngine` |
| Scope by capability | `--dot-output` | Hidden | `MayRenderPipeline` |
| Scope by capability | `--dot-focus-field` | Hidden | `MayRenderPipeline`; requires DOT output |
| Scope by capability | `--dot-show-internal` | Hidden | `MayRenderPipeline`; requires DOT output |
| Scope by capability and hide | `--interactive-command` | Visible | `MayCallEngine AND MayRenderPipeline`; only affects `--interactive` |
| Hide | `--x-release` | Visible | Bootstrap control; prefer `DAGGER_X_RELEASE` |
| - | `--workdir` | Hidden | Bootstrap working directory |
| - | `-v`, `--verbose` | Visible | Global diagnostic escape hatch |
| - | `-h`, `--help` | Hidden | Framework flag |

## Status

`MayCallEngine`, `MaySelectWorkspace`, `MayReadWorkspaceConfig`,
`MayWriteWorkspaceConfig`, `MayRenderPipeline`, and `MayProduceOutput`
capability scoping are implemented. All capability-scoped flag moves are
implemented except `--interactive` and `--interactive-command`. The
configuration and hide changes are planned.
