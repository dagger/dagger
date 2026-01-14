# Quickstart Guide

Build your first Dagger pipeline with Nushell in minutes!

## Prerequisites

- Nushell installed (v0.99.0+)
- Dagger CLI installed (v0.19.0+)
- Docker running locally

See [Installation Guide](installation.md) for setup instructions.

## Step 1: Create Your Module

```bash
mkdir hello-dagger
cd hello-dagger
dagger init --sdk=nushell
```

This creates a basic module with example functions.

## Step 2: Your First Function

Open `main.nu` and replace the contents with:

```nushell
# Build a simple greeting message
export def hello [
    name: string = "World"  # Name to greet (default: World)
] {
    $"Hello, ($name)!"
}
```

## Step 3: Run It

```bash
dagger call hello --name="Dagger"
```

Output:
```
Hello, Dagger!
```

## Step 4: Add Container Operations

Let's build something more interesting - a container that greets us:

```nushell
# Greet using a container
export def hello-container [
    name: string = "World"
] {
    dag container
    | container from "alpine:latest"
    | container with-exec ["echo", $"Hello, ($name)!"]
    | container stdout
}
```

Run it:
```bash
dagger call hello-container --name="Nushell"
```

## Step 5: Working with Files

Create a function that reads and processes files:

```nushell
# Count lines in a directory
export def count-lines [
    source: Directory
] {
    dag container
    | container from "alpine:latest"
    | container with-mounted-directory "/src" $source
    | container with-workdir "/src"
    | container with-exec ["sh", "-c", "find . -type f | xargs wc -l"]
    | container stdout
}
```

Use it:
```bash
# Count lines in current directory
dagger call count-lines --source=.
```

## Step 6: Pipeline Composition

Nushell's pipeline syntax makes complex operations elegant:

```nushell
# Build and test a Go application
export def build-and-test [
    source: Directory
] {
    # Build the application
    let binary = (
        dag container
        | container from "golang:1.21"
        | container with-mounted-directory "/src" $source
        | container with-workdir "/src"
        | container with-exec ["go", "build", "-o", "app"]
        | container file "/src/app"
    )
    
    # Run tests
    dag container
    | container from "golang:1.21"
    | container with-mounted-directory "/src" $source
    | container with-workdir "/src"
    | container with-exec ["go", "test", "./..."]
    | container stdout
}
```

## Step 7: Using Type Metadata

The SDK provides type metadata for intelligent operations:

```nushell
# Smart file or directory handler
export def process-path [
    path_obj: any  # Can be File or Directory
] {
    let obj_type = (get-object-type $path_obj)
    
    match $obj_type {
        "File" => {
            # Process as file
            $path_obj | file contents
        }
        "Directory" => {
            # Process as directory
            $path_obj | directory entries
        }
        _ => {
            error make {msg: $"Unknown type: ($obj_type)"}
        }
    }
}
```

## Step 8: Multi-Stage Builds

Build efficient container images:

```nushell
# Multi-stage Python build
export def build-python-app [
    source: Directory
] {
    # Build stage: install dependencies
    let deps = (
        dag container
        | container from "python:3.11-slim"
        | container with-mounted-directory "/app" $source
        | container with-workdir "/app"
        | container with-exec ["pip", "install", "-r", "requirements.txt"]
        | container directory "/usr/local/lib/python3.11/site-packages"
    )
    
    # Runtime stage: minimal image with deps
    dag container
    | container from "python:3.11-slim"
    | container with-directory "/packages" $deps
    | container with-mounted-directory "/app" $source
    | container with-workdir "/app"
    | container with-env-variable "PYTHONPATH" "/packages"
    | container with-entrypoint ["python", "app.py"]
}
```

## Step 9: Adding Checks

Add validation checks to your module:

```nushell
# @check
# Verify the build works
export def check-build [] {
    dag container
    | container from "alpine:latest"
    | container with-exec ["echo", "Build check passed!"]
}
```

Run checks:
```bash
dagger check
```

## Step 10: Working with Secrets

Handle sensitive data securely:

```nushell
# Deploy with credentials
export def deploy [
    source: Directory
    api_key: Secret
] {
    dag container
    | container from "alpine:latest"
    | container with-mounted-directory "/app" $source
    | container with-secret-variable "API_KEY" $api_key
    | container with-exec ["./deploy.sh"]
    | container stdout
}
```

Use it:
```bash
# Set secret from environment
dagger call deploy \
    --source=. \
    --api-key=env:API_KEY

# Or from file
dagger call deploy \
    --source=. \
    --api-key=file:./api-key.txt
```

## Common Patterns

### Pattern 1: Caching Dependencies

```nushell
export def build-with-cache [
    source: Directory
] {
    # Create a cache volume for dependencies
    let cache = (dag cache-volume "npm-cache")
    
    dag container
    | container from "node:20"
    | container with-mounted-cache "/root/.npm" $cache
    | container with-mounted-directory "/app" $source
    | container with-workdir "/app"
    | container with-exec ["npm", "install"]
    | container with-exec ["npm", "run", "build"]
    | container directory "/app/dist"
}
```

### Pattern 2: Parallel Execution

```nushell
export def test-all [
    source: Directory
] {
    # Run unit and integration tests in parallel
    # Note: Dagger automatically parallelizes independent operations
    
    let unit_results = (
        dag container
        | container from "node:20"
        | container with-mounted-directory "/app" $source
        | container with-workdir "/app"
        | container with-exec ["npm", "run", "test:unit"]
        | container stdout
    )
    
    let integration_results = (
        dag container
        | container from "node:20"
        | container with-mounted-directory "/app" $source
        | container with-workdir "/app"
        | container with-exec ["npm", "run", "test:integration"]
        | container stdout
    )
    
    {
        unit: $unit_results
        integration: $integration_results
    }
}
```

### Pattern 3: Service Dependencies

```nushell
export def test-with-database [
    source: Directory
] {
    # Start a PostgreSQL service
    let db = (
        dag container
        | container from "postgres:15"
        | container with-env-variable "POSTGRES_PASSWORD" "test"
        | container with-exposed-port 5432
        | container as-service
    )
    
    # Run tests with database
    dag container
    | container from "node:20"
    | container with-service-binding "db" $db
    | container with-mounted-directory "/app" $source
    | container with-workdir "/app"
    | container with-env-variable "DATABASE_URL" "postgresql://postgres:test@db:5432/test"
    | container with-exec ["npm", "run", "test"]
    | container stdout
}
```

## Next Steps

You've learned the basics! Continue with:

- **[API Reference](reference.md)** - Complete function reference
- **[Examples](examples.md)** - More real-world examples
- **[Architecture](architecture.md)** - Deep dive into SDK internals
- **[Testing Guide](testing.md)** - Write tests for your modules

## Tips and Best Practices

1. **Use Pipeline Syntax**: Nushell's `|` operator makes chains readable
2. **Leverage Type Metadata**: Use `get-object-type` for dynamic behavior
3. **Cache Dependencies**: Use `cache-volume` for faster builds
4. **Compose Functions**: Break complex pipelines into smaller functions
5. **Add Checks**: Use `@check` annotations for validation
6. **Use Secrets**: Never hardcode sensitive data
7. **Test Locally**: Use `dagger call` to test before CI

## Troubleshooting

**Error: "cannot find dag command"**
- The `dag` function is automatically available in Dagger modules
- Make sure you're running via `dagger call`, not directly with `nu`

**Pipeline doesn't execute**
- Remember to call `.stdout` or `.sync` to trigger execution
- Example: `container with-exec [...] | container stdout`

**Type errors with objects**
- Nushell objects are records with `id` and `__type` fields
- Use the provided wrapper functions for clean syntax

## Getting Help

- **Documentation**: [docs.dagger.io](https://docs.dagger.io)
- **Discord**: [discord.gg/dagger-io](https://discord.gg/dagger-io)
- **GitHub**: [github.com/dagger/dagger](https://github.com/dagger/dagger)
