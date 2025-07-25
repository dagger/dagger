# Dagger CLI Engine Provisioning Architecture

This document provides a comprehensive overview of how the Dagger CLI provisions and connects to the Dagger engine, including architecture details and extension points for modifications.

## Overview

The Dagger CLI uses a **driver-based architecture** to provision and connect to the Dagger engine. The engine is a companion container required for 99% of the CLI's functionality. The system supports multiple provisioning strategies through a pluggable driver system.

## Key Concepts

### Core Components

1. **Driver Interface** - Abstract interface for different engine provisioning strategies
2. **Connector Interface** - Handles actual network connections to provisioned engines  
3. **URL-based Routing** - Uses URI schemes to determine which driver to use
4. **Engine Client** - High-level orchestrator for the entire process

### Environment Variables

- `_EXPERIMENTAL_DAGGER_RUNNER_HOST` - Override default engine location (URI format)
- `_EXPERIMENTAL_DAGGER_GPU_SUPPORT` - Enable GPU passthrough for Docker driver
- `_EXPERIMENTAL_DAGGER_RUNNER_IMAGESTORE` - Override image loader backend

## Architecture Flow

### 1. Initialization (`cmd/dagger/engine.go`)

```go
func init() {
    if v, ok := os.LookupEnv(RunnerHostEnv); ok {
        RunnerHost = v
    }
    if RunnerHost == "" {
        RunnerHost = defaultRunnerHost()
    }
}
```

The CLI reads configuration at startup, defaulting to a Docker image URI if no override is provided.

### 2. Default Configuration

```go
func defaultRunnerHost() string {
    tag := engine.Tag
    if os.Getenv(GPUSupportEnv) != "" {
        tag += "-gpu"
    }
    return fmt.Sprintf("docker-image://%s:%s", engine.EngineImageRepo, tag)
}
```

Default behavior constructs `docker-image://registry.dagger.io/engine:v0.x.x` pointing to the official engine image with version matching the CLI.

### 3. Engine Provisioning (`engine/client/client.go`)

```go
func (c *Client) startEngine(ctx context.Context) error {
    remote, err := url.Parse(c.RunnerHost)
    driver, err := drivers.GetDriver(remote.Scheme)
    c.connector, err = driver.Provision(ctx, remote, opts)
    // ... connect and validate
}
```

Flow:
1. Parse RunnerHost URL
2. Look up driver by scheme
3. Call `driver.Provision()` to create resources
4. Use returned `Connector` to establish connection
5. Create BuildKit client for communication

## Supported URI Schemes

| Scheme | Description | Driver File |
|--------|-------------|-------------|
| `docker-image://` | Downloads and runs engine in new container | `docker.go` |
| `docker-container://` | Connects to existing container | `dial.go` |
| `tcp://` | Direct TCP connection | `dial.go` |
| `unix://` | Unix socket connection | `dial.go` |
| `ssh://` | SSH tunnel connection | `dial.go` |
| `kube-pod://` | Kubernetes pod connection | `dial.go` |
| `podman-container://` | Podman container connection | `dial.go` |
| `dagger-cloud://` | Dagger Cloud managed engine | `cloud.go` |

## Driver System Details

### Driver Interface (`engine/client/drivers/driver.go`)

```go
type Driver interface {
    // Provision creates underlying resources, returns Connector
    Provision(ctx context.Context, url *url.URL, opts *DriverOpts) (Connector, error)
    
    // ImageLoader returns optional image loader backend
    ImageLoader() imageload.Backend
}

type Connector interface {
    // Connect creates connection to dagger instance
    Connect(ctx context.Context) (net.Conn, error)
}
```

### Docker Driver (`docker.go`)

The most commonly used driver:
- **Image Resolution**: Converts image refs to container names using digest hashes
- **Container Lifecycle**: Creates new containers or reuses existing ones
- **Cleanup**: Garbage collects old engine versions automatically
- **GPU Support**: Passes through GPU access when enabled
- **Volume Management**: Mounts engine state directory and config files

Key behaviors:
- Container naming: `dagger-engine-{image-hash-16-chars}`
- Automatic image pulling if not present
- Restart policy: `always`
- Privileged mode for full container capabilities

### Cloud Driver (`cloud.go`)

Handles Dagger Cloud connections:
- Authenticates with cloud service using tokens
- Provisions managed engine instances
- Handles TLS certificate management for secure connections
- No local resource management required

### Dial Driver (`dial.go`)

Generic connection handler using BuildKit helpers:
- TCP/Unix direct connections
- SSH-tunneled connections  
- Container connections via existing BuildKit connhelpers

## Extension Points

### 1. Adding New Driver Schemes

Create new file in `engine/client/drivers/`:

```go
package drivers

import (
    "context"
    "net"
    "net/url"
)

func init() {
    register("my-scheme", &myDriver{})
}

type myDriver struct{}

func (d *myDriver) Provision(ctx context.Context, target *url.URL, opts *DriverOpts) (Connector, error) {
    // Custom provisioning logic
    return &myConnector{target: target}, nil
}

func (d *myDriver) ImageLoader() imageload.Backend {
    return nil // or custom backend
}

type myConnector struct {
    target *url.URL
}

func (c *myConnector) Connect(ctx context.Context) (net.Conn, error) {
    // Custom connection logic
}
```

### 2. Modifying Docker Driver Behavior

Key modification points in `docker.go`:

- **Container Naming**: Modify `resolveImageID()` for custom naming schemes
- **Image Selection**: Change image resolution logic in `create()`
- **Volume Mounting**: Customize volume attachment in container creation
- **Environment Variables**: Modify env var passing logic
- **Cleanup Policy**: Adjust garbage collection in `garbageCollectEngines()`

### 3. Changing Default Engine Selection

Modify `defaultRunnerHost()` in `engine.go`:

```go
func defaultRunnerHost() string {
    // Custom logic for default selection
    if customCondition() {
        return "my-scheme://custom-engine"
    }
    return originalLogic()
}
```

### 4. Custom Connection Logic

Implement `Connector` interface for specialized connection handling:

```go
type customConnector struct {
    // connection parameters
}

func (c *customConnector) Connect(ctx context.Context) (net.Conn, error) {
    // Custom connection establishment
    // Can handle authentication, proxying, connection pooling, etc.
}
```

## Key Files for Development

| File | Purpose | Modification Use Cases |
|------|---------|----------------------|
| `cmd/dagger/engine.go` | CLI entry point, env var handling | Change defaults, add new env vars |
| `engine/client/client.go` | Main client orchestration | Modify connection flow, add hooks |
| `engine/client/drivers/driver.go` | Driver interface definitions | Extend interfaces, add new options |
| `engine/client/drivers/docker.go` | Docker provisioning logic | Container customization, image handling |
| `engine/client/drivers/dial.go` | Generic connection handlers | Add new connection types |
| `engine/client/drivers/cloud.go` | Cloud engine integration | Modify cloud behavior |

## Development Workflow

### Adding New Provisioning Method

1. Create new driver file in `engine/client/drivers/`
2. Implement `Driver` and `Connector` interfaces
3. Register driver in `init()` function
4. Driver automatically available when package imported

### Modifying Existing Behavior

1. **Docker modifications**: Edit methods in `docker.go`
2. **Default behavior**: Modify `defaultRunnerHost()` in `engine.go`  
3. **Connection logic**: Implement custom `Connector`
4. **New options**: Extend `DriverOpts` struct

### Testing Changes

Use environment variable to test new drivers:
```bash
export _EXPERIMENTAL_DAGGER_RUNNER_HOST="my-scheme://my-config"
dagger version
```

## Implementation Notes

- **Driver Registration**: Happens automatically via `init()` functions
- **URL Parsing**: Standard Go `net/url` package handles URI parsing
- **Error Handling**: Drivers should return descriptive errors for troubleshooting
- **Context Cancellation**: All methods should respect context cancellation
- **Backward Compatibility**: New drivers don't affect existing functionality
- **Import Side Effects**: Drivers register themselves when package is imported

## Security Considerations

- **Privileged Containers**: Docker driver runs containers in privileged mode
- **Volume Mounts**: Engine has access to mounted directories
- **Network Access**: Engine containers can access network resources
- **Cloud Tokens**: Cloud driver handles authentication tokens securely
- **TLS Verification**: Cloud connections use proper certificate validation

This architecture provides a flexible foundation for supporting diverse engine provisioning scenarios while maintaining simplicity for the common Docker-based use case.