# Collection type columns: adversarial review

## Contract

Each schema collection type defines one column. Its identifier is the exact collection type name. Its default name comes from the item type. Field paths do not define collection columns.

The same type at two fields shares a column. Two distinct types remain distinct, including directly nested collections with no field between them.

## Findings fixed

- The schema compiler retained path identity. It now requires collection types and unifies each type across routes.
- The engine and CLI still consumed route identifiers. The new API view now uses type identifiers. Older views retain legacy metadata.
- `Rows -> Row -> Cell` had an alias collision: `row` selected the cell axis. Canonical item names now precede collection aliases.
- Address pruning inferred depth from slash counts. It now uses collection ancestry order.
- An exact type-column filter could hide a failed module. The filter now retains the failure when its schema is unavailable.
- A reserved collection type identifier could lose its key. Compilation now rejects reserved and malformed type identifiers.
- A parent check URI could also select its children. Absence selectors now distinguish the parent. Schema and full links keep the required structural guards.
- Projected coordinates could overwrite source address plans. Source address identity now retains the source path.
- Source filters could change fallback labels. Modern discovery now reads the complete schema before selecting rows.
- A failed module outside an include could enter selected rows. Its failure now follows the include filter.
- Legacy capture could use CLI spelling in full field names. It now resolves literal collection fields from schema metadata.
- Saved names and address plans could use the previous rules. Discovery keys and saved snapshots now have new versions.
- A column label could equal another exact collection identifier. Compilation now rejects that ambiguity.

## Spec decisions

The collection type rule stays simple. Other dimensions distinguish artifacts from different fields. The approved rules are:

- Use the full ordinary field path in `name`, starting at the exposed module root and omitting `get`. This separates `before.check` and `after.check`.
- Use item names by default. On a name conflict, use collection type names for all conflicting columns. Repeat for any new conflict. Reject a conflict that remains after fallback. Reserve `module` and `name`.
- Compute names from the complete schema, including with module and source filters. Keep CLI flag allocation separate from column names.

The rules are in [the spec](collections-issue.md#dimension-names).

## Absence selectors

`?!cell` selects artifacts without a cell coordinate. `?cell=` selects an empty key. The parser accepts an optional empty value and a percent-encoded `!`. These forms survive an ordinary query-tool round trip.

The minimum-address search includes absence tests. Equal-sized sets prefer fewer absence tests, then the existing collection/name/module order. No projection column was added.

The review found and fixed schema-list merging, full-form aliasing, cross-module path collisions, synthetic stale proofs, bound-value proofs, and shared alias-map mutation. Schema filters remove excluded collection levels before evaluating receivers. Failed modules remain visible when their schema cannot prove absence.

Exact minimum search has exponential worst-case cost. Cached projections handle common cases. Required predicates avoid subset search when another row differs in only one dimension. A test covers 64 required absence selectors.

## Validation

- Fresh QA uses `dagger/dagger@3991a28b9d`: 269 schema fields and 2,511 selected artifacts. There are 848 checks, 749 generators, 186 containers, and 728 Go packages. No module failed to load. [Capture trace](https://dagger.cloud/dagger/traces/8d3833e39187c7f0fe27300f88e24974).
- All 2,511 coordinates and links match the live engine. Each link selects one artifact with the minimum selector count. All printed commands select the correct row. All 240 CLI replay checks pass.
- All 24 complete outputs remain unchanged after the rebase on that main commit. The [CLI examples](https://gist.github.com/shykes/614674444b4c87944bea48eda03b9787) contain the current outputs.
- Pure Go and focused Linux core, schema, CLI, and server tests pass. All 16 Python capture tests and the PHP query regression test pass.
- New APIs start at beta.28. Compatibility tests retain beta.25–beta.27 behavior. PHP omits the new `all` argument unless true.

The first Cloud run after the rebase found stale integration assertions and an excluded module in the command report. The assertions now use canonical columns and full names. Copied-link tests check complete coordinates. A separate beta.27 test retains the shipped route-based API contract.

Schema loading now reports failures only for selected modules. A real-span test covers a silent metadata load followed by a selected load and a repeated load. Check execution reads the type from a typed URI. It no longer depends on a hidden type column.

The final independent review found no remaining code blocker. The full Cloud rerun is incomplete: three attempts could not start because the organization compute capacity was full. [Latest attempt](https://dagger.cloud/dagger/traces/4bb906980637f16d4fd0b8a53b00acfe).

## Earlier evidence and limits

- The earlier workspace capture had 109,349 artifacts with no duplicate typed coordinates. This is evidence for that workspace, not a universal identity proof.
- Native absence tests cover 2,304 targets across 511 snapshots and a case with 64 required absence selectors.
- Five earlier Cloud cases pass, including schema-only and expanded parent/child lists, copied links, and copied commands. Leaf checks stay deferred. [Trace](https://dagger.cloud/dagger/traces/710f8400748ec27f252915420bcebd42).
- Address shortening can read excluded collection receivers to test uniqueness against the unfiltered workspace. If that read fails, selected artifacts retain safe schema links.

An ancestry that repeats one collection type with two different keys is rejected. The captured workspace is a QA case, not a universal identity proof.
