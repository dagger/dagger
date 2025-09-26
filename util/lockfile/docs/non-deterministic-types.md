# Non-Deterministic Type Validation in Lockfile Package

## Overview

The lockfile package now validates that all arguments and return values can be JSON-encoded deterministically. This ensures that the same inputs always produce the same JSON representation, which is critical for reliable cache key generation and comparison.

## Problem

Maps (objects) in Go have non-deterministic iteration order. When `json.Marshal` encodes a map, the order of keys in the resulting JSON can vary between runs, even for identical map contents. This makes maps unsuitable for use in cache keys where deterministic encoding is required.

## Solution

The package now includes validation that rejects any values containing maps at any nesting level. This validation occurs in both `Set` and `Get` operations.

### Rejected Types

The following types are **not allowed** as they cannot be JSON-encoded deterministically:

- Maps (`map[string]interface{}`, `map[string]string`, etc.)
- Structs containing map fields
- Arrays/slices containing maps
- Any nested structure containing maps at any level

### Allowed Types

The following types **are allowed** as they produce deterministic JSON:

- Primitive types: `string`, `int`, `float64`, `bool`, `nil`
- Arrays and slices (as long as they don't contain maps)
- Structs (as long as they don't contain map fields)
- `json.RawMessage` (treated as raw bytes)
- Any nested combination of the above

## API Changes

### Set Method

The `Set` method now returns an error:

```go
func (l *Lockfile) Set(module, function string, args []FunctionArg, result FunctionResult) error
```

It will return an error if:
- Any argument contains non-deterministic types (with error message indicating which argument)
- The result contains non-deterministic types

### Get Method

The `Get` method silently returns `nil` if arguments contain non-deterministic types, treating it as a cache miss.

## Usage Examples

### Valid Usage

```go
// Primitive types
err := lf.Set("core", "container", []FunctionArg{
    {Name: "image", Value: "alpine:latest"},
    {Name: "platform", Value: "linux/amd64"},
}, "sha256:abc123")
// err == nil

// Arrays/slices
err := lf.Set("build", "compile", []FunctionArg{
    {Name: "files", Value: []string{"main.go", "util.go"}},
    {Name: "flags", Value: []string{"-o", "binary"}},
}, []string{"binary", "binary.sha256"})
// err == nil

// Structs without maps
type Config struct {
    Name    string
    Version string
    Ports   []int
}
err := lf.Set("deploy", "service", []FunctionArg{
    {Name: "config", Value: Config{
        Name:    "myapp",
        Version: "1.0.0",
        Ports:   []int{8080, 8443},
    }},
}, "deployment-id-123")
// err == nil
```

### Invalid Usage

```go
// Direct map usage
err := lf.Set("config", "update", []FunctionArg{
    {Name: "settings", Value: map[string]string{
        "key": "value",
    }},
}, "result")
// err != nil: "argument \"settings\": maps/objects cannot be JSON-encoded deterministically"

// Map in result
err := lf.Set("status", "check", []FunctionArg{
    {Name: "service", Value: "api"},
}, map[string]interface{}{
    "status": "healthy",
    "uptime": 3600,
})
// err != nil: "result: maps/objects cannot be JSON-encoded deterministically"

// Nested map in array
err := lf.Set("batch", "process", []FunctionArg{
    {Name: "items", Value: []interface{}{
        "item1",
        map[string]string{"nested": "map"},  // Not allowed!
    }},
}, "result")
// err != nil: "argument \"items\": maps/objects cannot be JSON-encoded deterministically"
```

## Migration Guide

If you have existing code using maps with the lockfile package:

1. **For configuration objects**: Convert maps to structs with defined fields
2. **For dynamic key-value pairs**: Consider using arrays of key-value tuples: `[][2]string{{"key1", "value1"}, {"key2", "value2"}}`
3. **For JSON data**: Use `json.RawMessage` if you already have deterministic JSON bytes
4. **For complex nested data**: Design struct types that capture the structure without using maps

## Implementation Details

The validation is performed using reflection to recursively check all values:

1. The `validateDeterministic` function checks if a value can be deterministically encoded
2. It recursively traverses:
   - Array/slice elements
   - Struct fields  
   - Interface and pointer dereferencing
3. Returns an error immediately upon finding any map type
4. Validation occurs before any JSON marshaling to fail fast

## Testing

The package includes comprehensive tests for validation:

- `TestNonDeterministicTypeValidation`: Validates rejection of maps and acceptance of deterministic types
- `TestComplexTypes`: Tests nested structures
- `TestDoubleEncoding`: Ensures `json.RawMessage` is handled correctly

All existing tests have been updated to use deterministic types, ensuring the validation doesn't break existing functionality.