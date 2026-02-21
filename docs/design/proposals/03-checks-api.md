# Part 3: Artifacts

*Builds on [Part 1: Module vs. Workspace](https://gist.github.com/shykes/e4778dc5ec17c9a8bbd3120f5c21ce73) and [Part 2: Workspace API](https://gist.github.com/shykes/86c05de3921675944087cb0849e1a3be)*

## Table of Contents

- [Problem](#problem)
- [Solution](#solution)
- [What is an Artifact](#what-is-an-artifact)
- [Operations](#operations)
  - [Check](#check)
  - [Ship](#ship)
  - [Generate](#generate)
- [Discovery](#discovery)
- [Provenance and Filtering](#provenance-and-filtering)
- [Workspace API](#workspace-api)
- [Module Composition](#module-composition)
- [CLI](#cli)
- [Status](#status)

## Problem

1. **No filtering** - Operations run on everything. There's no native way to say "lint only what I changed" or "deploy only services under ./cmd".

2. **No cross-module orchestration** - Different modules operate on the same parts of your repository, but there's no unified view. One module tests your app, another builds containers, another deploys them. Orchestrating requires custom glue code - no way to ask "what can be done to ./cmd/api?"

## Solution

Introduce **Artifacts** - objects discovered from the workspace that modules can operate on. Operations like check, ship, and generate are methods on artifacts. The engine discovers artifacts automatically, enabling filtering and cross-module composition.

## What is an Artifact

An **Artifact** is any object with operation methods (`+check`, `+ship`, `+generate`). The engine infers this from the methods - no explicit declaration needed.

```go
type GoModule struct {
    Source *dagger.Directory
}

// +check
func (m *GoModule) Test(ctx context.Context) error { ... }

// +check
func (m *GoModule) Lint(ctx context.Context) error { ... }

// +ship
func (m *GoModule) Publish(ctx context.Context, registry string) error { ... }

// +generate
func (m *GoModule) GenMocks(ctx context.Context) (*dagger.Directory, error) { ... }
```

`GoModule` is an artifact because it has operation methods.

## Operations

Artifacts support three types of operations:

### Check

Verify correctness. Returns success/failure.

```go
// +check
func (m *GoModule) Test(ctx context.Context) error
```

Examples: test, lint, typecheck, security scan.

### Ship

Publish or deploy. Releases the artifact to the world.

```go
// +ship
func (m *GoModule) Publish(ctx context.Context, registry string) error
```

Examples: push container, publish package, deploy to cloud.

### Generate

Produce derived files. Returns a Directory to merge back.

```go
// +generate
func (m *GoModule) GenMocks(ctx context.Context) (*dagger.Directory, error)
```

Examples: generate mocks, compile protobufs, update lockfiles.

## Discovery

Modules expose artifacts through fields. The engine walks the object graph:

```go
type Go struct {
    Modules []*GoModule
}

func New(ws dagger.Workspace) *Go {
    var modules []*GoModule
    for _, path := range findGoModFiles(ws) {
        modules = append(modules, &GoModule{
            Source: ws.Directory(path),
        })
    }
    return &Go{Modules: modules}
}
```

After construction, the engine:
1. Walks the module's fields
2. Finds objects with operation methods (`+check`, `+ship`, `+generate`)
3. Each becomes a queryable artifact

## Provenance and Filtering

Artifacts carry **provenance** - metadata about their workspace origin. When an artifact's source was loaded via `ws.Directory(path)`, the engine tracks which paths it came from.

This enables filtering by path, git status, and artifact type.

## Workspace API

The [Workspace type from Part 2](https://gist.github.com/shykes/86c05de3921675944087cb0849e1a3be#the-workspace-type) is extended with an `artifacts()` method to discover and operate on artifacts. This replaces `Module.Checks()`, `CheckGroup`, and similar APIs.

```graphql
extend type Workspace {
  """
  Discover artifacts from all installed modules.
  Returns artifacts matching the filters.
  """
  artifacts(
    """Filter by workspace path (glob pattern)"""
    path: String
    """Filter by multiple paths"""
    paths: [String!]
    """Filter to artifacts affected by changes since this git ref"""
    gitRef: String
    """Filter to artifacts affected by uncommitted changes"""
    gitUncommitted: Boolean
    """Filter by artifact type name"""
    type: String
  ): [Artifact!]!
}

interface Artifact {
  """The artifact type name (e.g. GoModule, Container)"""
  type: String!

  """Workspace paths this artifact's source came from"""
  source: [String!]!

  """Available check operations"""
  checks: [Function!]!

  """Available ship operations"""
  ships: [Function!]!

  """Available generate operations"""
  generates: [Function!]!

  """Run a check by name"""
  check(name: String!): CheckResult!

  """Run all checks"""
  checkAll: [CheckResult!]!

  """Run a ship operation by name"""
  ship(name: String!): ShipResult!

  """Run a generate operation by name"""
  generate(name: String!): Directory!
}
```

Example usage:

```go
// Get all artifacts under ./cmd with uncommitted changes
artifacts := ws.Artifacts(dagger.WorkspaceArtifactsOpts{
    Path: "./cmd/*",
    GitUncommitted: true,
})

// Run all checks on each
for _, artifact := range artifacts {
    artifact.CheckAll()
}
```

## Module Composition

Install modules, operations compose automatically:

```toml
# .dagger/config.toml
[modules]
go = "github.com/dagger/go-toolchain"
docker = "github.com/dagger/docker-toolchain"
k8s = "github.com/dagger/k8s-toolchain"
```

```bash
# See all artifacts and their operations
$ dagger artifact list

TYPE         SOURCE                CHECKS        SHIP          GENERATE
GoModule     ./cmd/api             test, lint    publish       mocks
GoModule     ./cmd/worker          test, lint    publish       mocks
Container    ./cmd/api             scan          push, deploy  -
Container    ./cmd/worker          scan          push, deploy  -
```

The engine aggregates artifacts from all installed modules. Different modules can expose different operations on related sources.

## CLI

### List Artifacts

```bash
$ dagger artifact list

TYPE         SOURCE                CHECKS        SHIP          GENERATE
GoModule     ./cmd/api             test, lint    publish       mocks
GoModule     ./cmd/worker          test, lint    publish       mocks
NpmPackage   ./web                 test, lint    publish       -
Container    ./cmd/api, ./deploy   scan          push, deploy  -
```

### Run Operations

```bash
# Check
$ dagger check                              # all checks
$ dagger check GoModule:test                # specific type and method
$ dagger check --path='./cmd/*'             # filter by path

# Ship
$ dagger ship                               # all ship operations
$ dagger ship Container:deploy --env=prod   # with arguments
$ dagger ship --git-uncommitted             # filter by git status

# Generate
$ dagger generate                           # all generators
$ dagger generate GoModule:mocks            # specific generator
```

### Direct Function Calls

For edge cases beyond the high-level commands:

```bash
$ dagger call go test --filter='TestAPI*'
$ dagger call go lint --fix
```

## Status

Design phase.

---

- Previous: [Part 2: Workspace API](https://gist.github.com/shykes/86c05de3921675944087cb0849e1a3be)
- Next: Part 4: Ship (coming soon)
