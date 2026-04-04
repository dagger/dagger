# Workspace Merge Tasks

Date: 2026-04-03

Source:
- [workspace-playground-qa.md](/Users/shykes/git/github.com/dagger/dagger_workspace/hack/designs/workspace-playground-qa.md)

Status legend:
- `[ ]` open
- `[x]` done
- `[-]` blocked on product decision

## Runtime Bugs

- [ ] Make `dagger update` behave sanely in an empty initialized workspace.
  Current behavior: `dagger init` followed by `dagger update` fails with `workspace lockfile does not exist`.

- [ ] Restore normal `dagger migrate` summary output when migration includes remote lookup sources.
  Current behavior: migration succeeds, but the summary is suppressed.

- [ ] Remove false `NotFound` progress noise from `dagger module init <path>` inside a workspace.
  Current behavior: command succeeds but emits scary transient errors before export creates the target directory.

## CLI Polish

- [ ] Fix root help grouping so workspace-native commands are not shown under `DAGGER MODULE COMMANDS`.

- [ ] Tighten workspace command help/output wording where the QA pass found ambiguity.
  Current focus:
  - `workspace list` vs `workspace config` local source path wording
  - any related examples/usages touched by the grouping fix

## Product Decision

- [-] Decide whether top-level `dagger install` should implicitly initialize a workspace.
  Observed behavior:
  - in a clean directory, `dagger install <module>` creates `.dagger/config.toml` and `.dagger/lock`
  Decision needed:
  - keep implicit bootstrap and document it
  - or require explicit `dagger init`

## Follow-Up

- [ ] Sweep docs after behavior settles.
  This is already on the broader branch task list and should happen after the command/help/runtime fixes land.
