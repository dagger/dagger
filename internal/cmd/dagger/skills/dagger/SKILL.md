---
name: dagger
description: Use Dagger to run project checks, generate files, and manage workspace modules. Use when a task involves dagger.toml, Dagger commands, or checks and generators provided by installed Dagger modules.
---

# Work with a Dagger workspace

Read `dagger.toml` and the project instructions first. The workspace config
selects modules and their settings. `dagger.lock` records resolved dependencies.
Keep both files when a module change updates them.

## Find the available operations

Run commands from the intended workspace. Use `dagger -W PATH` to select a
different workspace. Start with:

```sh
dagger module list
dagger check -l
dagger generate -l
```

Use `--help` on a command to find its current flags. Installed modules add
flags and operations, so do not assume that a module or check is available.
Add `--all` to a check or generator list to expand each collection into its
items. A summary row with `*` does not prove that the module found any items.

## Check a change

Use the installed checks that cover the changed code. Run `dagger check` for
all workspace checks. For a smaller selection, list the available commands:

```sh
dagger check -l --all --format=cli
```

Add `dagger check` before the flags in a listed row to run that selection.
Quote patterns so the shell does not expand them. A failed check returns a
nonzero exit code. Read its output and correct the cause before running it
again. State which checks ran and which could not run.

## Generate files

A generator returns a changeset: the proposed file changes. For a preview:

```sh
dagger generate --no-apply
```

When the task requires those file changes, run `dagger generate -y` to apply
them. Inspect the resulting diff and run the relevant checks. Use the list
and `--help` to select a specific generator when appropriate.

`dagger check` also checks for stale generated files by default. Do not edit
generated files by hand to make that check pass. Run the source generator.

## Change workspace modules

Use `dagger module search QUERY` to find modules and
`dagger module install SOURCE` to install a selected module. Use
`dagger module settings` to inspect settings. Reuse existing modules before
adding a new one. Keep module changes within the requested task.

Workspace configuration is in `dagger.toml`. A module's `dagger.json` is a
separate file. If a command requires legacy workspace migration, use
`dagger workspace migrate`; do not replace the old config with an empty file.

For module functions beyond checks and generators, inspect
`dagger api --help` and `dagger api call --help`. Follow the project's SDK
and module conventions when changing module code.

For further command details, see https://docs.dagger.io/using/checking/ and
https://docs.dagger.io/using/generating/.
