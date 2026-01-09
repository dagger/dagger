# Nushell SDK Implementation Gaps

This document outlines the key missing features in the current Nushell SDK implementation that prevent it from being a complete, production-ready SDK.

## Current State

The SDK is **functional for simple use cases** (top-level functions with basic types) but lacks support for complex module architectures (objects, methods, fields).

## Completed Features ✅

### Core Functionality
- ✅ **Function Discovery**: Automatic discovery of exported Nushell functions
- ✅ **Parameter-less Functions**: Functions without parameters fully supported
- ✅ **Type Mapping**: string, int, bool, Container, Directory, File, Secret, CacheVolume, etc.
- ✅ **Runtime Container Setup**: Go-based executor with Nushell runtime
- ✅ **Template Generation**: Scaffolding for new modules (templates/main.nu)
- ✅ **Idiomatic API Helpers**: 87 operations in dag.nu for pipeline-based API

### Advanced Types (Added)
- ✅ **Optional Parameters**: Default values with `param: type = "default"` syntax
- ✅ **List Types**: Full support for `list<string>`, `list<int>`, `list<Container>`, etc.
- ✅ **Return Type Annotations**: `# @returns(Type)` for explicit return types
- ✅ **Type Annotations**: `# @dagger(Type)` for parameter type hints
- ✅ **Records for Objects**: Dagger objects wrapped in records: `{id: "Container:..."}`

### Check Support (Added)
- ✅ **Check Functions**: `# @check` annotation for validation functions
- ✅ **Container-based Checks**: Return containers that execute validation logic
- ✅ **Check Registration**: Properly registered with `WithCheck()` in type system
- ✅ **CLI Integration**: Works with `dagger check` command

### Object ID Handling (Fixed)
- ✅ **ID Detection**: Detects both simple (`Container:abc`) and protobuf IDs (base64)
- ✅ **Parameter Marshaling**: Properly handles Dagger object IDs in parameters
- ✅ **Return Value Marshaling**: Correctly extracts and returns object IDs
- ✅ **Record Wrapping**: Wraps IDs in records for Nushell consumption

### File Structure (Reorganized)
- ✅ **Separated Runtime from Templates**: Clear distinction between infrastructure and scaffolding
- ✅ **runtime/**: dag.nu, executor.go, runtime.nu
- ✅ **templates/**: main.nu (user template)

## Summary of Remaining Gaps

The SDK is **functional for simple modules** but lacks architectural features for complex use cases:

| Feature | Status | Priority | Impact |
|---------|--------|----------|--------|
| Object/Method System | ❌ Not Started | **Critical** | Blocks complex modules |
| Pipeline Functions | ❌ Not Started | High | Limits idiomatic Nushell |
| Field Accessors | ❌ Not Started | Medium | Part of object system |
| Nested Object Params | ❌ Not Started | Low | Workaround possible |
| Module Metadata | ❌ Not Started | Low | Advanced use cases |

**Bottom Line:** Works great for simple modules. Needs object/method system for production use.

---

## Critical Missing Features

### 1. Object/Method System ❌

**Problem:** The SDK only discovers top-level functions. It doesn't support:
- Custom object types (like `@object_type` in Python)
- Methods on those objects
- Constructor functions
- Object fields/properties

**Example of what's missing:**

```nushell
# This should work but doesn't:
export def --env MyService [] {
    # Constructor
    {port: 8080, host: "localhost"}
}

# Method on MyService (currently not discovered)
export def "start" [] {
    let service = $in  # Receives MyService object
    # ... start the service
}
```

**What other SDKs do:**
- Python: Uses `@object_type` decorator to define custom objects, methods are discovered on the class
- Elixir: Uses `defstruct` and modules to define objects with functions
- Go: Uses struct types with methods

**What we need:**
- Nushell convention for defining objects (maybe records with metadata?)
- Parser changes in `runtime.nu` to discover object definitions
- Update `ModuleTypes()` in main.go to create object TypeDefs with functions

### 2. Pipeline Function Registration ❌

**Problem:** Functions that use `$in` (pipeline input) are not properly discovered or typed.

**Example:**

```nushell
# This function uses pipeline input but isn't registered correctly
export def "transform-data" [] {
    let input = $in
    $input | str upcase
}
```

**What's needed:**
- Detect when functions use `$in` 
- Infer or annotate the input type
- Register the function with correct input/output signature
- Handle the implicit `$in` parameter during invocation

### 3. Field Accessors ❌

**Problem:** No way to define read-only fields or computed properties on objects.

**Example of what's missing:**

```nushell
# Should be able to define a field that computes a value
export def "service port" [] {
    let obj = $in
    $obj.port
}
```

**What other SDKs do:**
- Python: Uses `@property` or `@field` decorators
- Go: Struct fields are automatically exposed

**What we need:**
- Syntax to distinguish fields from methods (maybe commands without parameters?)
- Parser support for field discovery
- Field TypeDef registration in main.go

### 4. Parameter Marshaling ✅ MOSTLY COMPLETE

**Status:** Basic and intermediate parameter types are fully supported.

**What works:**
- ✅ Optional parameters with defaults: `name: string = "World"`
- ✅ List types: `list<string>`, `list<int>`, `list<Container>`, etc.
- ✅ Dagger object parameters: `Container`, `Directory`, `File`, etc.
- ✅ Primitive types: `string`, `int`, `bool`

**What's still missing:**
- ❌ Nested object parameters: `config: {port: int, host: string}`
- ❌ Variadic parameters: `...args`
- ❌ List of custom objects (requires object/method system first)

**Example of what works:**

```nushell
# This works now!
export def greet [name: string = "World"] {
    $"Hello, ($name)!"
}

# This works too!
export def process [items: list<string>] {
    $items | str join ", "
}
```

### 5. Object ID Handling ✅ COMPLETE

**Status:** Fully implemented and working.

**What works:**
- ✅ Detects Dagger object IDs in both formats:
  - Simple: `Container:abc123`
  - Protobuf: Base64-encoded strings (100+ chars)
- ✅ Wraps IDs in records for Nushell: `{id: "Container:..."}`
- ✅ Extracts IDs from records when returning to Dagger
- ✅ Handles parameters with Dagger object types
- ✅ Returns Dagger objects correctly

**Implementation:**
```go
// runtime/executor.go
func isDaggerObjectID(s string) bool {
    // Check for simple type prefix format
    daggerTypes := []string{"Container:", "Directory:", "File:", ...}
    for _, prefix := range daggerTypes {
        if strings.HasPrefix(s, prefix) {
            return true
        }
    }
    // Check for protobuf format: long base64 strings
    if len(s) > 100 {
        return true
    }
    return false
}
```

### 6. Return Value Marshaling ✅ COMPLETE

**Status:** Fully implemented and working.

**What works:**
- ✅ Detects when return value is a Dagger object
- ✅ Extracts ID from record: `{id: "Container:..."}` → `"Container:..."`
- ✅ Handles primitive types (string, int, bool)
- ✅ Handles lists and complex types
- ✅ Properly marshals to JSON for Dagger

**Example (working):**

```nushell
# This works correctly!
export def build []: nothing -> record {  # @returns(Container)
    container from "golang:1.24"
    | container with-exec ["go", "build"]
    # Returns: {id: "Container:abc123..."}
    # Executor extracts: "Container:abc123..."
}
```

### 7. Module Metadata and Context ❌

**Problem:** No way to access module-level metadata or context in functions.

**What's missing:**
- Access to module name
- Access to source directory
- Access to introspection schema
- Module-level configuration

### 8. Error Handling ❌

**Problem:** Errors from Nushell functions aren't properly propagated to Dagger.

**What's needed:**
- Proper stderr capture
- Error code propagation
- Structured error messages
- Stack trace preservation

## Implementation Priorities

### ✅ Phase 1: Core Functionality - **COMPLETE**
1. ✅ **Proper Object ID Handling** - Fully implemented
2. ✅ **Return Value Marshaling** - Fully implemented  
3. ✅ **Basic Parameter Marshaling** - Optional params, lists, etc.
4. ✅ **Check Support** - Full integration with Dagger checks

**Result:** SDK is functional for simple modules with top-level functions.

### ❌ Phase 2: Object System - **NOT STARTED** (Required for Production)
1. **Object/Method System** - Needed for any non-trivial module
2. **Field Accessors** - Natural part of the object system
3. **Pipeline Function Registration** - Core to Nushell's idioms

**Blocker:** Architectural design needed for how objects work in Nushell.

### Phase 3: Enhanced Features - **PARTIALLY COMPLETE**
- ✅ List types (complete)
- ✅ Optional parameters (complete)
- ❌ Nested object parameters (not started)
- ❌ Module metadata access (not started)
- ⚠️ Error handling (basic, could be improved)

## Comparison with Other SDKs

### Python SDK Approach
- Uses decorators: `@object_type`, `@function`, `@field`
- Introspects Python classes and type hints
- Has full converter system for complex types
- Handles async/await for pipeline operations

### Elixir SDK Approach
- Uses modules and defstruct for objects
- Functions are discovered by convention
- Leverages Elixir's pattern matching for parameters
- Uses Mix tasks for codegen

### Go SDK Approach  
- Uses struct types for objects
- Methods are Go methods on structs
- Full type safety from Go's type system
- Code generation creates the entire SDK

## Recommendations

### ✅ Short Term - **COMPLETE**
1. ✅ Fix object ID handling in executor.go
2. ✅ Fix return value marshaling in executor.go
3. ✅ Add check support
4. ✅ Add optional parameters and list types
5. ⚠️ Add tests for these core flows (manual testing done, integration tests pending)

### ❌ Medium Term (Enable Real Use) - **NOT STARTED**
1. Design Nushell object convention (records + metadata comments?)
2. Implement object discovery in runtime.nu
3. Update ModuleTypes() to support objects and methods
4. Add field support

### Long Term (Production Ready)
1. ⚠️ Implement full type system (basic done, nested objects remaining)
2. ❌ Add comprehensive error handling
3. ❌ Generate type-safe Nushell bindings from schema
4. ❌ Add module context and metadata access
5. ❌ Performance optimization (caching, parallel execution)

## Questions to Resolve

1. **Object Definition Syntax**: How should users define objects in Nushell?
   - Option A: Special comments/annotations (like Python decorators)
   - Option B: Naming conventions (like Go SDK generation)
   - Option C: Separate metadata file (like TypeScript decorators in comments)

2. **Pipeline vs Explicit Parameters**: Should all methods accept pipeline input?
   - Current dag.nu uses pipeline (`$in`) for method chaining
   - But constructor functions can't use pipeline
   - Need clear convention

3. **Type Annotations**: How to express complex types in Nushell?
   - Nushell's type system is limited
   - May need to parse comments for full type info
   - Or generate code with embedded type metadata

4. **Backwards Compatibility**: Should we maintain compatibility with current template?
   - Current template only has top-level functions
   - Moving to objects would be a breaking change
   - Maybe support both modes?
