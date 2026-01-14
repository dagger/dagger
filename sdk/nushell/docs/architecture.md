# Architecture

Deep dive into the Nushell SDK architecture and internals.

## Overview

The Nushell SDK bridges Nushell's shell scripting with Dagger's container operations through a Go-based runtime executor.

```
┌─────────────┐
│   User      │
│  Module     │ (main.nu)
│  (Nushell)  │
└──────┬──────┘
       │ calls
       ▼
┌─────────────┐
│   Runtime   │
│   Library   │ (dag.nu, container.nu, etc.)
│  (Nushell)  │
└──────┬──────┘
       │ invokes
       ▼
┌─────────────┐
│  Executor   │
│   Runtime   │ (executor.go)
│     (Go)    │
└──────┬──────┘
       │ calls
       ▼
┌─────────────┐
│   Dagger    │
│   Engine    │
│  (GraphQL)  │
└─────────────┘
```

## Components

### 1. Go Runtime Executor

**Location**: `sdk/nushell/runtime/runtime/executor.go`

The executor handles:
- **Function Discovery**: Scans Nushell files for exported functions
- **Parameter Marshaling**: Converts between Dagger types and Nushell values
- **Execution**: Runs Nushell code with proper context
- **Return Value Handling**: Converts Nushell output back to Dagger types

Key features:
- Supports all Dagger object types (Container, Directory, File, etc.)
- Handles optional parameters with default values
- Type coercion between Nushell and Dagger
- Check annotation discovery (`# @check`)

### 2. Nushell Runtime Library

**Location**: `sdk/nushell/runtime/runtime/dag/`

Modular structure:
- `core.nu` - Core utilities and type detection
- `container.nu` - Container operations
- `directory.nu` - Directory operations
- `file.nu` - File operations
- `git.nu` - Git operations
- `host.nu` - Host operations
- `cache.nu` - Cache volume operations
- `secret.nu` - Secret operations
- `module.nu` - Module operations
- `check.nu` - Check operations
- `wrappers.nu` - Multi-type wrapper functions

### 3. Type System

Objects are represented as Nushell records:

```nushell
{
    id: "Container:abc123..."
    __type: "Container"
}
```

The `__type` field enables:
- Runtime type detection
- Multi-type wrapper functions
- Type-safe operations

### 4. Wrapper Layer

Wrappers provide clean pipeline syntax:

```nushell
# Without wrappers (namespace syntax):
dag container | container from "alpine" | container with-exec ["echo", "hi"]

# With wrappers (clean pipeline syntax):
dag container | container from "alpine" | with-exec ["echo", "hi"]
```

Wrappers intelligently dispatch based on object type:

```nushell
# with-file works on both Container and Directory
$container | with-file "/app/config" $file  # Mounts file
$directory | with-file "config" $file       # Adds file
```

## Execution Flow

### Function Call

1. User runs: `dagger call my-function --arg=value`
2. Dagger Engine calls Go executor
3. Executor discovers `my-function` in main.nu
4. Executor marshals arguments to Nushell format
5. Executor runs Nushell with function call
6. Nushell executes using runtime library
7. Runtime makes GraphQL calls to Dagger Engine
8. Executor unmarshals return value
9. Result returned to user

### Check Execution

1. User runs: `dagger check`
2. Executor scans for functions with `# @check`
3. Each check function is executed
4. Return value (Container) is synced
5. Exit code determines pass/fail

## Code Generation

**Location**: `sdk/nushell/runtime/runtime/dag.nu`

Generated file contains:
- All Dagger API functions as Nushell wrappers
- Type-safe function signatures
- Documentation from GraphQL schema

Generation happens during `dagger develop`.

## Error Handling

Errors flow through the stack:

```
Nushell Error
    ↓
Go Executor catches
    ↓
Dagger Engine receives
    ↓
User sees formatted error
```

The executor provides context:
- Function name
- Line numbers
- Parameter values (sanitized)

## Performance Optimizations

1. **Lazy Evaluation**: Operations aren't executed until needed
2. **Automatic Parallelization**: Independent operations run concurrently
3. **Caching**: Results cached automatically by Dagger Engine
4. **Minimal Overhead**: Direct Nu→Go→GraphQL path

## Future Enhancements

Potential improvements:
- Native Nushell plugin for faster execution
- Better IDE integration
- Enhanced type inference
- Custom types and interfaces

See [Reference](reference.md) for API documentation.
