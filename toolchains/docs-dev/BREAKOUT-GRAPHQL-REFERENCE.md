# Break Out GraphQL Reference Generation

## Summary

Generate the Engine GraphQL API reference by pointing SpectaQL at a live Dagger engine service.

This removes `docs/docs-graphql/schema.graphqls` from the public generation graph. The schema becomes SpectaQL's internal introspection result, not an explicit docs artifact.

## Current State

Today:

```text
toolchains/docs-dev.References()
  -> EngineDev.GraphqlSchema(version)
    -> docs/docs-graphql/schema.graphqls
  -> spectaql ./docs-graphql/config.yml -t ./static/api/reference/
    -> docs/static/api/reference/
```

The checked-in GraphQL schema is mainly an intermediate input to SpectaQL.

## Proposal

Introduce a reusable `github.com/dagger/spectaql` module that can render docs from a live GraphQL service:

```text
SpectaQL.RenderEndpoint(service, config, opts) -> Directory
```

The module owns:

```text
installing/running SpectaQL
binding the target service
setting the introspection URL
passing headers/secrets
writing the static output directory
```

The project owns:

```text
which service to document
which SpectaQL config/theme to use
where the generated output lands
```

## Checked-In Config

Keep the SpectaQL presentation config in git, because it is docs policy:

```text
docs/docs-graphql/spectaql.config.yml
docs/docs-graphql/custom-theme/
```

Example:

```yaml
spectaql:
  logoFile: ../website/static/img/dagger-logo-dark.png
  faviconFile: ../website/static/img/favicon.png
  themeDir: ./docs-graphql/custom-theme/

introspection:
  url: ${SPECTAQL_INTROSPECTION_URL}
  removeTrailingPeriodFromDescriptions: false
  fieldExpansionDepth: 1
  metadatas: true
  hideMutationsWithUndocumentedReturnType: false
  hideUnusedTypes: false
  inputValueDeprecation: true

extensions:
  graphqlScalarExamples: true

info:
  title: Dagger GraphQL API Reference
  docs-url: https://docs.dagger.io
  x-url: https://api.dagger.cloud/playgrounds
  x-hideIsDeprecated: false
  x-hideDeprecationReason: false
```

The service URL is injected at runtime:

```text
SPECTAQL_INTROSPECTION_URL=http://target:1234/query
```

## Dagger Flow

Desired dataflow:

```text
EngineDev.Service(version) -> Service
SpectaQL.RenderEndpoint(service, config/theme) -> Directory
docs/static/api/reference/
```

In `toolchains/docs-dev`, the generated reference becomes:

```go
engine := dag.EngineDev().Service(...)
apiRef := dag.Spectaql().RenderEndpoint(engine, config, opts)
src = src.WithDirectory("docs/static/api/reference", apiRef)
```

If workspace/module customization can reference another module's service output, this composition can move from Go glue to config. The important interface remains one edge:

```text
GraphQL Service -> SpectaQL
```

not:

```text
GraphQL Service -> schema file -> SpectaQL
```

## SpectaQL Requirements

SpectaQL supports live introspection through:

```yaml
introspection:
  url: http://target:1234/query
  headers:
    Header-Name: value
```

The reusable module should also support the CLI equivalents:

```text
--introspection-url
--headers
--target-dir
```

For Dagger engine services, the module or `engine-dev` may need to provide Dagger client metadata headers, or expose a wrapper service that can be introspected without callers knowing Dagger-specific headers.

## Migration

1. Add `github.com/dagger/spectaql` module with `RenderEndpoint`.
2. Rename `docs/docs-graphql/config.yml` to `docs/docs-graphql/spectaql.config.yml`.
3. Change the config from `schemaFile` to `${SPECTAQL_INTROSPECTION_URL}`.
4. Change `toolchains/docs-dev.References()` to call `EngineDev.Service` and `SpectaQL.RenderEndpoint`.
5. Stop generating `docs/docs-graphql/schema.graphqls`.
6. Remove `docs/docs-graphql/schema.graphqls` from git if no other workflow needs it.

## Non-Goals

- Do not create a Dagger-specific `EngineApiReference` module unless live-service composition proves insufficient.
- Do not keep `schema.graphqls` as a public dependency if it is only an intermediate.
- Do not hardcode a Dagger engine endpoint into the generic SpectaQL module.
- Do not rely on plain `config.yml` as a scan marker; prefer explicit config or `spectaql.config.yml`.
