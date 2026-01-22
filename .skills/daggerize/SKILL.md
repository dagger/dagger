---
name: daggerize
description: Add or improve Dagger automation in a codebase. Use when asked to create Dagger modules or toolchains, wire up dagger.json, or define build/test/run/service commands callable via `dagger call` (including `up --ports` for services). Also use to diagnose Dagger execution errors and adjust containers, caches, or inputs.
---

# Daggerize

## Overview
Create minimal, reusable Dagger workflows for building, testing, and running projects; prefer toolchains for project-facing automation and keep functions composable.

## Workflow
1. **Scan the repo**: identify server/client layout, languages, existing CI, and any `dagger.json` or prior Dagger modules.
2. **Choose integration**:
   - **Toolchain** when you want user-facing commands (`dagger call <toolchain> ...`) without turning the repo itself into a module.
   - **Module** when the repo should export reusable functions to other Dagger modules.
3. **Initialize**:
   - Module: `dagger init --sdk=<language>` in the module root.
   - Toolchain: `dagger init --sdk=<language>` in a `toolchains/<name>` dir and register it in the root `dagger.json`.
4. **Define core functions** (typical set):
   - `build(...)`: produce artifacts; use `.sync()` to force evaluation.
   - `test(...)`: run unit/integration tests.
   - `serve(...)`: return a `Service` with exposed ports.
   - For multi-component repos: `backendBuild`, `backendServe`, `clientBuild`, and a top-level `build`/`serve`.
5. **Use inputs, not host paths**:
   - Accept `Directory` arguments with `defaultPath` for local sources.
   - Keep ignore/exclude lists small and targeted (e.g., `target`, `node_modules`).
6. **Add caches and env**:
   - Mount language-specific caches (cargo, npm, go build cache, etc.).
   - Set `HOST=0.0.0.0` in containers for services that must bind on all interfaces.
7. **Document usage**:
   - For humans: `dagger call ...`
   - For LLMs/CI logs: `dagger --progress=plain call ...`

## Gotchas
- **No direct host access inside toolchains**: avoid `dag.host().directory(...)`; use `Directory` args with `defaultPath`.
- **Directory inputs are the contract**: if sources are missing, expose them as args rather than reaching outside the module.
- **Build without services**: use `.sync()` on the container/object to force evaluation for `build`.
- **Service exposure**: you must `withExposedPort` and run `dagger call ... up --ports host:container`.
- **Bind address**: services must bind to `0.0.0.0` inside the container to be reachable.
- **Interdependent services**: use `withServiceBinding("backend", backendService)` and point the client at `http://backend:<port>` via env; expose only the client port to the host.
- **Frontend dev servers**: run with `--host 0.0.0.0` and an explicit port; consider `strictPort: true` in Vite config for reproducibility.
- **Language/toolchain mismatch**: base images must support the project’s language version (e.g., Rust edition 2024 needs Rust 1.85+).
- **Don’t bake repo specifics into the toolchain**: keep functions generic and configurable via args.

## References
- `references/toolchains.md` — toolchain vs module, registration, CLI usage patterns.
- `references/services.md` — service patterns, ports, and `up` usage.
- `references/rust-server.md` — Rust container, caches, editions.
- `references/server-client.md` — patterns for multi-component repos.
