# Testing Guide

Learn how to write and run tests for your Nushell Dagger modules.

## Overview

Dagger supports two types of testing:
1. **Checks** - Validation functions using `@check` annotation
2. **Unit Tests** - Regular Nushell test functions

## Writing Checks

Checks are functions marked with `# @check` that validate your module works correctly.

### Basic Check

```nushell
# @check
export def verify-build [] {
    container from "alpine:3"
    | with-exec ["echo", "Build successful!"]
}
```

Run it:
```bash
dagger check
```

### Check Patterns

**Exit Code Validation:**
```nushell
# @check
export def check-tests-pass [] {
    container from "node:20"
    | with-directory "/app" (host directory ".")
    | with-workdir "/app"
    | with-exec ["npm", "test"]
    # Container exit code determines pass/fail
}
```

**File Output Validation:**
```nushell
# @check
export def check-output-exists [] {
    let result = (
        container from "alpine:3"
        | with-exec ["sh", "-c", "echo 'test' > /output.txt"]
        | get-file "/output.txt"
        | file contents
    )
    
    if ($result | str contains "test") {
        container from "alpine:3" | with-exec ["echo", "✓ Output verified"]
    } else {
        container from "alpine:3" | with-exec ["sh", "-c", "exit 1"]
    }
}
```

**Multi-Step Validation:**
```nushell
# @check  
export def check-full-pipeline [] {
    # Build
    let binary = (
        container from "golang:1.21"
        | with-directory "/src" (host directory ".")
        | with-workdir "/src"
        | with-exec ["go", "build", "-o", "app"]
        | get-file "/src/app"
    )
    
    # Test binary works
    container from "alpine:3"
    | with-file "/app" $binary
    | with-exec ["chmod", "+x", "/app"]
    | with-exec ["/app", "--version"]
}
```

## Unit Testing

While checks validate end-to-end functionality, you can also write unit-style tests.

### Test Structure

```nushell
# Test helper functions
def test-container-creation [] {
    let result = (container from "alpine:3" | get-object-type)
    assert ($result == "Container")
}

def test-file-operations [] {
    let content = "test data"
    let file = (
        directory
        | with-new-file "test.txt" $content
        | get-file "test.txt"
        | file contents
    )
    assert ($content == $file)
}

# Main test runner
export def run-tests [] {
    print "Running tests..."
    test-container-creation
    test-file-operations
    print "✓ All tests passed"
}
```

## Testing Best Practices

### 1. Fast Feedback

Use small, focused checks:

```nushell
# Good - Fast and specific
# @check
export def check-lint [] {
    container from "node:20"
    | with-directory "/app" (host directory ".")
    | with-exec ["npm", "run", "lint"]
}

# @check
export def check-unit-tests [] {
    container from "node:20"
    | with-directory "/app" (host directory ".")
    | with-exec ["npm", "run", "test:unit"]
}
```

### 2. Use Services for Integration Tests

```nushell
# @check
export def check-with-database [] {
    let db = (
        container from "postgres:15"
        | with-env-variable "POSTGRES_PASSWORD" "test"
        | with-exposed-port 5432
        | as-service
    )
    
    container from "node:20"
    | with-service-binding "postgres" $db
    | with-directory "/app" (host directory ".")
    | with-env-variable "DATABASE_URL" "postgresql://postgres:test@postgres:5432/test"
    | with-exec ["npm", "run", "test:integration"]
}
```

### 3. Cache Dependencies

```nushell
# @check
export def check-cached-build [] {
    let cache = (cache-volume "npm-cache")
    
    container from "node:20"
    | with-mounted-cache "/root/.npm" $cache
    | with-directory "/app" (host directory ".")
    | with-workdir "/app"
    | with-exec ["npm", "ci"]
    | with-exec ["npm", "test"]
}
```

### 4. Parallel Execution

Dagger automatically parallelizes independent checks:

```nushell
# @check - Runs in parallel with other checks
export def check-lint [] {
    container from "node:20"
    | with-directory "/app" (host directory ".")
    | with-exec ["npm", "run", "lint"]
}

# @check - Runs in parallel with other checks
export def check-typescript [] {
    container from "node:20"
    | with-directory "/app" (host directory ".")
    | with-exec ["npm", "run", "type-check"]
}

# @check - Runs in parallel with other checks
export def check-unit-tests [] {
    container from "node:20"
    | with-directory "/app" (host directory ".")
    | with-exec ["npm", "test"]
}
```

## Testing Patterns

### Pattern 1: Matrix Testing

Test multiple versions:

```nushell
def test-node-version [version: string] {
    container from $"node:($version)"
    | with-directory "/app" (host directory ".")
    | with-exec ["npm", "test"]
}

# @check
export def check-node-18 [] { test-node-version "18" }

# @check
export def check-node-20 [] { test-node-version "20" }

# @check
export def check-node-21 [] { test-node-version "21" }
```

### Pattern 2: Snapshot Testing

Compare output against expected:

```nushell
# @check
export def check-output-format [] {
    let output = (
        container from "alpine:3"
        | with-exec ["echo", "Hello, Dagger!"]
        | stdout
    )
    
    let expected = "Hello, Dagger!\n"
    
    if ($output == $expected) {
        container from "alpine:3" | with-exec ["echo", "✓ Output matches"]
    } else {
        container from "alpine:3" 
        | with-exec ["sh", "-c", $"echo 'Expected: ($expected)'; echo 'Got: ($output)'; exit 1"]
    }
}
```

### Pattern 3: Error Testing

Verify error handling:

```nushell
# @check
export def check-handles-errors [] {
    # This should fail gracefully
    try {
        container from "nonexistent:image"
        | with-exec ["echo", "Should not reach here"]
        | stdout
        
        # If we get here, the check should fail
        container from "alpine:3" | with-exec ["sh", "-c", "exit 1"]
    } catch {
        # Expected behavior - error was caught
        container from "alpine:3" | with-exec ["echo", "✓ Error handled correctly"]
    }
}
```

## Running Tests

### Run All Checks

```bash
dagger check
```

### Run Specific Check

```bash
dagger call check-lint
```

### Run in CI

```yaml
# .github/workflows/test.yml
name: Test
on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: dagger/dagger-for-github@v6
        with:
          version: "latest"
      - name: Run checks
        run: dagger check
```

## Debugging Failed Checks

### View Container Logs

```nushell
# @check
export def debug-check [] {
    let container_obj = (
        container from "node:20"
        | with-directory "/app" (host directory ".")
        | with-exec ["npm", "test"]
    )
    
    # Get both stdout and stderr for debugging
    print ($container_obj | stdout)
    print ($container_obj | stderr)
    
    $container_obj
}
```

### Interactive Debugging

```bash
# Get a shell in the container
dagger call debug-check terminal
```

## Testing Checklist

- [ ] Write check for build process
- [ ] Write check for unit tests
- [ ] Write check for integration tests
- [ ] Write check for linting
- [ ] Use caching for faster runs
- [ ] Test multiple versions/platforms
- [ ] Add checks to CI pipeline
- [ ] Document expected behavior

## See Also

- **[Quickstart Guide](quickstart.md)** - Basic examples
- **[API Reference](reference.md)** - Complete function reference  
- **[Examples](examples.md)** - Real-world examples
- **[Architecture](architecture.md)** - How checks work internally
