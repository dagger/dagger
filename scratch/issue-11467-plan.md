# Issue 11467 Plan

## Outcome

Commit the generated Go module files for the exact repo scope already defined by the top-level `dagger.json` Go toolchain customization, so those modules build and lint from a clean checkout without relying on on-the-fly regeneration.

## Authoritative Scope

Use the root `dagger.json` entry:

- `toolchains[name=go].customizations[0].argument == "source"`
- `toolchains[name=go].customizations[0].ignore`

That ignore list is the source of truth for which local Go modules are in scope.

This means:

- Include local modules in the repo whose `sdk.source` is `go`
- Exclude anything filtered out by that ignore list
- Do not invent a second scope rule

In particular, the ignore list already excludes categories like:

- `docs/**`
- `**/sdk/runtime/**`
- `dagql/idtui/viztest/broken/**`
- `**/broken*/**`
- the explicitly named integration fixtures under `core/integration/testdata/...`
- `toolchains/release/testdata/module/`

## Target Modules

The current in-scope set is 73 Go-sdk modules:

### Root

- `.`

### Core

- `core/integration/llmtest/go-programmer`
- `core/integration/llmtest/go-programmer/toy-workspace`
- `core/integration/testdata/generators/hello-with-generators/toolchain`
- `core/integration/testdata/modules/go/defaults`
- `core/integration/testdata/modules/go/defaults/foobar`
- `core/integration/testdata/modules/go/defaults/super-dash-dash`
- `core/integration/testdata/modules/go/defaults/superconstructor`
- `core/integration/testdata/modules/go/ifaces`
- `core/integration/testdata/modules/go/ifaces/impl`
- `core/integration/testdata/modules/go/ifaces/test`
- `core/integration/testdata/modules/go/ifaces/test/dep`
- `core/integration/testdata/modules/go/namespacing`
- `core/integration/testdata/modules/go/namespacing/sub1`
- `core/integration/testdata/modules/go/namespacing/sub2`
- `core/integration/testdata/modules/typescript/ifaces`
- `core/integration/testdata/sdks/only-codegen`
- `core/integration/testdata/sdks/only-runtime`
- `core/integration/testdata/test-blueprint/hello`
- `core/integration/testdata/test-blueprint/hello-with-constructor`
- `core/integration/testdata/test-blueprint/hello-with-container`
- `core/integration/testdata/test-blueprint/hello-with-objects`
- `core/integration/testdata/test-blueprint/myblueprint-with-dep`

### Dagql

- `dagql/idtui/viztest`
- `dagql/idtui/viztest/dep`
- `dagql/idtui/viztest/dep/nested-dep`

### Modules

- `modules/alpine`
- `modules/claude`
- `modules/daggerverse`
- `modules/dev`
- `modules/doug`
- `modules/doug/evals`
- `modules/evals`
- `modules/evals/facts-workspace`
- `modules/evals/testdata/nested-context-middle`
- `modules/evals/testdata/nested-context-middle/nested-context-leaf`
- `modules/evaluator`
- `modules/evaluator/eval-workspace`
- `modules/evaluator/examples/go`
- `modules/gha`
- `modules/gha/examples/go`
- `modules/git-releaser`
- `modules/metrics`
- `modules/ruff`
- `modules/wolfi`

### SDK

- `sdk/elixir`
- `sdk/java`
- `sdk/php`
- `sdk/python/runtime`
- `sdk/typescript/runtime`

### Toolchains

- `toolchains/all-sdks`
- `toolchains/cli-dev`
- `toolchains/docs-dev`
- `toolchains/docs-dev/docusaurus`
- `toolchains/docs-dev/docusaurus/tests`
- `toolchains/engine-dev`
- `toolchains/engine-dev/notify`
- `toolchains/go`
- `toolchains/helm-dev`
- `toolchains/helm-dev/k3s`
- `toolchains/helm-dev/k3s/examples/go`
- `toolchains/php-sdk-dev`
- `toolchains/python-sdk-dev`
- `toolchains/python-sdk-dev/dockerd`
- `toolchains/release`
- `toolchains/release/apko`
- `toolchains/release/apko/tests`
- `toolchains/release/gh`
- `toolchains/release/gh/tests`
- `toolchains/release/registry-config`
- `toolchains/release/registry-config/tests`
- `toolchains/rust-sdk-dev`

### Version

- `version`

## Implementation

1. Use the target module list above as the concrete expansion of the root Go toolchain scope.

2. For each in-scope Go module:
   - Update the module-root `dagger.json` to set:

   ```json
   {
     "codegen": {
       "automaticGitignore": false
     }
   }
   ```

   - Edit the module source subpath `.gitignore` and remove the Go codegen ignore entries:
     - `/dagger.gen.go`
     - `/internal/dagger`
     - `/internal/querybuilder`
     - `/internal/telemetry`
   - Keep `.gitattributes` entries that mark generated files as `linguist-generated`.

3. Regenerate with existing codegen entrypoints only:
   - Run `dagger develop -m <module-root>` for each target module
   - Do not add a custom repo generator for this issue

4. Commit the resulting changes:
   - `dagger.json` updates
   - `.gitignore` cleanup
   - generated Go files under each module source subpath

## Verification

1. Re-run the same regeneration and confirm it is a no-op.

2. Run `dagger check generated` and confirm it passes.

3. In a fresh checkout or worktree, spot-check representative in-scope modules with native Go build/lint commands without first running `dagger develop`.

4. Do not use the current `toolchains/go` auto-regeneration fallback as proof of correctness; the committed tree itself must be sufficient.
