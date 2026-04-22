# Workspace CWD Module

## Status

Locked.

## Summary

The CWD module is the nearest `dagger.json` found by find-up from the caller's working directory.

It is a permanent convenience. It is detected separately from ambient workspace context. If it is distinct from the already-loaded ambient modules, it is loaded as an additional module and becomes the active entrypoint for the invocation.

If explicit extra modules (`-m`) are present, the CWD module is suppressed entirely.

The CWD module participates in the same generic module-deduplication mechanism as other module-loading paths. If multiple paths nominate the same module, the engine loads it once.

## Problem

1. The special module near the caller is useful, but its role is blurred together with workspace and compat behavior.
2. Without an explicit contract, nested-module precedence and dedupe become ad hoc.
3. The CWD module should be a permanent convenience, not a migration-specific exception.

## Decision

The CWD module is a separate module-loading path.

The engine will:

- detect ambient workspace context first
- then detect the CWD module by find-up from the caller
- then apply generic module dedupe
- then let the distinct CWD module win as the active entrypoint for the invocation

This rule is independent of how the ambient workspace was detected.

If explicit extra modules (`-m`) are present, the engine skips CWD-module detection.

## CWD Module

The CWD module is the nearest `dagger.json` found by find-up from the caller's working directory.

It is not an alternate workspace. It is an additional module-loading convenience layered on top of the ambient workspace.

Conceptually:

```text
caller cwd
    ->
find-up nearest dagger.json
    ->
CWD module
```

## Behavior

### 1. Detection

After ambient workspace detection, the engine separately detects the CWD module:

```text
find-up nearest dagger.json
  -> load as CWD module
```

This step has no eligibility filter.

If explicit extra modules (`-m`) are present, this step is skipped.

### 2. Dedupe

The CWD module participates in generic module dedupe with other module-loading paths, including:

- ambient workspace modules
- extra modules (`-m`)
- any other engine-recognized module-loading path

If multiple paths nominate the same module, the engine loads it once.

For this purpose, "same module" means the same resolved module source identity:

- local modules compare by absolute source root plus source subpath
- git modules compare by clone ref plus source subpath
- if a pin is present, the pin must also match

### 3. Entrypoint

If the CWD module is distinct after dedupe, it becomes the active entrypoint for the invocation.

Ambient workspace modules remain loaded.

If the CWD module is not distinct after dedupe, nothing extra is loaded and the already-loaded module keeps its role.

If explicit extra modules (`-m`) are present, the CWD module is not loaded and therefore has no entrypoint role.

## Architecture

Target runtime path:

```text
engine/server
└─ detect ambient workspace context

then

engine/server
└─ detect CWD module
   ├─ if `-m` present, skip
   ├─ else find-up nearest dagger.json
   ├─ dedupe against already-nominated modules
   └─ if distinct, load as CWD module

then

engine/server
└─ resolve entrypoint
   ├─ distinct CWD module wins
   └─ otherwise keep existing entrypoint
```

## Implementation Guidance

1. Keep CWD-module detection separate from ambient workspace detection.
2. Reuse the generic module-deduplication mechanism. Do not add a CWD-specific dedupe hack.
3. Resolve CWD-module identity after source resolution, not by path string or module name alone.
4. Apply the same CWD-module rule regardless of how ambient workspace context was detected.

## Non-Goals

- This document does not define how ambient workspace context is detected.
- This document does not define migration or compat-workspace planning.
- This document does not define the full global module-loading pipeline beyond the CWD-module rule.
