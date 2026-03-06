# Replace `dagger develop` with `dagger generate` — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the special `dagger develop` command with the standard `dagger generate` by giving each module its own dev workspace that references an SDK generate module.

**Architecture:** Every module gets a `.dagger/config.toml` (dev workspace) alongside its `dagger.json`. The SDK's codegen becomes a `+generate` function in a regular Dang module, invoked through standard workspace generator machinery. Dang is the bootstrap foundation (baked into engine, needs no codegen). The Go SDK generate module is written in Dang.

**Tech Stack:** Go (engine/CLI), Dang (SDK generate modules), TOML (workspace config)

**Design Doc:** `hack/designs/module-develop-to-generate.md`

---

## Dependency Graph

```
Phase 1: Go SDK Generate Module (Dang)
  Task 1: Create module structure
  Task 2: Implement +generate function (codegen)
  Task 3: Implement first-time scaffolding (templates)
  Task 4: Integration tests
     |
Phase 2: Update `dagger module init`
  Task 5: Create .dagger/config.toml during module init
  Task 6: Bootstrap via dagger generate after init
  Task 7: Tests for new init flow
     |
Phase 3: Migration
  Task 8: Extend dagger migrate
  Task 9: Migration tests
     |
Phase 4: Remove `dagger develop` & Cleanup
  Task 10: Remove develop command
  Task 11: Remove CodeGenerator/ClientGenerator interfaces
  Task 12: Remove SkipWorkspaceModules path
  Task 13: Final cleanup & test updates
     |
Phase 5: Other SDK Generate Modules (future)
  Task 14: Python SDK generate module
  Task 15: TypeScript SDK generate module
```

---

## Phase 1: Go SDK Generate Module

The critical new artifact. A Dang module that wraps the existing `cmd/codegen` binary and
exposes a `+generate` function. This follows the exact pattern already used by
`toolchains/go-sdk-dev/main.dang` (which has a `@generate` function), but generalized
for any Go module.

### Task 1: Create Go SDK Generate Module Structure

**Context:** The pattern already exists in `toolchains/go-sdk-dev/` — a Dang module with a
`@generate` function. The `cmd/codegen` module (also Dang-based) builds the codegen binary.
We need a new module that combines these: it builds the codegen binary and exposes a
`+generate` function for general Go module development.

**Files:**
- Create: `sdk/go/dev/dagger.json`
- Create: `sdk/go/dev/.dagger/config.toml`
- Create: `sdk/go/dev/main.dang`

**Step 1: Create the module directory**

```bash
mkdir -p sdk/go/dev/.dagger
```

**Step 2: Create `dagger.json`**

The module uses the Dang SDK for its runtime. It depends on the codegen module (for the
binary) and the go toolchain (for `go mod tidy` etc).

```json
{
  "name": "go-dev",
  "engineVersion": "v0.20.1",
  "sdk": {
    "source": "github.com/vito/dang/dagger-sdk@<current-dang-commit>"
  },
  "dependencies": [
    {
      "name": "codegen",
      "source": "../../../cmd/codegen"
    },
    {
      "name": "go",
      "source": "../../../toolchains/go"
    }
  ]
}
```

Use the same Dang SDK commit hash as other toolchains (currently
`2de20f19b971dad3ee6038e6728736ef1f9a056b`, check `toolchains/go-sdk-dev/dagger.json`).

**Step 3: Create empty `.dagger/config.toml`**

```toml
# Go SDK development module workspace
```

**Step 4: Create initial `main.dang` skeleton**

```dang
pub description = "Generate Go module code for Dagger"

type GoDev {
  pub source: Directory! @defaultPath(path: "/")
}
```

**Step 5: Verify the module loads**

```bash
cd sdk/go/dev && dagger functions
```

Expected: Module loads, shows the `source` field.

**Step 6: Commit**

```bash
git add sdk/go/dev/
git commit -s -m "sdk/go/dev: create Go SDK generate module skeleton"
```

### Task 2: Implement the `+generate` Function (Codegen)

**Context:** The current Go SDK codegen flow is in `core/sdk/go_sdk.go:468-638`
(`baseWithCodegen`). It:
1. Gets the base container with the codegen binary
2. Mounts introspection JSON and module source
3. Runs `codegen generate-module` with flags
4. Extracts generated files

The Dang module needs to replicate this flow. The codegen binary comes from the `codegen`
dependency module. Introspection JSON comes from the Dagger API.

**Files:**
- Modify: `sdk/go/dev/main.dang`

**Step 1: Study how introspection works**

The codegen binary needs an introspection JSON file. Currently this is obtained via
`deps.SchemaIntrospectionJSONFileForModule(ctx)` in Go. In the new model, the generate
module needs to call the Dagger API to get this. Study how existing Dang modules access
the API — look at `toolchains/go-sdk-dev/main.dang` for patterns.

Note: The generate function operates on the workspace source files. It reads `dagger.json`
from the workspace root to get module name, dependencies, etc. It needs to figure out
how to obtain the introspection schema for those dependencies.

**Step 2: Implement the generate function**

The `+generate` function should:
1. Build the codegen binary from the `codegen` dependency
2. Read `dagger.json` from the workspace source to get module metadata
3. Create a container with the codegen binary
4. Mount the module source and introspection JSON
5. Run `codegen generate-module`
6. Diff the output against the original source
7. Return a `Changeset`

```dang
type GoDev {
  pub source: Directory! @defaultPath(path: "/")

  """
  Generate Go bindings and SDK code for a Dagger module
  """
  pub generate: Changeset! @generate {
    # Build codegen binary from dependency
    let codegenBin = codegen(source).binary

    # Create generation container
    let ctr = go(source)
      .env
      .withMountedFile("/usr/local/bin/codegen", codegenBin)
      .withWorkdir("/src")
      .withMountedDirectory("/src", source)
      # TODO: mount introspection JSON
      # TODO: run codegen generate-module with appropriate flags
      .withExec(["codegen", "generate-module",
        "--output", "/src",
        "--module-source-path", "/src",
        "--module-name", "TODO",  # read from dagger.json
        "--introspection-json-path", "/introspection.json",
      ])

    # Return changeset: diff between generated and original
    ctr.directory("/src").changes(source)
  }
}
```

**Important:** This is a sketch. The exact implementation depends on:
- How to pass introspection JSON to the container (the Dagger API must provide this)
- How to read module name from `dagger.json` (parse it in Dang or pass as config)
- Whether the codegen binary needs additional flags (like `--is-init`, `--lib-version`)

Study `core/sdk/go_sdk.go:512-528` for the full list of codegen arguments.

**Step 3: Handle `--is-init` (first-time detection)**

The codegen binary accepts `--is-init` when there's no existing `dagger.json` (i.e.,
`ConfigExists` is false). The generate function should detect this and pass the flag.

**Step 4: Handle `.gitattributes` and `.gitignore` updates**

The current codegen adds VCS paths to `.gitattributes` (generated files) and `.gitignore`
(ignored files). See `core/schema/modulesource.go:2233-2315`. This should be part of the
changeset returned by the generate function.

**Step 5: Test manually**

Create a test Go module, configure it with the new generate module in its
`.dagger/config.toml`, and run `dagger generate`.

**Step 6: Commit**

```bash
git add sdk/go/dev/main.dang
git commit -s -m "sdk/go/dev: implement +generate function for Go module codegen"
```

### Task 3: Implement First-Time Scaffolding (Templates)

**Context:** Currently `dagger init` generates starter template code (e.g., `main.go` with
a hello-world function). In the new model, the `+generate` function handles this: if it
detects no user source code exists, it generates the boilerplate too.

**Files:**
- Modify: `sdk/go/dev/main.dang`

**Step 1: Add template detection**

The generate function should check if `main.go` (or equivalent) exists in the source.
If not, generate the starter template as part of the changeset.

The current template generation logic is in `cmd/codegen/` — the `generate-module`
command already handles `--is-init` by generating template files. So this may already
work if we pass the right flag.

**Step 2: Verify template generation works end-to-end**

1. Create a directory with only `dagger.json` and `.dagger/config.toml`
2. Run `dagger generate`
3. Verify that both template code (main.go) and generated code (dagger.gen.go) appear

**Step 3: Commit**

```bash
git add sdk/go/dev/
git commit -s -m "sdk/go/dev: handle first-time scaffolding via +generate"
```

### Task 4: Integration Tests

**Context:** Generator tests live in `core/integration/generators_test.go` with test data
in `core/integration/testdata/generators/`. Follow the existing pattern.

**Files:**
- Create: `core/integration/testdata/generators/go-module-dev/` (test fixture)
- Modify: `core/integration/generators_test.go`

**Step 1: Create test fixture — a Go module with dev workspace**

```
core/integration/testdata/generators/go-module-dev/
  dagger.json              # sdk.source = "go"
  .dagger/
    config.toml            # references the Go SDK generate module
  main.go                  # simple module code
```

**Step 2: Write test — generators are discovered from dev workspace**

```go
func (GeneratorsSuite) TestGeneratorsGoModuleDev(ctx context.Context, t *testctx.T) {
    c := connect(ctx, t)
    // Set up the test environment with the go-module-dev fixture
    env, err := generatorsTestEnv(t, c, "go-module-dev")
    require.NoError(t, err)

    // Verify that the SDK generate module's +generate function is discovered
    out, err := env.WithExec([]string{"dagger", "generate", "-l"}).Stdout(ctx)
    require.NoError(t, err)
    require.Contains(t, out, "generate")

    // Run generation and verify output
    _, err = env.WithExec([]string{"dagger", "generate", "-y"}).Sync(ctx)
    require.NoError(t, err)
}
```

**Step 3: Write test — first-time scaffolding via generate**

Test that running `dagger generate` on a module with no source code produces the
template files.

**Step 4: Run tests**

```bash
go test -v -run TestGeneratorsGoModuleDev ./core/integration/
```

**Step 5: Commit**

```bash
git add core/integration/
git commit -s -m "test: add integration tests for Go SDK generate module"
```

---

## Phase 2: Update `dagger module init`

### Task 5: Create `.dagger/config.toml` During Module Init

**Context:** `dagger module init` is implemented in `cmd/dagger/module.go:189-321`. It
currently creates `dagger.json` and runs codegen via the old develop path. We need it to
also create `.dagger/config.toml` referencing the SDK generate module.

**Files:**
- Modify: `cmd/dagger/module.go` (moduleInitCmd, around lines 248-321)
- Modify: `core/schema/workspace.go` (moduleInit function)

**Step 1: Update the engine-side `moduleInit` to create dev workspace**

In `core/schema/workspace.go`, the `moduleInit` function creates the module. Extend it
to also create `.dagger/config.toml` inside the module directory, referencing the
appropriate SDK generate module based on the `--sdk` flag.

SDK to generate module mapping:
- `go` → `github.com/dagger/dagger/sdk/go/dev`
- `python` → Python SDK generate module (future, placeholder)
- `typescript` → TypeScript SDK generate module (future, placeholder)

**Step 2: Verify init creates both files**

```bash
mkdir test-module && cd test-module
dagger module init --sdk go my-module
ls -la my-module/dagger.json my-module/.dagger/config.toml
```

Expected: Both files exist. `config.toml` references the Go SDK generate module.

**Step 3: Commit**

```bash
git add cmd/dagger/ core/schema/
git commit -s -m "module init: create dev workspace (.dagger/config.toml) alongside dagger.json"
```

### Task 6: Bootstrap via `dagger generate` After Init

**Context:** Currently init runs codegen via the old develop path. Change it to run
`dagger generate` instead, which will use the new dev workspace.

**Files:**
- Modify: `cmd/dagger/module.go` (moduleInitCmd)
- Modify: `core/schema/workspace.go` (moduleInit)

**Step 1: Replace codegen call with generate call**

Instead of calling `modSrc.GeneratedContextDirectory().Export()`, the init command should:
1. Create `dagger.json` and `.dagger/config.toml`
2. Run `dagger generate` (which discovers the dev workspace and runs the SDK's +generate)

This may mean the init command creates the minimal files and then delegates to generate,
or the engine-side `moduleInit` does both steps internally.

**Step 2: Verify end-to-end**

```bash
mkdir test-module && cd test-module
dagger module init --sdk go my-module
cat my-module/main.go       # Should have template code
cat my-module/dagger.gen.go  # Should have generated bindings
```

**Step 3: Commit**

```bash
git add cmd/dagger/ core/schema/
git commit -s -m "module init: bootstrap via dagger generate instead of old codegen path"
```

### Task 7: Tests for New Init Flow

**Files:**
- Modify: `core/integration/module_test.go` (or relevant init test file)

**Step 1: Find existing init tests**

```bash
grep -rn "TestModule.*Init\|moduleInit" core/integration/ --include="*.go" -l
```

**Step 2: Update tests to verify new init creates dev workspace**

Add assertions that:
- `.dagger/config.toml` is created alongside `dagger.json`
- `config.toml` references the correct SDK generate module
- Running `dagger generate` after init works

**Step 3: Run tests**

```bash
go test -v -run TestModule.*Init ./core/integration/
```

**Step 4: Commit**

```bash
git add core/integration/
git commit -s -m "test: verify module init creates dev workspace"
```

---

## Phase 3: Migration

### Task 8: Extend `dagger migrate`

**Context:** Migration logic lives in `core/workspace/migrate.go:88-277` (`Migrate()`
function). It already handles toolchain-to-workspace and blueprint-to-workspace migrations.
Add a new migration path for modules with `sdk.source` but no `.dagger/config.toml`.

**Files:**
- Modify: `core/workspace/migrate.go`

**Step 1: Add SDK-to-workspace migration detection**

In `Migrate()`, after existing migration checks, add detection for:
- `dagger.json` exists with `sdk.source` field
- No `.dagger/config.toml` exists (or `.dagger/` doesn't exist)

**Step 2: Implement migration logic**

When detected:
1. Create `.dagger/` directory inside the module
2. Create `config.toml` referencing the SDK generate module:
   - `sdk.source = "go"` → `github.com/dagger/dagger/sdk/go/dev`
   - `sdk.source = "python"` → Python SDK generate module reference
   - `sdk.source = "typescript"` → TypeScript SDK generate module reference
   - Custom SDK → emit warning, user must configure manually
3. `dagger.json` remains unchanged

**Step 3: Test migration on a real module**

```bash
# Create old-style module
mkdir test-migrate && cd test-migrate
cat > dagger.json << 'EOF'
{"name": "test", "sdk": {"source": "go"}, "engineVersion": "v0.20.1"}
EOF
# No .dagger/ exists

dagger migrate
ls .dagger/config.toml  # Should exist now
cat .dagger/config.toml # Should reference Go SDK generate module
```

**Step 4: Commit**

```bash
git add core/workspace/migrate.go
git commit -s -m "migrate: create dev workspace for modules with sdk.source"
```

### Task 9: Migration Tests

**Files:**
- Modify: `core/integration/workspace_test.go` (or migration test file)

**Step 1: Find existing migration tests**

```bash
grep -rn "TestMigrat" core/integration/ --include="*.go" -l
```

**Step 2: Add test cases**

- Module with `sdk.source = "go"` and no `.dagger/` → migration creates dev workspace
- Module with `sdk.source = "python"` → correct generate module reference
- Module that already has `.dagger/config.toml` → no migration needed (idempotent)
- Module with custom SDK source → warning emitted

**Step 3: Run tests**

```bash
go test -v -run TestMigrat ./core/integration/
```

**Step 4: Commit**

```bash
git add core/integration/
git commit -s -m "test: verify migration creates dev workspace for existing modules"
```

---

## Phase 4: Remove `dagger develop` & Cleanup

### Task 10: Remove the Develop Command

**Files:**
- Modify: `cmd/dagger/module.go` — remove `moduleDevelopCmd` (lines 504-684) and
  `collectLocalModulesRecursive` (lines 686-721)
- Modify: `cmd/dagger/module.go` — remove develop command registration from parent command

**Step 1: Identify all references to the develop command**

```bash
grep -rn "develop\|moduleDevelopCmd" cmd/dagger/ --include="*.go"
```

**Step 2: Remove the command**

Delete `moduleDevelopCmd` variable, its `init()` registration, and the `RunE` function.
Delete `collectLocalModulesRecursive` helper.

**Step 3: Verify CLI builds**

```bash
go build ./cmd/dagger/
dagger develop  # Should error: unknown command
dagger generate # Should still work
```

**Step 4: Commit**

```bash
git add cmd/dagger/
git commit -s -m "cli: remove dagger develop command (replaced by dagger generate)"
```

### Task 11: Remove CodeGenerator/ClientGenerator Interfaces

**Context:** With codegen now handled by `+generate` functions in workspace modules, the
engine no longer needs the `CodeGenerator` and `ClientGenerator` interfaces.

**Files:**
- Modify: `core/sdk.go` — remove `CodeGenerator` (lines 93-124) and `ClientGenerator`
  (lines 20-79) interfaces
- Modify: `core/sdk.go` — remove `AsCodeGenerator()` and `AsClientGenerator()` from
  `SDK` interface (lines 226, 229)
- Delete: `core/sdk/module_code_generator.go`
- Delete: `core/sdk/module_client_generator.go`
- Modify: `core/sdk/module.go` — remove `AsCodeGenerator()` and `AsClientGenerator()`
  methods and related adapter structs
- Modify: `core/sdk/go_sdk.go` — remove `Codegen()` method (lines 179-225) and
  `GenerateClient()` method (lines 67-177)
- Modify: `core/sdk/consts.go` — remove `"codegen"`, `"requiredClientGenerationFiles"`,
  `"generateClient"` from `sdkFunctions`
- Modify: `core/schema/modulesource.go` — remove `runCodegen()` (lines 2170-2318),
  `runClientGenerator()` (lines 2320-2429), `runGeneratedContext()` (lines 2434-2493),
  and the `generatedContextDirectory`/`generatedContextChangeset` resolvers
  (lines 2495-2554)
- Modify: `core/codegen.go` — remove or repurpose `GeneratedCode` struct if no longer used

**Step 1: Identify all callers**

```bash
grep -rn "CodeGenerator\|ClientGenerator\|AsCodeGenerator\|AsClientGenerator\|runCodegen\|runClientGenerator\|GeneratedContext" core/ --include="*.go"
```

**Step 2: Remove interfaces and implementations bottom-up**

Start with the module wrapper implementations, then the interface definitions, then the
callers in schema resolvers.

**Step 3: Verify engine builds**

```bash
go build ./...
```

**Step 4: Commit**

```bash
git add core/
git commit -s -m "core: remove CodeGenerator and ClientGenerator interfaces (replaced by +generate)"
```

### Task 12: Remove SkipWorkspaceModules Path

**Context:** `SkipWorkspaceModules` exists solely for `dagger develop` to bypass workspace
loading. With develop removed, this flag and its code path are unnecessary.

**Files:**
- Modify: `engine/opts.go` — remove `SkipWorkspaceModules` field (line 119-120)
- Modify: `engine/client/client.go` — remove `SkipWorkspaceModules` from Params (line 128)
  and from `buildMetadata()` (lines 1411-1415)
- Modify: `engine/server/session.go` — remove `SkipWorkspaceModules` checks (search for
  all occurrences)

**Step 1: Find all references**

```bash
grep -rn "SkipWorkspaceModules" . --include="*.go"
```

**Step 2: Remove flag, propagation, and checks**

**Step 3: Verify builds and tests**

```bash
go build ./...
go test ./engine/... -count=1
```

**Step 4: Commit**

```bash
git add engine/
git commit -s -m "engine: remove SkipWorkspaceModules (no longer needed without develop command)"
```

### Task 13: Final Cleanup & Test Updates

**Files:**
- Various test files that reference `dagger develop`
- Documentation that mentions `dagger develop`

**Step 1: Find all remaining references to develop**

```bash
grep -rn "dagger develop\|dagger-develop\|moduleDevelop" . --include="*.go" --include="*.md"
```

**Step 2: Update tests**

Tests that used `dagger develop` should be converted to use `dagger generate` with
the appropriate dev workspace setup.

**Step 3: Update documentation**

Search for references to `dagger develop` in docs and update them.

**Step 4: Run full test suite**

```bash
go test ./... -count=1
```

**Step 5: Commit**

```bash
git add .
git commit -s -m "cleanup: update tests and docs for develop-to-generate transition"
```

---

## Phase 5: Other SDK Generate Modules (Future)

These tasks are outlined for completeness but are separate work items that can happen
after Phases 1-4 are stable.

### Task 14: Python SDK Generate Module

Create `sdk/python/dev/` as a Dang module with a `+generate` function that wraps the
Python SDK's codegen logic. Follow the same pattern as the Go SDK generate module.

### Task 15: TypeScript SDK Generate Module

Create `sdk/typescript/dev/` as a Dang module with a `+generate` function that wraps
the TypeScript SDK's codegen logic. Follow the same pattern as the Go SDK generate module.

---

## Key Reference Files

| Component | Path | Lines |
|-----------|------|-------|
| Current develop command | `cmd/dagger/module.go` | 504-684 |
| Current module init | `cmd/dagger/module.go` | 189-321 |
| Generate command | `cmd/dagger/generators.go` | 31-153 |
| SDK interfaces | `core/sdk.go` | 1-231 |
| Go SDK (special) | `core/sdk/go_sdk.go` | 38-789 |
| SDK loader | `core/sdk/loader.go` | 35-204 |
| Module SDK wrapper | `core/sdk/module.go` | 1-249 |
| CodeGenerator impl | `core/sdk/module_code_generator.go` | 1-54 |
| ClientGenerator impl | `core/sdk/module_client_generator.go` | 1-88 |
| Generators | `core/generators.go` | 1-151 |
| Module tree | `core/modtree.go` | 1-624 |
| Workspace config | `core/workspace/config.go` | 1-518 |
| Workspace detect | `core/workspace/detect.go` | 1-125 |
| Migration | `core/workspace/migrate.go` | 1-640 |
| Session module loading | `engine/server/session.go` | 1905-2080 |
| Workspace schema | `core/schema/workspace.go` | 1-1073+ |
| ModuleSource codegen | `core/schema/modulesource.go` | 2170-2554 |
| Existing Go SDK dev | `toolchains/go-sdk-dev/main.dang` | 1-136 |
| Codegen binary module | `cmd/codegen/.dagger/main.dang` | 1-16 |
| Generator test data | `core/integration/testdata/generators/` | — |
| Generator tests | `core/integration/generators_test.go` | 1-223 |

## Open Questions to Resolve During Implementation

1. **Introspection JSON access from Dang:** How does the `+generate` function in Dang
   obtain the schema introspection JSON needed by the codegen binary? This may require
   a new Dagger API endpoint or the generate function may need to call the introspection
   query directly.

2. **Module metadata from workspace:** The generate function needs to read `dagger.json`
   to get the module name, dependencies, etc. In Dang, this likely means reading the file
   from the workspace source directory and parsing it, or receiving it as config.

3. **SDK generate module versioning:** When `dagger module init --sdk go` references the
   Go SDK generate module in `config.toml`, what version/ref should it use? Should it pin
   to the current engine version? Use a `@main` float?

4. **Recursive develop:** The current `dagger develop --recursive` develops all local
   dependencies. In the new model, each dependency is its own module with its own workspace.
   Should `dagger generate` recurse into subdirectories, or should users run it in each
   module separately?

5. **Go SDK runtime path:** The Go SDK runtime (`core/sdk/go_sdk.go` `Runtime()` method)
   stays baked into the engine for now. But with `Codegen()` removed, `goSDK` only
   implements `Runtime` and `ModuleTypes`. Verify this doesn't break the SDK loader
   (`namedSDK()` still works with a reduced interface).
