---
name: ci-debug
description: >
  Investigate Dagger CI check failures for a given commit on the dagger/dagger
  repository. Pulls the check list from Dagger Cloud, fetches traces for the
  failing ones, and produces a triage summary that separates real test
  regressions from cascading kills, infra hiccups, and intentional negative-
  test noise. Use this whenever the user asks about CI status, failing checks,
  "what's failing on my PR", "why did CI fail", "is the build green", check
  retries, trace investigation, or anything similar in the context of the
  dagger/dagger repo — even if they don't say the word "CI". Also triggers on
  /ci-debug.
---

# CI Debug

Diagnose Dagger CI check failures for a commit. The point of this skill is to turn the noisy bubble of errors that comes out of `dagger trace` into a *triage* the user can act on: "these 2 are real failures worth investigating, these 4 are cascading kills, ignore them."

## When this skill applies

- "What's failing on CI?" / "Why did the checks fail on my PR?"
- "Is the build green for commit X?"
- "Investigate the CI failures for the current commit."
- The user pastes a commit SHA and asks what's wrong.
- The user has just pushed and wants a quick read on what broke.

It assumes the user is working in (or near) a checkout of the dagger/dagger repo and that they have `dagger` on their PATH and are authenticated to Dagger Cloud.

## Inputs

Figure out **which commit** to investigate:

- If the user gave an explicit SHA, use that.
- Otherwise default to `git rev-parse HEAD` in the current repo. Confirm with the user only if HEAD looks suspicious (e.g. an unpushed local commit on `main`).

The **module ref** is almost always the canonical upstream path `github.com/dagger/dagger`. **Do not** read it from `git remote get-url origin` — that may be a fork (e.g. `git@github.com:<you>/dagger.git`), and Dagger Cloud records checks against the upstream repository, not the fork. Pass the upstream form unless the user explicitly says otherwise.

## Workflow

### 1. Fetch the check list

```sh
dagger module-checks --json github.com/dagger/dagger@<sha>
```

Notes:
- `dagger module-checks` is hidden / experimental — that's fine. Just call it.
- The JSON output is the stable contract; don't try to parse the human-rendered form.
- If you see an error like `no checks found in github.com/dagger/dagger for commit <short-sha>`, the cloud hasn't recorded this commit yet (e.g. the user just pushed seconds ago). Wait a bit and retry, or suggest the user runs with `--watch` to poll.

### 2. Parse the JSON

Top-level fields you care about:

- `summary.{total,failure,cancelled,inProgress,success}` — headline counts.
- `checksUrl` — the Dagger Cloud UI link for this commit's full check view. **Always include this in your final summary** so the user can click through.
- `commit.{sha,message,authorName}` and `refs[].{type,number,title}` for context (which PR is this, what's the commit about).
- `checks[]` — one entry per (deduped, most-recent) check. Fields:
  - `name`, `status` (raw uppercase enum: `SUCCESS`, `FAILURE`, `CANCELLED`, `RUNNING`, `QUEUED`, `SKIPPED`), `statusBucket` (normalized camelCase: `failure` / `cancelled` / `inProgress` / `success`).
  - `durationSeconds`, `startedAt`, `endTime`.
  - `traceId`, `spanId`, `traceUrl`.
  - Optional `internal: true` — only present when the only available checks are internal ones (rare).

Filter to `statusBucket == "failure"` to get the list to investigate. If there's a single `cancelled` and zero `failure`s, also investigate the cancelled one — see "Timeouts" below.

### 3. Fetch each failing trace

For each failing trace ID:

```sh
dagger trace <traceId>
```

**Important: do not pass `--progress=logs`.** It currently produces no output locally — likely a bug in the streaming-logs path. The default progress mode prints the trace tree with bubbled-up error lines (`! ...`), which is what you want.

Capture the tail (`| tail -40` is usually enough) and look for the patterns described below.

### 4. Categorize each failure

The output of `dagger trace` is a fire-hose of errors. Many of those errors come from *intentional negative-test cases* — the test suite exercises bad inputs and asserts that they fail with specific messages. Those error lines bubble up into the trace summary and look identical to real failures unless you know what to look for.

Categorize each failing check into one of:

**(a) Cascading kill.** The check ended in `context canceled` / `context deadline exceeded`, *or* its duration is way shorter than typical (e.g. `go test ./...` ending at 33s when it normally takes 5+ minutes). Almost certainly killed by an upstream timeout or by the orchestrator after a sibling failure. Not a real test failure — investigate the upstream cancellation, not this one.

**(b) Timeout.** Status `CANCELLED` and duration around 30 minutes (1800s ± a bit). That's the CI step timeout. Worth investigating because the test legitimately ran too long — but the cause is "took too long", not "assertion failed". The trace usually doesn't show a specific failure.

**(c) Infra hiccup.** Trace contains `exit code: 137` on bound services (SIGKILL — usually OOM), `dial unix /run/dagger/engine.sock: ... no such file or directory` across many sibling spans, or `connection error: ...` repeated. The test infra crashed; the code under test may or may not have a real issue. Worth a retry before deeper investigation.

**(d) Real test failure.** Look for:
- `package had failures` (Go test summary — at least one subtest in the package failed).
- A specific assertion error (`expected ..., got ...`, etc.).
- `dagger checks '<glob>'` with non-zero exit code reporting `Nx exit code: 1` summaries from `ci:bootstrap` (real lint regressions or test failures across the suite).
- `failed to <do-something>` errors that aren't obviously test-fixture noise — like `failed to sync go module type defs generation container: exit code: 1` or `failed to run codegen: ...`.

**(e) Negative-test noise.** Patterns that *look* alarming but are part of the test design:
- `unknown flag: --matrix`, `unknown command "fn-a" for "dagger call"` — testing the CLI's error reporting.
- `path "../foo" escapes workdir`, `workdir path "/rootfile.txt" escapes workdir` — testing safety guards.
- `dockerfile parse error on line 1: unknown instruction: FRO (did you mean FROM?)` — testing Dockerfile parsing errors.
- `failed to determine Git URL protocol`, `repository does not contain ref "refs/heads/main"` — testing missing-ref handling.
- `module requires dagger v0.9.0, but support for that version has been removed` — testing version-gating.
- Long lists of `stat <path>/.gitignore: no such file or directory` — testing file-stat error reporting in module loading.

A useful heuristic: if a failed check's trace contains **dozens** of distinct error messages, most of them are likely (e). The actual failing assertion is usually mixed in among them. Look for a single distinct error (categories a–d) rather than getting lost in the list.

### 5. Write the summary

Group your findings by category, not by check. Within each group, list the checks with their trace IDs and durations. End with a one-sentence "bottom line" the user can act on.

Use this exact template:

```markdown
## CI summary — `github.com/dagger/dagger@<short-sha>` (<PR ref if any>)

**<total> checks: <success> pass / <failure> fail / <cancelled> cancelled / <inProgress> running**

Cloud UI: <checksUrl>

### Cascading kills (ignore, root cause is elsewhere)
- `<check-name>` — <durationSeconds>s — trace `<traceId>`
  Likely killed by <upstream check or timeout>.

### Timeouts
- `<check-name>` — ~<durationSeconds>s (≈ CI step timeout) — trace `<traceId>`

### Infra issues (consider retrying)
- `<check-name>` — <durationSeconds>s — trace `<traceId>`
  Signal: <exit 137 on service X / engine.sock disconnects / etc.>

### Real failures (worth investigating)
- `<check-name>` — <durationSeconds>s — trace `<traceId>`
  Failing signal: `<one-line excerpt that suggests a real regression>`

### Bottom line
<One sentence>. Examples:
"Nothing to investigate — everything failing is cascade or infra. Retry the run."
"Two real failures worth looking at: `<a>` and `<b>`. The other 4 are cascades."
"Investigate `<a>` first — the others all cancelled after it timed out."
```

Omit any section that's empty. If every failure is in the "real" bucket, just one section + bottom line is fine. The point of the categorization is to *save the user time*, not to fill in every header.

## Failure modes of the skill itself

- **Auth.** If `dagger module-checks` fails with `not authenticated`, tell the user to run `dagger login` (or set `DAGGER_CLOUD_TOKEN`) and stop — don't try to invent traces.
- **Wrong ref.** If the cloud says "no checks found in github.com/dagger/dagger for commit X", the user may have pushed to a personal fork that doesn't have its checks recorded against the upstream — verify by checking `git remote -v` and `git log <sha>` to confirm the commit is on a branch with a PR open against upstream. If not, the cloud genuinely has no data for this SHA.
- **`dagger trace` returns nothing.** If you pass `--progress=logs`, you'll get an empty output and exit 1 — that's the bug mentioned above. Re-run without `--progress=logs`.
- **Too much output.** Some traces are tens of thousands of lines. Pipe through `| tail -50` or `| grep -E "ERROR|! "` to keep the working set small. Don't dump the full trace into your response.

## Anti-patterns

- Don't paste raw `dagger trace` output into the summary. The user wants the *triage*, not the raw firehose.
- Don't claim a check is "real" just because the trace looks scary — re-read the negative-test patterns above. The dagger test suite intentionally produces a lot of error output.
- Don't run `--progress=logs` "to be thorough" — it's a known-empty code path right now.
- Don't auto-retry, push fixes, or open PRs without being asked. This skill is for *investigation*; acting on the findings is a separate decision.

## Example invocation, end to end

User: "Check what's failing on my current PR."

1. `git rev-parse HEAD` → `57847777f9ae3425c6c25df91cc58e09c0ff69c5`
2. `dagger module-checks --json github.com/dagger/dagger@57847777f9ae3425c6c25df91cc58e09c0ff69c5`
3. Parse JSON: `summary = {total:60, failure:6, cancelled:1, success:53, inProgress:0}`. 6 failing trace IDs + 1 cancelled.
4. For each of the 7, `dagger trace <id> 2>&1 | tail -40` — categorize.
5. Write the summary in the template above. Example bottom line: *"Investigate `test-split:test-call-and-shell` and `test-split:test-cli-engine` — both show real `failed to sync ... exit code 1` errors. The other 4 failures plus the cancellation look like a cascade from `test-split:test-base` hitting the 30-min timeout."*
