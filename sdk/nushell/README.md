# Dagger Nushell SDK

Official Nushell SDK for Dagger - build powerful CI/CD pipelines using Nushell's elegant shell syntax.

## Installation

Initialize a new Dagger module with the Nushell SDK:

```bash
dagger init --sdk=nushell
```

This creates:
- `dagger.json` - Module configuration
- `main.nu` - Your module's main file with example functions

## Quick Start

After initialization, your `main.nu` will contain two example functions:

```nushell
# Returns a container that echoes whatever string argument is provided
export def container-echo [
    string_arg: string
] {
    dag container
    | container from "alpine:latest"
    | container with-exec ["echo", $string_arg]
}

# Returns lines that match a pattern in files
export def grep-dir [
    directory_arg: Directory
    pattern: string
] {
    dag container
    | container from "alpine:latest"
    | container with-mounted-directory "/mnt" $directory_arg
    | container with-workdir "/mnt"
    | container with-exec ["grep", "-R", $pattern, "."]
    | container stdout
}
```

## Usage

### List available functions

```bash
dagger functions
```

### Call a function

```bash
dagger call container-echo --string-arg="Hello, Dagger!"
dagger call grep-dir --directory-arg=. --pattern="export"
```

## Writing Dagger Modules in Nushell

### Function Syntax

Export functions from your module using `export def`:

```nushell
# Function description goes here
# Returns: Container
export def my-function [
    param1: string  # Parameter description
    param2: int     # Another parameter
] {
    # Function body
}
```

### Type Annotations

Use comment-based type hints for return types:

```nushell
# Returns: string
# Returns: Container
# Returns: Directory
# Returns: File
```

### Using the Dagger API

Access the Dagger API through the `dag` command and pipe through operations:

```nushell
# Create a container
dag container | container from "alpine:latest"

# Chain operations
dag container
| container from "node:20"
| container with-exec ["npm", "install"]
| container with-exec ["npm", "test"]
| container stdout
```

### Common Patterns

#### Build a container

```nushell
export def build [] {
    dag container
    | container from "golang:1.21"
    | container with-workdir "/src"
    | container with-directory "/src" (dag host directory ".")
    | container with-exec ["go", "build", "-o", "app"]
    | container file "/src/app"
}
```

#### Work with directories

```nushell
export def package [
    source: Directory
] {
    dag container
    | container from "node:20"
    | container with-directory "/app" $source
    | container with-workdir "/app"
    | container with-exec ["npm", "ci"]
    | container with-exec ["npm", "run", "build"]
    | container directory "/app/dist"
}
```

#### Run tests

```nushell
export def test [
    source: Directory
] {
    dag container
    | container from "python:3.11"
    | container with-directory "/app" $source
    | container with-workdir "/app"
    | container with-exec ["pip", "install", "-r", "requirements.txt"]
    | container with-exec ["pytest"]
    | container stdout
}
```

## Nushell Conventions

The Nushell SDK follows Nushell naming conventions:

- **Functions**: Use kebab-case (`my-function`, `build-image`)
- **Parameters**: Use snake_case (`string_arg`, `source_dir`)
- **Pipeline-first**: Leverage Nushell's pipe operator for chaining

## Documentation

For more information:
- [Nushell Documentation](https://www.nushell.sh/book/)
- [Dagger Documentation](https://docs.dagger.io/)
- [Dagger SDK Development](https://docs.dagger.io/sdk/development)

## Examples

See the [examples](../../examples/) directory for real-world Nushell modules.

## Contributing

Contributions welcome! See [CONTRIBUTING.md](../CONTRIBUTING.md) for guidelines.

## License

Apache 2.0
