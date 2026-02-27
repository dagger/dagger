# Module: Docusaurus (Workspace API Dogfood + Performance)

Status: draft  
Date: 2026-02-27  
Owners: docs-dev / workspace-api porting

## Purpose

Capture the docusaurus-specific dogfood findings, design decisions, and concrete optimization plan so work can be handed off without losing context.

This document is a dependency of:
- [Dogfood: port toolchain modules to the Workspace API](/Users/shykes/git/_workspace-api_2026-Feb17/github.com/shykes/dagger/hack/designs/dogfood-workspace-api.md)

## Scope

In scope:
- `modules/docusaurus` behavior under Workspace API.
- `Site.optimizeConfig` and `runtimeExternalPaths` performance.
- Cache volume keying/sharing decisions for site-level cache.

Out of scope:
- General toolchain checklist/status across all modules (tracked in dogfood-workspace-api doc).

## Current Behavior

Relevant code:
- [`runtimeExternalPaths`](/Users/shykes/git/_workspace-api_2026-Feb17/github.com/shykes/dagger/modules/docusaurus/docusaurus.dang:326)
- [`sandbox` cache mount](/Users/shykes/git/_workspace-api_2026-Feb17/github.com/shykes/dagger/modules/docusaurus/docusaurus.dang:309)
- [JIT preload hook](/Users/shykes/git/_workspace-api_2026-Feb17/github.com/shykes/dagger/modules/docusaurus/jit-workspace-hook.cjs)
- [JIT log collector](/Users/shykes/git/_workspace-api_2026-Feb17/github.com/shykes/dagger/modules/docusaurus/jit-workspace-log.cjs)

Flow today:
1. `Site.optimizeConfig` calls `runtimeExternalPaths`.
2. `runtimeExternalPaths` starts from site-only files in `/workspace`.
3. JIT workspace hook is enabled.
4. It runs `installCmd` and then `docusaurus build`.
5. Hook logs hydrated/seen paths.
6. `node /jit-workspace-log.cjs` collects external hydrated paths.
7. These paths are written into `package.json` (`docusaurus.include`) via `package-json-edit.cjs`.

## Decisions Already Made

### Cache sharing mode

Site cache mount now uses `CacheSharingMode.LOCKED` for `./node_modules/.cache`:
- [`withMountedCache(... sharing: LOCKED)`](/Users/shykes/git/_workspace-api_2026-Feb17/github.com/shykes/dagger/modules/docusaurus/docusaurus.dang:312)

Reason:
- Avoid concurrent writer divergence/corruption for a shared site cache.

### Cache key includes workspace root + site path

Site cache key:
- `"docusaurus-cache-" + ws.root + "-" + path`
- [`siteCacheVolumeKey`](/Users/shykes/git/_workspace-api_2026-Feb17/github.com/shykes/dagger/modules/docusaurus/docusaurus.dang:293)

Reason:
- Better than a single global cache across all sites.
- Reduces collisions across distinct workspace roots.

Known limitation:
- `ws.root + path` still collides when two different logical workspaces map to the same absolute root + site path (rare but possible depending on environment model).

## Findings From Traces

### 1) `node /jit-workspace-log.cjs` is not the bottleneck

In traces where optimize-config was slow, the collector step is near-zero; wall time is in the earlier exec steps.

### 2) JIT-on-install can dominate wall time

Observed traces:
- `74f9fb18049cf78d11f466bb629e6efe`:
  - `runtimeExternalPaths` `npm install` with JIT: ~13m32s.
  - `node /jit-workspace-log.cjs`: ~0s.
- `18b22529a9704d04d6a38b2f20b0626d` (single site `docs`):
  - `npm install` with JIT: ~50s.
  - `docusaurus build` with JIT: ~4m20s.
  - total `runtimeExternalPaths`: ~5m35s.

### 3) Baseline without JIT is much faster for install/build

Controlled run with external files pre-included (`52356482ae530107082881bbb090bbd4`):
- `npm install`: ~18s.
- `docusaurus build`: ~18s.

### 4) JIT overhead on build is moderate when hydration is low

Controlled run with JIT enabled only for build, after pre-including externals (`59fea588c6632a099447bac9a174cc78`):
- `docusaurus build`: ~29s.

Interpretation:
- The dominant regression is from tracing install and from high hydration churn during build when workspace is sparse.

### 5) `docs/` include discovery currently comes from build-time access

Captured external paths for docs include:
- `../CONTRIBUTING.md`
- `../sdk/typescript/...`

`docs/package.json` does not currently use local dependency protocols (`file:`, `link:`, `workspace:`), so install tracing is not required for current docs include discovery.

## Open Questions / Risks

1. `ws.root` visibility:
- `ws.root` is host-path metadata. This is currently exposed and used for cache keying.
- If this is considered too leaky long-term, replace with stable opaque workspace identity.

2. Remote workspace semantics:
- If remote workspace roots are normalized similarly, collision behavior needs confirmation.

3. Cross-site optimize scope:
- `optimizeConfig` scans all docusaurus sites, including test fixtures, increasing total time and cache churn.

## Implementation Plan

### Task 1: Stop tracing install by default

Change `runtimeExternalPaths` to:
1. Run `installCmd` without JIT (`withJustInTimeWorkspace(false)`).
2. Enable JIT only for `docusaurus build`.

Expected outcome:
- Large reduction in wall time for optimize-config in repos like this one.

### Task 2: Conditional install tracing for local deps

Add package-json detection:
- If dependencies/devDependencies/optionalDependencies/peerDependencies contain `file:`, `link:`, or `workspace:`, then keep JIT on for install.
- Otherwise, keep install untraced.

Expected outcome:
- Preserve correctness for monorepo-local dependency installs.
- Avoid unnecessary tracing in common cases.

### Task 3: Hook hot-path reductions

Optimize [`jit-workspace-hook.cjs`](/Users/shykes/git/_workspace-api_2026-Feb17/github.com/shykes/dagger/modules/docusaurus/jit-workspace-hook.cjs):
1. Early string-level skip for obvious irrelevant paths before canonicalization.
2. Avoid double canonicalization between wrapper and `record`.
3. Apply workspace/site/exclude filters to recording path set, not just hydration decisions.
4. Keep/extend memoization for repeated negative candidates.

Expected outcome:
- Lower CPU + syscalls under heavy fs probing.

### Task 4: Site selection controls for optimize-config (optional)

Add filters to avoid scanning test fixture sites by default (or at least configurable include/exclude list).

Expected outcome:
- Lower total optimize-config wall time in this repo.

## Verification Plan

Primary commands:
1. `dagger --progress=plain call docusaurus site --path docs runtime-external-paths`
2. `dagger --progress=plain generate docusaurus`

Compare:
- Total duration.
- `withExec npm install` duration within `runtimeExternalPaths`.
- `withExec ./node_modules/.bin/docusaurus build` duration.
- `just-in-time-workspace-log` output stability (same or acceptable path deltas).

Acceptance:
- Significant reduction in optimize-config time.
- No regression in discovered external include paths for docs.
- No build correctness regression.

## Dependency Link Back

The broader dogfood doc should track high-level status and reference this document for docusaurus-specific implementation details and performance work:
- [Dogfood: port toolchain modules to the Workspace API](/Users/shykes/git/_workspace-api_2026-Feb17/github.com/shykes/dagger/hack/designs/dogfood-workspace-api.md)
