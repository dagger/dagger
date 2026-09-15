# `dagger generate` — surface the underlying exec error

Scope: generator execution + changeset evaluation, as consumed by `dagger
generate`. Fixes [#13606](https://github.com/dagger/dagger/issues/13606).

## Problem

When a generator's changeset fails to evaluate, `dagger generate` hides the real
failure. The generator renders as a **green ✔ check**, and the error the user is
left with — plain summary and pretty/report TUI alike — is content-free:

```text
Error: compute their paths: evaluate changeset directories: exit code: 3
```

It names neither the command that failed nor its stderr. Running the changeset
directly does show it (`dagger call golang --generate=sdk/go generate-all
is-empty`), so the information exists — `dagger generate` just drops it.

## Why it's broken

A `+generate` function returns a **lazy** `Changeset`: its `Before`/`After`
directories are backed by an exec (the SDK codegen) that has not run yet.

`ModTreeNode.runGeneratorLocally` runs *inside* the per-generator span
(`RunGenerator` opens it revealed, with rolled-up logs), but it only grabs the
lazy `Changeset` — it never forces it. And `Changeset.Evaluate` was a **no-op**
(`return nil`), so even `changeset.sync` forced nothing.

So the exec is forced much later, during the changeset **merge**
(`ComputePaths`/`AsPatch` → `cache.Evaluate`), *after* the generator span has
already ended successfully. Two consequences:

1. The generator row renders **green** even though it failed — a false positive
   (this is the tell that the failure is attributed to the wrong place).
2. The failure lands on the merge, not the generator. Worse, the failing exec is
   never a *visible* span there, and the merge error still carries the
   `*ExecError`'s origin annotation — which makes `renderStepError` suppress the
   merge span's own message in favor of an origin span it cannot render. Net: the
   user gets a bare `exit code: N`, and in the TUI, nothing at all.

## Fix

Force the changeset to evaluate **inside the generator's span**, so the exec runs
— and any failure is attributed — there, exactly like any other `withExec`
failure. Two small changes in `core`:

- **`Changeset.Evaluate(ctx)`** now evaluates its before/after directories
  (`dagql.EngineCache(ctx).Evaluate(ctx, ch.Before, ch.After)`) instead of being
  a no-op. `changeset.sync` (a registered `Syncer`) therefore actually forces the
  changeset now — the intended semantics of `sync`.
- **`ModTreeNode.runGeneratorLocally`** calls `changes.Self().Sync(ctx)` after
  grabbing the lazy changeset. Generators already run in parallel and the
  changeset has to sync eventually, so nothing that wasn't going to run is
  deferred — it just runs where it can be attributed.

Now a failing generator's exec runs within the revealed generator span: the row
goes **red**, its stderr and exit code surface through the span tree, and the run
fails at the generator instead of limping on to the merge. No error-message
reconstruction is needed — the earlier `mergeEvalError` message-building helper
is removed, and the merge wrap sites go back to plain `fmt.Errorf`.

Credit: this is [@vito's](https://github.com/dagger/dagger/pull/13664) direction
— an earlier revision reconstructed the message at the merge boundary, which left
the false-green check in place; forcing evaluation in the span fixes the
attribution at the root.

## Test strategy

**1. Integration — real `dagger generate` (`core/integration/generators_test.go`).**
A `lazy-exec-failure` `+generate` fixture whose changeset's exec fails only when
forced (stderr marker via `$MARKER` so it is distinct from the command text):

```go
// +generate
func (m *HelloWithGenerators) LazyExecFailure() *dagger.Changeset {
  failed := dag.Container().From("alpine").
    WithEnvVariable("MARKER", "STDERR_ONLY_MARKER").
    WithExec([]string{"sh", "-c", "echo $MARKER >&2; exit 3"}).
    Directory("/")
  return failed.Changes(dag.Directory())
}
```

Drive `dagger generate lazy-exec-failure --progress=plain` and assert the output
surfaces the command (`sh -c`), the stderr (`STDERR_ONLY_MARKER`), and the exit
code (`exit code: 3`). Keep the existing eager `changeset-failure` `"error"`
subtest.

**2. Golden — rendered TUI (`dagql/idtui/golden_test.go`).** The
`TestTelemetry/TestGolden` flow only drove `call`/`check`; add a `Generate` mode
(`dagger --workdir <mod> generate <fn> -y`) plus a `GenerateFail` `+generate`
generator in `viztest`. The committed golden
(`testdata/TestTelemetry/TestGolden/generate-fail`) pins the rendered output —
now a **red generator row** with the exec's logs, which is the attribution the
fix is really about and is visible in the PR diff. Regenerate with
`dagger -c 'engine-dev | test-telemetry --update | export .'`.

## Notes

- `dagger check` generate-as-check and other callers benefit for free: any
  generator whose evaluation fails now fails within its own span.
- Making `Changeset.Evaluate` real is a behavior change to the public
  `changeset.sync` field — but forcing evaluation is what `sync` is supposed to
  do, and no caller relied on the no-op.
