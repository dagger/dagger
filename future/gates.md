# Gates — Dagger Cloud CLI design

## In one sentence

Dagger Cloud publishes structured verdicts about workspaces; consumers subscribe.

## Principles

- **Engine smart, CLI dumb.** Backend owns evaluation, caching, and the verdict stream. CLI manages declarations.
- **Producers produce, subscribers subscribe.** PR-status sync, Discord notifications, Slack alerts — owned on the consumer platform, configured there. Not in Dagger's CLI.
- **Local execution stays first-class.** Pipeline logic lives in the workspace and runs identically locally and in Cloud. Cloud is one of many places that can evaluate.
- **The 80% case stays terse.** Onboarding to "my repo's checks gate my PRs" is two commands or fewer.

## Nouns

- **workspace** — the unit of state Dagger checks operate on (a repo, a derived/synthetic state).
- **check** — a deterministic function from workspace → evidence. Already exists; runs locally via `dagger check`. Unchanged by this spec.
- **integration** — an org-level capability provider for *reading* workspaces (e.g., the GitHub App that gives Cloud repo access).
- **gate** — a named policy: "watch checks on workspace X, produce a verdict." Optionally scheduled. One gate targets exactly one workspace.
- **org** — billing/cost owner.

No "pipeline", "watch" (as object), "destination", "report", "routine".

## CLI surface

```text
# Workspace-side declaration (in-repo, anyone with commit access)
dagger config cloud.checks.enabled true
  # Writes .dagger/config.toml. Signals "this workspace wants checks evaluated in Cloud."

# Local execution (unchanged)
dagger check
dagger check go:lint

# Org admin — capability provider
dagger integration setup github
dagger integration accounts github

# Gates — the managed object
dagger gate add [NAME]                            # current repo, default policy
dagger gate add NAME --workspace <ref>            # external repo
dagger gate setup                                 # interactive discovery
dagger gate list
dagger gate show NAME                             # current verdict + contributing evidence
dagger gate history NAME                          # verdict over time
dagger gate config NAME [KEY [VALUE]]
dagger gate rm NAME
dagger gate audience NAME                         # who's subscribed (read-only)
dagger gate watch [NAME] [--on red-edge] [--since 24h] [--from <id>] [--checkpoint <file>]
```

## Sample outputs

### `dagger gate list`

```text
NAME       WORKSPACE                STATUS  POLICY      SCHEDULE
default    github.com/acme/api      ✓ pass  all-pass    —
nightly    github.com/acme/api      ⚠ red   all-pass    0 4 * * *
upstream   github.com/golang/go     ✓ pass  all-pass    0 6 * * *
```

### `dagger gate show default`

```text
gate:       default
workspace:  github.com/acme/api
policy:     all-pass
schedule:   on push
status:     ✓ pass
evaluated:  2m ago (commit a1b2c3d)

checks:
  go:lint      PASS
  go:test      PASS
  docker:scan  PASS

evidence:   dagger.cloud/ev/x1b2c3
```

### `dagger gate audience default`

Read-only reflection of who's subscribed. Subscriptions themselves live on the consumer side.

```text
SUBSCRIBER          PLATFORM   FILTER       LAST SEEN
dagger-github-app   github     all          2m ago
my-server-bot       discord    red-edge     22h ago
acme-pagerduty      webhook    fail         (never)
```

### `dagger gate watch` — the glue primitive

```bash
$ dagger gate watch default --on red-edge
{"id":"evt_x1","gate":"default","workspace":"github.com/acme/api","verdict":"fail","at":"2026-05-27T11:23:00Z","evidence":"dagger.cloud/ev/abc","schema":1}

# A working bot in 3 lines:
$ dagger gate watch --on red-edge | while read e; do
    curl -X POST discord.example/webhook -d "$e"
  done
```

## Data model

```yaml
gate:
  name:           string         # addressable
  workspace:      workspace_ref  # exactly one
  check_pattern:  []glob         # which checks count for verdict
  policy:         enum           # all-pass | any-pass | quorum | ...
  schedule:       cron?          # event-driven if null
  org:            org_ref        # billing owner

integration:                     # SCM-only; capability provider for repo READ
  name, account, type, org
  auto_gate:      bool           # default false; auto-create gates for new visible repos
```

```toml
# .dagger/config.toml (in-repo, declarative)
[cloud]
checks.enabled = true
```

## Verdict event schema

The wire format for `dagger gate watch` and any push-style delivery added later.

```json
{
  "schema":    1,
  "id":        "evt_x1b2c3",
  "gate":      "default",
  "workspace": "github.com/acme/api",
  "verdict":   "pass",
  "at":        "2026-05-27T11:23:00Z",
  "evidence":  "dagger.cloud/ev/abc",
  "checks": [
    {"name": "go:lint",     "status": "PASS"},
    {"name": "go:test",     "status": "PASS"},
    {"name": "docker:scan", "status": "PASS"}
  ]
}
```

`schema` is pinned per event. Consumers can also pin via `dagger gate watch --schema-version 1`. Bumping the major version preserves old consumers.

## Filters

Used by `dagger gate watch --on` and by consumer-side subscription configuration.

| Filter        | Fires on                                  |
|---------------|-------------------------------------------|
| `any`         | every evaluation                          |
| `pass`        | every evaluation matching `pass`          |
| `fail`        | every evaluation matching `fail`          |
| `transition`  | only when verdict changes                 |
| `red-edge`    | green → red only (paging-friendly)        |
| `green-edge`  | red → green only (recovery notifications) |

Default for non-SCM consumers should be `red-edge` — otherwise long outages spam the channel.

## End-to-end scenarios

### A. Solo developer enables PR checks on their repo

```bash
# 1. Org admin (one-time)
$ dagger integration setup github
> Open this URL to set up the GitHub integration:
> https://dagger.cloud/...

# 2. Developer in repo
$ dagger config cloud.checks.enabled true
$ dagger gate add
> Created gate "api" for github.com/acme/api (default policy: all-pass)

# 3. Install the Dagger GitHub App from GitHub Marketplace.
#    Configure inside GitHub to subscribe to gates for this Dagger org.

# 4. Push
$ git push

# PR shows Dagger gate verdicts as check statuses.
```

### B. Discord alert bot

```bash
# 1. Install the Dagger Discord bot in your server (Discord-side install).
#    Bot authenticates to Dagger via OAuth.
#    Inside Discord:  /dagger subscribe default --channel #alerts --on red-edge

# 2. Verify from Dagger side
$ dagger gate audience default
SUBSCRIBER       PLATFORM   FILTER    LAST SEEN
my-server-bot    discord    red-edge  just now
```

### C. Track upstream weekly + alert on regression

```bash
$ dagger gate add upstream-go --workspace github.com/golang/go --schedule "0 6 * * 0"
> Created gate "upstream-go" for github.com/golang/go (weekly, default policy)

# Subscribe from your Slack app:
#    /dagger subscribe upstream-go --channel #deps --on red-edge

$ dagger gate watch upstream-go --on red-edge --since 30d
{"schema":1,"id":"evt_y1","gate":"upstream-go","verdict":"fail","at":"2026-04-30T06:00:00Z",...}
```

## Explicit non-goals

- **Multi-workspace gates / workspace patterns.** One gate, one workspace. Use `integration.auto_gate` for the bulk case.
- **Built-in Slack/Discord/PagerDuty destinations in the Dagger CLI.** Consumers configure themselves on their native platforms.
- **A generic "destinations" object.** Defer until a real use case emerges that `gate watch` + native apps can't cover.
- **Pipeline / job / workflow / routine / run as a noun.** Execution is implicit; the gate is the only managed thing the user creates.
- **Extending `dagger check` with `--on` / `--schedule` / `list`.** `dagger check` stays sync, imperative, ephemeral, local. The declarative/async/persistent surface lives under `dagger gate`.

## Open design points

1. **GitHub App: one or two?** Single App handling both read-repo and post-status (with two config sections), or split into two Apps. Lean: one.
2. **`gate add` default name.** Workspace short-name (e.g., `api` for `github.com/acme/api`) so `dagger gate config api ...` reads naturally.
3. **Gate auto-creation trigger.** When `integration.auto_gate = true` and the integration first sees a repo whose `.dagger/config.toml` has `cloud.checks.enabled = true`, spawn a gate. No file → no gate.
4. **Default policy.** Probably `all-pass`. Most permissive baseline; users tighten.
5. **Webhook delivery** as a complement to `gate watch`. Ship later; same event stream, push-style. `dagger gate webhook add <url>`.
6. **Service tokens for unattended `gate watch`.** Inherits `DAGGER_CLOUD_TOKEN` env var. Long-lived bot deployments use this.

## Future shape: evidence as first-class

The gate concept generalizes one level up. A gate is a *policy over evidence*. Checks are one kind of evidence today. As Dagger Cloud grows other evidence shapes (scans, benchmarks, attestations, model evaluations), the gate model holds — it consumes any evidence stream and produces a verdict. The `check_pattern` field becomes `evidence_pattern` once the verdict source isn't necessarily a check.

This is not a v1 change. v1 ships `gate` with `check_pattern` and the model expands later without breaking the noun.
