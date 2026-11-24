# Artifact flattening

Run the canonical utility from the repository root:

```sh
go run ./core/artifact/flattening/cmd/flatten < core/artifact/flattening/testdata/canonical-compose.json
go test ./core/artifact/flattening ./core/artifact/flattening/cmd/flatten
```

These commands use native Go. They do not start an engine.

The package maps schema fields and collection keys to dimensions and coordinates.
The engine uses these rules from API view `v1.0.0-beta.29`. Earlier views keep
the legacy rules described below.
A dimension is one column. A coordinate is the value in that column.
`NewSchema` compiles the complete field list. `Schema.Coordinates` returns each
row. The utility prints dimension identifiers and names, then each artifact type
and its full coordinates.

## Canonical input

The input is one JSON array. Each entry embeds a `Field` and adds `items`:

```json
[
  {
    "module": "docs",
    "type": "Check",
    "localPath": ["references", "stale"],
    "items": [[]]
  }
]
```

`module` is the configured module name. Omit it for a field with no module.
`type` is required. `localPath` contains the literal schema field names relative
to the nearest enclosing collection item. With no enclosing item, the local root
is the module's exposed root. An empty local path identifies the collection
item itself, or the exposed module root when there is no collection.
Do not include a module prefix or synthetic `get` operations. The utility does
not derive this path from a legacy `Artifact.Path`.

`collections` lists the collection boundaries from parent to child. Each entry
requires its exact schema `type` and `itemType`. One collection type defines one
column, with its item type name in kebab case. If two collection types have the
same item type name, both labels use their collection type names in kebab case.
Thus `Packages` and `Dependencies` with item type `Package` use labels `packages`
and `dependencies`. An unresolved label conflict causes an error.
Reserve `module` and `name`. Apply fallback to every conflict, and repeat for
any new conflict. Reject a label that equals another collection's identifier.
`NewSchema` calls `NameDimensions` on the complete schema before any row filter.
Reusing a type at another field does not add a column. Field routes do not
define column identity.
A scope is a module root or a collection item. When one scope reaches a
collection type by two or more routes, each key coordinate of that type in that
scope is the route `fields` joined with `.`, then `.`, then the key. For
example, `before` and `after` with key `unit` give `before.unit` and
`after.unit`. `NewSchema` decides this from the complete schema. A type with
one route in its scope keeps the key unchanged. `Schema.Routes` and `RoutedKey`
give callers the same result.
Each `Collection.fields` lists the fields since the previous collection item,
or since the module root for the first boundary. For a Compose service:

```json
{
  "module": "docker",
  "type": "Check",
  "localPath": ["healthCheck", "run"],
  "collections": [
    {"fields": ["compose", "projects"], "type": "DockerComposeProjects", "itemType": "DockerComposeProject"},
    {"fields": ["services"], "type": "DockerComposeServices", "itemType": "DockerComposeService"}
  ],
  "items": [["e2e", "web"], ["e2e", "worker"]]
}
```

Each item supplies one key per collection boundary. `items: [[]]` supplies one
row with no collection keys. An omitted or empty `items` list supplies no rows.
Such a field still contributes dimensions. Use it for an empty collection.

| Dimension | Coordinate |
| --- | --- |
| `module` | Configured module name, when present |
| Exact collection type name | Collection item key, with a route prefix on a route conflict |
| `name` | Full local path joined with `.`, or `.` for the local root |

The name is the field path from the nearest parent that is a collection item
or the module root. It is not the path through the whole schema.
The service item itself has `name=.`. Its check has `name=healthCheck.run`.
A function on the services collection has only the project item boundary.
For example, `name=services.all.up` has a project key and no service key.
Ordinary object fields remain in the name. Thus `python310.unit` and
`python310.slow` remain distinct. Artifact types do not add dimensions.

A dash marks an absent coordinate. Empty keys, literal dashes, and control
characters are quoted. Unknown JSON fields, trailing JSON, malformed rows, and
duplicate coordinates for the same type cause an error before any output.
Different types can have the same coordinates.

To select an exact name while keeping the complete dimension table:

```sh
go run ./core/artifact/flattening/cmd/flatten -only healthCheck.run \
  < core/artifact/flattening/testdata/canonical-compose.json
```

Selection occurs after all rows are computed and checked. It does not change
dimension names or columns. Canonical output contains full names and coordinates.
It has no flag allocation, links, or display pruning.

`testdata/canonical-compose.json` covers a static field, nested service items,
a function on a collection, and an empty collection.
`testdata/canonical-dagger.json` covers real `dagger/dagger` fields: docs
`check` and `references.stale`, Python `python310.unit` and `python310.slow`,
a Go package generator and its `generate.stale` check, and Go test items.
`testdata/canonical-columns.json` shows the global label fallback for two
collection types with the same item type, and the route prefix for one
collection type at two fields.
The source of the Dagger fields is commit `7976c6f655aecf3d8de37326bfc09e01e666c98a`
(November 6, 2026). The matching `.txt` files contain exact expected output.

`CanonicalCoordinates` is the pure coordinate helper. It preserves supplied
collection coordinates and assigns module and name coordinates. Supply the
local path. `NewSchema` compiles the structural input chunks,
validates collection metadata, and assigns complete-schema column labels. This package
has no engine runtime, GraphQL, or CLI framework dependency.

## Compatibility

`legacy.go` keeps the old type-path selector rules. `New`, `Plan.Flatten`,
`Coordinates`, and `ArtifactDimensions` serve that compatibility path.
New callers use `NewSchema`.

Use `-legacy` to read the old utility input and display legacy coordinates.
See [legacy artifact selectors](LEGACY.md).

Directly nested distinct collection types have separate columns. For example,
`Rows.get -> Row.get -> Cell` has a `Rows` column named `row` and a `Row`
column named `cell`. Selecting two item keys for the same collection type on
one ancestry is rejected: one cell cannot hold both keys. Ordinary parent
references stop traversal at the repeated object type.
