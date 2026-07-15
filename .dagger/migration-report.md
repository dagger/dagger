# Migration Report

Dagger migrated `dagger.json`, but some old settings need a manual check.

ACTION: Review each item below. If your project still relies on it, add the setting back manually.

Legacy config: `dagger.json`

## 1. `go` needs a manual check

Dagger could not migrate this setting automatically: constructor arg "source" has 'ignore', which workspace settings do not support

Original setting:

```json
{
  "argument": "source",
  "ignore": [
    "bin",
    ".git",
    "**/.git",
    "**/node_modules",
    "**/.venv",
    "**/__pycache__",
    "**/sdk/runtime/**",
    "docs/node_modules",
    "sdk/typescript/node_modules",
    "sdk/typescript/dist",
    "sdk/rust/examples/backend/target",
    "sdk/rust/target",
    "sdk/php/vendor",
    "docs/**",
    "dagql/idtui/viztest/broken/**",
    "**/broken*/**",
    "core/integration/testdata/checks/hello-with-checks/",
    "core/integration/testdata/generators/hello-with-generators/",
    "core/integration/testdata/services/hello-with-services/",
    "core/integration/testdata/services/port-collision/",
    "core/integration/testdata/services/partial-failure/",
    "core/integration/testdata/services/service-binding/",
    "toolchains/release/testdata/module/"
  ]
}
```

## 2. `security` needs a manual check

Dagger could not migrate this setting automatically: function setting "scanSource.source" is not supported in workspace config

Original setting:

```json
{
  "function": [
    "scanSource"
  ],
  "argument": "source",
  "ignore": [
    "bin",
    ".git",
    "docs",
    "sdk/rust/examples",
    "sdk/rust/crates/dagger-sdk/examples",
    "core/integration/testdata",
    "dagql/idtui/viztest"
  ]
}
```
