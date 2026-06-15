# Go review reference (engine, core, dagql, codegen, sdk/go)

## Lint

Don't hand-replicate the linter config — it drifts. The curated linter set in `.golangci.yml` is enforced by `dagger check go:lint`; run that to surface anything golangci-lint catches rather than eyeballing it. Focus your review on what the linter *can't* catch (the points below); only call out a lint-class issue manually if it suggests the author never ran the linters at all.

One config rule worth remembering, since it's a judgment call the linter only partially enforces: **do not blindly add `//nolint` to silence linters**. A new `//nolint` without a justification comment is a finding (`nolintlint` is on, so bare nolints fail anyway).

## Engine-specific review points

- **Context discipline**: ctx is the first parameter; propagate cancellation; don't store ctx in structs. Be alert to operations that should respect client disconnects.
- **Cache & determinism (core/, dagql/)**: every dagql operation's result must be fully determined by its inputs. New fields/args that affect output must participate in the cache key. Reading wall-clock time, env vars, or host state inside a cached op is a red flag. Consult `skills/cache-expert/SKILL.md` when in doubt.
- **Resource cleanup**: buildkit refs, snapshots, sessions, and containers must be released on all paths — check `defer` placement relative to error returns.
- **Locking**: the engine is highly concurrent. New maps/slices reachable from multiple sessions need synchronization; check existing mutexes' documented invariants before adding fields they're meant to guard.
- **Errors crossing the API boundary**: GraphQL errors should be actionable to users; avoid leaking internal stack details, but don't lose the cause either.

## Codegen-specific review points

- Template changes (`cmd/codegen/generator/...`) must come with regenerated outputs across affected SDKs in the same PR.
- Generated code must stay compatible with the oldest supported language versions documented for each SDK runtime.
- Check that introspection-schema handling is backwards compatible: generated clients run against older engines.

## Go style expectations beyond lint

- Accept interfaces, return concrete types, except where the codebase establishes otherwise.
- Table-driven tests with `t.Run` subtests; integration tests live in `core/integration` and use the existing harness (see `skills/engine-dev-testing`).
- Error message strings: lower-case, no trailing punctuation, include the failing identifier/value.
- Keep exported API surface minimal; new exported symbols need doc comments (revive enforces).
