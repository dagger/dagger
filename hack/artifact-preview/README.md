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

The input is an array of schema paths. Each entry has `Module`, `Path`, `Type`,
and `Collections`, as in `core/artifact/flattening/cmd/flatten`. `Description`
holds the field description. Each entry in `Items` has `Keys`: one observed key
per collection boundary. An item can also have `Description`. Use `LocalPath`
to preserve exact schema field spelling. This path starts at the nearest
collection item or module root.
Without `LocalPath`, the utility derives it from `Path` and `Collections`.

Keep the full schema, including paths with no items. Include every observed
item of each selected type. Link pruning uses this complete item set. Filters
apply after the flattening plan and address index are built. The utility supports list formats, module filters,
collection filters, and one path or DAG link. It rejects execution options.
Workspace command policies are outside this replay. Use items captured after
those policies when they change the selected set.

Convert saved API responses:

```sh
python3 hack/artifact-preview/capture.py \
  --schema schema.json --listing listing.json --output input.json
```

The schema response must contain `currentWorkspace.artifacts.dimensionDefinitions`
and `pathDefinitions`, plus `modules` with each object's field names and return
types. The listing response must contain `currentWorkspace.artifacts.__listing`.
The converter resolves `LocalPath` from these exact schema field names. It fails
if a name is missing or ambiguous. It also checks each observed path, type, and
collection route. `--types Check Generator Container GolangPackage` retains
items of those types and keeps all schema paths.

For an installed SDK provider, add `--sdk-module MODULE`. Repeat this option for
more providers. This selects the engine's `WorkspaceSDK.generate` field at that
module's root. Ordinary provider fields still use captured object metadata.
The converter also handles the engine's injected `Generator.stale` field.

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
