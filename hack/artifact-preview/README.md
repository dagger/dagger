# Artifact list preview

Replay saved workspace data without an engine. The utility calls the current
flattening code, link pruning code, and CLI renderer. It does not evaluate leaf
artifacts.

On Linux:

```sh
go run ./hack/artifact-preview input.json check -l
go run ./hack/artifact-preview input.json generate -la -f=link
go run ./hack/artifact-preview input.json list containers
go run ./hack/artifact-preview input.json list golang-packages -a -f=cli
```

The CLI capture format is an array of schema paths. Each entry has `Module`,
`Path`, `Type`, `Collections`, and `LocalPath`. `Path` holds the CLI field path.
`LocalPath` holds literal schema field names from the nearest collection item
or exposed module root. Use `[]` for that root. Missing or null `LocalPath`
values are rejected. The utility does not derive schema names from CLI paths.
`Description` holds the field description. Each entry in `Items` has `Keys`:
one observed key per collection boundary. An item can also have `Description`.

Canonical output names contain the full field path from the module root.
The converter keeps `LocalPath` relative to its enclosing collection item.
The schema compiler joins each collection's `Fields` with `LocalPath` to form
the output name. For example, a collection at `before` with local path `check`
has the name `before.check`. No synthetic `get` operation is added.

Collection labels use the item type name. If two collection types share that
name, both labels use their collection type names. For example, collection
types `Packages` and `Dependencies`, both with item type `Package`, use labels
`packages` and `dependencies`. Labels are assigned from the complete schema.
A row filter cannot change them. An unresolved label conflict causes an error.

This capture format differs from the native input for
`core/artifact/flattening/cmd/flatten`. That input uses `flattening.Field`:
`module`, `type`, `localPath`, and `collections`, plus example `items` as arrays
of keys. It has no CLI `Path` or item description.

Keep the full schema, including paths with no items. Include every observed
item of each selected type. Link pruning uses this complete item set. Filters
apply after the canonical schema and address index are built. The utility
supports list formats, module filters, collection filters, and one path or DAG
link. It rejects execution options.
Workspace command policies are outside this replay. Use items captured after
those policies when they change the selected set.

Convert saved API responses:

```sh
python3 hack/artifact-preview/capture.py \
  --schema schema.json --listing listing.json --output input.json
```

CLI replay is the default output format. To use the native flattening utility:

```sh
python3 hack/artifact-preview/capture.py \
  --schema schema.json --listing listing.json \
  --format flattening --output flattening.json && \
go run ./core/artifact/flattening/cmd/flatten < flattening.json
```

The native format keeps all schema fields. It uses the same resolved schema names
and collection keys as CLI replay. `--types` selects items in both formats.
Empty collections remain in the schema.

Capture schema metadata with this query. Save the response as `schema.json`:

```graphql
{
  currentWorkspace {
    artifacts {
      dimensionDefinitions {
        identifier
        kind
        name
        collectionType
        itemType
      }
      __listing(schema: true, typeAssertion: true)
    }
  }
}
```

Capture observed items in a separate response, saved as `listing.json`:

```graphql
{
  currentWorkspace {
    artifacts {
      __listing(typeAssertion: true)
    }
  }
}
```

Schema listing rows include `projection`: the engine's exact field metadata.
The converter uses its module, type, local path, and collection boundaries.
Collection identifiers are exact schema type names. Collection names remain
human-readable aliases. The converter uses raw `dimensionKeys` from observed
rows. It does not reconstruct collection routes or keys from those aliases.
The source `path` supplies the CLI path. A path-form URI is the fallback.

The converter also accepts older captures with `pathDefinitions` and `modules`
containing each object's field names and return types. For those captures, it
resolves `LocalPath` from exact schema field names and checks each collection
route. Missing or ambiguous names cause an error. Modern collection type IDs
require schema projection metadata. Missing projection metadata causes an error.

`--types Check Generator Container GolangPackage` retains items of those types
and keeps all schema fields, including empty collections.

For an older capture with an installed SDK provider, add `--sdk-module MODULE`.
Repeat this option for more providers. This selects the engine's
`WorkspaceSDK.generate` field at that module's root. Ordinary provider fields
still use captured object metadata. The old capture adapter also handles the
engine's injected `Generator.stale` field.

On macOS, the CLI imports Linux code. Build a Linux binary and run it in Docker:

```sh
GOOS=linux CGO_ENABLED=0 go build -o /tmp/artifact-preview ./hack/artifact-preview
docker run --rm -i -v /tmp/artifact-preview:/preview:ro alpine:3.22 \
  /preview - check -l < input.json
```

The utility uses the same canonical coordinates, column pruning, and table
renderer as the CLI. Tables keep columns whose values differ in the selected
rows. Each row ends with its DAG link. Collection listings expand items by
default. CLI format emits complete commands.
