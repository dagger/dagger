# Adding Decorators to the Dagger Python SDK

This guide explains how to add new decorators (Python's equivalent of Go pragmas) to the Dagger Python SDK that integrate with the GraphQL API.

## Overview

The Python SDK uses runtime decorators that store metadata on functions, which is then used during module registration to call the appropriate Dagger API methods. Unlike TypeScript decorators (which are no-ops parsed via AST introspection), Python decorators are actual functions that execute at module load time.

## Prerequisites

Before adding a new decorator parameter:

1. **The GraphQL directive must exist** in `dagql/server.go` (see main contributor guide)
2. **The API method must exist** in `core/schema/module.go` (e.g., `functionWithCheck`)
3. **The SDK must be regenerated** to include the new API method in `sdk/python/src/dagger/client/gen.py`

## Architecture

The Python decorator system has 4 key components:

1. **`_module.py`**: The `Module.function()` decorator method that accepts parameters
2. **`_types.py`**: The `FunctionDefinition` dataclass that stores metadata
3. **`_module.py`**: The `Module._typedefs()` method that registers functions with the API
4. **`client/gen.py`**: The generated API client with methods like `with_check()`

## Implementation Steps

### Step 1: Add Parameter to `function()` Decorator

**File**: `sdk/python/src/dagger/mod/_module.py` (around line 630)

Add your parameter to the `function()` decorator method signature:

```python
def function(
    self,
    fn: Callable[..., Any] | None = None,
    *,
    name: str | None = None,
    doc: str | None = None,
    check: bool = False,  # ADD YOUR PARAMETER HERE
) -> Any:
    """Register a function to include in the module's API.
    
    Args:
        fn: The function to register.
        name: Override the function's name.
        doc: Override the function's docstring.
        check: Mark this function as a check.  # ADD DOCUMENTATION
    """
```

**Notes**:
- Use keyword-only parameters (after `*`)
- Provide sensible defaults (typically `False` for booleans, `None` for optional values)
- Add parameter documentation to the docstring

### Step 2: Add Field to `FunctionDefinition` Dataclass

**File**: `sdk/python/src/dagger/mod/_types.py` (around line 19)

Add a field to store your metadata:

```python
@dataclass(frozen=True, slots=True)
class FunctionDefinition:
    """Metadata about a function exposed in the module's API."""
    
    name: str | None = None
    doc: str | None = None
    cache: CachePolicy | None = None
    deprecated: str | None = None
    check: bool = False  # ADD YOUR FIELD HERE
```

**Notes**:
- The dataclass is frozen (immutable) and uses `__slots__` for efficiency
- Provide a default value that matches your decorator parameter default
- Keep the field name consistent with the decorator parameter name

### Step 3: Store Value in `FunctionDefinition`

**File**: `sdk/python/src/dagger/mod/_module.py` (around line 671)

Update the `FunctionDefinition` instantiation to include your parameter:

```python
def decorator(fn: Callable[..., Any]) -> Any:
    fn_def = FunctionDefinition(
        name=name,
        doc=doc,
        cache=cache,
        deprecated=deprecated,
        check=check,  # ADD YOUR PARAMETER HERE
    )
    setattr(fn, _DEFINITION_METADATA_NAME, fn_def)
    setattr(self, fn.__name__, Function(fn, parent=self))
    return fn
```

**Notes**:
- The metadata is stored as an attribute on the function object
- The attribute name is defined by `_DEFINITION_METADATA_NAME` constant
- This happens at module load time when the decorator is applied

### Step 4: Check Field During Registration

**File**: `sdk/python/src/dagger/mod/_module.py` (around line 207 in `_typedefs()`)

Add logic to check your field and call the appropriate API method:

```python
# Build the function definition
fn_def: Function = (
    api_mod.with_function(py_func.name)
    .with_description(py_func.doc or "")
)

# Apply cache policy if set
if defn.cache is not None:
    fn_def = fn_def.with_cache_policy(
        max_age=defn.cache.max_age,
        max_concurrent=defn.cache.max_concurrent,
    )

# Apply deprecated marker if set
if defn.deprecated is not None:
    fn_def = fn_def.with_deprecated(defn.deprecated)

# ADD YOUR CHECK HERE
if defn.check:
    fn_def = fn_def.with_check()

# Continue with arguments...
for arg in py_func.parameters:
    # ...
```

**Notes**:
- The `_typedefs()` method iterates through all registered functions
- Each function is built up incrementally by calling API methods
- The order of API method calls generally doesn't matter
- Each `with_*()` method returns a new `Function` object (fluent API)

### Step 5: Test Your Decorator

Create a test module to verify the decorator works:

```python
from dagger import function, object_type

@object_type
class MyModule:
    @function(check=True)
    def my_check(self) -> str:
        """A check function."""
        return "all good"
```

Run the module and verify the GraphQL schema includes the `@check` directive:

```bash
dagger develop --sdk=python
dagger functions  # Should show my-check function
```

## Common Patterns

### Pattern 1: Boolean Flag

**Use case**: Simple on/off feature (e.g., `@function(check=True)`)

```python
# Step 1: Decorator parameter
def function(self, fn=None, *, check: bool = False) -> Any:
    ...

# Step 2: Dataclass field
@dataclass(frozen=True, slots=True)
class FunctionDefinition:
    check: bool = False

# Step 3: Store value
fn_def = FunctionDefinition(check=check)

# Step 4: Call API
if defn.check:
    fn_def = fn_def.with_check()
```

### Pattern 2: String Argument

**Use case**: Single configuration value (e.g., `@function(default_path="./config")`)

```python
# Step 1: Decorator parameter
def function(self, fn=None, *, default_path: str | None = None) -> Any:
    ...

# Step 2: Dataclass field
@dataclass(frozen=True, slots=True)
class FunctionDefinition:
    default_path: str | None = None

# Step 3: Store value
fn_def = FunctionDefinition(default_path=default_path)

# Step 4: Call API
if defn.default_path is not None:
    fn_def = fn_def.with_default_path(defn.default_path)
```

### Pattern 3: List of Strings

**Use case**: Multiple values (e.g., `@function(ignore=["node_modules", ".git"])`)

```python
# Step 1: Decorator parameter
def function(self, fn=None, *, ignore: list[str] | None = None) -> Any:
    ...

# Step 2: Dataclass field
@dataclass(frozen=True, slots=True)
class FunctionDefinition:
    ignore: list[str] | None = None

# Step 3: Store value
fn_def = FunctionDefinition(ignore=ignore or [])

# Step 4: Call API
if defn.ignore:
    fn_def = fn_def.with_ignore(defn.ignore)
```

### Pattern 4: Nested Options (CachePolicy Example)

**Use case**: Complex configuration object

```python
# Define the options dataclass in _types.py
@dataclass(frozen=True, slots=True)
class CachePolicy:
    max_age: int | None = None
    max_concurrent: int | None = None

# Step 1: Decorator parameter
def function(self, fn=None, *, cache: CachePolicy | None = None) -> Any:
    ...

# Step 2: Dataclass field
@dataclass(frozen=True, slots=True)
class FunctionDefinition:
    cache: CachePolicy | None = None

# Step 3: Store value
fn_def = FunctionDefinition(cache=cache)

# Step 4: Call API with unpacked values
if defn.cache is not None:
    fn_def = fn_def.with_cache_policy(
        max_age=defn.cache.max_age,
        max_concurrent=defn.cache.max_concurrent,
    )
```

## Argument-Level Decorators

Some decorators apply to function **arguments** rather than functions. Python doesn't have first-class syntax for this, so the pattern uses type annotations:

### Using `Annotated` for Argument Metadata

```python
from typing import Annotated
from dagger import Doc, DefaultPath

@function
def my_function(
    self,
    # Argument with documentation
    name: Annotated[str, Doc("The name to use")],
    # Argument with default path
    config: Annotated[str, DefaultPath("./config.yaml")],
) -> str:
    ...
```

**Implementation**: These use `typing.Annotated` to attach metadata to type hints. The introspection code in `_arguments.py` extracts this metadata during module registration.

**Adding a new argument decorator**:

1. Define a marker class in `_types.py` (e.g., `class MyMarker`)
2. Export it from `__init__.py`
3. Update `_arguments.py` to extract the marker from `Annotated` types
4. Call the appropriate `with_*()` method when building arguments in `_typedefs()`

## Key Files Reference

| File | Purpose | What to Change |
|------|---------|----------------|
| `_module.py` | Module class with decorator methods | Add decorator parameter, store in FunctionDefinition, check in `_typedefs()` |
| `_types.py` | Dataclass definitions | Add field to `FunctionDefinition` |
| `_resolver.py` | Function wrapper | Usually no changes needed (metadata flows through `FunctionDefinition`) |
| `client/gen.py` | Generated API client | Read-only (regenerated from GraphQL schema) |
| `__init__.py` | Public exports | Export new marker classes for argument decorators |
| `_arguments.py` | Argument introspection | Extract `Annotated` metadata for argument decorators |

## Common Gotchas

### 1. Forgetting to Regenerate the SDK

If you add a new API method to `core/schema/module.go`, you must regenerate the Python SDK:

```bash
dagger develop --sdk=python
# or
make sdk-generate
```

Without this, `with_my_feature()` won't exist in `client/gen.py`.

### 2. Type Hint Compatibility

The `function()` decorator is generic and returns `Any` to avoid type checking issues. This is intentional:

```python
def function(self, fn=None, *, ...) -> Any:
    # Returns Any because decorated functions keep their signatures
```

### 3. Dataclass Immutability

`FunctionDefinition` is frozen, so you can't modify it after creation:

```python
# ❌ This will raise an error
fn_def.check = True

# ✅ Create a new instance instead
fn_def = FunctionDefinition(check=True)
```

### 4. Default Value Consistency

Make sure defaults match across decorator parameter and dataclass field:

```python
# Decorator parameter default
def function(self, fn=None, *, check: bool = False):

# Dataclass field default
@dataclass(frozen=True)
class FunctionDefinition:
    check: bool = False  # Should match!
```

### 5. None vs Empty List

For list parameters, use `None` as the default and convert to empty list when storing:

```python
# Decorator parameter
def function(self, fn=None, *, ignore: list[str] | None = None):
    ...

# Store as empty list if None
fn_def = FunctionDefinition(ignore=ignore or [])

# Check for non-empty list
if defn.ignore:
    fn_def = fn_def.with_ignore(defn.ignore)
```

## Comparison with Other SDKs

| Aspect | Python | TypeScript | Go |
|--------|--------|------------|-----|
| **Syntax** | `@function(check=True)` | `@func() @check()` | `// +check` |
| **Mechanism** | Runtime decorator | AST introspection | Comment parsing |
| **Storage** | `FunctionDefinition` dataclass | `DaggerFunction` properties | `FunctionArg` struct |
| **Parsing** | At module load | During introspection | During codegen |
| **Registration** | `_typedefs()` method | `register.ts` | `module_funcs.go` |
| **Type Safety** | Runtime (type hints) | Compile-time (TypeScript) | Compile-time (Go) |

## Example: Adding `@function(check=True)`

Here's a complete example of adding the `check` decorator parameter:

### 1. `_types.py`: Add field

```python
@dataclass(frozen=True, slots=True)
class FunctionDefinition:
    name: str | None = None
    doc: str | None = None
    cache: CachePolicy | None = None
    deprecated: str | None = None
    check: bool = False  # NEW
```

### 2. `_module.py`: Add parameter

```python
def function(
    self,
    fn: Callable[..., Any] | None = None,
    *,
    name: str | None = None,
    doc: str | None = None,
    check: bool = False,  # NEW
) -> Any:
    """Register a function to include in the module's API.
    
    Args:
        fn: The function to register.
        name: Override the function's name.
        doc: Override the function's docstring.
        check: Mark this function as a check.  # NEW
    """
```

### 3. `_module.py`: Store value

```python
def decorator(fn: Callable[..., Any]) -> Any:
    fn_def = FunctionDefinition(
        name=name,
        doc=doc,
        cache=cache,
        deprecated=deprecated,
        check=check,  # NEW
    )
    setattr(fn, _DEFINITION_METADATA_NAME, fn_def)
    setattr(self, fn.__name__, Function(fn, parent=self))
    return fn
```

### 4. `_module.py`: Register with API

```python
# In _typedefs() method, after building fn_def
if defn.check:
    fn_def = fn_def.with_check()  # NEW
```

### 5. Usage

```python
from dagger import function, object_type

@object_type
class MyModule:
    @function(check=True)
    def lint(self) -> str:
        """Check code style."""
        return "✓ All checks passed"
```

## Testing

After implementing your decorator:

1. **Unit test**: Add tests to `sdk/python/tests/` verifying metadata storage
2. **Integration test**: Create a test module using the decorator
3. **Schema verification**: Inspect the generated GraphQL schema for the directive
4. **API test**: Verify the `with_*()` method is called correctly

```bash
# Run unit tests
cd sdk/python
pytest tests/

# Test a sample module
cd /tmp
dagger init --sdk=python my-test
# Edit dagger.json module file with @function(check=True)
dagger functions  # Should show the check function
dagger call lint  # Should execute successfully
```

## Troubleshooting

### Decorator parameter not recognized

**Symptom**: `TypeError: function() got an unexpected keyword argument 'check'`

**Solution**: Make sure you added the parameter to the `function()` method signature in `_module.py`.

### API method doesn't exist

**Symptom**: `AttributeError: 'Function' object has no attribute 'with_check'`

**Solution**: Regenerate the SDK after adding the API method to `core/schema/module.go`:

```bash
dagger develop --sdk=python
```

### Directive not in schema

**Symptom**: GraphQL schema doesn't include `@check` directive

**Solution**: Verify the directive exists in `dagql/server.go` and the API method chains correctly in `_typedefs()`.

### Metadata not preserved

**Symptom**: Decorator parameter is ignored during registration

**Solution**: Check that you:
1. Added the field to `FunctionDefinition`
2. Passed the parameter when creating `FunctionDefinition`
3. Checked the field in `_typedefs()` before calling the API method

## Summary

Adding a decorator to the Python SDK requires 4 file changes:

1. **`_module.py`**: Add decorator parameter to `function()` method
2. **`_types.py`**: Add field to `FunctionDefinition` dataclass
3. **`_module.py`**: Store parameter in `FunctionDefinition` instance
4. **`_module.py`**: Check field and call API method in `_typedefs()`

The pattern is: **Decorator parameter → Dataclass field → API method call**

Each decorator parameter flows through this pipeline, ultimately calling a generated API method that sets the corresponding GraphQL directive.
