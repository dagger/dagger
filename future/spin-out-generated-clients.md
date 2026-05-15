# Future Spin Out Generated Clients

author: shykes
created: 2026-05-15
status: future task
related: future/workspace-command-migration-review.md

## Goal

Remove the `dagger client` command group from the core CLI and move equivalent
generated-client workflows into SDK-as-module repos.

The first SDK-module targets should be:

- Go SDK module
- Python SDK module
- TypeScript SDK module
- other SDK modules as they are developed

## Context

Generated clients are SDK authoring behavior. They are closer to `dagger
develop` and SDK codegen than to workspace management or module execution.

The core CLI should not own SDK-specific generated-client UX. Core should keep
only the engine/runtime/client-generator primitives that SDK modules need.

## Commands In Scope

Inventory the current hidden `dagger client *` command surface before removal.
Expected candidates include:

- `dagger client install`
- `dagger client update`
- `dagger client list`
- `dagger client uninstall`

Use the workspace command migration review to confirm the exact command paths,
aliases, hidden status, flags, and tests before deleting anything.

## Target Shape

Each SDK-as-module should expose the generated-client workflow for its language.
The SDK module API does not need to preserve the old CLI command syntax, flag
names, or exact error strings.

Preserve the behavior that matters:

- add generated client files for a selected SDK/language
- update generated client files when the engine/API changes
- support custom output directories where the SDK supports them
- preserve or intentionally migrate config entries needed by generated clients
- handle dependency-generated clients where that remains a supported workflow

## Test Migration

Core tests that only validate hidden `dagger client *` command UX should be
deleted when the commands are removed.

Known command-surface candidates in core:

- `ClientGeneratorTest.TestClientCommands`
- `ClientGeneratorTest.TestClientUpdate`

Generated-client behavior that remains a lower-level core API should stay in
core only if SDK modules still depend on that engine/client-generator primitive.
Otherwise, port it to the relevant SDK module test suite.

## Process

1. Complete the command-surface review in
   `future/workspace-command-migration-review.md`.
2. Capture the current `dagger client *` behavior and decide which behavior is
   still supported.
3. Add equivalent generated-client functions/tests to the Go SDK module first.
4. Repeat for Python, TypeScript, and other SDK modules as they become
   available.
5. Remove `dagger client *` command registrations, command implementations,
   docs, and command-only tests from core.
6. Keep or replace lower-level core coverage for any client-generator primitive
   that remains part of the engine contract.

## Done Criteria

- `dagger client` no longer appears in the core CLI command tree, including
  hidden commands.
- SDK modules own generated-client UX and tests.
- Core retains only SDK-neutral client-generator primitives that are still
  needed by SDK modules.
- `ClientGeneratorTest.TestClientCommands` and
  `ClientGeneratorTest.TestClientUpdate` are deleted or replaced with
  SDK-module coverage.
