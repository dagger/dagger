# Nushell SDK Implementation Gaps

This document outlines the key missing features in the current Nushell SDK implementation that prevent it from being a complete, production-ready SDK.

## Current State

The SDK currently has:
- ✅ Basic function discovery (top-level exported functions)
- ✅ Simple type mapping (string, int, Container, Directory, File)
- ✅ Runtime container setup
- ✅ Template generation (main.nu)
- ✅ Idiomatic API helpers (dag.nu)
- ✅ Basic executor for function invocation

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

### 4. Proper Parameter Marshaling ❌

**Problem:** Complex parameter handling is incomplete:
- No support for optional parameters with defaults
- No support for list types beyond `list<string>`
- No support for nested objects as parameters
- No support for variadic parameters (beyond basic rest params)

**Example of what doesn't work:**

```nushell
# Optional with default - parser can't handle
export def greet [name: string = "World"] {
    $"Hello, ($name)!"
}

# List of custom objects - not supported
export def process [items: list<MyObject>] {
    $items | each {|item| ... }
}

# Nested object parameter - not supported
export def configure [config: {port: int, host: string}] {
    ...
}
```

**What we need:**
- Enhanced parameter parser in `runtime.nu`
- Type conversion in executor.go
- Support for complex types in `typeStringToTypeDef()`

### 5. Proper Object ID Handling ❌

**Problem:** The executor doesn't properly handle Dagger object IDs.

When Dagger calls a function with a `Container` parameter, it passes a JSON-encoded ID string like:
```json
{"container_id": "Container:abc123..."}
```

The executor needs to:
1. Detect that the parameter is a Dagger object type
2. Extract the ID from the JSON
3. Pass just the ID string to the Nushell function

**Current code in executor.go:**
```go
// This is too simplistic - just passes the raw JSON
paramValue, _ := arg.Value.MarshalJSON()
args = append(args, string(paramValue))
```

**What's needed:**
```go
// Need something like:
if isDaggerObjectType(argDef.TypeDef) {
    id := extractIDFromJSON(paramValue)
    args = append(args, id)
} else {
    args = append(args, string(paramValue))
}
```

### 6. Return Value Marshaling ❌

**Problem:** Functions that return Dagger objects don't properly marshal the result.

**Example:**

```nushell
# Returns a Container ID, but Dagger expects structured JSON
export def build [] {
    container from "golang:1.24"
    | container with-exec ["go", "build"]
    # Returns: "Container:abc123..."
    # Dagger expects: {"id": "Container:abc123..."}
}
```

**What's needed:**
- Detect when return type is a Dagger object
- Wrap the ID in proper JSON structure
- Handle primitive types vs object types differently

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

### Phase 1: Core Functionality (Required for MVP)
1. **Proper Object ID Handling** - Without this, passing Dagger objects to functions is broken
2. **Return Value Marshaling** - Without this, returning Dagger objects is broken
3. **Pipeline Function Registration** - Core to Nushell's idioms

### Phase 2: Object System (Required for Real-World Use)
4. **Object/Method System** - Needed for any non-trivial module
5. **Field Accessors** - Natural part of the object system

### Phase 3: Enhanced Type Support (Nice to Have)
6. **Proper Parameter Marshaling** - Better DX, more flexible APIs
7. **Module Metadata and Context** - Advanced use cases
8. **Error Handling** - Better debugging experience

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

### Short Term (Fix Blockers)
1. Fix object ID handling in executor.go
2. Fix return value marshaling in executor.go
3. Add tests for these core flows

### Medium Term (Enable Real Use)
1. Design Nushell object convention (records + metadata comments?)
2. Implement object discovery in runtime.nu
3. Update ModuleTypes() to support objects and methods
4. Add field support

### Long Term (Production Ready)
1. Implement full type system (optional params, complex types)
2. Add comprehensive error handling
3. Generate type-safe Nushell bindings from schema
4. Add module context and metadata access
5. Performance optimization (caching, parallel execution)

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
