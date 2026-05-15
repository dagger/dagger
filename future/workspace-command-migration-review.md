# Future Workspace Command Migration Review

author: shykes
created: 2026-05-15
after: `future/module-test-cleanup.md`
status: future task

## Context

The workspace branch removes some command paths and redefines others. Do not
add command-presence or command-absence guard tests opportunistically while
removing individual commands.

Instead, do a single workspace-wide migration review against `main`, using
`main` as the baseline and this branch as the target.

## Goal

Produce an intentional command migration map:

- commands that are removed
- commands that are redefined
- commands that are renamed
- commands that remain workspace-owned
- hidden/deprecated commands that should disappear
- command aliases that should stay or go

Then add focused CLI tests that lock in the final intended command surface.

## Why

Testing absence one command at a time is too local for this migration. Some
commands are being removed, while others are being preserved with workspace
semantics or changed in scope. The right test is a migration review of the full
CLI surface, not a piecemeal guard test added from the middle of the branch.

## Process

1. Check out or inspect `main`.
2. Capture the root command tree and hidden command tree from `main`.
3. Capture the same command tree from the workspace branch.
4. Build a table with:
   - command path
   - status: removed, kept, renamed, redefined, hidden-only, unknown
   - old owner: module, workspace, execution, client, other
   - new owner, if any
   - expected test coverage
5. Review the table before adding assertions.
6. Add CLI tests only after the intended command map is agreed.

## Candidate Assertions

Add tests for broad invariants rather than isolated command accidents:

- removed module-management commands are absent
- workspace-level `install`, `update`, and `lock update` remain present
- command groups show workspace and execution commands correctly
- hidden deprecated aliases do not reappear unintentionally
- command paths with changed semantics have updated help text and flag sets

## Done Criteria

This task is done when:

- a main-vs-workspace command migration table exists
- every changed command path has an explicit disposition
- CLI tests cover the agreed final command surface
- test names describe migration intent, not just individual deleted commands
