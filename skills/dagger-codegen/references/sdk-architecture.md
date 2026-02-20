# SDK Architecture

> **Load when:** You need to understand SDK interfaces, add a new SDK, or figure out why Go SDK is different from others.

## Built-in SDKs

Defined in `core/sdk/consts.go`:

| SDK | Codegen | Notes |
|-----|---------|-------|
| Go | `cmd/codegen/generator/go/` | Hardcoded in engine, NOT a module |
| Python | `sdk/python/codegen/` | Native Python, ~870 lines |
| TypeScript | `cmd/codegen/generator/typescript/` | Shares cmd/codegen binary |
| Java | `sdk/java/dagger-codegen-maven-plugin/` | Maven plugin |
| PHP | `sdk/php/src/Codegen/` | PHP classes |
| Elixir | `sdk/elixir/dagger_codegen/` | Elixir mix |

## Experimental SDK

| SDK | Codegen | Notes |
|-----|---------|-------|
| .NET | `sdk/dotnet/sdk/Dagger.SDK.SourceGenerator/` | C# Source Generator; not in consts.go |

## Interfaces

All at `core/sdk.go`:

| Interface | Line | Purpose | Implementers |
|-----------|------|---------|--------------|
| `ClientGenerator` | 20 | Generate standalone clients | Go, TypeScript |
| `CodeGenerator` | 93 | Generate in-module bindings | All 6 built-in |
| `Runtime` | 145 | Provide execution container | All 6 built-in |
| `ModuleTypes` | 179 | Extract user type definitions | All 6 built-in |

## Why Go is Special

Go SDK is **not** a module. It's hardcoded in the engine.

**Implementation:** `core/sdk/go_sdk.go`

**How it works:**
1. `cmd/codegen` binary is built
2. Packaged into container, tarball'd
3. Embedded in engine image
4. Engine calls it directly (no module invocation)

**All other SDKs** are modules that expose interfaces via GraphQL fields. The engine loads them dynamically.

**Consequence:** Code paths differ between Go and module-based SDKs. Look at both when debugging.

## Key Files

| File | Purpose |
|------|---------|
| `core/sdk/consts.go` | Built-in SDK list |
| `core/sdk.go` | Interface definitions |
| `core/sdk/go_sdk.go` | Go SDK (hardcoded) |
| `core/sdk/module.go` | Module-based SDK wrapper |
| `core/sdk/module_code_generator.go` | CodeGenerator for module SDKs |
| `core/sdk/module_client_generator.go` | ClientGenerator for module SDKs |
