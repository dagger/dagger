# Real-World Examples

Practical examples demonstrating common use cases.

## Example 1: Multi-Language CI Pipeline

Build and test projects in multiple languages:

```nushell
# Build Node.js application
export def build-node [source: Directory] {
    dag container
    | container from "node:20"
    | with-directory "/app" $source
    | with-exec ["npm", "install"]
    | with-exec ["npm", "run", "build"]
    | get-directory "/app/dist"
}

# Build Go application
export def build-go [source: Directory] {
    dag container
    | container from "golang:1.21"
    | with-directory "/src" $source
    | with-exec ["go", "build", "-o", "app"]
    | get-file "/src/app"
}

# Build Python application
export def build-python [source: Directory] {
    dag container
    | container from "python:3.11"
    | with-directory "/app" $source
    | with-exec ["pip", "install", "-r", "requirements.txt"]
    | with-exec ["python", "-m", "build"]
    | get-directory "/app/dist"
}
```

## Example 2: Docker Image Build and Push

Build and publish Docker images:

```nushell
# Build optimized container image
export def build-image [
    source: Directory
    platform: string = "linux/amd64"
] {
    dag container --platform $platform
    | container from "node:20-alpine"
    | with-directory "/app" $source
    | with-workdir "/app"
    | with-exec ["npm", "ci", "--production"]
    | with-entrypoint ["node", "server.js"]
    | with-exposed-port 3000
}

# Publish to registry
export def publish [
    source: Directory
    registry: string
    tag: string
    username: string
    password: Secret
] {
    let image = (build-image $source)
    
    $image
    | container with-registry-auth $registry $username $password
    | container publish $"($registry):($tag)"
}
```

## Example 3: Database Integration Testing

Run tests with database services:

```nushell
# Test with PostgreSQL
export def test-with-postgres [source: Directory] {
    # Start PostgreSQL service
    let db = (
        dag container
        | container from "postgres:15"
        | with-env-variable "POSTGRES_PASSWORD" "test"
        | with-env-variable "POSTGRES_DB" "testdb"
        | with-exposed-port 5432
        | container as-service
    )
    
    # Run tests
    dag container
    | container from "node:20"
    | with-service-binding "postgres" $db
    | with-directory "/app" $source
    | with-workdir "/app"
    | with-env-variable "DATABASE_URL" "postgresql://postgres:test@postgres:5432/testdb"
    | with-exec ["npm", "install"]
    | with-exec ["npm", "test"]
    | stdout
}
```

## Example 4: Monorepo Build Pipeline

Build multiple packages in a monorepo:

```nushell
# Build all packages
export def build-monorepo [source: Directory] {
    let packages = ["api", "web", "shared"]
    
    $packages | each {|pkg|
        {
            package: $pkg
            result: (
                dag container
                | container from "node:20"
                | with-directory "/workspace" $source
                | with-workdir $"/workspace/packages/($pkg)"
                | with-exec ["npm", "install"]
                | with-exec ["npm", "run", "build"]
                | get-directory $"/workspace/packages/($pkg)/dist"
            )
        }
    }
}
```

## Example 5: End-to-End Testing

Run browser-based tests:

```nushell
# E2E tests with Playwright
export def test-e2e [source: Directory] {
    # Start application
    let app = (
        dag container
        | container from "node:20"
        | with-directory "/app" $source
        | with-workdir "/app"
        | with-exec ["npm", "install"]
        | with-exec ["npm", "run", "build"]
        | with-exposed-port 3000
        | with-exec ["npm", "start"]
        | container as-service
    )
    
    # Run tests against app
    dag container
    | container from "mcr.microsoft.com/playwright:latest"
    | with-service-binding "app" $app
    | with-directory "/tests" $source
    | with-workdir "/tests"
    | with-env-variable "APP_URL" "http://app:3000"
    | with-exec ["npm", "install"]
    | with-exec ["npx", "playwright", "test"]
    | stdout
}
```

See [Reference](reference.md) for complete API documentation.
