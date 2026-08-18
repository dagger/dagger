# LLM workspace cleanup handoff

## Current state

The cleanup is complete and all deliverable branches are pushed. The main
feature remains a seven-PR stack; six unrelated fixes live on standalone refs.
The three tests found missing after the first cleanup pass have been restored.

Stack order, bottom to top:

| Branch | Head | PR | Base |
|---|---:|---:|---|
| `llm-workspace-services` | `32bd74e83d` | [#13832](https://github.com/dagger/dagger/pull/13832) | `main` |
| `llm-workspace-trace-reports` | `7778b2806a` | [#13833](https://github.com/dagger/dagger/pull/13833) | `llm-workspace-services` |
| `llm-workspace-volumes` | `c25e5213d3` | [#13834](https://github.com/dagger/dagger/pull/13834) | `llm-workspace-trace-reports` |
| `llm-workspace-commit` | `4bf76ebbed` | [#13835](https://github.com/dagger/dagger/pull/13835) | `llm-workspace-volumes` |
| `llm-workspace-continuations` | `966ffab99c` | [#13836](https://github.com/dagger/dagger/pull/13836) | `llm-workspace-commit` |
| `llm-workspace-host-git` | `d4da0a0fb4` | [#13837](https://github.com/dagger/dagger/pull/13837) | `llm-workspace-continuations` |
| `llm-workspace-dev-env` | `8c798f1706` | [#13838](https://github.com/dagger/dagger/pull/13838) | `llm-workspace-host-git` |

Standalone refs:

| Branch | Head | Remote |
|---|---:|---|
| `fix-llm-refusal-limit` | `8c7f7e6de3` | `upstream` |
| `fix-schema-builder-lookup-nil` | `14039a0255` | `upstream` |
| `fix-telemetry-missing-caller` | `51e1f8e83c` | `upstream` |
| `fix-discarded-cache-sweep` | `a0e772e9f8` | `upstream` |
| `tui-crop-fix` | `828d6b7005` | `upstream` |
| `fix-h2c-read-header-timeout` | `ba8373210e` | `origin` |

The H2 branch existed before the cleanup and is patch-identical to the fix
removed from the stack. The TUI branch contains bubble cropping, removal of
cursor-moving controls, the required Tuist bump, frozen flowing indicators,
and their regression tests.

The call-payload tests now live on `llm-workspace-trace-reports`. Restoring them
also moved their required telemetry attributes and `ServiceNode.Origin` into
that lower layer. The flowing-render regression now lives on `tui-crop-fix`.

## Suggested PR titles and descriptions

### Main stack

1. **Services: discover and surface service instances**

   Add service discovery and trace-relative surfacing, preserve exited services,
   and keep service logs available without leaking tool-only output into reports.

2. **LLM: render tool results from trace reports**

   Build bounded tool results from captured trace scopes, publish the call
   payload closure needed to rebuild IDs, and support importing and restoring
   agent sessions from published traces.

3. **Workspace: add cache volume mounts**

   Allow workspaces to mount cache volumes at explicit paths and preserve those
   mounts through workspace operations. This PR was previously labelled draft
   and unfinished; review its intended product status before marking ready.

4. **Workspace: stage and replay Git commits**

   Add engine-side commit staging, commit provenance and replay across
   workspaces, conflict inspection, and Changes-bubble support for staged
   commits.

5. **LLM: adopt conversations returned by tools**

   Let tools return a replacement conversation and have the active LLM adopt it,
   enabling continuation-style tools without losing the surrounding session.

6. **Workspace: reconstruct host Git state portably**

   Capture host Git worktrees as bounded portable checkpoints, reconstruct them
   from client-provided packs, and retain a safe save target across resumed
   sessions.

7. **Agent: add the in-repo development environment**

   Add the asynchronous agent runtime, agent-session CLI and TUI, trace resume,
   request-scoped model credentials, addressable workspace objects, sandbox
   agents, context visualization, recipe replay semantics, development modules,
   and one final generated SDK/reference update.

### Standalone fixes

8. **LLM: fail turns stopped by refusal or token limits**

   Treat provider refusals and output-limit terminations as failed turns instead
   of accepting truncated or empty responses as successful completions.

9. **Core: make SchemaBuilder lookup nil-safe**

   Guard schema lookup against absent builders so malformed or incomplete module
   state returns an error instead of panicking.

10. **Telemetry: tolerate missing caller metadata**

    Handle telemetry records without caller metadata and retain coverage for the
    degraded path rather than assuming every call has a complete parent chain.

11. **Engine: sweep discarded cache state gradually**

    Move discarded cache cleanup to a paced background sweep to avoid large
    synchronous pauses while still reclaiming abandoned engine state.

12. **TUI: keep notification and flowing output stable**

    Clamp notification content to its box, remove cursor-moving control
    characters, and freeze running indicators once output enters native terminal
    scrollback so incremental repaints cannot corrupt prior rows.

13. **Server: omit ReadHeaderTimeout for HTTP/2**

    Avoid applying the HTTP/1 header timeout to HTTP/2 servers, where it can
    terminate otherwise healthy long-lived connections.

## Verification and known issue

Passing checks:

- Call-payload publication and per-session deduplication tests.
- Flowing conversation and notification-bubble regression tests.
- Agent runtime integration suite.
- Workspace-address integration tests.
- DagUI and DagQL unit tests.
- Compile-only checks for core, schema, SDK, and CLI packages.

Known failure: `TestSandbox/TestDangUsesNestedDaggerSchema` consistently ends
with a nested dev-engine `unexpected EOF`. Six other sandbox/address tests in
that focused run passed. A broad host-side run also encountered environment-
sensitive CLI/Git tests involving the installed CLI version and local fixtures.

`CLEANUP_HANDOFF.md` is intentionally untracked so it does not alter PR #13838.

## Recovery refs

Do not delete these until the PRs are submitted and reviewed:

```text
cleanup-backup-commit-20260817                 79b0f08d3f
cleanup-backup-continuations-20260817          c8bb51721d
cleanup-backup-dev-env-20260817                76108d2ad4
cleanup-backup-host-git-20260817               244d1bd718
cleanup-backup-services-20260817               32bd74e83d
cleanup-backup-trace-reports-20260817          29c3782efc
cleanup-backup-volumes-20260817                556d7666ef
cleanup-post-commit-dev-env-20260817            243dae7582
cleanup-post-commit-host-git-20260817           27895600ef
cleanup-post-host-commit-20260817               01b76c2df9
cleanup-post-host-continuations-20260817        ab2af396ad
cleanup-post-host-dev-env-20260817              b0983a5970
cleanup-post-host-host-git-20260817             f1a0ee4421
cleanup-post-host-trace-reports-20260817        0f14444151
cleanup-post-host-volumes-20260817              5d21fcf0be
cleanup-pre-drop-standalone-dev-env-20260817    8333daa2be
cleanup-pre-squash-dev-env-20260817             32aa4c5f7f
cleanup-pre-standalone-dev-env-20260817         8333daa2be
cleanup-pre-test-restore-commit-20260817        73b1016bd1
cleanup-pre-test-restore-continuations-20260817 3dfdb6be7d
cleanup-pre-test-restore-dev-env-20260817       e9310ffd8a
cleanup-pre-test-restore-host-git-20260817      2b38647af5
cleanup-pre-test-restore-trace-reports-20260817 ebd7ece21e
cleanup-pre-test-restore-tui-crop-20260817      9e15337fd9
cleanup-pre-test-restore-volumes-20260817       527c2a389e
cleanup-restored-tree-20260817                  b10bb12226
cleanup-step1-commit-20260817                   8c3d3d375d
cleanup-step1-continuations-20260817            49dd87b7bb
cleanup-step1-dev-env-20260817                  cc4939079f
cleanup-step1-host-git-20260817                 d04f86a58a
cleanup-step1-trace-reports-20260817            0f14444151
cleanup-step1-volumes-20260817                  5d21fcf0be
```

The most useful broad snapshots are:

- `cleanup-backup-dev-env-20260817`: original pre-cleanup top.
- `cleanup-post-commit-dev-env-20260817`: state before later pushdowns and
  standalone extraction; useful for checking whether content was lost.
- `cleanup-pre-squash-dev-env-20260817`: exact input to the thematic squash.
- `cleanup-pre-test-restore-dev-env-20260817`: cleaned top before restoring the
  missing tests and lower-layer prerequisites.
