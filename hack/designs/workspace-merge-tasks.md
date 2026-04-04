# Workspace Merge Tasks

Date: 2026-04-03

Source:
- [workspace-playground-qa.md](/Users/shykes/git/github.com/dagger/dagger_workspace/hack/designs/workspace-playground-qa.md)

Status legend:
- `[ ]` open
- `[x]` done
- `[-]` blocked on product decision

## Runtime Bugs

- [x] Make `dagger update` behave sanely in an empty initialized workspace.
  Current behavior: `dagger init` followed by `dagger update` fails with `workspace lockfile does not exist`.

- [x] Restore normal `dagger migrate` summary output when migration includes remote lookup sources.
  Current behavior: migration succeeds, but the summary is suppressed.

- [x] Remove false `NotFound` progress noise from `dagger module init <path>` inside a workspace.
  Current behavior: command succeeds but emits scary transient errors before export creates the target directory.

## CLI Polish

- [x] Fix root help grouping so workspace-native commands are not shown under `DAGGER MODULE COMMANDS`.

- [x] Tighten workspace command help/output wording where the QA pass found ambiguity.
  Current focus:
  - `workspace list` vs `workspace config` local source path wording
  - any related examples/usages touched by the grouping fix

## Product Decision

- [x] Decide whether top-level `dagger install` should implicitly initialize a workspace.
  Decision:
  - keep implicit bootstrap
  Follow-through:
  - document it in command help and output
  - keep the behavior behind a single schema policy choke point for a future `--require-init` style option

## Follow-Up

- [ ] Sweep docs after behavior settles.
  This is already on the broader branch task list and should happen after the command/help/runtime fixes land.
