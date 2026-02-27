---
name: engine-dev-testing
description: "How to test Dagger engine changes. Covers manual e2e testing via the playground (bootstrapping a dev engine from source, running commands inside it, capturing crash logs) and running the integration test suite. Use when modifying engine code (engine/server, core/, dagql/) and needing to verify changes work. Triggers on: testing engine changes, dev engine, playground, e2e test, engine crash, debug engine, manual testing, verify engine fix."
---

# Engine Dev Testing

Two testing modes: **playground** (ephemeral sandbox with dev engine and CLI built from source) and **integration tests** (automated test harness).

## Prerequisites

- **Stable `dagger` CLI** on PATH (system install). This bootstraps everything.
- Do NOT use `go build`, `docker`, or any other toolchain directly. Dagger builds and tests Dagger.

## Playground

The playground builds a fresh dev engine from source and drops you into an ephemeral container with the dev `dagger` CLI. It has no side effects and no dependencies other than your system dagger. You execute "inner commands" inside the playground — typically invoking the dev dagger CLI. Great for debugging engine crashes and validating new features interactively.

By default, everything will be built from your local source checkout of dagger/dagger, including local changes. But, you may optionally set the DAGGER_MODULE env variable to point to a remote git ref, for example 'github.com/dagger/dagger@my-upstream-branch'. In that case, local source will be ignored (and in fact doesn't even need to exist) and all files will be pulled from the remote ref.

### Usage

**Always run with `run_in_background: true`** and poll with `TaskOutput`. Playground builds take 1-5 minutes; the Bash tool's default timeout (120s) and max timeout (10 min) are both problematic for synchronous execution.

**Step 1: Start the playground in the background.**

```
Bash(command="skills/engine-dev-testing/with-playground.sh 'dagger version'", run_in_background=true)
→ returns task_id
```

**Step 2: Poll for completion.** Use `TaskOutput` with `block: true` and a reasonable timeout (60s). The script prints a heartbeat every 30s, so you'll see `[playground: 30s elapsed, still running...]` while it builds.

```
TaskOutput(task_id=<id>, block=true, timeout=60000)
→ if still running: heartbeat messages visible
→ if done: full output with inner command results + status line
```

Repeat polling until you see `=== Playground: SUCCESS ===` or `=== Playground: FAILED ===`.

**Step 3: Interpret the results.** See "Interpreting results" below.

### Examples

Simple command:

```
with-playground.sh "dagger version"
```

Working with source code (mounted at `./src`):

```
with-playground.sh "cd src/dagger && dagger functions"
```

Multi-line scripts and heredocs work reliably:

```
with-playground.sh '
mkdir -p /tmp/test/.dagger && cd /tmp/test
cat > .dagger/config.toml <<TOML
[modules.wolfi]
source = "github.com/dagger/dagger/modules/wolfi"
TOML
dagger functions
'
```

The script writes the inner command to a file inside the container, then executes it. This avoids quoting issues with special characters, newlines, and nested heredocs. The script builds the playground, mounts sample source code at `./src`, executes the command, and prints combined output.

### Environment variables

You may set these environment variables to customize execution of with-playground.sh

| Variable | Default | Description |
|---|---|---|
| `PLAYGROUND_TIMEOUT` | `300` | Timeout in seconds. Set to `0` to disable. Exits 124 on timeout. |
| `DAGGER_MODULE` | unset | Override module ref (e.g. `github.com/dagger/dagger@my-branch`). Useful with cloud engine to skip local uploads. |

### Interpreting results

The script manages output formatting, error extraction, and status reporting. See comments in [with-playground.sh](with-playground.sh) for implementation details. Here's what to look for in the output:

- **Still running (polling)**: Heartbeat messages but no status line → poll again.
- **Success**: Look for `=== Playground: SUCCESS ===` at the end. Inner command output appears above it.
- **Engine crash**: Look for `panic:` with a goroutine stack trace. The script extracts panics automatically, even if they occurred early in the build. Trace backward from the panic to find root cause (often a nil field on a partially-initialized struct).
- **Empty inner command output**: The dagger pipeline failed before reaching your inner command. Check the progress trace below it for the error.
- **Silent failure**: Exit code non-zero with no panic. A progress trace section with error details appears after the inner command output.
- **Timeout (exit code 124)**: The inner command hung (deadlock, infinite loop). Look for `=== TIMEOUT ===` at the end. Reduce scope of the inner command to isolate the hang.

Remember, dagger logs not just the execution of your inner command, but the entire process of building a complete dagger engine, dagger CLI, and the execution environment wrapping them; then running the dev engine as a service (including streaming its logs) while concurrently executing your inner command. Keep this in mind when reading logs.

## Integration Tests

FIXME
