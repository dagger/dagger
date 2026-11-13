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
- A parent check URI could select both parent and child checks at the same public path. Without a runtime uniqueness proof, the formatter now returns an error. It does not emit that unsafe fallback.
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

- Pure schema, naming, address, and native utility tests pass.
- Focused Linux core, schema, and CLI tests pass.
- All 14 Python capture tests pass.
- The native utility accepts all 256 captured schema fields and 109,349 artifacts, with no duplicate typed coordinates.
- All 2,424 captured links select one artifact with the minimum key count. All printed CLI selectors select the same row. Seven sampled CLI replays pass.
- Three Cloud tests pass: nested collections, shared collection types at different fields, and cross-module fallback labels. [Trace](https://dagger.cloud/dagger/traces/b9ef81388d862d8f7deeaad848b6db47).

The independent review accepts the implementation. No blocker remains in the approved naming rules.

The absence review also accepts the implementation. Native tests include 2,304 reference targets across 511 snapshots. Focused Linux core, schema, and CLI tests pass. All 2,424 captured links retain their previous minimum size, and all 194 CLI replay checks pass. The capture needs no absence selectors.

Five Cloud cases pass, including schema-only and expanded parent/child listings with copied links and commands. Leaf checks remain deferred. [Trace](https://dagger.cloud/dagger/traces/710f8400748ec27f252915420bcebd42).

An ancestry that repeats one collection type with two different keys is rejected. The captured workspace is a QA case, not a universal identity proof.
