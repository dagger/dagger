# Lockfile Format Changes

## Summary

The Dagger lockfile uses a compact tuple format with a version header. The old JSON object format is no longer supported.

## Format
```json
[["version","1"]]
["core","container.from",["alpine:latest"],"sha256:abc123"]
["core","container.from",["alpine:1.23"],"sha256:foobar"]
```

## Benefits

1. **Compact format** - Tuple-based format reduces file size
2. **Fully deterministic** - All fields have strict ordering, eliminating JSON key sorting issues  
3. **Version header** - Enables future format evolution
4. **Cleaner format** - More readable and easier to parse

## Implementation Details

### Structure
- **Version Header**: First line is always `[["version","1"]]`
- **Entry Format**: Each entry is a 4-element tuple: `[module, function, inputs_array, output]`
- **Inputs Array**: Function inputs are stored as an ordered array instead of a JSON object

### Field Ordering Rules
The inputs are converted to arrays with deterministic ordering:
- `container.from`: `[ref]` or `[image]`
- `http.get`: `[url]`
- `git.branch/tag/commit`: `[repo, ref]`
- Unknown functions: Fields in alphabetical order

## Files Changed

### Core Implementation
- `engine/session/lockfile/lockfile.go`
  - Added `inputsToArray()` - converts inputs map to deterministic array
  - Added `arrayToInputs()` - reconstructs inputs map from array
  - Modified `Save()` to write tuple format with version header
  - Modified `load()` to handle tuple format with version validation

### Tests
- `engine/session/lockfile/lockfile_test.go`
  - Updated existing tests for tuple format
  - Added `TestTupleFormat` - verifies tuple structure
  - Added `TestInputsToArray` and `TestArrayToInputs` - conversion logic

- `engine/session/lockfile/format_test.go` (new)
  - Added comprehensive format-specific tests
  - `TestNewFormatStructure` - verifies tuple structure
  - `TestInputFieldOrdering` - deterministic ordering
  - `TestRoundTrip` - save/load cycle integrity
  - `TestVersionHeader` - version handling
  - `TestEmptyLockfile` - handling of empty/missing lockfiles

## Testing

All tests verify:
- Format structure and version header
- Deterministic field ordering
- Round-trip integrity (save → load → save produces identical files)
- Proper version validation