# Dagger Architecture Overview

A containerized workflow runtime with GraphQL API, universal type system, and language SDKs.

## Core Components

### CLI (`cmd/dagger/`)
User-facing command-line interface. Key files:
- `main.go` - Entry point, command routing
- `module.go`, `functions.go` - Module/function execution
- `shell.go` - Interactive shell
- `llm.go` - AI agent integration
- `checks.go` - Health checks system

### Engine (`engine/`)
Container runtime and execution layer:
- `server/` - gRPC API server
- `client/` - Client connection handling
- `buildkit/` - OCI container operations
- `cache/`, `session/` - State management
- `telemetry/` - Observability

### Core API (`core/`)
GraphQL API implementation (~29k LOC). Major types:
- `container.go` - Container operations
- `directory.go` - Filesystem operations
- `module.go` - Module definitions
- `modulesource.go` - Module resolution
- `typedef.go` - Type system
- `mcp.go`, `llm.go` - AI/LLM integration
- `schema/` - GraphQL schema
- `integration/` - 66+ test suites

### DagQL (`dagql/`)
Specialized GraphQL implementation with:
- Content-addressable IDs (derived from queries)
- Immutable objects
- `@impure` directive for non-cacheable operations
- `idtui/` - Terminal UI framework

## SDKs (`sdk/`)

Language runtimes + code generators, each following the pattern:
```
sdk/<language>/
├── src/              # SDK implementation
├── codegen/          # Generate from GraphQL schema
├── runtime/          # Module execution template
└── dev/              # Development module
```

**Supported:** Go, Python, TypeScript, Rust, Java, Elixir, PHP, .NET, CUE

## Self-Hosting & CI

### `.dagger/` - Dogfooding
Dagger builds itself using Dagger. Key functions:
- `main.go` - DaggerDev module (dev environment)
- `test.go` - Test suite runner
- `sdk*.go` - SDK-specific operations
- `generate.go` - Code generation
- `docs.go` - Documentation building

Usage: `dagger call <function>` (e.g., `dagger call test all`)

### `modules/` - Reusable Modules
Shared modules for CI and public use:
- Language toolchains: `go/`, `alpine/`, `wolfi/`
- Integrations: `gha/` (GitHub Actions), `claude/` (AI)
- Utilities: `shellcheck/`, `ruff/`, `metrics/`

### `.github/` - GitHub Actions
28 workflow files orchestrating CI, plus a `main.go` Dagger module for GHA integration.

## Key Architectural Patterns

1. **Content-Addressable IDs** - Objects identified by their construction query, enabling caching and deduplication
2. **Immutability** - All operations return new objects (`withX`/`withoutX` convention)
3. **GraphQL-First** - Core API is GraphQL; SDKs auto-generate from schema
4. **Containerized Execution** - Everything runs in containers via Buildkit
5. **Module System** - First-class code modules with type-safe cross-language interop

## Data Flow

```
┌─────────────┐
│ CLI         │ cmd/dagger/
└──────┬──────┘
       ↓
┌─────────────┐
│ Engine      │ engine/
│ (gRPC)      │
└──────┬──────┘
       ↓
┌─────────────┐
│ Core API    │ core/ + dagql/
│ (GraphQL)   │
└──────┬──────┘
       ↓
┌─────────────┐
│ Buildkit    │ internal/buildkit/
└─────────────┘
```

## Getting Started

**Interactive Development:**
```bash
dagger call playground terminal  # Dev environment shell
```

**Run Tests:**
```bash
dagger call test all              # All tests
dagger call test-sdks             # SDK tests
```

**Key Entry Points:**
- CLI: `cmd/dagger/main.go:817`
- Engine: `cmd/engine/main.go`
- Core API: `core/schema/schema.go`
- Module System: `core/module.go:33k`, `core/modulesource.go:40k`

## Additional Directories

- `internal/` - Buildkit fork, cloud integration, test utilities
- `docs/` - Documentation site and schema docs
- `util/` - Shared utilities (parallel, gitutil, fsxutil, etc.)
- `network/`, `auth/`, `analytics/` - Supporting services

## Contributing

See `CONTRIBUTING.md` for development workflow. The `.dagger/` module provides all dev tooling.
