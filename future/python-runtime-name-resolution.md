# Resolving the runtime name `python` to `dagger/python-sdk`

author: yves
created: 2026-08-20
status: **rejected — do not implement.** The investigated engine change is
unsound; the recommendation is the no-engine-change path (option C below).
related: `future/cli-1.0.md` (§ SDK module interface — the `runtime` field
contract, amended by this investigation),
`future/spin-out-generated-clients.md`,
`hack/designs/typescript-no-codegen-at-runtime.md`,
`github.com/dagger/python-sdk` PR #17 and its
`future/done/self-contained-python-sdk.md`

## Summary

**Question.** `dagger/python-sdk` PR #17 brings the Python module runtime into
that repository, deliberately without codegen at module load. Can the engine
route the short runtime name `python` there, so modules keep writing
`runtime = "python"` instead of a full git ref?

**Answer: no — not by any mechanism the engine has or could reasonably grow.**
The only route that keeps pre-1.0 `dagger.json` modules working is to split
resolution by module config format. That looks sound and is not: three
independent, verified defects sink it, one of which is fatal to the *concept*
rather than to an implementation of it.

**Recommendation.** Take option C: change nothing in `dagger/dagger`. Let
`dagger/python-sdk` PR 2 flip `targetRuntime` to
`github.com/dagger/python-sdk/runtime@<pin>`, which the engine already writes
verbatim into a new module's `[runtime] source` and already resolves. Reserve
builtin runtime names for artefacts the engine actually bundles, and record
that in `future/cli-1.0.md` so this is not re-litigated.

This document is the investigation, not a plan. Nothing in `core/sdk` changes.

## Problem

`runtime = "python"` in a `dagger-module.toml` has exactly one resolution
target, the module baked into the engine container
(`core/sdk/loader.go:141-142`):

```go
case sdkPython:
    return l.loadBuiltinSDK(ctx, root, sdk, digest.Digest(os.Getenv(distconsts.PythonSDKManifestDigestEnvName)))
```

`namedSDK` is tried before any ref resolution and only `errUnknownBuiltinSDK`
falls through (`core/sdk/loader.go:57-61`). The workspace
`[modules.<n>.as-sdk]` registry is an authoring registry that module loading
never consults. So python-sdk's runtime is reachable only by writing its full
ref into `[runtime] source`.

What a short name would buy is the indirection `runtime = "go"` exists to
provide: a portable name where the engine, not user config, owns which artefact
executes the module. That is a real benefit. It is not worth what it costs.

## Why the config-format split fails

The idea: `python` resolves to the engine-baked module for `dagger.json` and to
`github.com/dagger/python-sdk/runtime` for `dagger-module.toml`. It borrows a
seam the engine already has — `useRuntimeCodegen` (`core/sdk/utils.go:20-25`)
keys the runtime *contract* on config format, so keying the runtime *identity*
on it looks like the same move.

It is not. Three defects, each verified against `upstream/main` at `3e485b3ed`.

### 1. Config format is not a proxy for "has committed generated files" — fatal

`dagger setup` migration copies the legacy SDK source into the modern config
**verbatim** and marshals it as `ConfigFormatCurrent`:

- `core/modules/config_format.go:85` — `Runtime: modCfg.SDK`.
- `core/schema/workspace_migrate_module_config.go:82-93`
  (`legacyModuleConfigAsCurrent`) and `core/workspace/migrate.go:331-345`
  (`buildMigratedModuleConfig`) both marshal with
  `modules.ConfigFormatCurrent`.
- `internal/cmd/dagger/setup.go:335-388` (`setupResolveMigratedSDKs`) rewrites
  only workspace `[modules.*.as-sdk]` entries — never the migrated module's
  `[runtime] source`.

So `{"sdk": {"source": "python"}}` becomes `[runtime] source = "python"` in a
`dagger-module.toml`. Every one of this repository's 118 legacy Python modules,
and every one in the wild, lands in the "modern" bucket the moment the user runs
the migration verb this repo ships. The design's central safety claim —
"existing `dagger.json` Python modules: nothing changes" — is true only for as
long as nobody migrates.

This is not an implementation bug. The format is a statement about *file
syntax*; the property that matters is *whether the module has committed its
generated bindings*, and migration deliberately preserves the former while
saying nothing about the latter. No amount of care at the dispatch site
recovers a signal that isn't there.

(Partial mitigation, stated for fairness: a migrated module already switches to
the engine-baked runtime's *trusted* path today, because `useRuntimeCodegen` is
false for TOML and the baked runtime declares `introspectionJSON` optional —
`sdk/python/runtime/main.go:186-196`. So migration already requires committed
files at load. What migration does *not* change today is codegen, which is
defect 2.)

### 2. It breaks `Codegen` for modern Python modules — a released API

`runSDKCodegen` dispatches to the SDK's `Codegen` unconditionally, with no
format gate (`core/schema/modulesource.go:2593-2612`), and it is what
`ModuleSource.generatedContextDirectory` / `generate` reach. The engine-baked
Python runtime's `Codegen` is substantive — it vendors the SDK and regenerates
`gen.py` (`sdk/python/runtime/main.go:155-183`). python-sdk's runtime
implements `Codegen` as a documented no-op, because that repository's
*authoring* module owns generation through its `@generate` hook.

Under the split, any modern Python module generating through the engine API
gets nothing back. This repository proves it: `core/integration/module_runtime_codegen_test.go`
builds `dagger-module.toml` Python modules (`:103-107`, `:159-164`, `:192-203`,
via `configFile` at `core/integration/module_helpers_test.go:89-97`) and drives
`generatedContextDirectory`. The earlier claim that the change would be "inert
in this repository's CI" was wrong — it was based on finding no
`dagger-module.toml` files on disk, missing that the tests write them at
runtime.

### 3. `SDKForModule` is not only a runtime loader

`core/schema/modulesource.go:2742-2766` (`runClientGenerator`) loads the
**client generator** named in `[[clients]] generator` through the same
`SDKForModule`, passing the *hosting* module's source as `parentSrc`.

A format gate there decides generator identity by the consuming module's config
format — the wrong axis entirely, since a module's generator language is
independent of its runtime language. A TOML module in any language with
`generator = "python"` would resolve to python-sdk's runtime, which implements
neither `generateClient` nor `requiredClientGenerationFiles`
(`core/sdk/module_client_generator.go:23,52`), and fail. This one is mechanical
to avoid — gate only the runtime path — but it shows the dispatch point is
shared by questions the format cannot answer.

## The other options, and why they also lose

**A. python-sdk's runtime re-acquires a `dagger.json` codegen path.** Re-adds in
full what PR #17 removed: the code generator and client library move back into
the runtime module. It is also a `dagger/python-sdk` change, out of scope by
construction. And it makes the runtime re-derive a legacy/modern decision the
engine already makes and hands over explicitly.

**B. Engine-side split by config format.** The above.

**C. No `dagger/dagger` change — `targetRuntime` writes the ref.** Chosen.

**D. Consult the workspace `as-sdk` registry at module load.** Contradicts an
explicit decision (`engine/server/session_workspaces.go`: the runtime resolves
in-engine when a consuming module loads) and cannot work for the case that
matters most — a module loaded from Git as a dependency has no workspace of its
own. It also breaks the `dagger-module.toml` portability that
`future/cli-1.0.md:335` states outright.

**E. Bake python-sdk's runtime into the engine container.** The only option that
preserves offline loading, and the only one that would survive migration
unchanged. Rejected because it re-creates the release coupling PR #17 exists to
remove: python-sdk could not ship a runtime fix without an engine release, and
the engine build would vendor a second repository
(`.dagger/modules/engine-dev/build/sdk.go:38-131`). Worth keeping on the shelf
if the network dependency ever proves unacceptable.

## Why option C is right, not merely left standing

It is not a fallback. It is the mechanism `future/cli-1.0.md` already designed
for exactly this case.

- **`targetRuntime` is the delegating-SDK contract.** `future/cli-1.0.md:297`:
  *"Decoupling is opt-in: an SDK that delegates execution to a separate runtime
  module declares it via `targetRuntime`."* python-sdk is precisely that:
  authoring module at the repo root, runtime at `/runtime`. The engine already
  implements it — `core/schema/workspace_module_init.go:133-153` writes the
  returned value verbatim into the new module's `[runtime] source`, and
  `externalSDKForModule` resolves it. **Zero `dagger/dagger` changes are
  required for python-sdk PR 2 to work.**
- **A ref is a legal `runtime` value** (`future/cli-1.0.md:331`), pinnable by
  the author (`@<pin>`), visible in the config, and identical in lockfile
  treatment to anything the engine would have resolved on the user's behalf —
  both go through `ResolveDepToSource` → `git.head` → `git.ref`, which locks
  (`core/schema/git.go:1000-1005,1143-1149`; verified).
- **Migration stays safe.** A migrated module keeps `runtime = "python"`,
  keeps the engine-baked runtime, keeps working `Codegen`. Only newly authored
  modules get the ref, and they get it with their generated files already
  committed.
- **The engine ships no SDK alias knowledge**, which is what
  `future/cli-1.0.md:552` says it should do.

### The cost of option C, honestly

The ref becomes permanent data in every module ever created, so a repository
rename or path move breaks them. That is real, and it is the one thing the
short name would have prevented. The remedy already exists and is CLI-side:
`setupResolveMigratedSDKs` (`internal/cmd/dagger/setup.go:335-388`) is precisely
a "rewrite recorded sources during `dagger setup`" mechanism, established by
PR #13810. A rename is a data migration for the doctor verb, not a reason for
the engine to hold a table pointing at another project's default branch.

The second cost is that `python` and `go` now mean different kinds of thing —
`go` is engine-bundled, `python` is a ref written at init. That asymmetry is
honest: it reflects that python-sdk's runtime genuinely left the engine and
Go's did not.

## Consequences to record

- **Builtin runtime names stay reserved for what the engine bundles.** This
  closes `future/cli-1.0.md`'s open question at `:682` in the direction it
  already leaned. Amended there as part of this work.
- **`WorkspaceModuleForRuntime` (`core/sdk/workspace_module.go`) still has no
  non-test callers**, and its `sdkPython -> github.com/dagger/python-sdk` row
  is the *authoring* module, which implements no `moduleRuntime`. It should not
  be repointed at `/runtime` (python-sdk's own design doc proposed that); the
  right cleanup is deleting the function. Not done here — unrelated to this
  question and it would be scope creep on a doc-only change.
- **`parseSDKName` keeps rejecting `python@<version>`.** With option C a user
  who wants a pin writes the full ref, natively. There is nothing to relax.
  `dagger/python-sdk` also has **no git tags at all**, so a version selector
  would resolve to nothing today regardless.
- **Generated clients for modern Python modules are an open gap**, independent
  of this decision: python-sdk's runtime implements neither `generateClient`
  nor `requiredClientGenerationFiles`. `future/spin-out-generated-clients.md`
  already says those workflows should move SDK-side; that pending work becomes
  load-bearing once python-sdk serves modern modules. Flagged for
  `dagger/python-sdk`, not solved here.
- **`dagger/python-sdk` PR 2 should pin.** `targetRuntime` is written with no
  `@version` and no pin, so an unpinned value floats python-sdk's default
  branch for every module it creates. A pinned ref costs nothing and removes
  the float. python-sdk's call, reported not made.

## What would change this answer

- If migration stopped preserving `runtime = "python"` across the format
  boundary — i.e. if `dagger setup` resolved builtin runtime names to explicit
  targets at migration time, the way it already resolves `as-sdk` sources
  (`setupResolveMigratedSDKs`). Then config format would carry real
  information and defect 1 would go away. That is a much larger change to the
  migration contract and belongs to whoever owns migration, not to this
  question.
- If the engine grew a codegen-capability negotiation for the modern path, so
  a runtime with a no-op `Codegen` could delegate generation to the workspace's
  authoring SDK instead of silently producing nothing. That would fix defect 2
  on its own merits, whatever happens to the name.

## Method

Investigated against `upstream/main` at `3e485b3ed` (2026-08-20), with
`dagger/python-sdk` PR #17's branch read directly for the runtime's actual
surface. The rejected design was written out in full and put through two
independent adversarial reviews (a correctness skeptic and a design/spec
reviewer) before any code was written; both rejected it independently and
converged on option C. All three defects above were then re-verified
first-hand against the cited lines. No engine code was written.

## Progress

- **Phase 0 — orientation: done.**
  - Worktree `…/python-runtime-name-resolution-lead-327a7f37-914e99eb`, branch
    `python-runtime-name-resolution-lead-327a7f37`, clean, at `upstream/main`
    `3e485b3ed`. Base `main`. Remotes: `origin=eunomie/dagger`,
    `upstream=dagger/dagger`.
  - Design-doc home: `future/`. VCS: plain git. Sign-off:
    `Signed-off-by: Yves Brissaud <yves@dagger.io>` per `CONTRIBUTING.md`'s
    DCO. No AI attribution anywhere. Host: GitHub.
- **Phase 1/2 — design and implementation plan: written, then rejected.** The
  superseded plan proposed a `builtinRuntimeRef(name, suffix, format)` table
  and a 6-commit series in `core/sdk`. It is not reproduced here; the three
  defects above are why.
- **Phase 3 — adversarial design review: FAILED, round 1, and the design was
  withdrawn rather than revised.** Two independent reviewers, both rejecting,
  both recommending option C. The blockers were verified first-hand rather
  than taken on report. Revising was not attempted: defect 1 falsifies the
  premise, not the plan.
- **Phases 4–5 — implementation and code review: not applicable.** No engine
  change is warranted. The deliverable is this document plus the
  `future/cli-1.0.md` amendment.
