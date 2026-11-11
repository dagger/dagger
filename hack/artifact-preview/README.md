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
per collection boundary. An item can also have `Description`.

Keep the full schema, including paths with no items. Filters apply after the
flattening plan is built. The utility supports list formats, module filters,
collection filters, and one path or DAG link. It rejects execution options.
Workspace command policies are outside this replay. Use items captured after
those policies when they change the selected set.

Convert saved API responses:

```sh
python3 hack/artifact-preview/capture.py \
  --schema schema.json --listing listing.json --output input.json
```

The schema response must contain `currentWorkspace.artifacts.dimensionDefinitions`
and `pathDefinitions`. The listing response must contain
`currentWorkspace.artifacts.__listing`. The converter checks every observed
path, type, and collection route against the schema. `--types Check Generator
Container GolangPackage` retains items of those types and keeps all schema paths.

On macOS, the CLI imports Linux code. Build a Linux binary and run it in Docker:

```sh
GOOS=linux CGO_ENABLED=0 go build -o /tmp/artifact-preview ./hack/artifact-preview
docker run --rm -i -v /tmp/artifact-preview:/preview:ro alpine:3.22 \
  /preview - check -l < input.json
```

This shows current behavior. It does not add the proposed `name` column or
selection-based column pruning to the CLI.
