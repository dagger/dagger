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
    container from "alpine:latest"
    | with-exec ["echo", $string_arg]
}

# Returns lines that match a pattern in files
export def grep-dir [
    directory_arg: Directory
    pattern: string
] {
    container from "alpine:latest"
    | with-mounted-directory "/mnt" $directory_arg
    | with-workdir "/mnt"
    | with-exec ["grep", "-R", $pattern, "."]
    | stdout
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

### @check Annotation

Mark functions with `# @check` to have them automatically discovered and run by `dagger check`:

```nushell
# @check
# Verify the container from operation works correctly
export def "check-container-from" []: nothing -> record {
    let c = (container from "alpine")
    if (($c | get -i id | is-not-null) and ($c | get -i __type) == "Container") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}
```

A check function should return a container that exits 0 on pass and non-zero on fail.

### Using the Dagger API

Access the Dagger API through the `dag` command and pipe through operations:

```nushell
container from "alpine:latest"
```

```nushell
container from "alpine:latest"
| with-exec ["echo", "hello"]
| stdout
```

### Common Patterns

#### Build a container

```nushell
export def build [] {
    container from "golang:1.21"
    | with-workdir "/src"
    | with-directory "/src" (host directory ".")
    | with-exec ["go", "build", "-o", "app"]
    | get-file "/src/app"
}
```

#### Work with directories

```nushell
export def package [
    source: Directory
] {
    container from "node:20"
    | with-directory "/app" $source
    | with-workdir "/app"
    | with-exec ["npm", "ci"]
    | with-exec ["npm", "run", "build"]
    | get-directory "/app/dist"
}
```

## Testing

### Writing Tests

The Nushell SDK supports test functions with the `# @check` annotation. Dagger automatically discovers and runs all functions marked with `@check`:

```nushell
# @check
# Verify container from works correctly
export def "test-container-from" []: nothing -> record {
    let c = (container from "alpine")
    if (($c | get -i id | is-not-null) and ($c | get -i __type) == "Container") {
        container from "alpine" | with-exec ["true"]
    } else {
        container from "alpine" | with-exec ["false"]
    }
}
```

### Running Tests

```bash
# List all available checks
dagger check -l

# Run all checks
dagger check

# Run specific check
dagger call my-module test-container-from
```

### Test Suite Structure

```
tests/
├── core.nu              # Tests for __type metadata and get-object-type
├── wrappers.nu          # Tests for multi-type wrapper functions
├── objects.nu           # Tests for @object, @method, @field patterns
├── operations/          # Operation-specific tests
│   ├── container.nu
│   ├── directory.nu
│   └── file.nu
└── integration/         # End-to-end workflow tests
    └── pipelines.nu
```

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
