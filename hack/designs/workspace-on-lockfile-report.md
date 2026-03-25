# Workspace Replay Onto Lockfile: Maintainer Report

## Status

As of March 25, 2026, the workspace replay is forward-ported onto the current
`origin/lockfile` tip on `tmp/workspace-on-lockfile`.

Current `origin/lockfile` base commit:

- `e4f5be63c` `generate: refresh generated lockfile APIs`

Replay ledger:

- [workspace-on-lockfile-replay.md](/Users/shykes/git/github.com/dagger/dagger_workspace-on-lockfile/hack/designs/workspace-on-lockfile-replay.md)

## Conclusion

Yes, this can be the new `workspace` branch.

Why:

- the replay scope is complete
- the lockfile ownership boundary is correct
- the branch is now on the latest `origin/lockfile`
- the focused forward-port verifier set passes

The remaining caveat is ordinary branch hygiene, not replay scope: I have not
run a full repo-wide CI sweep from this tip.

## What Landed

- workspace targeting for `call`, `check`, `generate`, and `functions`
- `dagger workspace info`, `init`, `config`, `list`
- workspace install plus top-level `dagger install` routing
- workspace-routed `dagger module init`
- explicit `dagger module install` and `dagger module update`
- local `dagger migrate`
- config-owned module loading during session startup
- workspace install and migrate using the generic
  `dag.ModuleSource()` / `modules.resolve` lock path
- selective refresh via `dagger lock update <module...>`

Important shape:

- whole-lock refresh stays on `currentWorkspace.update()` /
  `dagger lock update`
- selective refresh is additive, not a replacement

## What Was Dropped

These old buckets were intentionally not carried forward:

- workspace-owned lockfile wrappers and `modules.resolve` substrate
- workspace-specific update mutation and `dagger workspace update`
- workspace-specific lock mode plumbing
- generic lockfile work already owned by `lockfile`

This replay reused the old bucketing work, not the old patch shapes.

## Verification

Each bucket was committed and verified before moving on.

Verified:

- focused Linux compile checks for touched packages
- `engine-dev` schema and CLI tests
- `engine/server` session tests
- focused integration tests for init, config, install, list, module
  init/install/update, migrate, and selective lock refresh
- forward-port rerun on the latest `origin/lockfile`:
  - Linux compile checks for `./cmd/dagger` and `./core/schema`
  - focused `cmd/dagger` suite for workspace targeting, sibling traversal,
    workspace info, and span naming
  - full `TestWorkspace` integration suite

Not yet done:

- a full repo-wide CI sweep from this branch tip

The exact commands and traces are in the replay ledger.

## Bottom Line

- Did the replay succeed? Yes.
- Is this the right new workspace line? Yes.
- Is it current on latest `lockfile`? Yes.
- Would I use this as the new `workspace` branch now? Yes.
