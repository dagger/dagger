# Proposal 2: Artifacts & Checks

*Builds on [Proposal 1: Workspace & Toolchains](./01-workspace-toolchains.md)*

## Problem

Dagger's current check system has usability issues:

1. **"What can I check?" is unanswerable** - Checks are buried in a static function tree (`go:modules:test`). There's no way to enumerate what artifacts exist or what checks apply to them.

2. **Checks are all-or-nothing** - A check like `go:lint` operates on everything. There's no native way to say "lint only modules under ./app" or "lint only what I changed."

3. **Tree navigation doesn't match mental model** - Users think "I have modules at paths, I want to check them." The function tree (`go:modules:lint`) inverts this.

## Solution

Introduce **Artifacts** as first-class objects with identity, and enable path-based filtering via **Workspace Provenance**.

### Artifacts

An **Artifact** is any object type that has `+check` methods. The engine infers this - no explicit interface needed.

```go
type GoModule struct {
    Source Directory  // carries workspace provenance
    Path   string     // identity
}

// +check
func (m *GoModule) Test() error { ... }

// +check
func (m *GoModule) Lint() error { ... }
```

GoModule is an Artifact because it has `+check` methods.

### Artifact Discovery

Artifacts are discovered via regular list fields on toolchains:

```go
type Go struct {
    Modules []*GoModule  // regular list
}

func New(ws Workspace) *Go {
    modules := discoverModules(ws)  // finds all go.mod files
    return &Go{Modules: modules}
}
```

After construction, the engine:
1. Walks the toolchain's object fields
2. Finds list fields containing objects with `+check` methods
3. Each such object is an artifact with its own ID and provenance

### Workspace Provenance

When `ws.Directory()` or `ws.File()` is called, the resulting Directory/File carries internal metadata about its workspace origin:

```go
type WorkspaceProvenance struct {
    Path     string   // "./src"
    Includes []string // ["**/*.go"]
    Excludes []string
}
```

This provenance:
- Is set at creation time
- Unions on composition (`dir.WithDirectory(other)` merges provenance)
- Is workspace-specific (ignores remote/synthetic sources)
- Enables path-based filtering

### The Recording Window

The constraint that Workspace cannot be stored as a field is not a limitation - it's the architectural foundation.

Because all workspace access must happen during construction:
- Construction is a bounded "recording window"
- When construction completes, we have a complete record of all workspace paths accessed
- No late/lazy accesses can happen - the recording is authoritative
- This enables reliable path filtering and introspection

### From Tree to Database

**Current: Static tree navigation**
```
go
 └── modules
      └── lint    ← global function
      └── test
```

**New: Queryable artifact database**
```
TYPE        PATH             DIRTY   CHECKS
GoModule    ./cmd/foo        true    test, lint
GoModule    ./cmd/bar        false   test, lint
GoModule    ./lib/shared     true    test, lint
NpmPackage  ./web            false   test, typecheck
```

### Query Interface

All artifact queries go through Workspace:

```graphql
type Workspace {
  directory(path: String!): Directory!
  file(path: String!): File!

  artifacts(
    path: String
    paths: [String!]
    gitRef: String
    gitUncommitted: Boolean
    type: String
  ): [Artifact!]!
}

interface Artifact {
  type: String!
  path: String!
  checkFunctions: [Function!]!
  check(name: String): CheckResult!
  checkAll: [CheckResult!]!
}
```

## CLI

### List Artifacts

```bash
$ dagger artifact list

TYPE        PATH                  CHECKS
GoModule    ./cmd/foo             test, lint
GoModule    ./cmd/bar             test, lint
GoModule    ./lib/shared          test, lint
NpmPackage  ./packages/web        test, typecheck
```

### Filter by Path

```bash
$ dagger artifact list --path='./cmd/*'

TYPE        PATH              CHECKS
GoModule    ./cmd/foo         test, lint
GoModule    ./cmd/bar         test, lint
```

### Filter by Git Status

```bash
$ dagger artifact list --git-uncommitted

TYPE        PATH              CHECKS
GoModule    ./cmd/foo         test, lint
GoModule    ./lib/shared      test, lint
```

### Run Checks

```bash
# All checks on all artifacts
$ dagger check

# All checks on dirty artifacts
$ dagger check --git-uncommitted

# Specific check on path-filtered artifacts
$ dagger check GoModule:test --path='./cmd/*'

# Combine filters
$ dagger check GoModule:lint --path='./app/*' --git-uncommitted
```

### Direct Function Calls

High-level commands cover common workflows, but direct `dagger call` remains available for edge cases:

```bash
# Custom test filtering (not yet in high-level API)
$ dagger call go test --filter=TestFoo

# Call fix function, CLI prompts to apply Changeset
$ dagger call go lint --fix
```

## Design Rationale

### Why Directory/File Carry Provenance (Not New Types)

We considered several approaches:

- **Option A: New WorkspaceDir/WorkspaceFile types** - Explicit but adds types to learn, breaks composability.
- **Option B: Explicit artifact registration** - Toolchains call `ws.RegisterArtifact()`. Boilerplate, easy to forget.
- **Option C: Schema annotations** - Declare workspace paths in type annotations. Drifts from actual code.
- **Option D: Standard types carry provenance internally** - Directory/File remember their workspace origin.

Option D won: no new types, no boilerplate, composes naturally, can't drift from reality.

### Why Artifact Inferred from +check

No explicit interface or registration needed. If an object has `+check` methods, it's an artifact. This:
- Eliminates boilerplate
- Enables progressive enhancement (add a method, become an artifact)
- Matches how Go interfaces work (structural, not nominal)

### Don't Reinvent Cache Invalidation

**Critical guardrail**: Workspace provenance is purely for UX filtering. The Dagger engine already handles cache invalidation correctly via content-addressed IDs.

We are NOT:
- Building a new invalidation system
- Tracking general provenance (git, remote, synthetic sources)

We ARE:
- Adding a human-readable layer for path-based filtering
- Enabling "check only what I changed" workflows

If this scope creeps toward "smarter caching," stop and reconsider.

## Open Questions

1. **Identity field** - How does an artifact declare which field is its "path" identity? Annotation? Convention?

2. **CEL expressions** - Power-user filtering: `dagger artifact list -f 'path.matches("app/*") && dirty'`

3. **Test splitting** - `Collection.split(n)` for parallel CI execution. Defer until needed.

## Implementation Phases

1. Provenance tracking on Directory/File
2. Artifact concept in engine
3. `Workspace.artifacts()` query
4. CLI: `dagger artifact list`
5. CLI: `dagger check` with filtering

## References

- Previous: [Proposal 1: Workspace & Toolchains](./01-workspace-toolchains.md)
- Next: [Proposal 3: Ship](./03-ship.md)
