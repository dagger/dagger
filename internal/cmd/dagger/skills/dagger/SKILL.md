---
name: dagger
description: Set up and use Dagger workspace checks, generators, and modules. Use for Dagger CLI tasks, dagger.toml changes, and validation through installed Dagger modules.
---

# Work with Dagger

Use command-specific `--help` and printed next steps from the installed CLI.
Commands and flags can depend on installed modules. Read existing
`dagger.toml` and keep related `dagger.lock` changes. Work from the intended
project, or select it with `dagger -W PATH`.

## Set up only what is missing

Reuse an existing workspace and its modules. For a new workspace, run
`dagger init` and follow its setup guidance. A config file alone does not mean
checks are installed. Non-interactive `dagger module recommend` without `-y`
prints install commands but installs nothing. Select modules for the project's
tools. Use `dagger module search QUERY` if recommendations miss a tool.

A known module can be installed directly with `dagger module install SOURCE`;
this also creates missing workspace configuration. Prefer existing modules
for standard tools. Use `dagger module settings` to discover settings.

If the CLI directs you to migrate, use that migration command and its help.
Do not replace legacy configuration with an empty file. Installing checks
does not require creating a module or adding an SDK.

## Discover, then run

Use `dagger generate -l` and `dagger check -l` to discover available work.
During setup, apply needed generated files before checking the project.
For later edits, select work relevant to the change.

A `*` in a list is a collection summary. Add `--all` to see actual items;
a summary can represent an empty collection. Setup is complete only when
at least one relevant check runs and all workspace checks pass.

Run `dagger check` for all workspace checks. To find a focused selection, use
`dagger check -l --all --format=cli`, then add `dagger check` before a listed
row's flags. Use returned links and help rather than inventing filter names.

## Apply fixes deliberately

A changeset contains proposed file changes. Choose `dagger generate -y` to
apply changes required by the task, or `dagger generate --no-apply` for a
preview. A preview run is not required before an authorized change.

A successful preview does not mean generated files are current. Use
`dagger check --generated=true` to test that without applying changes.
Regenerate stale files rather than editing generated output by hand.

Not every fix is a generator. Formatters can return changesets through
`dagger api call`. Use `--help` along the function path to find arguments.
Use `-y` to apply changes required by the task. Inspect the diff and rerun
the check.

## Cloud Checks when requested

Use `dagger cloud checks on` and follow its prerequisite and retry commands.
Browser steps can require user action; do not loop on unmet prerequisites.
Check that the Git remote identifies the intended repository. After pushing
configuration and generated files, verify the run with
`dagger workspace activity`. Cloud setup is not required for local checks.

For setup context, read https://docs.dagger.io/getting-started/.
