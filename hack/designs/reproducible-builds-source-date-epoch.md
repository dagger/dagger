# Reproducible Builds: `SOURCE_DATE_EPOCH` / `rewriteTimestamp`

*Revives [#10318](https://github.com/dagger/dagger/pull/10318), addressing the review feedback. Tracks [discussion #12313](https://github.com/dagger/dagger/discussions/12313).*

## Table of Contents
- [Problem](#problem)
- [Solution](#solution)
- [Core Concept](#core-concept)
- [API](#api)
- [Implementation](#implementation)
- [What's intentionally out of scope](#whats-intentionally-out-of-scope)
- [Cache interaction](#cache-interaction)
- [Testing](#testing)
- [Status](#status)

## Problem

1. **Non-deterministic digests** — Exported/published images embed wall-clock timestamps (layer tar `mtime`, OCI config `created`, history entries), so identical sources produce different digests on every run.
2. **No timestamp control** — There is no way to pin those timestamps to a fixed value, which blocks the standard [`SOURCE_DATE_EPOCH`](https://reproducible-builds.org/docs/source-date-epoch/) reproducible-builds workflow.
3. **Directory export drifts too** — `Directory.export` streams files with their wall-clock `mtime`, so on-host exports are non-reproducible as well.

The downstream effects: image pinning in k8s churns, digest-based change detection produces false positives, and binary reproducibility for security verification is impossible.

## Solution

Expose an optional `rewriteTimestamp` (a Unix timestamp in seconds) on the export/publish surface. When set, file and image timestamps **newer than** that epoch are clamped down to it — exactly the BuildKit `rewrite-timestamp` semantics. When omitted, it defaults to the client's `SOURCE_DATE_EPOCH` environment variable (propagated via `ClientMetadata`). When neither is present, behavior is unchanged (wall-clock).

This is **export-time clamping**, which is all that reproducible digests require. The clamping machinery already exists in-tree — this change is almost entirely wiring it to the public API.

## Core Concept

The native exporter ([`engine/engineutil/imageexport/writer.go`](../../engine/engineutil/imageexport/writer.go)) already implements the full mechanism behind `CommitOpts{ Epoch, RewriteTimestamp }`:

- `rewriteExportChainWithEpoch` clamps layer tar headers (`ModTime`/`AccessTime`/`ChangeTime`) via `util/converter.NewWithRewriteTimestamp`, skipping immutable base-image layers.
- `patchImageConfig` normalizes the config `created` field and per-history `Created` entries to the epoch.

But the one place that builds `CommitOpts` — `exportCommitOpts` in [`engine/engineutil/containerimage.go`](../../engine/engineutil/containerimage.go) — hardcoded `Epoch: nil`. The whole feature is feeding it a value from the caller.

`Directory.export` takes a different path (`LocalDirExport` → fsutil `DiffCopy`) with no clamping; it gets a small new piece.

## API

```graphql
extend type Container {
  """
  Package the container state as an OCI image, and publish it to a registry.
  """
  publish(
    address: String!
    platformVariants: [ContainerID!]
    forcedCompression: ImageLayerCompression
    mediaTypes: ImageMediaTypes = OCI
    registryService: ServiceID

    """
    Clamp file and image timestamps newer than the given Unix timestamp
    (in seconds) down to it, for reproducible image digests. Defaults to the
    client's SOURCE_DATE_EPOCH when present; otherwise wall-clock is kept.
    """
    rewriteTimestamp: Int
  ): String!

  """Writes the container as an OCI tarball to the destination file path on the host."""
  export(path: String!, "...": "...", rewriteTimestamp: Int): String!

  """Package the container state as an OCI image, and return it as a tar archive."""
  asTarball("...": "...", rewriteTimestamp: Int): File!
}

extend type Directory {
  """Writes the contents of the directory to a path on the host."""
  export(path: String!, wipe: Boolean = false, rewriteTimestamp: Int): String!
}
```

`rewriteTimestamp` is a single optional `Int` (the explicit value), defaulting to `clientMetadata.SourceDateEpoch` — this is precisely the shape [@jedevc requested on #10318](https://github.com/dagger/dagger/pull/10318) (explicit value rather than a bool + separate client config), so module code can set it per-call without reconfiguring the client.

## Implementation

Resolution helper (schema layer), used by every handler:

```go
// explicit arg wins; else client's SOURCE_DATE_EPOCH; else nil (wall-clock).
func resolveSourceDateEpoch(ctx context.Context, explicit dagql.Optional[dagql.Int]) *int64 {
    if explicit.Valid {
        v := explicit.Value.Int64()
        return &v
    }
    if md, err := engine.ClientMetadataFromContext(ctx); err == nil && md.SourceDateEpoch != nil {
        return md.SourceDateEpoch
    }
    return nil
}
```

The image chokepoint — once this is set, layer + config clamping activates downstream:

```go
// engine/engineutil/containerimage.go
if sourceDateEpoch != nil {
    epoch := time.Unix(*sourceDateEpoch, 0).UTC()
    opts.Epoch = &epoch
    opts.RewriteTimestamp = true
}
```

The only genuinely new clamp — non-mutating, applied as files stream to the caller (the immutable ref on disk is untouched):

```go
// engine/engineutil/filesync.go (LocalDirExport)
if sourceDateEpoch != nil {
    epochNano := *sourceDateEpoch * int64(time.Second)
    outputFS, err = fsutil.NewFilterFS(outputFS, &fsutil.FilterOpt{
        Map: func(_ string, stat *fsutiltypes.Stat) fsutil.MapResult {
            if stat.ModTime > epochNano {
                stat.ModTime = epochNano
            }
            return fsutil.MapResultKeep
        },
    })
}
```

Touched files (`+~160/-15`):

| Layer | File | Change |
|-------|------|--------|
| Schema | `core/schema/container.go` | `rewriteTimestamp` arg on publish/export/asTarball + `resolveSourceDateEpoch` helper |
| Schema | `core/schema/directory.go` | `rewriteTimestamp` arg on export |
| Core | `core/container.go` | `Publish` + `ExportOpts.SourceDateEpoch` threading |
| Core | `core/container_image.go` | `AsTarball` threading |
| Core | `core/directory.go` | `Directory.Export` threading |
| Core | `core/changeset.go` | pass-through (`nil`) |
| Engine | `engine/engineutil/containerimage.go` | populate `CommitOpts.Epoch`/`RewriteTimestamp` |
| Engine | `engine/engineutil/filesync.go` | fsutil mtime clamp |
| Engine | `engine/opts.go` | `ClientMetadata.SourceDateEpoch` field |
| Client | `engine/client/client.go` | read `SOURCE_DATE_EPOCH` env |
| Test | `core/integration/container_test.go` | digest-stability test |
| Design | `hack/designs/reproducible-builds-source-date-epoch.md` | this document |

After schema changes: regenerate SDK bindings (`dagger call go-sdk generate export --path=.`, and equivalents for other SDKs).

## What's intentionally out of scope

- **Build-time mtime rewriting** — clamping every file's `mtime` as it's mutated mid-build. BuildKit deliberately doesn't do this, it's not needed for reproducible digests, and it would mean intercepting every exec/copy. Export-time clamping is sufficient.
- **Git-checkout normalization** — already normalized to `1` ([`core/git.go`](../../core/git.go), #13151); making it epoch-aware is a separate concern (input vs. export normalization).
- **`Container.exportImage`** (host image store) and **changeset export** — left on wall-clock for now; can adopt the same `*int64` plumbing later.

## Cache interaction

Safe by construction under the native (Project Theseus) cache:

- publish/export/asTarball/dir-export are terminal **sink** operations, not cached build steps. The rewrite happens in `Assemble`/`LocalDirExport` at export time, *downstream* of the layer cache — so the epoch cannot poison upstream build cache or LLB/ref digests.
- `rewriteTimestamp` is a normal dagql field arg, so it is part of the **export node's** cache key only. Changing it re-runs the export, not the build.
- Rewritten layers yield new, deterministic blob digests → the image digest changes with the epoch as intended, identically across runs.

## Testing

`core/integration/container_test.go`:

```go
func (ContainerSuite) TestAsTarballRewriteTimestamp(ctx, t) {
    // same epoch → identical digest (reproducible)
    // different epoch → different digest (baked into layers + config)
}
```

Run in a from-source engine (no local Go toolchain needed):

```bash
dagger call engine-dev test --run='TestContainer/TestAsTarballRewriteTimestamp' --pkg=./core/integration
```

## Status

Source implemented and compile-clean. Pending: Go SDK regeneration + integration test run.

---

- Discussion: [#12313](https://github.com/dagger/dagger/discussions/12313)
- Supersedes: [#10318](https://github.com/dagger/dagger/pull/10318)
