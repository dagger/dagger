# Proposal 3: Ship

*Builds on [Proposal 2: Artifacts & Checks](./02-artifacts-checks.md)*

## Problem

Checking and shipping are separate lifecycle phases, but only checking has a standard pattern. Publishing a container, deploying to cloud, releasing a package - these are all ad-hoc, with no uniform toolchain interface.

Users must:
- Know each toolchain's specific publish/deploy function names
- Wire up their own workflows for each artifact type
- Handle the "preview before apply" pattern manually

## Solution

Introduce `+ship` as the peer to `+check`, with `dagger ship` as the high-level command.

### +ship Methods

Artifacts can declare `+ship` methods alongside `+check` methods:

```go
type GoModule struct {
    Source Directory
    Path   string
}

// +check
func (m *GoModule) Test() error { ... }

// +check
func (m *GoModule) Lint() error { ... }

// +ship
func (m *GoModule) Publish(registry string) error { ... }
```

```go
type Container struct {
    Image     *dagger.Container
    ImageName string
}

// +check
func (c *Container) Scan() error { ... }

// +ship
func (c *Container) Push() (string, error) { ... }

// +ship
func (c *Container) Deploy(env string) error { ... }
```

### Artifact Interface Extension

```graphql
interface Artifact {
  type: String!
  path: String!

  # From Proposal 2
  checkFunctions: [Function!]!
  check(name: String): CheckResult!
  checkAll: [CheckResult!]!

  # New
  shipFunctions: [Function!]!
  ship(name: String, args: [Arg!]): ShipResult!
  shipAll: [ShipResult!]!
}
```

## CLI

### Basic Ship

```bash
# Ship all artifacts (runs all +ship methods)
$ dagger ship

# Ship specific artifact type and method
$ dagger ship Container:push

# Ship with args
$ dagger ship Container:deploy --env=staging
```

### Filtered Ship

Uses the same filtering from Proposal 2:

```bash
# Ship only changed artifacts
$ dagger ship --git-uncommitted

# Ship specific path
$ dagger ship --path='./cmd/myapp'

# Combine
$ dagger ship Container:push --path='./services/*' --git-uncommitted
```

### Preview Mode

For destructive operations, support preview/confirm:

```bash
$ dagger ship --dry-run

ARTIFACT              METHOD    EFFECT
Container ./cmd/foo   push      Push to registry.io/foo:v1.2.3
Container ./cmd/bar   push      Push to registry.io/bar:v1.2.3

Proceed? [y/N]
```

## Integration with Fix

The `dagger fix` command (mentioned in Proposal 2) returns Changesets that the CLI prompts to apply. Ship could follow a similar pattern for operations that modify external state:

```bash
$ dagger ship Container:deploy --env=staging

Will deploy:
  - registry.io/foo:v1.2.3 → staging.example.com
  - registry.io/bar:v1.2.3 → staging.example.com

Proceed? [y/N]
```

## Design Rationale

### Why +ship as Peer to +check

The artifact lifecycle has natural phases:
1. **Check** - Verify correctness (test, lint, scan)
2. **Ship** - Release to the world (publish, deploy, release)

Making these parallel concepts enables:
- Uniform discovery (`shipFunctions` like `checkFunctions`)
- Consistent filtering (same `--path`, `--git-uncommitted` flags)
- Predictable toolchain authoring patterns

### Why Not Just dagger call

Direct function calls work, but:
- Users must know each toolchain's naming conventions
- No uniform filtering across artifact types
- No preview/confirm pattern
- No discoverability ("what can I ship?")

`dagger ship` provides the high-level UX while `dagger call` remains the escape hatch.

## Open Questions

1. **Ship vs Deploy vs Publish** - Should there be sub-verbs, or is `+ship` the universal annotation with method names distinguishing operations?

2. **Approval workflows** - How does preview/confirm interact with CI? Skip confirmation in CI? Require explicit `--yes` flag?

3. **Rollback** - Should `+ship` methods have corresponding `+rollback`? Or is that out of scope?

4. **Dependencies between ships** - If shipping artifact A requires artifact B to be shipped first, how is that expressed?

## Status

Design phase. Depends on Proposal 2 (Artifacts & Checks) being implemented first.

## References

- Previous: [Proposal 2: Artifacts & Checks](./02-artifacts-checks.md)
- [Proposal 1: Workspace & Toolchains](./01-workspace-toolchains.md)
