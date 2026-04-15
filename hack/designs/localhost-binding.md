# Localhost Service Forward

## Table of Contents
- [Problem](#problem)
- [Solution](#solution)
- [Core Concept](#core-concept)
- [Implementation](#implementation)
- [Status](#status)

## Problem

1. **Many apps expect localhost** - Test frameworks, ORMs, and dev tools often default to `localhost:PORT` for databases, caches, and other services. Today, `withServiceBinding` only exposes services on custom hostnames (e.g., `db:5432`), forcing users to override application config.

2. **No way to bind to loopback** - There's no mechanism to make a Dagger service appear on `127.0.0.1` inside a container. Users must reconfigure their app to use the alias hostname, which isn't always possible (hardcoded defaults, third-party tools, convention-over-configuration frameworks).

3. **Friction for "just run my tests"** - The most common case is running a test suite that expects `localhost:5432` (postgres), `localhost:6379` (redis), etc. Today this requires config changes that don't exist outside of Dagger.

## Solution

Add `withLocalhostForward` to `Container`. It forwards a service's port to `127.0.0.1:<port>` inside the container's network namespace via a lightweight TCP proxy, so applications connect to `localhost` without config changes.

## Core Concept

### API

```graphql
extend type Container {
  """
  Forward a service port to localhost inside this container.

  The service will be started automatically when needed and detached
  when it is no longer needed.

  TCP traffic to 127.0.0.1:<port> inside the container will be
  forwarded to the specified port on the service. Unlike
  withServiceBinding, no hostname alias is created — the service
  is only reachable via localhost.
  """
  withLocalhostForward(
    """Port to listen on 127.0.0.1 inside this container."""
    port: Int!

    """The target service."""
    service: ServiceID!

    """
    Port on the service to forward to.
    Defaults to the same as port.
    """
    servicePort: Int
  ): Container!
}
```

### Usage

**Go — postgres on localhost:**
```go
func (m *MyModule) Test(ctx context.Context) (string, error) {
    pg := dag.Container().
        From("postgres:16").
        WithExposedPort(5432).
        WithEnvVariable("POSTGRES_PASSWORD", "test").
        AsService()

    return dag.Container().
        From("golang:1.23").
        WithLocalhostForward(5432, pg).
        WithExec([]string{"go", "test", "./..."}).
        Stdout(ctx)
}
```

**Python — redis on localhost:**
```python
def test(self) -> dagger.Container:
    redis = (
        dag.container()
        .from_("redis:7")
        .with_exposed_port(6379)
        .as_service()
    )

    return (
        dag.container()
        .from_("python:3.12")
        .with_localhost_forward(6379, redis)
        .with_exec(["pytest"])
    )
```

**Different frontend/backend ports:**
```go
// Service listens on 15432, but app expects localhost:5432
dag.Container().
    From("golang:1.23").
    WithLocalhostForward(5432, pg, dagger.ContainerWithLocalhostForwardOpts{
        ServicePort: 15432,
    }).
    WithExec([]string{"go", "test", "./..."})
```

**Multiple services:**
```go
dag.Container().
    From("node:22").
    WithLocalhostForward(5432, pg).
    WithLocalhostForward(6379, redis).
    WithExec([]string{"npm", "test"})
```

### Comparison with `withServiceBinding`

| | `withServiceBinding` | `withLocalhostForward` |
|---|---|---|
| Access pattern | `alias:port` (e.g. `db:5432`) | `localhost:port` (e.g. `localhost:5432`) |
| DNS/hosts entry | Yes (alias → service IP) | No |
| Loopback forward | No | Yes (`127.0.0.1:port` via TCP proxy) |
| Use case | Service-oriented access | Drop-in for apps expecting localhost |
| Multiple ports per call | All exposed ports accessible | One port per call |

## Implementation

### Data Model

New field on `Container`:

```go
// In core/container.go, Container struct
type Container struct {
    // ... existing fields ...
    Services          ServiceBindings
    LocalhostForwards LocalhostForwards  // NEW
}

type LocalhostForwards []LocalhostForward

type LocalhostForward struct {
    Service     dagql.ObjectResult[*Service]
    Port        int // port on 127.0.0.1 inside container
    ServicePort int // port on the service (0 = same as Port)
}
```

New field on `ExecutionMetadata`:

```go
// In engine/buildkit/executor.go
type ExecutionMetadata struct {
    // ... existing fields ...
    HostAliases        map[string][]string
    LocalhostForwards  []LocalhostForwardMD  // NEW
}

type LocalhostForwardMD struct {
    ServiceHostname string // hostname to resolve → service IP
    Port            int    // port on 127.0.0.1
    ServicePort     int    // port on service
}
```

### Execution Flow

The flow mirrors `withServiceBinding` for service lifecycle, but diverges at the networking layer:

```
1. Container.WithLocalhostForward(port, svc)
   → stores LocalhostForward in container

2. container_exec.go: Exec preparation
   → populates execMD.LocalhostForwards with service hostname + ports

3. Services.StartBindings()
   → starts all bound services (same as today)

4. executor_spec.go: setupNetwork()
   → after netns is created, for each LocalhostForward:
      a. resolve service hostname → IP (same lookup as HostAliases)
      b. start proxy goroutine in container netns
```

### Proxy Mechanism

A lightweight TCP proxy runs inside the container's network namespace, following the same pattern as the existing `c2hTunnel`:

```go
// In executor_spec.go, after network namespace setup
func (w *Worker) setupLocalhostForwards(ctx context.Context, state *execState) error {
    for _, fwd := range w.execMD.LocalhostForwards {
        serviceIP := resolveServiceIP(fwd.ServiceHostname) // same as hosts file lookup

        // Start proxy in container's network namespace
        listener, err := RunInNetNS(ctx, state.networkNamespace, func() (net.Listener, error) {
            return net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", fwd.Port))
        })

        // Proxy goroutine: accept → dial serviceIP:servicePort → bidirectional copy
        go proxyConnections(ctx, listener, serviceIP, fwd.ServicePort)
    }
    return nil
}
```

This reuses the `RunInNetNS` infrastructure already used by `c2hTunnel` and health checks. The proxy lifecycle is tied to the container's context — when the container stops, the context is cancelled and listeners close.

### Duplicate Port Semantics

A second `WithLocalhostForward` on the same port replaces the previous forward (same as `WithEnvVariable` replacing a previous value for the same key). This enables swapping services mid-pipeline:

```go
dag.Container().
    From("golang:1.23").
    WithLocalhostForward(5432, pgSvc).
    WithExec([]string{"go", "test", "-run", "Integration", "./..."}).
    WithLocalhostForward(5432, mockPgSvc).  // replaces pgSvc
    WithExec([]string{"go", "test", "-run", "Mock", "./..."})
```

### Error Cases

All errors surface at execution time:

- **Service port not exposed**: Service doesn't expose the requested port → error at service start
- **Port already in use**: Container process already bound to the port → proxy `Listen` fails with "address already in use"

### Schema Registration

```go
// In core/schema/container.go
dagql.Func("withLocalhostForward", s.withLocalhostForward).
    Doc(`Forward a service port to localhost inside this container.`,
        `The service will be started automatically when needed and detached when it is no longer needed.`,
        `TCP traffic to 127.0.0.1:<port> inside the container will be forwarded to the specified port on the service.`).
    Args(
        dagql.Arg("port").Doc(`Port to listen on 127.0.0.1 inside this container`),
        dagql.Arg("service").Doc(`The target service`),
        dagql.Arg("servicePort").Doc(`Port on the service to forward to (defaults to same as port)`),
    ),
```

### Files to Modify

| File | Change |
|------|--------|
| `core/container.go` | Add `LocalhostForwards` field, `WithLocalhostForward` method, update `Clone` |
| `core/service.go` | Add `LocalhostForward` / `LocalhostForwards` types |
| `core/container_exec.go` | Populate `execMD.LocalhostForwards` during exec prep |
| `core/schema/container.go` | Register `withLocalhostForward` GraphQL field |
| `engine/buildkit/executor.go` | Add `LocalhostForwardMD` to `ExecutionMetadata` |
| `engine/buildkit/executor_spec.go` | Add `setupLocalhostForwards` after `setupNetwork` |
| `docs/docs-graphql/schema.graphqls` | Generated schema update |
| `sdk/*/` | Auto-generated SDK updates |

## Status

Design proposal — ready for implementation.
