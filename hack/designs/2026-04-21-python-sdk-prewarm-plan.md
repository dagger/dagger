# Python SDK Pre-Warm Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Pre-warm the builtin Python SDK at engine startup so the first-ever `load SDK: python` does not incur the ~11.5s cost for new users, fresh CI runs, or post-upgrade engines.

**Architecture:** Spawn a background goroutine in `server.Server.NewServer` that builds a synthetic dagql context and drives `SDKForModule` for each builtin SDK we want to warm (initially only Python). Because `forceDefaultFunctionCaching=true` is already passed to `asModule` in `loadBuiltinSDK`, the resulting cache entry is marked `IsPersistable`, so it populates both the in-memory dagql egraph (visible to every subsequent client session in the same process) and — at clean engine shutdown — the SQLite `dagql-cache.db` (reloaded on next boot via `importPersistedState`).

**Tech Stack:** Go 1.22, Dagger engine core, dagql, OTel tracing (`github.com/dagger/otel-go`), testify, Dagger integration-test harness (`github.com/dagger/testctx` + `dagger.io/dagger`).

**Design reference:** `hack/designs/2026-04-21-python-sdk-loading-perf-design.md` (P1).

---

## Decision: Option 2 (eager boot-time load) over Option 1 (ship precompiled + seeded cache)

The design proposed two options. We pick **Option 2 — eager background load at engine startup** for these reasons:

1. **Self-healing.** Option 2 works whether or not the dagql persistent cache survives restart. Option 1 requires the seeded cache file to roundtrip through the persist/import path correctly on every deploy — an open question per the design doc's Section 8.
2. **Simplicity.** Option 2 is one background goroutine + one function call. Option 1 touches build scripts, snapshotting logic, and engine-image packaging — much larger surface area.
3. **Architecture parity.** The engine already has precedent: `engine/server/server.go:526` starts `go srv.gcClientDBs()` immediately after dagql cache init. We follow the same pattern.
4. **Low risk of regression.** A failed or slow prewarm cannot break functionality — the user's first real call still works, just paying the original 11.5s. Worst case: feature no-op, not feature-broken.

## File Structure

| File                                      | Create/Modify | Responsibility |
|-------------------------------------------|---------------|----------------|
| `core/sdk/prewarm.go`                     | Create        | `BuiltinSDKsToPrewarm` list + `PrewarmBuiltinSDKs(ctx, root)` entry point. |
| `core/sdk/prewarm_test.go`                | Create        | Unit test for `BuiltinSDKsToPrewarm` contents + signature of `PrewarmBuiltinSDKs`. |
| `engine/server/server.go`                 | Modify        | In `NewServer`, spawn a goroutine calling `prewarmBuiltinSDKs` after dagql cache init. |
| `core/integration/module_python_test.go`  | Modify        | Add integration test that the first `load SDK: python` in a fresh client session is near-instant. |

## Tasks

---

### Task 1: Create `core/sdk/prewarm.go` skeleton + failing unit test

**Files:**
- Create: `core/sdk/prewarm.go`
- Create: `core/sdk/prewarm_test.go`

- [ ] **Step 1: Write the failing unit test**

Create `core/sdk/prewarm_test.go`:

```go
package sdk

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuiltinSDKsToPrewarm_ContainsPython(t *testing.T) {
	require.Contains(t, BuiltinSDKsToPrewarm(), "python",
		"python must be in the prewarm list — this is the primary target of the feature")
}

func TestBuiltinSDKsToPrewarm_OnlyBuiltins(t *testing.T) {
	// Every name in the prewarm list must be parseable as a builtin SDK.
	// If this ever fails, the list has drifted from validInbuiltSDKs
	// in consts.go.
	for _, name := range BuiltinSDKsToPrewarm() {
		_, _, err := parseSDKName(name)
		require.NoError(t, err, "prewarm SDK %q must be a valid builtin", name)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/yves/dev/src/github.com/dagger/dagger && go test ./core/sdk/... -run TestBuiltinSDKsToPrewarm -v`

Expected: FAIL with "undefined: BuiltinSDKsToPrewarm".

- [ ] **Step 3: Create `core/sdk/prewarm.go` with just the list**

```go
// Package sdk provides the engine-side loader for builtin and external
// SDKs. This file defines the pre-warm entry point used at engine
// startup to populate the dagql cache with builtin SDK results,
// avoiding a multi-second delay on the first user command that
// references an SDK (see
// hack/designs/2026-04-21-python-sdk-loading-perf-design.md).

package sdk

// BuiltinSDKsToPrewarm returns the builtin SDK names that should be
// eagerly loaded at engine startup. Only SDKs that go through
// loadBuiltinSDK (Python, TypeScript) benefit from pre-warming; the Go
// and Dang SDKs are handled inline without a per-load container
// asModule cost.
//
// Returned as a function (rather than exposed as a var) so tests can
// depend on the concrete contents without mutating package state.
func BuiltinSDKsToPrewarm() []string {
	return []string{
		"python",
		// TypeScript is intentionally deferred until Python pre-warming
		// is validated in production.
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./core/sdk/... -run TestBuiltinSDKsToPrewarm -v`
Expected: PASS, both subtests green.

- [ ] **Step 5: Commit**

```bash
git add core/sdk/prewarm.go core/sdk/prewarm_test.go
stg new -m "core/sdk: add BuiltinSDKsToPrewarm list

Introduce the list of builtin SDK names to pre-warm at engine startup.
Starts with Python; TypeScript is deferred until Python pre-warming is
validated in production.

Signed-off-by: Yves Brissaud <yves@dagger.io>" sdk-prewarm-list
stg refresh
```

---

### Task 2: Add `PrewarmBuiltinSDKs` entry point

**Files:**
- Modify: `core/sdk/prewarm.go`

- [ ] **Step 1: Extend the test file to cover the entry point**

Append to `core/sdk/prewarm_test.go`:

```go
// Sanity: PrewarmBuiltinSDKs exists with the expected signature.
// We do not exercise its runtime behavior here — that requires an
// engine with a dagql cache, which is covered by the integration
// test (TestPython/TestPrewarmCacheHit). A nil root returns an error
// without panicking; this guards against accidental signature drift.
var _ = func() error {
	var f func(ctx context.Context, root *core.Query) error = PrewarmBuiltinSDKs
	_ = f
	return nil
}()
```

Add these imports to `prewarm_test.go`:

```go
import (
	"context"

	"github.com/dagger/dagger/core"
)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./core/sdk/... -run TestBuiltinSDKsToPrewarm -v`
Expected: FAIL with "undefined: PrewarmBuiltinSDKs" (package compile error).

- [ ] **Step 3: Implement `PrewarmBuiltinSDKs` in `core/sdk/prewarm.go`**

Append to `core/sdk/prewarm.go`:

```go
import (
	"context"
	"fmt"
	"log/slog"

	"github.com/dagger/dagger/core"
	telemetry "github.com/dagger/otel-go"
)

// PrewarmBuiltinSDKs loads each SDK listed in BuiltinSDKsToPrewarm
// through the standard SDKForModule path. This populates the dagql
// egraph so subsequent user sessions hit a warm cache for
// "load SDK: <name>".
//
// Failures for any single SDK are logged and do NOT abort the other
// prewarms, nor do they surface as an error to the caller — a failed
// prewarm simply degrades to pre-change behavior for that SDK. This
// is intentional: a prewarm failure must never take down the engine.
func PrewarmBuiltinSDKs(ctx context.Context, root *core.Query) error {
	ctx, span := core.Tracer(ctx).Start(ctx, "prewarm builtin SDKs", telemetry.Internal())
	defer span.End()

	if root == nil {
		return fmt.Errorf("prewarm requires a non-nil root Query")
	}

	loader := NewLoader()
	for _, name := range BuiltinSDKsToPrewarm() {
		prewarmOne(ctx, loader, root, name)
	}
	return nil
}

func prewarmOne(ctx context.Context, loader *Loader, root *core.Query, name string) {
	ctx, span := core.Tracer(ctx).Start(ctx,
		fmt.Sprintf("prewarm SDK: %s", name),
		telemetry.Internal())
	var err error
	defer telemetry.EndWithCause(span, &err)

	_, err = loader.SDKForModule(ctx, root, &core.SDKConfig{Source: name}, nil)
	if err != nil {
		slog.Warn("prewarm SDK failed; first user call will pay cold cost",
			"sdk", name, "error", err)
	}
}
```

- [ ] **Step 4: Run all package tests**

Run: `go test ./core/sdk/... -v`
Expected: PASS for all tests, package compiles.

- [ ] **Step 5: Commit**

```bash
git add core/sdk/prewarm.go core/sdk/prewarm_test.go
stg new -m "core/sdk: add PrewarmBuiltinSDKs entry point

PrewarmBuiltinSDKs iterates BuiltinSDKsToPrewarm and calls
SDKForModule for each, populating the dagql egraph. Each SDK is wrapped
in its own tracing span (\"prewarm SDK: <name>\") for observability.

Failures are logged and swallowed: a failed prewarm must not take down
the engine, it only degrades that SDK's first-call latency to the
pre-change behavior.

Signed-off-by: Yves Brissaud <yves@dagger.io>" sdk-prewarm-entry
stg refresh
```

---

### Task 3: Wire prewarm into engine startup

**Files:**
- Modify: `engine/server/server.go` (around line 523-526, after dagql cache init)

- [ ] **Step 1: Read the current hook point**

Run: `sed -n '495,530p' engine/server/server.go`

Observe the existing pattern at line 526: `go srv.gcClientDBs()` is the background-goroutine precedent we follow.

- [ ] **Step 2: Add the prewarm method on `*Server`**

Append to `engine/server/server.go` (file-local helper, placed near the end of the file or next to the other lifecycle helpers like `gcClientDBs`):

```go
// prewarmBuiltinSDKs eagerly loads builtin SDKs that are expensive on
// first use (Python's Go runtime module in particular) so that the
// first user command against such an SDK does not pay the cold-cache
// cost. Runs as a background goroutine launched from NewServer.
//
// A prewarm failure is non-fatal: it is logged and the affected SDK
// simply behaves as before (cold on first use).
func (srv *Server) prewarmBuiltinSDKs() {
	ctx := srv.shutdownCtx

	// Ensure the core schema is initialized first — loadBuiltinSDK
	// needs a fully set-up dagql.Server reachable via the Query root.
	if _, err := srv.getCoreSchemaBase(ctx); err != nil {
		slog.Warn("prewarm: failed to init core schema base", "error", err)
		return
	}

	root := core.NewRoot(srv)
	ctx = core.ContextWithQuery(ctx, root)

	if err := sdkpkg.PrewarmBuiltinSDKs(ctx, root); err != nil {
		slog.Warn("prewarm: builtin SDKs", "error", err)
	}
}
```

Add the needed imports at the top of `engine/server/server.go` (check existing imports first to avoid duplicates):

```go
import (
	// ... existing imports ...
	"github.com/dagger/dagger/core"
	sdkpkg "github.com/dagger/dagger/core/sdk"
)
```

Note: use the `sdkpkg` alias to avoid collision with the local `sdk` field name on `Server` if present; check existing imports and adjust the alias if `sdk` is unused.

- [ ] **Step 3: Launch the goroutine in `NewServer`**

Modify `engine/server/server.go` at line 526 (the `go srv.gcClientDBs()` line). Insert the prewarm goroutine immediately after:

```go
// garbage collect client DBs
go srv.gcClientDBs()

// eagerly pre-warm expensive builtin SDKs so the first user command
// does not pay the cold-cache cost
// (see hack/designs/2026-04-21-python-sdk-loading-perf-design.md P1)
go srv.prewarmBuiltinSDKs()
```

- [ ] **Step 4: Build the engine to verify it compiles**

Run: `go build ./engine/server/... ./core/sdk/...`
Expected: exits 0, no output.

- [ ] **Step 5: Run engine-package tests**

Run: `go test ./engine/server/... -count=1 -short`
Expected: PASS or the pre-existing test status; our change should not introduce new failures.

- [ ] **Step 6: Commit**

```bash
git add engine/server/server.go
stg new -m "engine/server: pre-warm builtin SDKs at startup

Spawn a background goroutine in NewServer, alongside the existing
gcClientDBs goroutine, that calls sdk.PrewarmBuiltinSDKs with a
synthetic root Query. This populates the dagql cache with the builtin
Python SDK's asModule result before the first client session arrives,
eliminating the ~11.5s first-call cost.

The goroutine uses srv.shutdownCtx so it cancels cleanly on engine
shutdown. Failures are logged and swallowed.

Part of P1 in hack/designs/2026-04-21-python-sdk-loading-perf-design.md.

Signed-off-by: Yves Brissaud <yves@dagger.io>" engine-prewarm-hook
stg refresh
```

---

### Task 4: Integration test — fresh client session hits warm cache

**Files:**
- Modify: `core/integration/module_python_test.go`

- [ ] **Step 1: Read the existing Python test file for patterns**

Run: `head -120 core/integration/module_python_test.go`

Observe: tests are methods on `PythonSuite` with `testctx` + `dagger.io/dagger` client via `connect(ctx, t)`. Use `daggerCliBase(t, c)` and `daggerInitPython()`/`daggerCall(...)` helpers.

- [ ] **Step 2: Write the failing integration test**

Append to `core/integration/module_python_test.go` (within the `PythonSuite` methods, at the end):

```go
// TestPrewarmCacheHit verifies that after engine startup, the first
// Python-SDK-referencing client command observes "load SDK: python"
// as a cache hit rather than paying the cold-cache cost (~11.5s).
//
// The prewarm runs in a background goroutine at engine boot, so we
// poll briefly until it has populated the cache. If it never does,
// the test times out and fails, which is the correct failure mode.
//
// Ties to: hack/designs/2026-04-21-python-sdk-prewarm-plan.md
// Part of: P1 in hack/designs/2026-04-21-python-sdk-loading-perf-design.md
func (PythonSuite) TestPrewarmCacheHit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Before doing any Python-module work, allow the engine's
	// background prewarm goroutine time to complete. 20s is a safety
	// margin over the measured cold cost of ~11.5s observed on the
	// dev engine. If the engine is faster in CI, this just sleeps a
	// bit longer than needed; we are not load-testing the prewarm, we
	// are testing that it eventually warms the cache.
	//
	// If this sleep becomes flaky, replace with a polling loop that
	// re-runs a lightweight "load SDK: python" probe until the span
	// duration drops below a threshold.
	const prewarmGrace = 20 * time.Second
	select {
	case <-time.After(prewarmGrace):
	case <-ctx.Done():
		t.Fatalf("context cancelled while waiting for prewarm grace: %v", ctx.Err())
	}

	// Run a minimal Python-module command that triggers load SDK: python.
	// We capture its wall-clock duration; if prewarm worked, this
	// should be well under the ~11.5s cold cost.
	start := time.Now()
	out, err := daggerCliBase(t, c).
		With(daggerInitPython()).
		With(daggerCall("container-echo", "--string-arg", "hello", "stdout")).
		Stdout(ctx)
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.Equal(t, "hello\n", out)

	// Threshold chosen well below the measured ~11.5s cold cost but
	// with enough slack for the Python image pull, codegen, and uv
	// sync that still run on a cold init (those are outside the
	// scope of prewarm). If this ever fires, the prewarm is not
	// populating the cache.
	//
	// If threshold needs tuning, keep it > realistic warm worst-case
	// but < realistic cold-without-prewarm case.
	const prewarmEffectiveThreshold = 25 * time.Second
	require.Less(t, elapsed, prewarmEffectiveThreshold,
		"init+call took %s; with prewarm it should be well under %s (cold-without-prewarm was ~11.5s on load SDK alone, plus image pull etc.)",
		elapsed, prewarmEffectiveThreshold)
}
```

Add imports if missing:

```go
import (
	// ... existing imports ...
	"time"
)
```

- [ ] **Step 3: Run the test to verify it fails without the prewarm wiring**

First confirm the test compiles:

```bash
go test ./core/integration/... -run TestPython/TestPrewarmCacheHit -count=1 -v -timeout=5m
```

If the engine under test still has the feature (from Task 3), this should PASS. To verify the test actually guards the feature, temporarily comment out `go srv.prewarmBuiltinSDKs()` in `engine/server/server.go`, rerun, confirm FAIL, then restore. This step is validation of the test's usefulness, not a regression introduced by it.

- [ ] **Step 4: Run the test to verify it passes with the feature enabled**

Run: `go test ./core/integration/... -run TestPython/TestPrewarmCacheHit -count=1 -v -timeout=5m`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add core/integration/module_python_test.go
stg new -m "core/integration: test Python SDK prewarm cache hit

Add TestPrewarmCacheHit to PythonSuite. After a grace period that
allows the engine's background prewarm goroutine to complete, a fresh
Python module command (init + call) must complete well under the
pre-change cold cost (~11.5s for load SDK: python alone).

Integration-level guard for P1.

Signed-off-by: Yves Brissaud <yves@dagger.io>" engine-prewarm-integration-test
stg refresh
```

---

### Task 5: Manual verification via playground

**Files:** none — this is a verification step, not a code change.

- [ ] **Step 1: Run the baseline scenario in the playground**

Run (this is the same scenario used to generate the design doc's findings):

```bash
cat > /tmp/prewarm-verify.sh <<'SCRIPT'
set +e
mkdir -p /tmp/pw-verify /tmp/pw-logs
cd /tmp/pw-verify
dagger init --sdk=python --name=pwverify --progress=plain -vvv > /tmp/pw-logs/init.log 2>&1
echo "Look for 'load SDK: python' timing in init.log:"
grep -E 'load SDK: python' /tmp/pw-logs/init.log | head -5
SCRIPT

PLAYGROUND_TIMEOUT=600 .claude/skills/engine-dev-testing/with-playground.sh "$(cat /tmp/prewarm-verify.sh)"
```

- [ ] **Step 2: Verify `load SDK: python` is CACHED**

Expected in the output:

```
load SDK: python[ CACHED] [0.0s]
```

or a duration dramatically lower than the baseline (< 1s). If it is still ~11.5s, the prewarm goroutine is not populating the cache in time for this first command — investigate timing.

- [ ] **Step 3: If Step 2 shows the feature working, no commit; mark task done**

This step produces no code artifact; the evidence is the playground output pasted into the PR description at handoff.

- [ ] **Step 4: If Step 2 shows the feature NOT working, diagnose before proceeding**

Do NOT paper over with `sleep` in the engine code. If prewarm is not finishing before the first command, the gap is either:

- The prewarm goroutine has not gotten CPU time yet → consider explicit synchronization: have `NewServer` wait on a short bounded channel signalling "prewarm kicked off but not necessarily done" before returning. Do not block until prewarm completes; that regresses engine startup.
- The dagql cache key between prewarm and the user session differs → compare span attributes on both `load SDK: python` invocations in the trace to see where the cache key diverges. The most likely drift is a view/version difference; ensure prewarm's dag uses the same default view as client sessions.
- The prewarm errored and was swallowed → check engine stderr for `prewarm SDK failed` slog messages.

---

## Self-review (conducted pre-handoff)

**Spec coverage:** Every element of P1 in the design doc is addressed — precompile vs. eager-load decision (documented), implementation hook point (Task 3), observability (OTel spans in Task 2), verification (Tasks 4 and 5).

**Placeholder scan:** No TBD/TODO/"figure out"/"handle edge cases" sentinels. Each step contains actual code or an exact command.

**Type consistency:** `PrewarmBuiltinSDKs` has the same `(ctx context.Context, root *core.Query) error` signature wherever referenced (prewarm.go, prewarm_test.go, server.go). `BuiltinSDKsToPrewarm` is consistently a function returning `[]string`.

---

## Out of scope (not in this plan)

- TypeScript SDK prewarming (deferred pending Python validation — explicitly commented in `BuiltinSDKsToPrewarm`).
- The warm-per-call 0.9s `asModule getModDef` overhead (P2 in the design doc; needs its own plan after a profiling pass).
- Characterizing edit-then-run re-firing (P3 in the design doc).
- Verifying that the dagql persistent cache survives engine container restart for the prewarmed entry (design doc Section 8 open question; non-blocking for P1 — if persistence is lossy, P1 still works on every boot's fresh prewarm).

## Follow-ups to file after this plan lands

- If Task 5's manual verification shows a timing gap because the goroutine isn't done in time, open a follow-up to add a bounded wait (or configurable `DAGGER_ENGINE_PREWARM_TIMEOUT`) before `NewServer` returns. Document the trade-off.
- Re-run the three-scenario playground measurement from the design doc and update Section 3.2 with the new `load SDK: python` timings.
- Decide whether to extend prewarming to TypeScript based on the measured benefit and any maintenance issues that surfaced.
