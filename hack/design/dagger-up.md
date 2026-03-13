# `dagger up`

## Problem

Dagger modules can define services (web servers, databases, caches), but there's no standardized way to:

1. **Declare** which functions represent long-running services
2. **Discover** all services across a module and its toolchains
3. **Start** them with a single command and expose them on the host

## Solution

A new `+up` annotation on functions returning `Service`, and a `dagger up` CLI command that discovers and starts all annotated services in parallel, tunneling their ports to the host.

This follows the same pattern as `dagger check` (`+check`) and `dagger generate` (`+generate`): annotate functions, discover them via the module tree, and run them from a single CLI command.

## Core Concept

```graphql
type Up {
  "The service name (kebab-case path, e.g. 'infra:database')"
  name: String!
  description: String!
  path: String!
  originalModule: Module!

  "Start the service, tunnel ports, and block"
  run: Void
}

type UpGroup {
  "List all discovered services"
  list: [Up!]!

  "Start all services in parallel"
  run: Void
}

extend type Module {
  services(include: [String!]): UpGroup!
  service(name: String!): Up!
}

extend type Env {
  services(include: [String!]): UpGroup!
}

extend type Function {
  withService: Function!
}
```

### Service Discovery

Services are discovered by walking the module tree (same as checks and generators). Each `ModTreeNode` gains an `IsService` flag. `RollupServices()` collects all leaf nodes marked as services into a flat list.

When a module is used as a toolchain dependency, its `+up` services are visible to the parent module's `dagger up`.

### Health Checks

Services automatically benefit from Dagger's Docker `HEALTHCHECK` support. If a service's container defines a `HEALTHCHECK` (via Dockerfile or `WithDockerHealthcheck()`), Dagger waits for it to pass before marking the service as started. No extra annotation needed — this is inherited from the `Service` type.

### Port Tunneling

Each service's exposed ports are tunneled to the host. If two services expose the same host port, `dagger up` fails fast with a clear error listing the conflict.

## CLI

```bash
# Start all services (blocks until Ctrl+C)
dagger up

# List available services
dagger up --list

# Start specific services by pattern
dagger up web redis

# With module flag
dagger up --mod ./path/to/module
```

## SDK Annotations

**Go:**
```go
// Starts the web server on port 8080
// +up
func (m *MyModule) Web() *dagger.Service {
    return dag.Container().From("nginx").
        WithExposedPort(8080).
        AsService()
}
```

**Python:**
```python
@dagger.up()
def web(self) -> dagger.Service:
    """Starts the web server on port 8080"""
    return dag.container().from_("nginx").with_exposed_port(8080).as_service()
```

**TypeScript:**
```typescript
@up()
web(): Service {
  return dag.container().from("nginx").withExposedPort(8080).asService()
}
```

**Java:**
```java
@Up
public Service web() {
    return dag.container().from("nginx").withExposedPort(8080).asService();
}
```

### Hierarchical Naming

Functions on sub-objects get colon-separated paths, same as checks:

```go
func (m *MyModule) Infra() *Infra { return &Infra{} }

// +up
func (i *Infra) Database() *dagger.Service { ... }
// → discovered as "infra:database"
```

## Configuration

### Toolchain: ignoring services

```json
{
  "dependencies": [{
    "name": "my-toolchain",
    "source": "./toolchain",
    "ignoreServices": ["debug-server"]
  }]
}
```

### Toolchain: customizing service arguments

Using the existing `customizations` pattern, function arguments can be configured in `dagger.json`:

```json
{
  "dependencies": [{
    "name": "my-toolchain",
    "source": "./toolchain",
    "customizations": [{
      "function": "web",
      "argument": "port",
      "value": "3000"
    }]
  }]
}
```

## Implementation Layers

The implementation follows the established check/generate pattern across these layers:

| Layer | Files | What |
|-------|-------|------|
| Core typedef | `core/typedef.go` | `Function.IsService`, `WithService()` |
| Module tree | `core/modtree.go` | `ModTreeNode.IsService`, `RollupServices()`, `RunUp()` |
| Runtime types | `core/up.go` | `Up`, `UpGroup` with `List()`, `Run()` |
| GraphQL schema | `core/schema/serviceentries.go`, `core/schema/module.go` | Resolvers for `services`, `service`, `withService` |
| CLI | `cmd/dagger/up.go` | `dagger up` cobra command |
| Go codegen | `cmd/codegen/generator/go/templates/module_funcs.go` | Parse `// +up` pragma |
| Python SDK | `sdk/python/src/dagger/mod/` | `@dagger.up()` decorator |
| TypeScript SDK | `sdk/typescript/src/module/` | `@up()` decorator |
| Java SDK | `sdk/java/` | `@Up` annotation |
| Telemetry | `sdk/go/telemetry/attrs.go`, `dagql/dagui/spans.go` | `dagger.io/service.name` span attribute |
| Config | `core/modules/config.go` | `ignoreServices` on dependencies |
| Toolchain | `core/toolchain.go` | `IgnoreServices` on `ToolchainEntry` |

## Phased Rollout

### Phase 1: Core (current PR)

- `+up` annotation across all SDKs (Go, Python, TypeScript, Java)
- `Function.IsService` / `WithService()` in core typedef
- `ModTreeNode.IsService` + `RollupServices()` tree traversal
- `Up` / `UpGroup` types with `List()` and `Run()`
- GraphQL schema resolvers
- `dagger up` CLI command (hidden/experimental) with `--list` flag
- Parallel service startup with port tunneling to host
- Automatic health checks via Docker `HEALTHCHECK` support
- Port collision: fail fast with clear error message
- Toolchain integration with `ignoreServices`
- Parameterized services via `customizations` in `dagger.json`
- Telemetry spans with `dagger.io/service.name` attribute

### Phase 2: Port Configuration

- Port overrides via CLI flags: `dagger up web --port 3000:8080`
- Port mapping configuration in `dagger.json`
- Optional auto-remap on collision

### Phase 3: Daemon Mode

- `dagger up -d` to run services in the background
- `dagger up --status` to show running services
- `dagger up --stop` to stop background services
- PID file / socket management

### Phase 4: Enhanced UX

- Logs streaming per service
- TUI with per-service status indicators
- Restart on failure policies

### Out of Scope (by design)

- Inter-service dependency ordering (use `withServiceBinding` at the function level)
- Docker Compose-like networking/volumes (Dagger handles this in the DAG)
- Service mesh / load balancing

## Status

Phase 1 implementation in progress on the `dagger-up` branch. Command is hidden behind `Hidden: true` as experimental.
