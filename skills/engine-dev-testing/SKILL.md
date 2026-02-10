---
name: engine-dev-testing
description: "How to test Dagger engine changes. Covers manual e2e testing via the playground (bootstrapping a dev engine from source, running commands inside it, capturing crash logs) and running the integration test suite. Use when modifying engine code (engine/server, core/, dagql/) and needing to verify changes work. Triggers on: testing engine changes, dev engine, playground, e2e test, engine crash, debug engine, manual testing, verify engine fix."
---

# Engine Dev Testing

Two testing modes: **playground** (ephemeral sandbox with dev engine and CLI built from source) and **integration tests** (automated test harness).

## Prerequisites

- **Stable `dagger` CLI** on PATH (system install). This bootstraps everything.
- Do NOT use `go build`, `docker`, or any other toolchain directly. Dagger builds and tests Dagger.

## Manual testing with the playground

The playground is the best place to start. It is great for debugging engine crashes and validating new features interactively. It is a good starting point for testing things that are not yet covered by integration tests, or are too custom to be worth automating.

### How the playground works

The playground builds a fresh dev engine from source and drops you into an ephemer container with the dev `dagger` CLI.
It has no side effects, and has no dependencies other than your system dagger. You can then execute "inner commands" inside the playground.
Typically these inner commands invoke the (dev) dagger CLI.

### Using the playground

This skill bundles a [shell script](with-playground.sh) that is ready to go.

You execute it with the inner command as argument (it may be a shell script). It will:

1) build an engine playground from source
2) mount sample source code at ./src for convenience
3) execute the given inner command arguments in an ephemeral container, while streaming dagger logs
4) wind down the container and print the output of the inner command.

Examples:

- `with-playground.sh "uname -a; which dagger; dagger version"`
- `with-playground.sh dagger core version`

### Handling Timeouts

Playground commands can hang (deadlocks, infinite loops). Run with a hard timeout:

```bash
timeout 300 dagger --progress=plain -m github.com/dagger/dagger@workspace call engine-dev playground \
  with-exec --args 'sh','-c','dagger functions 2>&1; echo EXIT_CODE=$?' \
  stdout
```

Or when using the Bash tool, set `timeout: 300000` (5 minutes).

For agent use: run the command in background with `run_in_background: true`, then poll the output file periodically. If no new output appears for 60+ seconds, the command is likely hung — stop it and analyze what was captured.

### Adding Local Source to Playground

To test with a source repo available inside the playground:

```bash
dagger -m github.com/dagger/dagger@workspace call engine-dev playground \
  with-directory --path=./src/myrepo --source=https://github.com/org/myrepo \
  with-exec --args 'sh','-c','cd src/myrepo && dagger functions 2>&1; echo EXIT_CODE=$?' \
  stdout
```

## Interpreting Results

### Success

```
Name          Description
container     ...
some-func     ...
EXIT_CODE=0
```

Functions listed, exit code 0.

### Engine Crash

```
panic: runtime error: nil pointer dereference
goroutine 123 [running]:
github.com/dagger/dagger/engine/buildkit.(*Client).withClientCloseCancel(0x0, ...)
    /src/engine/buildkit/client.go:456
...
EXIT_CODE=1
```

The stack trace shows exactly where the crash occurred. Trace backward from the panic to find the root cause (often a nil field on a partially-initialized struct).

### Silent Failure

If the command returns `EXIT_CODE=1` with no panic but also no useful output, re-run with `--progress=plain` and look for error messages in the engine log stream.

## Integration Test Suite

The full integration test suite can be run via:

```bash
dagger -m github.com/dagger/dagger@BRANCH call engine-dev test
```

This is slower but covers the complete test matrix. Use manual e2e for rapid iteration, integration suite for validation before merge.


## Integration tests

Not yet documented.
