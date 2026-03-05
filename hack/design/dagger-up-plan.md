# `dagger up` Phase 1 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement `dagger up` — a CLI command that discovers `+up`-annotated functions returning `Service`, starts all services in parallel, and tunnels their ports to the host.

**Architecture:** Follows the established check/generate pattern: pragma annotation → `Function.IsService` field → `ModTreeNode` rollup → `Up`/`UpGroup` runtime types → GraphQL schema → CLI command. Each layer mirrors its check/generate counterpart exactly.

**Tech Stack:** Go (core engine, CLI, codegen), Python/TypeScript/Java (SDK annotations), GraphQL (schema), dagql (server framework)

**Design doc:** `hack/design/dagger-up.md`

**Existing WIP:** `git stash@{0}` contains a nearly-complete implementation (2410 lines across 40 files). The plan references this stash extensively. Most tasks involve popping the stash, fixing known issues, and committing in logical patches.

**Testing:** Use the `engine-dev-testing` skill (playground) to build and test engine changes. Do NOT use `go build`, `go test`, or any other toolchain directly — Dagger builds and tests Dagger. The playground builds a dev engine from source and runs commands inside an ephemeral container. It is heavy (1-5 min build time), so use it only at key verification checkpoints, not after every small edit.

---

## Known Issues in Stash

Before starting, be aware of these bugs discovered during audit:

1. **`core/modtree.go` — `RunUp()` signature mismatch**: Calls `node.Run()` with 7 args but the function takes 5. Must be rewritten to match `RunCheck`/`RunGenerator` pattern (inline tracing).
2. **Phantom file**: `sdk/python/src/dagger/client/sdk/python/src/dagger/client/gen.py` — wrongly nested duplicate. Must be deleted.
3. **`go.mod` version**: `core/integration/testdata/services/hello-with-services/go.mod` is pinned to `v0.19.11`. May need bumping.

---

## Task 1: Pop Stash and Create Base Patch

**Files:** All 40 files from `stash@{0}`

**Step 1: Pop all patches to get a clean base**

```bash
stg pop --all
```

**Step 2: Pop the stash onto the working tree**

```bash
git stash pop stash@{0}
```

**Step 3: Delete the phantom file**

```bash
rm -rf sdk/python/src/dagger/client/sdk/
```

**Step 4: Verify the deletion removed only the wrongly-nested duplicate**

```bash
ls sdk/python/src/dagger/client/gen.py  # should exist (the real one)
ls sdk/python/src/dagger/client/sdk/    # should not exist
```

**Step 5: Stage everything and create a WIP patch**

```bash
git add -A
stg new wip-dagger-up -m "wip: dagger up implementation" --sign
stg refresh
```

**Step 6: Push remaining patches back on top in order**

```bash
stg push playground-skill design-doc impl-plan
stg series
```

Expected stack (bottom to top):
```
  wip-dagger-up       — wip: dagger up implementation
  playground-skill     — (adds engine-dev-testing skill, needed by Tasks 3/4/7)
  design-doc           — docs: add dagger up design document
  impl-plan            — docs: add dagger up implementation plan
```

---

## Task 2: Fix `RunUp()` Signature in `core/modtree.go`

**Files:**
- Modify: `core/modtree.go` — the `RunUp` function

**Context:** The stash calls `node.Run()` with extra tracing args that don't exist in the `Run()` signature. `RunUp` must follow the same pattern as `RunCheck` (line 114–139) — tracing is done inside the `runLeaf` callback.

**Step 1: Read current `RunCheck` to confirm the pattern**

Read `core/modtree.go:114-139` — this is the reference implementation.

**Step 2: Rewrite `RunUp` to match `RunCheck` pattern**

The corrected `RunUp` should look like:

```go
func (node *ModTreeNode) RunUp(ctx context.Context, include, exclude []string) error {
	return node.Run(ctx,
		func(n *ModTreeNode) bool { return n.IsService },
		func(ctx context.Context, n *ModTreeNode, clientMD *engine.ClientMetadata) (rerr error) {
			ctx, span := Tracer(ctx).Start(ctx, node.PathString(),
				telemetry.Reveal(),
				trace.WithAttributes(
					attribute.Bool(telemetry.UIRollUpLogsAttr, true),
					attribute.Bool(telemetry.UIRollUpSpansAttr, true),
					attribute.String(telemetry.ServiceNameAttr, node.PathString()),
				),
			)
			defer func() {
				telemetry.EndWithCause(span, &rerr)
			}()
			return n.runUpLocally(ctx)
		},
		include, exclude)
}
```

**Step 3: Verify `runUpLocally` exists and is correct**

Read the `runUpLocally` function from the stash. It should:
1. Call `node.DagqlValue(ctx, &result)` to evaluate the `+up` function
2. Get a `*Service` from the result
3. Call `srv.Select(ctx, svcResult, &voidResult, dagql.Selector{Field: "up"})` to tunnel and block

**Step 4: Refresh the patch**

```bash
stg refresh
```

---

## Task 3: Verify Engine Builds with Playground

**Context:** Do NOT use `go build` directly — it won't work on macOS for engine code. Use the `engine-dev-testing` skill's playground to build a dev engine from source.

**Step 1: Run a basic smoke test via the playground**

```bash
# Run in background — playground takes 1-5 minutes to build
skills/engine-dev-testing/with-playground.sh 'dagger version'
```

This builds the entire engine + CLI from your local source. If there are compilation errors in any modified files (core/, cmd/, etc.), the build will fail here.

Expected: `=== Playground: SUCCESS ===` with a `dagger version` output showing the dev build.

**Step 2: If the build fails, read the error output**

Look for Go compilation errors in the progress trace. Fix them in the relevant files.

**Step 3: Refresh the patch if any fixes were needed**

```bash
stg refresh
```

---

## Task 4: Smoke Test `dagger up --list` via Playground

**Files:**
- Test: `core/integration/up_test.go`
- Fixture: `core/integration/testdata/services/hello-with-services/`

**Step 1: Update the test fixture `go.mod` if needed**

Check if `go.mod` references an old engine version. If so, update to match the current engine version used by other test fixtures.

**Step 2: Smoke test `dagger up -l` in the playground**

```bash
skills/engine-dev-testing/with-playground.sh '
cd src/dagger/core/integration/testdata/services/hello-with-services
dagger up -l
'
```

Expected: `=== Playground: SUCCESS ===` with a table listing `web`, `redis`, and `infra:database`.

**Step 3: Refresh the patch if any fixture fixes were needed**

```bash
stg refresh
```

---

## Task 5: Add Integration Test for Service Startup

**Files:**
- Modify: `core/integration/up_test.go`
- Reference: `core/integration/services_test.go` (for patterns on testing service tunneling)

**Step 1: Read existing service tests for patterns**

Read `core/integration/services_test.go` to understand how service startup and port tunneling are tested in this codebase. Look for patterns around `AsService()`, tunnel verification, and context cancellation.

**Step 2: Write a test that starts a service and verifies it's reachable**

Add a test `TestUpRunService` that:
1. Loads the `hello-with-services` module
2. Calls `dagger up web` (pattern filter to just one service)
3. Verifies the service starts (nginx on port 80 is tunneled to host)
4. Cancels the context and verifies clean shutdown

**Step 3: Refresh the patch**

```bash
stg refresh
```

---

## Task 6: Add Port Collision Detection Test

**Files:**
- Modify: `core/integration/up_test.go`
- May modify: `core/up.go` or `core/modtree.go` (if collision detection isn't implemented yet)

**Step 1: Write the failing test**

Add a test `TestUpPortCollision` that:
1. Creates a module with two `+up` functions exposing the same port
2. Calls `dagger up` (run all services)
3. Expects a clear error about port collision

**Step 2: Implement collision detection if needed**

If the Service tunnel layer doesn't already detect port conflicts, add detection in `UpGroup.Run()` before starting services:
- Collect all exposed ports from all services
- Check for duplicates
- Fail fast with an error message listing conflicting services and ports

**Step 3: Commit**

```bash
stg new port-collision -m "feat: detect port collisions in dagger up" --sign
stg refresh
```

---

## Task 7: Run Integration Tests via Playground

**Context:** Use the playground to run the integration tests. This is the final verification before finalizing patches. The playground is heavy, so this is a single consolidated run.

**Step 1: Run `TestUp*` tests in the playground**

The integration test suite uses `go test` internally, but must run inside the dev engine environment. Check how other integration tests are run (look at `core/integration/` test patterns and CI configuration) to determine the correct invocation.

A typical approach:

```bash
skills/engine-dev-testing/with-playground.sh '
cd src/dagger
go test -v -run "TestUp" -count=1 ./core/integration/
'
```

Expected: All `TestUp*` tests pass.

**Step 2: If tests fail, fix issues and re-run**

Fix any failures, `stg refresh`, and re-run the playground. Since each playground run is expensive, batch all fixes before re-running.

**Step 3: Verify the patch stack is clean**

```bash
stg series
```

Expected: A clean stack of patches — `wip-dagger-up`, `playground-skill`, `port-collision`, `design-doc`, `impl-plan`.

---

## Task 8: Squash and Finalize Patches

**Step 1: Review the full patch stack**

```bash
stg series --all
```

**Step 2: Consider squashing related patches**

The WIP + fix patches can be squashed into logical commits:
- One commit for core engine changes (typedef, modtree, up.go, schema)
- One commit for SDK annotations (Go codegen, Python, TypeScript, Java)
- One commit for CLI (cmd/dagger/up.go, main.go, module.go)
- One commit for tests
- One commit for the design doc

Or keep as a single feature commit if preferred. This is a judgment call at review time.

---

## Summary of Patch Stack (expected final state)

```
  wip-dagger-up       — feat: implement dagger up command
  playground-skill     — adds engine-dev-testing skill
  port-collision      — feat: detect port collisions in dagger up
  design-doc          — docs: add dagger up design document
  impl-plan           — docs: add dagger up implementation plan
```

## Files Touched (complete list)

**New files:**
- `cmd/dagger/up.go`
- `core/up.go`
- `core/schema/serviceentries.go`
- `core/integration/up_test.go`
- `core/integration/testdata/services/hello-with-services/` (dagger.json, go.mod, go.sum, main.go)
- `sdk/java/dagger-java-sdk/src/main/java/io/dagger/module/annotation/Up.java`

**Modified files:**
- `cmd/codegen/generator/go/templates/module_funcs.go` — `+up` pragma parsing
- `cmd/dagger/main.go` — register `upCmd`
- `cmd/dagger/module.go` — add flags to `upCmd`
- `core/env.go` — `Env.Services()`
- `core/modtree.go` — `IsService`, `RollupServices()`, `RunUp()`, `runUpLocally()`
- `core/module.go` — `Module.Services()`
- `core/modules/config.go` — `ignoreServices` config field
- `core/schema/coremod.go` — register `upSchema`
- `core/schema/env.go` — `Env.services` resolver
- `core/schema/module.go` — `services`, `service`, `withService` resolvers
- `core/schema/modulesource.go` — minor
- `core/toolchain.go` — `IgnoreServices` field
- `core/typedef.go` — `IsService`, `WithService()`
- `dagql/dagui/spans.go` — `ServiceName` span field
- `sdk/go/dagger.gen.go` — generated types
- `sdk/go/dag/dag.gen.go` — generated delegators
- `sdk/go/telemetry/attrs.go` — `ServiceNameAttr`
- `sdk/java/.../DaggerModuleAnnotationProcessor.java` — `@Up` detection
- `sdk/java/.../FunctionInfo.java` — `isUp` field
- `sdk/python/src/dagger/client/gen.py` — generated types
- `sdk/python/src/dagger/mod/__init__.py` — export `up`
- `sdk/python/src/dagger/mod/_module.py` — `up()` decorator
- `sdk/python/src/dagger/mod/_resolver.py` — `SERVICE_DEF_KEY`
- `sdk/python/src/dagger/mod/_types.py` — `service` field
- `sdk/typescript/src/api/client.gen.ts` — generated types
- `sdk/typescript/src/module/decorators.ts` — export `up`
- `sdk/typescript/src/module/entrypoint/register.ts` — `withService()` call
- `sdk/typescript/src/module/introspector/dagger_module/decorator.ts` — `UP_DECORATOR`
- `sdk/typescript/src/module/introspector/dagger_module/function.ts` — `isService`
- `sdk/typescript/src/module/registry.ts` — `up` decorator factory
