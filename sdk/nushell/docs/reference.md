# API Reference

Complete reference for the Dagger Nushell SDK.

## Core Functions

### dag
Entry point to the Dagger API. Access all Dagger functionality through this namespace.

```nushell
# Access container API
dag container

# Access directory API  
dag directory

# Access Git API
dag git "https://github.com/user/repo"
```

### get-object-type
Detect the type of a Dagger object at runtime.

```nushell
let obj = (dag container from "alpine")
let type = (get-object-type $obj)  # Returns: "Container"
```

## Container Operations

### Core Container Functions

#### container from
Create a container from a base image.

```nushell
dag container | container from "alpine:latest"
```

#### container with-exec
Execute a command in the container.

```nushell
container with-exec ["echo", "hello"]
```

#### container with-directory
Mount a directory into the container.

```nushell
container with-directory "/app" $source_dir
```

####container with-file
Mount a file into the container.

```nushell
container with-file "/app/config.txt" $config_file
```

#### container with-env-variable
Set an environment variable.

```nushell
container with-env-variable "APP_ENV" "production"
```

#### container with-workdir
Set the working directory.

```nushell
container with-workdir "/app"
```

#### container with-entrypoint
Set the container entrypoint.

```nushell
container with-entrypoint ["python", "app.py"]
```

#### container with-exposed-port
Expose a port.

```nushell
container with-exposed-port 8080
```

### Container Outputs

#### container stdout
Get standard output from the last executed command.

```nushell
container stdout  # Returns: string
```

#### container stderr
Get standard error from the last executed command.

```nushell
container stderr  # Returns: string
```

#### container file
Export a file from the container.

```nushell
container file "/app/output.txt"  # Returns: File
```

#### container directory
Export a directory from the container.

```nushell
container directory "/app/dist"  # Returns: Directory
```

### Container Services

#### container as-service
Convert container to a service for use by other containers.

```nushell
let db = (
    dag container
    | container from "postgres:15"
    | container with-env-variable "POSTGRES_PASSWORD" "secret"
    | container with-exposed-port 5432
    | container as-service
)
```

#### container with-service-binding
Bind a service to the container.

```nushell
container with-service-binding "db" $db_service
```

## Directory Operations

### directory entries
List entries in a directory.

```nushell
$dir | directory entries  # Returns: list<string>
```

### directory with-file
Add a file to a directory.

```nushell
directory with-file "config.txt" $file
```

### directory with-directory
Add a subdirectory to a directory.

```nushell
directory with-directory "subdir" $other_dir
```

### directory with-new-file
Create a new file in a directory with content.

```nushell
directory with-new-file "README.md" "# My Project"
```

### directory with-new-directory
Create a new subdirectory.

```nushell
directory with-new-directory "src"
```

### directory without-directory
Remove a subdirectory.

```nushell
directory without-directory "node_modules"
```

### directory without-file
Remove a file.

```nushell
directory without-file ".DS_Store"
```

## File Operations

### file contents
Read file contents as a string.

```nushell
$file | file contents  # Returns: string
```

### file name
Get the file name.

```nushell
$file | file name  # Returns: string
```

### file size
Get file size in bytes.

```nushell
$file | file size  # Returns: int
```

## Git Operations

### dag git
Clone a Git repository.

```nushell
dag git "https://github.com/user/repo"  # Returns: GitRepository
```

### git branch
Select a specific branch.

```nushell
git branch "main"
```

### git commit
Select a specific commit.

```nushell
git commit "abc123"
```

### git tag
Select a specific tag.

```nushell
git tag "v1.0.0"
```

### git tree
Get the repository tree as a directory.

```nushell
git tree  # Returns: Directory
```

## Host Operations

### dag host
Access the host environment.

```nushell
dag host
```

### host directory
Access a directory on the host.

```nushell
host directory "."  # Current directory
host directory "/path/to/dir"
```

### host file
Access a file on the host.

```nushell
host file "./config.yaml"
```

### host env-variable
Read a host environment variable.

```nushell
host env-variable "PATH"
```

## Cache Operations

### dag cache-volume
Create or access a cache volume.

```nushell
let cache = (dag cache-volume "npm-cache")
```

### container with-mounted-cache
Mount a cache volume into a container.

```nushell
container with-mounted-cache "/root/.npm" $cache
```

## Secret Operations

### dag set-secret
Create a secret from a value.

```nushell
let secret = (dag set-secret "api-key" "secret-value")
```

### container with-secret-variable
Add a secret as an environment variable.

```nushell
container with-secret-variable "API_KEY" $secret
```

## Module Operations

### dag current-module
Access the current module.

```nushell
dag current-module
```

### module source
Get the module source directory.

```nushell
module source  # Returns: Directory
```

### module with-source
Update the module source.

```nushell
module with-source $new_source
```

## Check Operations

### @check Annotation
Mark a function as a check (validation).

```nushell
# @check
export def verify-build [] {
    dag container
    | container from "alpine"
    | container with-exec ["echo", "Check passed!"]
}
```

Run checks:
```bash
dagger check
```

## Wrapper Functions

Wrapper functions provide clean pipeline syntax and work across multiple types.

### with-directory
Mount a directory (works on Container and Directory).

```nushell
# On container
$container | with-directory "/app" $dir

# On directory
$directory | with-directory "subdir" $other_dir
```

### with-file
Mount a file (works on Container and Directory).

```nushell
$container | with-file "/app/config" $file
$directory | with-file "config.txt" $file
```

### with-new-file
Create a new file with content.

```nushell
$container | with-new-file "/app/README.md" "# Docs"
$directory | with-new-file "README.md" "# Docs"
```

### get-file
Get a file from a container or directory.

```nushell
$container | get-file "/app/output.txt"
$directory | get-file "output.txt"
```

### get-directory
Get a subdirectory.

```nushell
$container | get-directory "/app/dist"
$directory | get-directory "dist"
```

### with-exec
Execute a command (Container only).

```nushell
$container | with-exec ["npm", "run", "build"]
```

### stdout
Get standard output (Container only).

```nushell
$container | stdout
```

### stderr  
Get standard error (Container only).

```nushell
$container | stderr
```

### path-exists
Check if a path exists.

```nushell
$container | path-exists "/app/output.txt"  # Returns: bool
$directory | path-exists "README.md"  # Returns: bool
```

### glob-files
List files matching a pattern.

```nushell
$directory | glob-files "**/*.nu"  # Returns: list<string>
```

## Type System

### Object Structure

All Dagger objects are Nushell records with:
- `id`: Unique identifier (string)
- `__type`: Type name (string)

```nushell
{
    id: "Container:abc123..."
    __type: "Container"
}
```

### Type Detection

Use `get-object-type` to detect types:

```nushell
let type = (get-object-type $obj)

match $type {
    "Container" => { "It's a container!" }
    "Directory" => { "It's a directory!" }
    "File" => { "It's a file!" }
    _ => { "Unknown type" }
}
```

### Available Types

- `Container` - Container operations
- `Directory` - Directory operations
- `File` - File operations
- `GitRepository` - Git repository
- `GitRef` - Git reference (branch/tag/commit)
- `Secret` - Secret value
- `CacheVolume` - Cache volume
- `Service` - Container service
- `Module` - Dagger module

## Function Naming Conventions

The SDK follows Nushell conventions with some adjustments to avoid conflicts:

- **Kebab-case**: `with-exec`, `with-directory` (Nushell standard)
- **Avoiding conflicts**:
  - `get-file` instead of `file` (conflicts with `/usr/bin/file`)
  - `glob-files` instead of `glob` (conflicts with Nushell builtin)
  - `path-exists` instead of `exists` (more explicit)
  - `get-directory` instead of `directory` (consistency with get-file)

## Error Handling

```nushell
# Try-catch pattern
try {
    let result = (
        dag container
        | container from "invalid:image"
        | container stdout
    )
    $result
} catch { |e|
    error make {msg: $"Container failed: ($e)"}
}
```

## Advanced Patterns

### Composition

```nushell
# Define reusable functions
def build-base []: nothing -> record {
    dag container
    | container from "node:20"
    | with-exec ["npm", "install"]
}

def run-tests []: record -> string {
    with-exec ["npm", "test"]
    | stdout
}

# Compose them
build-base | run-tests
```

### Parallel Operations

Dagger automatically parallelizes independent operations:

```nushell
# These run in parallel
let unit = (build-base | with-exec ["npm", "run", "test:unit"] | stdout)
let lint = (build-base | with-exec ["npm", "run", "lint"] | stdout)

{unit: $unit, lint: $lint}
```

## See Also

- **[Quickstart Guide](quickstart.md)** - Getting started tutorial
- **[Examples](examples.md)** - Real-world examples  
- **[Architecture](architecture.md)** - SDK internals
- **[Testing Guide](testing.md)** - Writing tests
