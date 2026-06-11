# TypeScript SDK review reference (sdk/typescript)

## Layout

- `src/api/` — client, generated bindings (`client.gen.ts` — generated, never style-reviewed)
- `src/module/` — module runtime support (decorators, registry, introspection of user modules)
- `src/provisioning/`, `src/connect.ts` — engine provisioning and connection
- `src/telemetry/` — OTel integration
- `src/common/` — shared errors/utilities
- `runtime/` — the **Go** module implementing the TS runtime (entrypoint generation, package-manager detection, node/bun/deno specifics). Review with `references/go.md`. Pinned tool versions live in `runtime/tsdistconsts`.

## Multi-runtime support is the trap zone

Everything in the SDK must work on **Node, Bun, and Deno**. Common regressions:

- Node-only built-ins or APIs not available (or behaving differently) in Deno/Bun.
- `node:` import specifiers vs bare imports — match existing patterns in the file.
- Filesystem/path assumptions: POSIX-only path handling breaks Windows users; check `path.join`/`sep` usage.
- Package-manager detection logic in `runtime/` must handle yarn/npm/pnpm/bun lockfiles consistently — changes here need test coverage for each manager.
- Changes to runtime defaults or pinned versions (`tsdistconsts`) are user-facing: changie entry required.

## Lint/style in force (eslint.config.js)

typescript-eslint recommended + Prettier. Flag what these would catch only as a "linters not run" checklist finding, but also watch for what they *don't* catch:

- `any` creep: prefer precise types or generics; `unknown` + narrowing over `any`. (tseslint recommended warns on explicit `any` — treat new ones as findings needing justification.)
- Floating promises: every promise is awaited or explicitly voided with a reason.
- Public API: exported symbols need TSDoc comments; check `docs` generation isn't broken by signature changes (`eslint-docs.config.js` exists for doc linting).
- Errors: use the SDK's error classes from `src/common/errors` rather than throwing raw `Error` where a typed error exists; error messages should be actionable.
- Avoid default exports; match named-export style of surrounding code.

## API surface & codegen interplay

- Changes to `src/api/client.gen.ts` must originate from codegen template changes (in `cmd/codegen`) — hand edits to generated files are a blocker.
- New public SDK API should be consistent with the Go and Python SDKs' naming for the same concept; divergence needs justification or a cross-SDK plan.
- Decorator/registry changes in `src/module/` affect how user modules are introspected — verify against the test fixtures and check `runtime/` template code that consumes them.

## Tests

- Unit tests colocated under `src/test` / alongside modules; SDK integration tests run via `dagger check *sdk:*test*`.
- Module-runtime changes need fixture coverage across node/bun/deno where behavior differs.
- A bugfix without a test that fails on main is a `major` finding unless genuinely untestable.
