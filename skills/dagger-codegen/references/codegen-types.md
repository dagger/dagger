# The Four Codegen Types

> **Load when:** You need to understand what kind of codegen you're working on, or why similar code exists in multiple places.

## Quick Reference

| Type | Trigger | Output | Key File |
|------|---------|--------|----------|
| In-Module Bindings | `dagger develop` | `internal/dagger/dagger.gen.go` | `core/sdk.go:93` (CodeGenerator) |
| Runtime Dispatch | Module startup | `dagger.gen.go` (main pkg) | `modules.go:140` (moduleMainSrc) |
| SDK Libraries | `go generate` | `sdk/go/dagger.gen.go` | `sdk/go/generate.go` |
| Generated Clients | `dagger client install` | `dagger/dagger.gen.go` | `core/sdk.go:20` (ClientGenerator) |

## Type 1: In-Module Client Bindings

**When:** `dagger develop` or `dagger call` on a module

**What:** Generates client bindings so module code can call `dag.Container()`, `dag.Directory()`, dependency APIs.

**Implementation:** `CodeGenerator` interface at `core/sdk.go:93`

```go
type CodeGenerator interface {
    Codegen(ctx, deps, introspection, pkgName) (*GeneratedState, error)
}
```

## Type 2: Runtime Dispatch

**When:** Module starts up and needs to route incoming function calls

**What:** Generates `invoke()` function that dispatches calls to user implementations.

**Key insight:** SDKs differ here:

| SDK | Approach | Location |
|-----|----------|----------|
| Go | Static generated switch/case | `cmd/codegen/generator/go/templates/modules.go:140` |
| Python | Dynamic introspection | `sdk/python/src/dagger/mod/_module.py` |
| TypeScript | Hybrid AST + reflection | Runtime, no generated dispatch |

**Go example output:**
```go
func invoke(ctx context.Context, parentJSON []byte, parentName, fnName string, inputArgs map[string][]byte) (any, error) {
    switch parentName {
    case "MyModule":
        switch fnName {
        case "Build":
            // deserialize, call, return
        }
    }
}
```

## Type 3: SDK Libraries

**When:** During Dagger development via `go generate`

**What:** Builds the shipped SDK packages (`dagger.io/dagger` Go package, `dagger` Python package, etc.)

**Implementation:** Same `CodeGenerator` interface, but:
- No module context
- No dependency handling
- Different config paths

**Entry point:** `sdk/go/generate.go` runs `cmd/codegen generate-library`

## Type 4: Generated Clients

**When:** `dagger client install` (experimental)

**What:** Like Type 1, but for regular programs outside module runtime.

**Key difference:** Includes `Connect()`, `Close()`, `serveModuleDependencies()`.

**Implementation:** `ClientGenerator` interface at `core/sdk.go:20`

**Supported:** Go and TypeScript only.

See [generated-clients.md](generated-clients.md) for details.
