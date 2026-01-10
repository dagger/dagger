# Dagger Nushell SDK

Write Dagger modules using [Nushell](https://www.nushell.sh/), a modern shell with structured data and pipelines.

## Features

- ✅ **Top-level Functions**: Export Nushell functions as Dagger functions
- ✅ **Type System**: Full support for primitives, lists, and Dagger objects
- ✅ **Pipeline API**: Idiomatic Nushell syntax with 87 built-in operations
- ✅ **Check Functions**: Validation functions with `# @check` annotation
- ✅ **Optional Parameters**: Default values and flexible APIs

## Quick Start

### Initialize a Module

```bash
dagger init --sdk=nushell
```

This creates:
```
.dagger/
  main.nu          # Your module code
```

### Example Module

```nushell
#!/usr/bin/env nu
# A simple Dagger module

use /usr/local/lib/dag.nu *

# Build a Go application
export def build [
    source: record  # @dagger(Directory)
]: nothing -> record {  # @returns(Container)
    container from "golang:1.24-alpine"
    | container with-directory "/src" $source
    | container with-workdir "/src"
    | container with-exec ["go", "build", "-o", "app", "."]
}

# Run the built application
export def run [
    source: record  # @dagger(Directory)
]: nothing -> string {
    build $source
    | container with-exec ["./app"]
    | container stdout
}

# @check
# @returns(Container)
# Validate the build succeeds
export def build-check []: nothing -> record {
    host directory "."
    | build
    | container with-exec ["test", "-f", "app"]
}
```

### Run Functions

```bash
# Call a function
dagger call build --source=.

# Get output
dagger call run --source=.

# Run checks
dagger check
```

## Type System

### Primitives

```nushell
export def greet [
    name: string
    age: int
    happy: bool
]: nothing -> string {
    $"($name) is ($age) years old"
}
```

### Dagger Objects

Use records for Dagger objects (Container, Directory, File, etc.):

```nushell
export def process [
    dir: record  # @dagger(Directory)
    ctr: record  # @dagger(Container)
]: nothing -> record {  # @returns(Container)
    $ctr
    | container with-directory "/data" $dir
    | container with-exec ["process", "/data"]
}
```

### Lists

```nushell
export def process-items [
    items: list<string>
    numbers: list<int>
]: nothing -> list<string> {
    $items | each {|item| $"Processed: ($item)"}
}
```

### Optional Parameters

```nushell
export def greet [
    name: string = "World"
    format: string = "Hello, %s!"
]: nothing -> string {
    $format | fill -c $name
}
```

**Note:** Parameters must be in alphabetical order (Dagger requirement), and once a parameter is optional, all following parameters must be optional (Nushell requirement).

### Return Types

Use `# @returns(Type)` for explicit return types:

```nushell
# @returns(Container)
export def build []: nothing -> record {
    container from "alpine"
}

# @returns(list<string>)
export def list-files []: nothing -> list<string> {
    ["file1.txt", "file2.txt"]
}
```

## Pipeline API

The SDK provides 87 operations in `/usr/local/lib/dag.nu`:

### Container Operations

```nushell
# Build and publish a container
container from "golang:1.24"
| container with-directory "/src" (host directory ".")
| container with-workdir "/src"
| container with-exec ["go", "build", "-o", "app"]
| container publish "registry.example.com/myapp:latest"
```

### Directory Operations

```nushell
# Create a directory with files
directory
| directory with-new-file "config.json" "{}"
| directory with-new-file "README.md" "# Hello"
| directory export "./output"
```

### File Operations

```nushell
# Read file contents
host directory "."
| directory file "README.md"
| file contents
```

### Git Operations

```nushell
# Clone and build from git
git "https://github.com/example/repo"
| git-repository branch "main"
| git-branch tree
| build
```

### Cache Volumes

```nushell
# Use cache for dependencies
container from "golang:1.24"
| container with-mounted-cache "/go/pkg/mod" (cache-volume "go-mod")
| container with-exec ["go", "build"]
```

### Secrets

```nushell
# Use secrets
container from "alpine"
| container with-secret-variable "API_KEY" (host env-variable "API_KEY")
| container with-exec ["./deploy"]
```

## Check Functions

Define validation functions with `# @check`:

```nushell
# @check
# @returns(Container)
# Validate tests pass
export def test-check []: nothing -> record {
    container from "golang:1.24"
    | container with-directory "/src" (host directory ".")
    | container with-workdir "/src"
    | container with-exec ["go", "test", "./..."]
}
```

Run checks:

```bash
# List all checks
dagger check -l

# Run all checks
dagger check

# Run specific check
dagger check test-check
```

## API Reference

### Core Operations

- `container from <image>` - Create container from image
- `container with-exec <cmd>` - Execute command in container
- `container with-env-variable <name> <value>` - Set environment variable
- `container with-directory <path> <dir>` - Mount directory
- `container with-file <path> <file>` - Mount file
- `container publish <address>` - Publish to registry
- `container export <path>` - Export as tarball
- `container stdout` - Get stdout from last exec
- `container stderr` - Get stderr from last exec

### Directory Operations

- `directory` - Create empty directory
- `directory with-new-file <path> <contents>` - Add file
- `directory with-new-directory <path>` - Add directory
- `directory with-file <path> <file>` - Add file from File
- `directory with-directory <path> <dir>` - Add subdirectory
- `directory entries` - List directory contents
- `directory export <path>` - Export to local filesystem

### Host Access

- `host directory <path>` - Access host directory
- `host file <path>` - Access host file
- `host env-variable <name>` - Get host environment variable
- `host unix-socket <path>` - Access Unix socket

### Git Operations

- `git <url>` - Create git repository reference
- `git-repository branch <name>` - Get branch
- `git-repository tag <name>` - Get tag
- `git-repository commit <hash>` - Get commit
- `git-branch tree` - Get branch tree as directory

## Current Limitations

This SDK is **functional for simple modules** but has architectural limitations:

- ❌ **No Object/Method System**: Only top-level functions (no custom objects with methods)
- ❌ **No Pipeline Input Functions**: Functions using `$in` aren't properly typed
- ❌ **No Field Accessors**: No computed properties or fields

See [`IMPLEMENTATION_GAPS.md`](./IMPLEMENTATION_GAPS.md) for detailed analysis.

## Examples

### Multi-stage Build

```nushell
export def build-optimized [
    source: record  # @dagger(Directory)
]: nothing -> record {  # @returns(Container)
    # Build stage
    let builder = (
        container from "golang:1.24"
        | container with-directory "/src" $source
        | container with-workdir "/src"
        | container with-exec ["go", "build", "-o", "app"]
    )
    
    # Runtime stage
    container from "alpine:latest"
    | container with-file "/app" ($builder | container file "/src/app")
    | container with-entrypoint ["/app"]
}
```

### Environment-specific Builds

```nushell
export def deploy [
    source: record  # @dagger(Directory)
    environment: string = "development"
]: nothing -> string {
    let config = if $environment == "production" {
        "config.prod.json"
    } else {
        "config.dev.json"
    }
    
    container from "node:20"
    | container with-directory "/app" $source
    | container with-workdir "/app"
    | container with-file "/app/config.json" (host file $config)
    | container with-exec ["npm", "install"]
    | container with-exec ["npm", "run", "build"]
    | container publish $"registry.example.com/myapp:($environment)"
}
```

### Testing Pipeline

```nushell
# @check
# @returns(Container)
export def lint []: nothing -> record {
    container from "golang:1.24"
    | container with-directory "/src" (host directory ".")
    | container with-exec ["golangci-lint", "run"]
}

# @check
# @returns(Container)
export def test []: nothing -> record {
    container from "golang:1.24"
    | container with-directory "/src" (host directory ".")
    | container with-exec ["go", "test", "-v", "./..."]
}

# @check
# @returns(Container)
export def integration-test []: nothing -> record {
    container from "golang:1.24"
    | container with-directory "/src" (host directory ".")
    | container with-exec ["go", "test", "-tags=integration", "./..."]
}
```

## Contributing

Contributions welcome! This SDK is under active development.

### Project Structure

```
sdk/nushell/runtime/
├── runtime/              # Runtime infrastructure
│   ├── dag.nu           # Dagger API library (87 operations)
│   ├── executor.go      # Function executor binary
│   └── runtime.nu       # Runtime entrypoint for registration
├── templates/           # User scaffolding
│   └── main.nu         # Template for user modules
├── main.go             # SDK implementation
└── dagger.json
```

### Development

```bash
# Test changes
cd /tmp/test-module
dagger init --sdk=/path/to/dagger/sdk/nushell/runtime
dagger functions

# Run checks
dagger check
```

## Resources

- [Nushell Documentation](https://www.nushell.sh/book/)
- [Dagger Documentation](https://docs.dagger.io)
- [Implementation Gaps](./IMPLEMENTATION_GAPS.md)

## License

Apache 2.0
