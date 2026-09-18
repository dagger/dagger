# Cache savings report demo

## What to explain

- Every completed workflow now shows its **cache hit rate** and number of
  **mutualized jobs**.
- After Dagger has seen a comparable colder run, a warmer run can also show:
  - wall-clock time saved;
  - CPU work avoided;
  - network transfer avoided.
- Wall-clock savings compare whole-workflow makespans. Parallel branches are
  not added together: if 10-second and 8-second branches run together, their
  combined benefit is at most 10 seconds.
- Unrelated work is included too. If an 8-second job still runs beside a
  cached 10-second branch, the workflow finishes about 2 seconds faster.
- These are estimates based on an earlier, colder local run. Missing or
  incompatible measurements are omitted instead of guessed.
- Peak memory is intentionally not reported because independent memory peaks
  may occur at different times and cannot safely be added together.

## Before the demo

From the repository root, build a fresh demo engine and CLI. The unique engine
name gives the demo a fresh engine cache without deleting an existing one.

```bash
export CACHE_DEMO_ENGINE="dagger-engine.cache-demo-$(date +%s)"
export _EXPERIMENTAL_DAGGER_DEV_CONTAINER="$CACHE_DEMO_ENGINE"
export _EXPERIMENTAL_DAGGER_DEV_IMAGE="localhost/$CACHE_DEMO_ENGINE"
export XDG_CACHE_HOME="$(mktemp -d)"
./hack/build
```

Keep this terminal open so all commands retain the same environment.

## Run the demo

1. Explain that the first run establishes the colder local baseline.

   ```bash
   ./bin/dagger api call engine-dev network-cidr
   ```

2. Point out the first report's exact counters:

   ```text
   == CACHE ==
   Cache hit rate: ...
   Mutualized jobs: ...
   ```

3. Run the identical workflow again. This run should reuse substantially more
   work.

   ```bash
   ./bin/dagger api call engine-dev network-cidr
   ```

4. Point out the new section. Exact values vary by machine and network:

   ```text
   == ESTIMATED CACHE SAVINGS ==
   Compared with a colder run (... cache hit rate)
   Finished ~... faster (...%)
   Compute avoided: ~... of CPU work
   Network transfer avoided: ~...
   ```

5. Emphasize that CPU work can exceed elapsed time because work runs in
   parallel. It describes avoided compute, not additional waiting time.

## If the estimates do not appear

- Confirm both runs used the same working directory and `XDG_CACHE_HOME`.
- Confirm the second run has a higher cache hit rate than the first.
- Start again with a new `CACHE_DEMO_ENGINE` and a new `XDG_CACHE_HOME`.
- CPU or network lines can be absent when that workflow emitted no matching
  resource measurements; this is expected.

The local comparison history is stored in
`$XDG_CACHE_HOME/dagger/cache-impact.json`.
