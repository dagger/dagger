# Workspace Playground QA

Date: 2026-04-03

Environment:
- branch-built CLI + engine via `dagger call -m ./toolchains/engine-dev playground`
- manual scripted QA in ephemeral playground directories

Scope:
- `dagger init`
- `dagger workspace ...`
- `dagger module init`
- `dagger install`
- `dagger update`
- `dagger module install`
- `dagger module update`
- `dagger migrate`
- generated files, lockfiles, and migrated file contents

## Summary

The core shape now feels much closer to the intended workspace UX:
- `dagger init` initializes a workspace
- `dagger module init` creates modules
- top-level `install` and `update` are workspace-native
- local-source and root-source migration both rewrote files correctly in the tested cases
- the deprecated `--license` shim behaves as intended

I found three concrete runtime issues, two CLI/help inconsistencies, and one behavior that needs an explicit product decision.

## Findings

### 1. `dagger update` fails on a freshly initialized workspace

Repro:

```sh
mkdir empty && cd empty
dagger init
dagger update
```

Observed:

```text
Error: workspace lockfile does not exist
```

Notes:
- This also happens immediately after `dagger init`, before any modules are installed.
- The error reads like a broken workspace state, not a normal empty-workspace case.
- Expected behavior is probably either:
  - no-op with `Lockfile already up to date`, or
  - create an empty `.dagger/lock`

Severity: high

### 2. `dagger migrate` loses its summary when the migration has remote lookup sources

Repro:

```json
{
  "name": "remoteapp",
  "toolchains": [
    {"name": "wolfi", "source": "github.com/dagger/dagger/modules/wolfi@main", "pin": "main"}
  ]
}
```

Then:

```sh
dagger migrate
```

Observed:
- migration succeeds
- `.dagger/config.toml` is written
- `.dagger/lock` is written
- legacy `dagger.json` is removed
- but no `Migrated to workspace format: ...` summary is printed

Notes:
- local-source and root-source migration still print the summary
- the silent path seems specific to the follow-up remote lookup refresh

Severity: high

### 3. `dagger module init <path>` inside a workspace succeeds, but emits scary `NotFound` errors during progress

Repro:

```sh
dagger init
dagger module init --name standalone --sdk=go ./standalone
```

Observed:
- command exits `0`
- `./standalone/dagger.json` is created correctly
- workspace config is unchanged, which is correct
- but progress output includes transient errors like:

```text
failed to receive stat message ... lstat /tmp/.../standalone: no such file or directory
failed to get content hash ...
```

Notes:
- this looks like the command failed even though it succeeded
- likely caused by probing the target directory before export creates it

Severity: medium

### 4. Root help still classifies workspace commands as module commands

Observed in `dagger --help`:
- `init`
- `install`
- `update`
- `workspace`

all appear under `DAGGER MODULE COMMANDS`.

Notes:
- this clashes with the intended command split
- it makes the restored workspace-native UX look half-migrated even when the runtime behavior is right

Severity: medium

### 5. `workspace list` and `workspace config` disagree on local module source paths

Repro after:

```sh
dagger init
dagger module init --name hello --sdk=go
```

Observed:

```sh
dagger workspace config modules.hello.source
```

prints:

```text
modules/hello
```

but:

```sh
dagger workspace list
```

prints:

```text
hello   .dagger/modules/hello
```

Notes:
- both values are understandable in isolation
- together they are confusing because one shows the config value and the other shows the resolved on-disk path
- the command does not explain that distinction

Severity: medium

## Resolved Decisions

### 6. `dagger install` should implicitly initialize a workspace

Repro:

```sh
mkdir clean && cd clean
dagger install github.com/dagger/dagger/modules/wolfi@main
```

Observed:
- command succeeds
- `.dagger/config.toml` is created
- `.dagger/lock` is created
- output only says:

```text
Installed module "wolfi" in /tmp/.../.dagger/config.toml
```

Notes:
- this is convenient
- the product decision is to keep this implicit bootstrap
- command help and output should say so clearly

## Good

- `dagger init` now behaves as workspace init, and `dagger workspace init` matches it.
- `dagger module init` with no path inside a workspace creates a config-owned module under `.dagger/modules/<name>`.
- `dagger module init` no longer generates a `LICENSE` file, and `--license=true` fails with the intended deprecation error.
- top-level `dagger install` writes both workspace config and lock entries.
- install name collisions now fail cleanly and preserve config.
- `dagger module install` and `dagger module update` still work for standalone modules.
- local-source migration moved source files, rewrote relative dependency/include paths, and removed the old source directory.
- root-source migration kept source files at the project root and rewrote migrated module source to `../../../`.

## Suggested Next Pass

1. Fix empty-workspace `dagger update`.
2. Restore migration summary output for remote-lookup migrations.
3. Remove or suppress the false `NotFound` noise from explicit-path `dagger module init`.
4. Fix root help grouping and tighten help/examples.
5. Document the implicit workspace bootstrap behavior of `dagger install`.
