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

### Usage

Run [with-playground.sh](with-playground.sh) with the inner command as argument:

```bash
with-playground.sh "uname -a; which dagger; dagger version"
with-playground.sh "cd src/dagger && dagger functions"
with-playground.sh "dagger -m github.com/dagger/jest call --help"
```

The script builds the playground, mounts sample source code at `./src`, executes the command, and prints combined output.

### Environment variables

You may set these environment variables to customize execution of with-playground.sh

| Variable | Default | Description |
|---|---|---|
| `PLAYGROUND_TIMEOUT` | `300` | Timeout in seconds. Set to `0` to disable. Exits 124 on timeout. |
| `DAGGER_MODULE` | unset | Override module ref (e.g. `github.com/dagger/dagger@my-branch`). Useful with cloud engine to skip local uploads. |

### Interpreting results

- **Success**: Functions listed, exit code 0.
- **Engine crash**: Look for `panic:` with a goroutine stack trace. Trace backward from the panic to find root cause (often a nil field on a partially-initialized struct).
- **Silent failure**: Exit code 1 with no panic. Look for error messages in the engine log stream above the command output (the script runs with `--progress=plain`).
- **Timeout**: Exit code 124. The command hung (deadlock, infinite loop). Examine partial output above the timeout message. Reduce scope of the inner command to isolate the hang.

Remember, dagger logs not just the execution of your inner command, but the entire process of building a complete dagger engine, dagger CLI, and the execution environment wrapping them; then running the dev engine as a service (including streaming its logs) while concurrently executing your inner command. Keep this in mind when reading logs.

## Integration Tests

FIXME
