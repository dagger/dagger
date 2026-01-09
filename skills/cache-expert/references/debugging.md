# Debugging Cache Issues

This document covers techniques for diagnosing cache misses, unexpected invalidations, and other caching-related bugs.

## Core Debugging Approach

**The best way to debug cache issues is to print cache keys and related state to the engine logs and find where an unexpected mismatch occurs.**

### General Debugging Loop

When stuck on any cache issue:

1. **Explicate expected behavior step-by-step**: Write out what you expect to happen at each stage of the caching flow

2. **Verify steps are happening**: Use logs or other techniques to confirm each step matches expectation

3. **Investigate deviations**: When you find behavior that doesn't match expectation:
   - Is this the root cause of the bug?
   - Or is it a misunderstanding of expected behavior?

4. **Fix or repeat**: If root cause found, fix it. Otherwise, update your understanding of expected behavior and repeat

### Cache-Specific Debugging Checklist

When debugging a cache issue specifically:

1. **Understand the cache key being used**: Log the cache key at the point of lookup
2. **Check if the key is being stored correctly**: Verify the key used for storage matches expected
3. **Check if the key is being retrieved correctly**: Verify the key used for lookup matches what was stored
4. **Verify if the value associated with the key is correct**: Ensure the cached value is what you expect

## Running Tests

**The best way to debug is to have a test that reproduces the issue.**

### Integration Tests

Integration tests live in `./core/integration/`. They:
- Build an engine with your local changes
- Start the engine as a Dagger service
- Run Go tests in a separate container connected to that engine

### Running Tests

```bash
# Run a specific integration test
dagger call --progress=plain engine-dev test --run=<GO_TEST_NAME_FORMAT>

# Examples:
dagger call --progress=plain engine-dev test --run=TestContainerExec
dagger call --progress=plain engine-dev test --run=TestCache.*
```

The `--progress=plain` flag ensures you see full output including logs.

### Debugging Loop

1. Write or identify a test that reproduces the issue
2. Add logging to relevant cache code paths
3. Run the test
4. Analyze logs to find deviation from expected behavior
5. Repeat until root cause is found

## Adding Debug Logging

TODO: Document specific locations where to add logging for different types of cache issues:
- dagql cache lookup/storage (`dagql/session_cache.go`, `engine/cache/cache.go`)
- Cache key computation (`dagql/objects.go` - `preselect`)
- ID digest computation (`dagql/call/id.go`)
- BuildKit cache key flow (`core/modfunc.go`, `core/schema/container.go`)

## Prior Art

TODO: Maintain a searchable list of previous cache debugging sessions with:
- Symptoms observed
- Root cause found
- Key insights / techniques used
- Links to relevant PRs or issues

This helps when encountering similar issues in the future.

## Common Patterns

TODO: Document common cache issue patterns based on experience:
- Unexpected input changes affecting cache key
- Session/client scoping issues
- TTL-related issues
- Custom cache key function bugs
- ID digest mismatches
