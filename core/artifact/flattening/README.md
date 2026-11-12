# Artifact flattening

Run the canonical utility from the repository root:

```sh
go run ./core/artifact/flattening/cmd/flatten < core/artifact/flattening/testdata/canonical-compose.json
go test ./core/artifact/flattening ./core/artifact/flattening/cmd/flatten
```

These commands use native Go. They do not start an engine.

The package maps schema fields and collection keys to dimensions and coordinates.
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
is the module's exposed root. An empty local path names the root itself with `.`.
Do not include a module prefix or synthetic `get` operations. The utility does
not derive this path from a legacy `Artifact.Path`.

`collections` lists the collection boundaries from parent to child. Each entry
requires its exact schema `type` and `itemType`. One collection type defines one
column, with its item type name in kebab case. Reusing a type at another field
does not add a column. Field routes do not define column identity.
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
| Exact collection type name | Collection item key |
| `name` | Full local path joined with `.`, or `.` for the local root |

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
The source is commit `7976c6f655aecf3d8de37326bfc09e01e666c98a`
(November 6, 2026). The matching `.txt` files contain exact expected output.

`CanonicalCoordinates` is the pure coordinate helper. It preserves supplied
collection coordinates and assigns module and name coordinates. `NewSchema`
adds schema validation, collection metadata, and row validation. This package
has no engine runtime, GraphQL, or CLI framework dependency.

## Compatibility

`legacy.go` keeps the old type-path selector rules. `New`, `Plan.Flatten`,
`Coordinates`, and `ArtifactDimensions` serve that compatibility path.
New callers use `NewSchema`.

The old utility input is available with `-legacy`. Its fixtures remain unchanged.
See [legacy artifact selectors](LEGACY.md).

Directly nested distinct collection types have separate columns. For example,
`Rows.get -> Row.get -> Cell` has a `Rows` column named `row` and a `Row`
column named `cell`. Repeating the same type on one ancestry is rejected,
because one scalar cell cannot hold two keys for that type.
