# Universal Lockfile Feature

## Overview

The Universal Lockfile feature in Dagger caches lookup results for external resources, eliminating redundant network calls and ensuring reproducible builds. It stores mappings like:
- Container registry tags → digests
- Git refs → commit SHAs
- HTTP URLs → content checksums (future)

## How It Works

### Automatic Caching

When you use operations that resolve external resources, Dagger automatically:
1. Checks the lockfile for cached results
2. Uses cached values if available (avoiding network lookups)
3. Performs the lookup if not cached
4. Stores new results in the lockfile

### File Format

The lockfile (`dagger.lock`) uses JSON lines format (one JSON object per line):

```json
{"module":"core","function":"container.from","inputs":{"image":"alpine:latest","platform":"linux/amd64"},"output":"sha256:abc123..."}
{"module":"core","function":"container.from","inputs":{"image":"ubuntu:22.04","platform":"linux/amd64"},"output":"sha256:def456..."}
{"module":"core","function":"git.resolve","inputs":{"url":"https://github.com/example/repo","ref":"main"},"output":"abc123:refs/heads/main"}
```

### Location

The lockfile is placed next to your `dagger.json` file. If no `dagger.json` exists, it's created in the current directory.

## Supported Operations

### Container Images

```go
// First run: Resolves alpine:latest to a digest via registry lookup
container := dag.Container().From("alpine:latest")

// Subsequent runs: Uses cached digest from dagger.lock
container := dag.Container().From("alpine:latest")
```

### Git References

```go
// First run: Resolves main branch to commit SHA via git remote
repo := dag.Git("https://github.com/example/repo").Ref("main")

// Subsequent runs: Uses cached commit SHA from dagger.lock
repo := dag.Git("https://github.com/example/repo").Ref("main")
```

### HTTP Resources (Coming Soon)

```go
// Will cache URL → content hash mappings
file := dag.HTTP("https://example.com/file.tar.gz")
```

## Benefits

### Performance

- **Eliminates redundant lookups**: Container registry and git remote lookups are slow and add up
- **Faster CI builds**: Cached lookups mean less time waiting for network operations
- **Reduced API rate limits**: Fewer requests to registries and git providers

### Reproducibility

- **Consistent builds**: Same resolved versions across all environments
- **Version pinning**: Floating tags (like `latest`) resolve to the same digest
- **Auditable changes**: Lockfile changes show exactly what versions changed

### Example Performance Impact

```bash
# First run (cold cache)
$ time dagger run ./my-pipeline
real    0m45.123s  # Includes registry lookups

# Second run (warm cache)
$ time dagger run ./my-pipeline
real    0m12.456s  # Skips registry lookups
```

## Working with Lockfiles

### Automatic Updates

The lockfile is automatically updated when:
- A new external resource is referenced
- An existing reference can't be found in the cache

### Manual Updates

To force-update all entries (e.g., to get latest versions):

```bash
# Remove lockfile to force fresh lookups
rm dagger.lock
dagger run ./my-pipeline
```

### Version Control

**Commit your lockfile** to version control to ensure:
- Team members use the same resolved versions
- CI builds are reproducible
- Changes to resolved versions are tracked

```bash
git add dagger.lock
git commit -m "Update lockfile with resolved dependencies"
```

### Viewing Changes

Lockfile changes are easy to review:

```diff
diff --git a/dagger.lock b/dagger.lock
-{"module":"core","function":"container.from","inputs":{"image":"alpine:latest"},"output":"sha256:abc123..."}
+{"module":"core","function":"container.from","inputs":{"image":"alpine:latest"},"output":"sha256:def456..."}
```

## Architecture

The lockfile is implemented as a session attachable:

```
┌─────────────┐                    ┌─────────────┐
│   Client    │                    │   Engine    │
│             │◄───────RPC─────────┤             │
│ ┌─────────┐ │   Get()/Set()      │             │
│ │Lockfile │ │                    │             │
│ │Attachable│ │                    │             │
│ └─────────┘ │                    │             │
│      ▲      │                    │             │
│      │      │                    │             │
│ dagger.lock │                    │             │
└─────────────┘                    └─────────────┘
```

1. Client loads `dagger.lock` at session start
2. Engine checks cache before external lookups via RPC
3. Engine stores new results via RPC
4. Client saves updated lockfile at session end

## Troubleshooting

### Lockfile Not Being Created

- Ensure you have write permissions in the directory
- Check for warnings in the output about lockfile initialization

### Stale Entries

If you need to force-refresh specific entries:
1. Edit `dagger.lock` and remove the specific lines
2. Run your pipeline to re-fetch those entries

### Large Lockfiles

For very large projects, lockfiles may grow. They're designed to handle this:
- Entries are deduplicated automatically
- JSON lines format allows streaming/partial reads (future optimization)
- Typical lockfiles remain small (<100KB)

## Future Enhancements

### Module-Defined Lookups

Modules will be able to define their own cacheable lookup functions:

```go
// @lookup
func (m *MyModule) ResolvePythonPackage(name string, version string) string {
    // Results automatically cached in dagger.lock
}
```

### Selective Updates

```bash
# Update only container images
dagger lock update --filter="container.*"

# Update only git refs
dagger lock update --filter="git.*"
```

### Conflict Resolution

Future versions will support merge strategies for lockfile conflicts in version control.

## FAQ

**Q: Is the lockfile required?**
A: No, Dagger works without it. The lockfile is optional but recommended for performance and reproducibility.

**Q: Can I disable the lockfile?**
A: Currently it's always active but non-fatal. If initialization fails, Dagger continues without caching.

**Q: How do I update a specific image tag?**
A: Remove its entry from `dagger.lock` and run your pipeline. The new digest will be cached.

**Q: Should I commit the lockfile?**
A: Yes, commit it to ensure reproducible builds across your team and CI.

**Q: What happens if the lockfile is corrupted?**
A: Dagger will warn and continue without it. Delete the file to start fresh.