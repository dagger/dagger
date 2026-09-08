# Proposal: Ergonomic CLI-first Cloud onboarding

Goal: an anonymous user can go from "just installed dagger" to a working Dagger
Cloud account with checks + telemetry **without leaving the terminal**, except
for the two consent screens that genuinely require a browser (identity provider
login and, only for paid plans, the payment page).

## Target cold-start flow

```
$ dagger setup            # or first `dagger` invocation
  1. Cloud login/signup   → device auth (browser consent, already works)
  2. Ensure an org        → createQuickstartOrg (NO browser) instead of dagger.cloud/traces/setup
  3. Connect Git source   → GitHub OAuth via loopback redirect (browser consent only)
  4. Enable checks        → configureSource + startFeatureTrial(CLOUD_CHECKS)
  5. Telemetry            → automatic once org is selected (already works)
```

Everything except steps 1 and 3's provider consent screens is pure CLI.

## Key finding: the backend already supports this

The Cloud GraphQL API (`dagger.io: cloud/api/schema.graphql`) already exposes
CLI-friendly mutations the current CLI does **not** use:

| Mutation | Use in onboarding | CLI status |
|---|---|---|
| `createQuickstartOrg(name)` | Create a free org, no browser | **not wired** |
| `createQuickstartOrgWithSourceSelections(name, sources)` | Org + auto-checked repos in one call | not wired |
| `createQuickstartOrgWithMappings(name, installationIds)` | Org + mapped installations | not wired |
| `githubOAuthURL(redirectURI)` | OAuth URL; `localhost` is an allowed redirect host | wired (browser-only) |
| `connectGitHub(code, state)` | Finish GitHub OAuth from a code | **not wired** |
| `configureSource(source)` | Toggle autocheck on a repo set | wired (`cloud check on`) |
| `startFeatureTrial(org, CLOUD_CHECKS, days)` | Turn on the Checks feature | **not wired** |
| `createOrg(items, paymentInfo, coupon)` / `createPortalSession(org)` | Paid plans / Stripe portal | portal wired |

`AllowedRedirectHosts` defaults to `dagger.cloud,localhost`, so the CLI can run
a **loopback OAuth flow** (spin up `http://localhost:PORT/callback`, capture
`code`+`state`, call `connectGitHub`) — the same pattern `dagger login` already
uses for device auth. The GitHub authorize/app-install consent screen is the
only unavoidable browser hop for source connection.

## Current gaps (what to build)

1. **Org creation opens a browser.** `internal/cmd/dagger/cloud.go:createNewOrg`
   opens `https://dagger.cloud/traces/setup` and polls `client.User` for up to
   5 minutes. Replace with a `client.CreateQuickstartOrg(name)` call. Derive the
   default org name from the account (GitHub login / email local-part) so the
   user isn't asked to invent one (Solomon: "org names really do not matter").

2. **No CLI GitHub connect.** `integration_cloud.go:integrationSetupGitHub` only
   prints the OAuth URL. Add a loopback flow + `connectGitHub(code,state)` so
   the CLI completes the connection and can immediately `configureSource`.

3. **`dagger cloud check on` dead-ends.** When no source mapping exists it errors
   `no Cloud source mapping found for <repo>` (`workspace.go:1309/1317`). It
   should offer to run the GitHub connect flow, then enable the check — and call
   `startFeatureTrial(CLOUD_CHECKS)` if the feature is off.

4. **`setup` login step is isolated.** `setup.go:setupStepLogin` stops at login.
   Extend the setup walker with steps 2–4 above so a single `dagger setup`
   reaches a working checks + telemetry state.

5. **Known bugs from the thread** (track, don't regress):
   - "Already logged in" false positive — fixed in PR #13948 (verify merged).
   - Empty org switcher on first signup / possible race in auto-created org.
   - `dagger cloud rerun` naming vs `dagger cloud check rerun` (bikeshed, defer).

## Proposed implementation slices

### Slice A — CLI-only org creation (highest value, lowest risk) — ✅ IMPLEMENTED
- Added `internal/cloud`: `CreateQuickstartOrg(ctx, name) (*OrgResponse, error)`
  (GraphQL op mirroring `createQuickstartOrg`).
- Rewrote `createNewOrg` (`internal/cmd/dagger/cloud.go`): removed the
  `dagger.cloud/traces/setup` browser open + 5-minute poll loop; it now derives
  a default org name from the account (`nickname` → email local-part → `my-org`)
  and calls `CreateQuickstartOrg`. A `dagger login <org>` / `--org` value still
  overrides the derived name.
- Extended `UserResponse` with `nickname`/`email` for name derivation.
- Tests: `sanitizeOrgName` / `defaultOrgName` unit tests
  (`internal/cmd/dagger/cloud_org_name_test.go`); the browser poll path is gone.

### Slice B — GitHub connect via loopback
- Add `client.ConnectGitHub(ctx, code, state)`.
- Add a small loopback OAuth helper (reuse `dagger login`'s browser-open + local
  listener patterns): request `githubOAuthURL(http://localhost:PORT/callback)`,
  open it, capture `code`/`state`, exchange via `ConnectGitHub`.
- Wire into `dagger cloud integration create github` (default) with the existing
  `--open`/manual-URL path as fallback for headless environments.

### Slice C — one-shot checks enablement
- Add `client.StartFeatureTrial(ctx, orgID, feature, days)`.
- `dagger cloud check on`: if `no source mapping`, run Slice B connect, then
  `configureSource(..., SELECTED, [repo])`; ensure `CLOUD_CHECKS` via
  `startFeatureTrial` when absent.

### Slice D — unified `dagger setup` walkthrough
- Extend the setup step machine: after login, ensure org (A), offer source
  connect (B), offer checks (C). Each step is skippable and idempotent, matching
  the existing huh-form TUI style. Non-interactive/CI: skip prompts, respect
  `DAGGER_CLOUD_TOKEN`/OIDC.

### Slice E — paid plans (later)
- `createOrg(items, paymentInfo)` + `createPortalSession` for the single
  necessary payment browser roundtrip; `dagger billing plans` already lists
  plans. Out of scope for the initial anonymous free-tier onboarding.

## Open questions for the team
- Default org-name policy: account-derived silently, or confirm once? Backend
  auto-creates an org on signup already — do we adopt that org instead of
  creating a second one? (Ties to the "empty org switcher" bug.)
- Headless/CI story for GitHub connect (loopback needs a browser+localhost).
- Feature-trial length + what happens at expiry for `CLOUD_CHECKS`.
