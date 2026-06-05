# Dagger Cloud Check Report Endpoint

## Status

Proposed.

This document is intended to be handoff-ready for a Cloud backend implementer.
It describes the endpoint shape, the report-building algorithm, the CLI
integration point, rollout behavior, and the acceptance tests needed to replace
local trace replay for past Cloud Checks.

## Summary

Dagger Cloud already stores enough data to explain a past check result: check
metadata, a trace ID, the check root span ID, spans, span status, span
attributes, and logs. Today the CLI still has to replay that trace locally to
produce a useful failure report.

Add a Cloud-side check report API that returns a small, structured report for a
single Cloud Check. The report should include:

- check identity and status
- the error/root-cause summary for failed checks
- relevant failed span details
- bounded log excerpts for the failure roots
- test summary and failed/skipped test details when test spans are available
- trace links for deeper inspection

The first user-facing consumer is `dagger check --past`: it should fetch the
report directly instead of streaming the full trace into the local TUI.

## Current Behavior

The reference implementation lives on branch `1.0-beta`.

Important files:

- `cmd/dagger/checks.go`
- `cmd/dagger/cloud_checks.go`
- `internal/cloud/checks.go`
- `internal/cloud/trace.go`

Current `dagger check --past` flow:

1. `maybeReplayPastChecks` decides whether past Cloud Checks should be used.
2. `checkPastWorkspaceAddress` infers a remote workspace address, or rejects
   dirty/no-remote local workspaces.
3. `loadCloudCheckRowsForWorkspaceAcrossUserOrgs` queries Cloud checks across
   the user's orgs.
4. Cloud is queried through `moduleChecks`/`checks`, returning `Check` fields:
   `id`, `name`, `status`, `startedAt`, `endTime`, `duration`, `traceId`,
   `spanId`, `moduleRef`, `moduleVersion`, and `internal`.
5. `replayCloudCheckResult` picks the matching check commit and rows.
6. `replayCloudChecks` streams spans through `StreamSpans`.
7. For three or fewer checks, `replayCloudChecks` also streams descendant logs
   through `StreamLogs`.
8. The CLI rewrites all streamed spans into a synthetic local trace and feeds
   it to the local `idtui` frontend.

This gives a good report, but the CLI has to download spans and logs, rebuild a
trace, and rely on terminal rendering logic just to answer "why did this check
fail?"

## Problem

Consumers need a cheap way to fetch the useful part of a Cloud Check result:
the failure report. Replaying a trace is too much work for:

- Discord and GitHub bots that want to explain a failed check inline.
- Web UI surfaces that need a compact failure card without loading an entire
  trace view.
- CLI commands such as `dagger check --past` that only need the final report.
- Automation that wants machine-readable failure and test summaries.

The current API exposes the trace coordinates but not the summarized report.

## Goals

- Return a single-check report without requiring clients to stream/replay the
  trace.
- Preserve the useful information that `dagger check --past` currently derives
  from local TUI replay.
- Include test report data when OpenTelemetry test semantic attributes are
  present.
- Keep the response bounded and safe to render in chat, PR comments, terminal
  output, and API clients.
- Provide enough stable structure for machines, while also including
  human-ready summary strings.
- Reuse existing Cloud trace/span/log storage. Do not require Engine changes for
  the first version.

## Non-Goals

- Do not recreate the full trace UI in the API response.
- Do not return all spans or all logs.
- Do not add a new check execution model.
- Do not depend on local CLI rendering code inside Cloud.
- Do not solve cross-check aggregation in the first endpoint. A group endpoint
  can be added after the single-check endpoint is stable.

## Proposed API

Expose the report from Cloud GraphQL on `Check`.

```graphql
type Check {
  id: ID!
  name: String!
  status: CheckStatus!
  traceId: ID
  spanId: ID

  report(input: CheckReportInput): CheckReport!
}

input CheckReportInput {
  logLines: Int = 120
  maxFailedSpans: Int = 20
  maxTests: Int = 50
  includeLogs: Boolean = true
  includePassedTests: Boolean = false
}
```

Recommended query:

```graphql
query CheckReport($org: String!, $checkID: ID!) {
  org(name: $org) {
    check(id: $checkID) {
      id
      name
      status
      traceId
      spanId
      report {
        summary
        status
        traceUrl
        failure {
          message
          roots {
            spanId
            name
            message
            startedAt
            endedAt
            logs {
              lines
              truncated
            }
          }
        }
        tests {
          total
          passed
          failed
          skipped
          running
          failures {
            name
            suite
            message
            spanId
            logs {
              lines
              truncated
            }
          }
          skippedTests {
            name
            suite
            message
            spanId
          }
        }
      }
    }
  }
}
```

If the Cloud schema already prefers `org.check(...)` alternatives such as
`node(id:)`, keep that convention. The important contract is that callers with a
Cloud Check ID can request the report directly from the check object.

## Response Schema

```graphql
type CheckReport {
  checkId: ID!
  checkName: String!
  status: CheckStatus!
  summary: String!
  traceId: ID
  spanId: ID
  traceUrl: String
  generatedAt: Time!
  partial: Boolean!
  notices: [String!]!
  failure: CheckFailureReport
  tests: CheckTestReport
}

type CheckFailureReport {
  message: String
  roots: [CheckFailureRoot!]!
}

type CheckFailureRoot {
  spanId: ID!
  traceId: ID!
  name: String!
  message: String
  statusCode: String
  startedAt: Time
  endedAt: Time
  path: [String!]!
  attributes: JSON
  logs: CheckLogExcerpt
  traceUrl: String
}

type CheckLogExcerpt {
  lines: [String!]!
  truncated: Boolean!
  totalLineCount: Int
}

type CheckTestReport {
  total: Int!
  passed: Int!
  failed: Int!
  skipped: Int!
  running: Int!
  failures: [CheckTestCase!]!
  skippedTests: [CheckTestCase!]!
}

type CheckTestCase {
  name: String!
  suite: String
  status: String!
  spanId: ID!
  traceId: ID!
  message: String
  startedAt: Time
  endedAt: Time
  logs: CheckLogExcerpt
  traceUrl: String
}
```

Response rules:

- `summary` must be useful by itself. Example:
  `go:generate-dagger-runtimes failed: go test ./... exited with code 1`
- `partial` is true when spans or logs could not be fully loaded within backend
  limits.
- `notices` explains partial output, missing traces, missing logs, permission
  filtering, or unsupported legacy data.
- `failure` is null for successful checks unless Cloud has a useful warning to
  report.
- `tests` is null when no test semantic spans exist.
- Logs are excerpts, not full logs. The default response must be small enough
  for bots and PR comments.

## Report-Building Algorithm

Input:

- org ID or org name
- check ID
- optional report input limits

Required stored fields:

- check `id`, `name`, `status`, `traceId`, `spanId`, `moduleRef`,
  `moduleVersion`, `startedAt`, `endTime`
- spans for `traceId`
- logs by `traceId` and span ID, with descendant lookup if available

Algorithm:

1. Load the check and authorize access exactly as the current check metadata
   query does.
2. If `traceId` or `spanId` is missing, return a metadata-only report with
   `partial: true` and a notice.
3. Load the check root span by `traceId` + `spanId`.
4. Load descendant spans under the check span. The implementation may cap this
   by count and duration, but must set `partial: true` when capped.
5. Normalize spans using the same concepts as `dagql/dagui`:
   - failed span: OpenTelemetry status code is error
   - caused failure: failed descendant or linked error origin
   - test status: normalize `test.case.result.status` and
     `test.suite.run.status`
6. Compute failure roots:
   - Prefer explicit error origins parsed from span status descriptions when
     present.
   - Otherwise use failed descendant spans with no failed descendant of their
     own.
   - Otherwise use the check root span if the check is failed but no descendant
     explains it.
7. Sort failure roots by:
   - error-origin order when available
   - deepest/most specific failed span before broad parent spans
   - earliest start time as a stable tiebreaker
8. For each selected failure root, fetch bounded logs for that span and
   descendants when `includeLogs` is true.
9. Build the test report from spans with OpenTelemetry test attributes:
   - case status from `test.case.result.status`
   - suite status from `test.suite.run.status`
   - case name from `test.case.name`
   - suite name from `test.suite.name`
10. Add failing and skipped test cases to the response, with bounded logs for
    failures.
11. Generate a concise `summary` from the first failure root or failing test.

## Test Report Semantics

Dagger's TUI already understands OpenTelemetry test semantic attributes in
`dagql/dagui/tests.go`. The Cloud report should follow the same normalization:

- `pass` and `success` count as passed
- `fail` and `failure` count as failed
- `skipped` counts as skipped
- `aborted` and `timed_out` count as failed
- `in_progress` counts as running

When both suite and case statuses exist, case-level spans should drive case
counts. Suite spans should provide grouping and suite-level failure context, but
must not double-count cases.

If test case spans are not parented under suite spans, group them by
`test.suite.name`, matching the existing TUI behavior.

## Error Report Semantics

Use the same failure concepts as `dagql/dagui` where practical:

- A span with OpenTelemetry error status is failed.
- A span can cause parent failure through failed descendants.
- Error-origin traceparents embedded in status descriptions should point to
  root-cause spans when available.
- A broad parent span should not hide more specific failed descendants.

The endpoint should not expose internal-only spans to callers who cannot see
them in the Cloud UI. If permission filtering removes the true root cause, set
`partial: true` and include a notice such as `Some internal failure details were
hidden.`

## REST Alternative

GraphQL is the preferred first implementation because existing Cloud checks are
already GraphQL-backed. If REST is needed for external integrations, add it as a
thin wrapper around the same report builder:

```text
GET /v1/checks/{checkID}/report?log_lines=120&max_failed_spans=20&max_tests=50
```

The REST response should match the GraphQL `CheckReport` JSON shape.

## CLI Integration

Once Cloud exposes the report field, update `1.0-beta`'s CLI path:

1. Keep the existing check lookup and selection logic.
2. Replace `replayCloudChecks` with report fetching for selected checks.
3. Render reports directly for `dagger check --past`.
4. Fall back to trace replay only when the report field is unavailable or the
   user requests full trace detail.

Proposed CLI behavior:

- `dagger check --past`: fetch and render reports. Exit 1 if any selected check
  is not green.
- `dagger check --past --run`: render past reports, then run current checks,
  preserving current semantics.
- `dagger check --past --full-trace` or similar future flag: explicitly use the
  old replay behavior for debugging deep trace issues.

The initial CLI renderer can be plain text. It does not need interactive TUI
state.

Example terminal output:

```text
Replaying Cloud Checks result from 12m ago

go:generate-dagger-runtimes failed
  go test ./... exited with code 1

Failure root:
  TestFoo/bar
  core/integration/module_test.go:123: expected ...

Tests: 281 total, 1 failed, 0 skipped
  FAIL TestFoo/bar
    core/integration/module_test.go:123: expected ...

Trace: https://dagger.cloud/dagger/traces/135fb9685692002b248535fb76dac8a0
```

## Backend Implementation Plan

1. Add a Cloud service function:
   `BuildCheckReport(ctx, orgID, checkID, opts) (*CheckReport, error)`.
2. Add data access helpers for:
   - loading check metadata by ID
   - loading the check root span
   - loading bounded descendant spans
   - loading bounded logs for selected spans
3. Implement span normalization and failure-root selection in a package that can
   be unit tested without GraphQL.
4. Implement test report aggregation from span attributes.
5. Add the GraphQL `Check.report(input:)` resolver.
6. Add optional REST wrapper only if required by external consumers.
7. Update CLI client types and query.
8. Switch `dagger check --past` to prefer the report endpoint.

Keep the report builder independent from presentation. It should return data,
not terminal-formatted output.

## Limits and Defaults

Suggested defaults:

- `maxFailedSpans`: 20
- `maxTests`: 50 failed/skipped tests
- `logLines`: 120 total lines per root/test, or 120 combined lines per report if
  response size becomes a concern
- `includeLogs`: true
- maximum response body target: 256 KiB

If limits are exceeded:

- truncate logs from the middle or tail, whichever preserves the error best
- set `truncated: true` on the affected log excerpt
- set `partial: true` on the report if omitted spans/tests could change the
  conclusion
- add a human-readable notice

## Caching

Check reports are immutable after a check reaches a terminal status, except for
late-arriving logs/spans. Recommended behavior:

- Cache terminal reports for a short initial TTL, for example 60 seconds.
- Promote to a longer TTL after the trace has had time to settle.
- Do not cache running reports for long. A 5 to 10 second TTL is enough.
- Include `generatedAt` so clients can decide whether to refresh.

If Cloud already has trace ingestion completion signals, use them to decide when
the report is stable.

## Security and Privacy

The report must use the same authorization rules as the trace/check UI.

Specific requirements:

- Do not expose logs or spans the caller cannot view in Cloud.
- Preserve existing redaction behavior for secrets.
- Do not include raw environment variables or unbounded attributes by default.
- Consider allowlisting attributes in `CheckFailureRoot.attributes`.
- If data is hidden due to permissions, mark the report partial.

## Compatibility and Rollout

Rollout order:

1. Ship backend report resolver behind a feature flag.
2. Add Cloud-side tests against existing stored trace fixtures.
3. Add CLI support that probes the field and falls back to replay on GraphQL
   validation failure.
4. Enable the feature for internal Dagger orgs.
5. Compare report output with `dagger check --past` replay output on real failed
   checks.
6. Enable generally.
7. Remove or demote the replay path only after the report endpoint has covered
   the common debugging cases.

Backward compatibility:

- Existing `moduleChecks` and `checks` fields remain unchanged.
- Existing `dagger check --past` behavior remains available through fallback.
- Older Cloud instances simply do not expose `report`; the CLI should detect
  that and replay as it does today.

## Tests

Backend unit tests:

- successful check returns no failure and no test failures
- failed root span with status message becomes failure root
- failed child span is preferred over broad failed parent
- error-origin traceparent points to root-cause span
- missing trace ID returns partial metadata-only report
- missing logs returns report with notice, not an error
- logs are truncated and marked as truncated
- internal/private spans are hidden for unauthorized callers
- test statuses normalize `pass`, `success`, `fail`, `failure`, `skipped`,
  `aborted`, `timed_out`, and `in_progress`
- orphan test cases group by `test.suite.name`

Backend integration tests:

- GraphQL `Check.report` returns expected JSON for a stored failed check
- report generation works for checks found through `moduleChecks`
- report respects org membership and repo visibility
- running checks return partial/in-progress reports

CLI tests:

- `dagger check --past` uses `Check.report` when available
- CLI falls back to trace replay when `report` is unavailable
- failed reports exit 1
- successful reports exit 0
- report rendering includes failed tests when present
- `--run` still runs current checks after displaying past results

Manual validation:

- Pick a recent failed Dagger CI check with test spans.
- Compare:
  - current `dagger check --past` replay output
  - new `Check.report` output
  - Cloud trace UI
- Verify the endpoint identifies the same root cause and failed tests.

## Acceptance Criteria

- A caller with a Cloud Check ID can fetch a useful failure report in one API
  call.
- The response includes failed test details when test spans exist.
- The response is bounded and safe for bots to render.
- `dagger check --past` can use the endpoint without local trace replay for the
  common case.
- The old replay path remains as fallback during rollout.
- Missing or partial trace data produces a partial report, not a hard failure.

## Open Questions

- Should the first API be `Check.report`, `Check.errorReport`, or
  `Check.failureReport`? `report` is broader and leaves room for test summaries.
- Should there be a group-level endpoint for multiple selected checks after the
  single-check endpoint ships?
- What response-size limit is appropriate for Discord/GitHub consumers?
- Should logs be per failure root or globally budgeted across the report?
- Does Cloud have a reliable trace-ingestion-complete signal for stable caching?
- Which span attributes are safe/useful enough to expose by default?
- Should the REST wrapper ship with the GraphQL resolver or wait for an
  external integration need?

## Handoff Notes

Start with the Cloud backend. The OSS repo has the client-side reference flow,
but not the Cloud server implementation.

Implementation anchors from `1.0-beta`:

- `internal/cloud/checks.go`: current Cloud check metadata query and response
  types.
- `internal/cloud/trace.go`: current span/log streaming operations.
- `cmd/dagger/checks.go`: `--past`, `--no-past`, and `--run` command flow.
- `cmd/dagger/cloud_checks.go`: check row selection and `replayCloudChecks`.
- `dagql/dagui/tests.go`: test status normalization and grouping semantics.
- `dagql/dagui/spans.go`: failed span/error-origin concepts used by the TUI.

The key refactor is conceptual: move the report extraction currently implied by
local replay and TUI rendering into a backend report builder that operates over
stored Cloud spans and logs.
