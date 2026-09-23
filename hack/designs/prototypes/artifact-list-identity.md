# Artifact lists: dimensions and links

## Problem

A dimension identifies an item in a collection. It does not identify every operation on that item.

For example, `--go-module=./api` can select both a test and a generator. A container such as `backend/container` may have no dimensions at all.

Making every artifact identifiable through dimensions would require synthetic module, operation, and path dimensions. This would move paths into flags, with more names and rules to learn.

## Decision

Keep two parts of identity:

- **Dimensions select collection items.**
- **Links select paths.** A path with its dimensions is a matrix.

Keep the complete identity in the data: workspace, path, and dimension keys. Shorten only the output. A relative link uses the selected workspace. An absolute link also records the workspace and revision.

The goal is complete selection with few terms. It is not complete coverage through dimensions alone.

## Three views of one selection

| Format | Purpose | Rule |
| --- | --- | --- |
| `table` | Read and compare | Show dimension columns and descriptions. Add a final `LINK` column when the dimensions do not identify the rows. |
| `link` | Save or exchange addresses | Print complete typed links with every key. Use `--absolute` to include the workspace and revision. |
| `cli` | Reuse a selection | Print all required key flags, then a link when needed, then a description comment. |

When a table contains several matrices, show their links even if their current keys differ. A table can omit a parent column when child keys distinguish its displayed rows. CLI output must retain that parent filter. Display uniqueness does not prove that a copied command selects the same items.

Illustrative output:

```text
$ dagger list go-tests
GO-MODULE   GO-TEST
./api       TestHealth
./worker    TestHealth

$ dagger list go-tests -f=cli
--go-module=./api --go-test=TestHealth
--go-module=./worker --go-test=TestHealth

$ dagger list containers
GO-MODULE   LINK
./api       dag+container://go/modules/build
./api       dag+container://go/modules/test-container
```

Collection rows are reusable item filters. Prefix them with `dagger check` to run checks under those items. A row for a specific artifact retains its operation scope.

```text
# All checks in one module:
dagger check --go-module=./api

# One check in that module:
dagger check --go-module=./api dag+check://go/modules/test
```

## When can the link disappear?

Remove it only when the schema proves that the remaining filters preserve the intended selection.

1. Use canonical dimension identifiers. Print flag names that are unambiguous across the workspace and do not conflict with options in peer commands.
2. For a collection item, the filters must stay within that item's collection path and descendants.
3. For a command row, the filters must select only that operation within the command's full target set. Keep explicit command policy flags.
4. For generic artifact CLI output, retain the typed link. The receiving command is unknown.
5. Keep absolute links. Flags alone do not carry a workspace or revision.
6. If proof is unavailable, keep the link.

Use schema paths for this proof. Do not enumerate unrelated collections to see whether they happen to be empty. A sibling operation must prevent link omission even when it has no current items.

`check -l` and peer lists use CLI format. They list schema paths by default. `--all` reads keys and expands items. Formatting must not trigger additional collection evaluation.

## Layering and next steps

The Artifacts API owns selection and identity. The CLI owns display, quoting, and flag names. Existing `pathDefinitions`, `dimensionDefinitions`, and item keys are sufficient. This design needs no new public API.

Prototype the shared schema proof, command-list link omission, and table identity rules. Keep complete links unchanged. Test selection collisions, nested collections, empty collections, absolute links, and shell quoting.

Whole-collection flags remain a separate extension: `--go-modules` selects a dimension; `--go-module=PATH` selects a key. They can reduce typing, but cannot distinguish operations on the same item.

## Prototype status

Implemented on [prototype/artifact-list-identity](https://github.com/dagger/dagger/tree/prototype/artifact-list-identity), based on Collections. The two existing PR branches are unchanged.

The CLI build and focused Cloud tests passed. Tests cover ambiguous paths, empty sibling collections, nested keys, absolute links, grouping, shell quoting, and flag-name conflicts.

Grouping uses the listed key pairs. It does not query the engine again for each row. If repeated flags would select extra pairs, keep separate rows.

Full workspace integration is not verified. The existing dynamic-help collection expansion issue remains open. Whole-collection flags remain queued.
