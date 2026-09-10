# Integration merge state

`vitoland` is a local dogfooding branch. Do not push or ship it. Source changes belong on their source branches; integration resolutions and this record belong here. The five historical configuration commits were explicitly preserved during this rebuild.

## September 10 rebuild

Base: `upstream/main` at `59a9b904d1` (fetched September 10, 2026).

Previous integration tip: `5173087e00`, retained as `backup/vitoland-2026-09-10`. Its `MERGE_STATE.md` records the earlier integration and validation. The rebuilt branch has no remote tracking branch.

| Order | Source | Integrated tip |
| --- | --- | --- |
| 1 | `extract/agent-runtime` | `e922cb5d9f` |
| 2 | `workspace-git` | `67c925f589` |
| 3 | `origin/client-lifecycle-v2` | `be630ff9f2` |
| 4 | `origin/fix/patch-apply-repo-discovery` (#14071) | `2503df8877` |
| 5 | `origin/feat/dump-id-modes` (#14073) | `4210f65e4e` |
| 5 | `origin/fix/mcp-mount-rebind-summary` (#14074) | `8548656568` |

The workspace-git tip includes the local SDL declaration-order fix, one commit ahead of origin. Agent runtime matches its upstream remote. The smaller branches use their fetched heads, including updates since the previous integration.

Already upstream: mount search (#14068), patch preview width (#14069), changeset diff headers (#14070), and legacy toolchain loading in value workspaces. Their old commits were not merged again.

Superseded: workspace reloaded identity (#14072). The updated workspace-git branch replaces `reloaded`/`checkpoint` with `sync` and removes the old host-read epoch mechanism. Merging that fix would restore obsolete implementation and tests for a removed API, so it was omitted.

## Preserved direct commits

Replayed in original order with cherry-pick provenance:

| Original | Replayed | Purpose |
| --- | --- | --- |
| `c6867f7063` | `1de3e6b180` | Install editor |
| `f6c2266746` | `b04c206a9e` | Sync local development modules and install contributor |
| `0d082a75ea` | `53df956f95` | Install and polish go-cli |
| `193c6be7cf` | `33598691ec` | Install staff and committer |
| `dd4ca5a411` | `84dc3b7286` | Bump agents |

All 32 module configurations match the previous integration. Upstream's SDK migration comment and existing tla-check declaration are retained without duplication. SDK lock entries retain upstream's v0.1 tag selection and pins; the final agent pin is the user's `b66ffae4e8434fbd9c5ba91cc32a722be7bac6d1`. Other configuration changes and image/editor pins from the direct commits were replayed.

## Integration resolutions

- Ported the previous per-agent workspace previews, commit integration before export, save/reload baseline refresh, and startup composition handling onto the current agent runtime. Capture uses the new `Workspace.sync` API; obsolete checkpoint flags were not restored. Preserve per-agent locking, busy-turn checks, runtime reseeding, and synchronization baselines.
- Retained queued prompt forms, explicit confirmations, and placement above the shell draft. Ported workspace preview tests to the per-agent owner and asynchronous UI refresh.
- Retained both independent-agent export tests and upstream's new workspace CLI export tests from their add/add conflict.
- Client lifecycle retains the current workspace/value loading paths and invalidation API. Agent creation holds tombstone leases; start/resume hold detached loop leases, including propagation of errors from initially dormant resumes. Git push approval uses held scopes, validated ancestry, and cloned metadata. The obsolete `daggerClient` attachables test is replaced by coverage of the current caller resolver; existing tests retain the synthetic proxy and registered-caller cases.
- Adapted Git bundle creation to upstream's detached-HEAD lookup: keep the checkout unnamed but retain `HEAD` as the bundle transport ref. The CLI save regression caught this incompatibility; a focused resolver regression covers the distinction.
- Retained the nil-safe mount accessor returning a cloned slice for workspace Git and MCP summaries.
- Regenerated API reference stubs for both current and rolling beta docs and ran the CLI reference generator. PHP navigation includes both branches' types; Workspace method source locations follow the merged PHP SDK. Full SDK/doc generation was not rerun.

## Validation

Tests ran through `dagger api call engine-dev test` in `/tmp/vitoland-validation-20260910`, with only lockfile comments stripped for compatibility with the installed beta. The working branch retains its original lockfile comments. The disposable checkout was removed after validation. Validation source files matched the final integration fixes; generated documentation is checked in the main checkout.

| Check | Result | Trace |
| --- | --- | --- |
| `go build ./cmd/dagger ./cmd/engine ./cmd/dump-id` | Passed | Local |
| Core bundle refs, agent lifetime, and wait guards | 4 passed | [core](https://dagger.cloud/dagger/traces/a0d39073ed32f2034a556c0fcde58600) |
| CLI agent and workspace previews/save/reload | 8 passed, known debug-listener test excluded | [CLI](https://dagger.cloud/dagger/traces/6e371efe3807fd261453115f4b0b23ee) |
| Git push authorization, client scopes, and host caller routing | 9 passed | [server](https://dagger.cloud/dagger/traces/037e4143ea3906e717db0adb6cb7d405) |
| Agent lifecycle, spawner-release access, workspace exports, and delegated push | 33 passed (runner entries) | [integration](https://dagger.cloud/dagger/traces/382eb0b46dd06c133ed8546ea53bde64) |
| Queued forms, push confirmations, passphrases, checkpoint selection | 17 passed | [TUI](https://dagger.cloud/dagger/traces/81ae49895a786ee7ab4a3d0be014d458) |

The first CLI run exposed the detached-HEAD bundle incompatibility; the corrected save/reload test passes. `TestAgentDebugServerContextCancellation` reproduced the listener-close timing failure already recorded in the previous integration, including on rerun ([trace](https://dagger.cloud/dagger/traces/b9f169aee2775826ee898963b46c665b)). No unrelated debug-server change was made; the final focused CLI run explicitly skips that test.

API stubs and CLI reference generation passed. PHP navigation syntax and inclusion of both source branches' types passed. All 32 module configurations match the old branch; TOML and lockfile entries parse, source branch ancestry checks pass, and diff whitespace checks exclude the PHP generator's existing whitespace style. The full repository and race suites were not run.
