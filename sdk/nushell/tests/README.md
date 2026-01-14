# Nushell SDK Tests

This directory contains comprehensive tests for the Nushell SDK.

## Structure

```
tests/
├── core.nu              # Tests for __type metadata and get-object-type function
├── wrappers.nu          # Tests for wrapper functions (multi-type detection)
├── objects.nu           # Tests for object/method pattern (@object, @method, @field)
├── operations/          # Tests for individual operations
│   ├── container.nu     # Container operation tests
│   ├── directory.nu     # Directory operation tests (to be created)
│   ├── file.nu          # File operation tests (to be created)
│   └── git.nu           # Git operation tests (to be created)
└── integration/
    └── pipelines.nu     # Complete workflow tests
```

## Running Tests

### Run all tests
```bash
dagger call test-module test-all
```

### Run specific test categories
```bash
dagger call test-module test-core-all        # Core metadata tests
dagger call test-module test-wrappers-all    # Wrapper tests
dagger call test-module test-objects-all     # Object/method tests
dagger call test-module test-integration-all # Integration tests
dagger call test-module test-container-all   # Container operation tests
```

### Run individual tests
```bash
dagger call test-module test-type-metadata-present
dagger call test-get-object-type-container
dagger call test-with-directory-container
```

## Test Categories

### Core Tests (core.nu)
- `__type` metadata presence and correctness
- `get-object-type` function accuracy
- Type preservation through operations

### Wrapper Tests (wrappers.nu)
- Multi-type wrapper detection (Container/Directory)
- Error handling for wrong type usage
- Cross-type operations (export, entries, contents, etc.)

### Object/Method Tests (objects.nu)
- Object creation with `@object` annotation
- Method calls with `@method` annotation
- Field access with `@field` annotation
- Method chaining
- Automatic object name inference

### Operation Tests (operations/)
- Container operations: from, with-exec, with-env-variable, etc.
- Directory operations: from, entries, file, with-*, without, etc.
- File operations: contents, size, name, export, etc.
- Git operations: repo, branch, tag, commit, tree

### Integration Tests (integration/pipelines.nu)
- Complete workflows combining multiple operations
- Cross-type operations (Container <-> Directory)
- Real-world usage patterns

## Test Patterns

Each test file follows these patterns:

1. **Test functions**: Named `test-*` that return a success message
2. **Test helpers**: `assert-equal` and `assert-truthy` for assertions
3. **All tests**: `test-*-all` function that runs all tests in the file
4. **Return format**: Success message string or error if test fails

## Test Requirements

- All operations must return proper `__type` metadata
- All wrappers must detect types correctly
- Error messages must be clear and helpful
- Tests should be independent and repeatable
