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

## Spec decisions

The collection type rule stays simple. Other dimensions must distinguish artifacts from different fields. Two recommendations await a decision:

- Use the full ordinary field path in `name`, starting at the exposed module root and omitting `get`. This separates `before.check` and `after.check`. The alternative keeps local names and adds a `field` column.
- Use item names by default. On a name conflict, use collection type names for the conflicting columns. Alternatives use collection type names everywhere, or reject duplicate item names.

The complete options are in [the spec](collections-issue.md#dimension-names).

## Remaining address limit

The current URI grammar cannot require a dimension to be absent. A parent collection check and its item check can have the same public path and type. The child has an extra collection coordinate. Positive predicates cannot select the parent alone.

The explicit error is safe behavior. Full support needs an absence selector or another explicit address rule. No extra projection column was added.

## Validation

- Pure schema, naming, address, and native utility tests pass.
- Focused Linux core, schema, and CLI tests pass.
- All 12 Python capture tests pass.
- The Cloud grid test passed. It checks four cells and four deferred checks, exact type columns, schema metadata, tables, copied links, and copied commands.
- The final Cloud rerun could not provision an engine: the organization compute limit was full. Two attempts failed before tests started. The later safety changes passed focused Linux tests. The earlier grid trace is https://dagger.cloud/dagger/traces/2c1997824ec43d177e6f62fb4d2144aa.

The review does not claim unconditional unique projected coordinates while the name-scope decision is open. It does not claim support for an ancestry that repeats one collection type with two different keys; that input is rejected.
