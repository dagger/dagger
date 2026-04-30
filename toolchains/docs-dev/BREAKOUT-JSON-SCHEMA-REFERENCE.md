# Break Out JSON Schema Reference Generation

## Summary

Move JSON schema reference generation out of `toolchains/docs-dev` and into source-adjacent Go generation.

The source of truth for these schemas is Go types, so `//go:generate` is the right declaration point.

## Current State

Today:

```text
toolchains/docs-dev.References()
  -> EngineDev.ConfigSchema("dagger.json")
    -> go run ./cmd/json-schema dagger.json
    -> docs/static/reference/dagger.schema.json
```

`cmd/json-schema` can generate two schemas:

```text
dagger.json -> core/modules.ModuleConfigWithUserFields
engine.json -> engine/config.Config
```

but `docs-dev.References()` currently writes `dagger.schema.json` twice and does not regenerate `engine.schema.json`.

## Proposal

Make `cmd/json-schema` a normal Go generator and declare schema outputs beside the Go types that define them.

Add explicit output support:

```text
go run ./cmd/json-schema dagger.json -o docs/static/reference/dagger.schema.json
go run ./cmd/json-schema engine.json -o docs/static/reference/engine.schema.json
```

Then add source-adjacent declarations:

```text
core/modules/generate.go
engine/config/generate.go
```

Example:

```go
package modules

//go:generate go run ../../cmd/json-schema dagger.json -o ../../docs/static/reference/dagger.schema.json
```

```go
package config

//go:generate go run ../../cmd/json-schema engine.json -o ../../docs/static/reference/engine.schema.json
```

## Why `go:generate`

The schemas are derived from Go source:

```text
Go struct fields
json tags
jsonschema tags
Go comments
custom JSONSchema() methods
```

The schema generator already reflects concrete Go values:

```text
core/modules.ModuleConfigWithUserFields
engine/config.Config
```

So the package that owns each type should also declare how its public JSON schema is regenerated.

## Dagger Flow

After this breakout:

```text
dagger generate
git add .
git commit -s -m "re-generate JSON schemas"
```

`toolchains/docs-dev.References()` should stop calling `EngineDev.ConfigSchema`. The generated JSON schema files become regular generated repo artifacts, updated through the workspace generator flow.

## Migration

1. Add `-o/--output` to `cmd/json-schema`.
2. Add `core/modules/generate.go` for `dagger.schema.json`.
3. Add `engine/config/generate.go` for `engine.schema.json`.
4. Remove JSON schema generation from `toolchains/docs-dev.References()`.
5. Delete or simplify `EngineDev.ConfigSchema` if no other workflow needs it.
6. Fix the current duplicate `dagger.schema.json` write in `docs-dev`.

## Non-Goals

- Do not create a generic JSON-schema module just to copy files.
- Do not keep schema generation in `docs-dev`; the source of truth is not the docs tree.
- Do not use shell redirection in `//go:generate`; make the generator accept `-o`.
- Do not infer arbitrary schemas from arbitrary Go packages in this breakout.
