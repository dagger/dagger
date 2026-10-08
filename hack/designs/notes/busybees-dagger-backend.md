# `dagger agent` as a busybees backend

Design investigation (angle 3 of 3): what it takes to add `agent = "dagger"`
next to claude/codex/opencode/pi in [busybees](https://github.com/kpenfound/busybees)
(`bees`), instead of rewriting busybees natively on Dagger.

Code cited as `bb:<path>` is busybees at `main` (635ab62); bare paths are this
repository.

## TL;DR

- busybees' backend seam is small and well factored: a `Backend` descriptor
  plus a two-method `backend` impl (`command`, `consume`), with the runner
  owning session dir, env, process group, timeout, transcript, pid file,
  outcome and result file (`bb:core/agent/backend.go:23-47`,
  `bb:core/agent/backends.go:10-67`, `bb:core/agent/session.go:262-562`).
- `dagger agent` today is **interactive only**: it always ends in
  `handler.runInteractive` → `Frontend.Shell` (`internal/cmd/dagger/agent.go:117`,
  `internal/cmd/dagger/functions.go:1159-1217`, `internal/cmd/dagger/shell.go:645-663`),
  and the plain/logs frontends refuse or ignore a shell
  (`dagql/idtui/frontend_plain.go:143`, `dagql/idtui/frontend_logs.go:186`).
  Host writes happen only on an explicit Ctrl+S export
  (`internal/cmd/dagger/session_agent.go:1221-1262`).
- Everything a headless turn needs already exists one layer down:
  `sessionAgent.WithPromptInput` is a blocking one-turn entry point
  (`session_agent.go:787-887`), `ExportChanges` writes the overlay to the
  checkout, pricing exists (`core/modelcatalog/modelcatalog.go:100-116`),
  per-span LLM token metrics flow through telemetry (`dagql/idtui/frontend.go:1049-1081`),
  and trace archives give a resume id (`agent.go:45-125`).
- Two real gaps: **(1)** `LLM.withMCPServer` only speaks stdio to a
  container `Service` (`core/mcpclient.go:17-53`), while the `bees` MCP
  server must run on the host (it writes `outcome.json` and
  `touched-issues.txt` into the host session dir); **(2)** there is no
  tool allowlist, so busybees' grant/restricted model maps only coarsely.
- Recommendation: add `dagger agent --headless` (JSONL on stdout, export on
  success) plus an HTTP transport for `withMCPServer`; then a ~400-line
  `bb:core/agent/dagger.go`. Ship it as "placement `none` only, grants `*`
  only", like pi (`bb:core/agent/backends.go:245-261`), and grow from there.

## 1. The backend contract busybees requires

From the descriptor (`bb:core/agent/backends.go:22-67`), the impl interface
(`bb:core/agent/backend.go:32-47`) and what the runner does around them
(`bb:core/agent/session.go:262-562`):

| # | Requirement | Where |
|---|---|---|
| C1 | **Executable + name**: `Backend.Name` is both the `agent` setting and the executable basename; `procs.AgentExecutables` must list it; doctor carries a check | `backends.go:22-28,198-203`, `bb:core/agent/procs/procs.go:89` |
| C2 | **Credentials / ProviderEnv**: env vars that carry provider credentials and CLI settings; granted per backend | `backends.go:29-39`, `bb:internal/session/session.go:328-342,363` |
| C3 | **Command line**: `command()` returns bin, args, stdin, extra env; system prompt and task prompt are delivered from `system-prompt.md` / `prompt.md` written by the runner | `backend.go:41`, `session.go:310-319` |
| C4 | **Model / effort / max turns / fallback model** from `Profile` | `backend.go:117-128` (claude), `bb:core/agent/pi.go:108-113` |
| C5 | **MCP injection**: the session's complete MCP set, including the built-in `bees` server (`bees mcp serve`, stdio with `BEES_*` env, or HTTP + bearer token via `HostMCP` for isolated sessions); no inherited servers | `bb:internal/session/session.go:259-310,461-487`, `bb:core/agent/container.go:124-171`, `bb:core/mcphost/lease.go` |
| C6 | **Stream reader**: `consume()` tees every stdout line to `transcript.jsonl`, reports cost to a `costMeter` as it goes, returns `streamEnd{SessionID, Result, IsError, Subtype, NumTurns}` and optional `RateLimit` | `backend.go:42-46,70-83`, `session.go:461-485,621-641` |
| C7 | **Turn counting without a final event**: `CountTurns` recognizes `assistant`, `item.completed`, `step_finish`, `turn_end` lines | `bb:core/agent/interrupted.go:115-139` |
| C8 | **Outcome** via the `done` MCP tool → `<session>/outcome.json`, read after exit | `bb:core/agent/outcome.go`, `bb:core/mcphost/done.go`, `session.go:546-553` |
| C9 | **Cost** in dollars (or "unknown"), cost cap enforced by cancelling on the meter | `session.go:77-83,391-400,524-532`, `bb:core/agent/costcap.go` |
| C10 | **Resume id**: `Result.ClaudeID` → next round's `Request.ResumeID`; only the issuing backend may resume | `session.go:67-76,100-106`, `bb:core/agent/restricted.go:75-83` |
| C11 | **Process identity**: pid file always; `ArgvMarker` when argv carries a path-bearing marker scoped to the sessions dir (`--name bees-…` + a session-dir path) | `backends.go:40-44`, `bb:core/agent/procs/procs.go:1-47,451-461`, `bb:internal/session/session.go:73` |
| C12 | **Timeout / cancel**: SIGKILL to the process group; `WaitDelay` 10s; no result file on caller cancel | `session.go:387-416,496-506` |
| C13 | **Restricted (read-only) mode** for review angles: declare `Restricted{Supported, FollowUp, ReadServer}`; no MCP, no writes, no VCS, no skills | `backends.go:45-50,69-81`, `bb:core/agent/restricted.go:47-121`, `bb:core/review/agent.go`, `bb:internal/review/agent.go:89-118` |
| C14 | **WritableTools per Placement**: can a writable turn be held to granted built-in tools, per `{none, none+confine, claude, claude+confine, container, sbx}` | `backends.go:51-58,83-184`, `bb:core/agent/grants.go:379-417` |
| C15 | **Placements / sandbox**: `Profile.Validate` refuses unsupported combos; container/sbx are `box`es that rewrite the command and MCP entries | `bb:core/agent/profile.go:94-125`, `session.go:321-385,753-760` |
| C16 | **Workspace**: the session runs in the role's git worktree (`req.workDir()`), and the developer's work must end up *pushed* (best-of-N judges branches by what was pushed) | `session.go:402-403`, `bb:internal/scheduler/bestofn.go:49,441-445`, `bb:internal/prompts/system/developer.md:50-51` |
| C17 | **Env**: `BEES_SESSION_DIR`, `BEES_STATE_DIR`, `BEES_ROLE`, … and gh/git credentials when `VCSAccess` | `bb:internal/session/session.go:25-50,431-456,489-560` |
| C18 | **Skills**: prepared plugin dirs (`Skills.Prepare`) | `backend.go:184-195` |

## 2. Item by item: what Dagger has, what is missing, smallest change

### C3/C12 — headless invocation, prompt delivery, exit

**Today.** `dagger agent [FILTERS]` composes the workspace's `@agent`
functions onto `dag.LLM().WithWorkspace(snapshot).Compose(...)`
(`agent.go:359-379`) and opens the REPL. There is no prompt flag, no
non-TTY path (`dagger shell` reads a script from stdin when not a TTY,
`shell.go:486-499`, but `dagger agent` bypasses `RunAll`). A module function
returning `LLM` also lands in prompt mode (`functions.go:1129-1132`).

Zero-change workarounds lose too much: `dagger call` on a bees-owned
function `run(ws: Workspace!, system, prompt): Changeset` (`withWorkspace`
→ `withSystemPrompt` → `withPrompt` → `loop`, return the workspace's
changes, `-y` to apply, `functions.go:1081-1093`) is headless today but has
no streaming, composition, resume or host MCP; a Go-SDK program in `bees`
loses the CLI-only machinery (composition, restore-from-trace, pricing,
capture approval).

**Smallest change (dagger).** A `--headless` mode on `dagger agent`, built
from the pieces `startInteractivePromptModeWithResume` already assembles,
replacing `handler.runInteractive` with:

```
dagger agent --headless \
  --system-prompt-file <session>/system-prompt.md \  # LLM.withSystemPrompt
  --prompt-file <session>/prompt.md \                # or "-" for stdin
  --name bees-<session> \                            # archive title + argv marker
  [--model M] [--effort E] [--max-steps N] \         # LLM.withModel / withReasoningEffort / loop(maxSteps)
  [--resume <trace> [--reload-workspace]] \
  [--mcp-url name=URL --mcp-bearer-env name=VAR]... \
  [--skills-dir DIR]... \
  [--untracked include|drop] [--no-export] \
  [FILTERS...]
```

Implementation sketch: build the `LLMSession` as today, call
`own.WithPromptInput(ctx, prompt)` (blocks until the turn ends,
`session_agent.go:849-850`), then `own.ExportChanges(ctx)` unless
`--no-export`, then print a final JSON `result` line and exit 0/1.
`--model` already exists on `dagger shell` (`shell.go:48`) and
`DAGGER_MODEL` works today (`internal/cmd/dagger/llmconfig/env.go:59-60`);
`.effort` exists as a shell command (`shell_commands.go:288`). Exit on
SIGTERM should interrupt (`Agent.pause(interrupt:)`, `session_agent.go:90-93`)
and still print `result`.

### C6/C7 — the stream: transcript and result

**Today.** Frontends render to stderr: pretty, plain, dots, logs, report
(`internal/cmd/dagger/main.go:1053-1065`). Nothing machine-readable on stdout.
The data exists: LLM messages are log records with `LLMRoleAttr` etc.
(`core/llm.go:2959-2981,3097-3117`), token usage is a per-span metric
(`telemetry.LLMInputTokens`…, `dagql/idtui/frontend.go:1049-1081`).

**Smallest change (dagger).** A JSONL emitter for `--headless`, fed by the
same telemetry DB the pretty frontend uses (or a `--progress=jsonl`
frontend). Pick event names busybees already understands, so `CountTurns`
works unchanged (`interrupted.go:134`):

```json
{"type":"session","session_id":"<trace id>","agent":"<handle>","model":"…","provider":"…","trace_url":"…"}
{"type":"text","role":"assistant","text":"…"}
{"type":"tool_use","name":"edit","args":{…},"is_error":false}
{"type":"step_finish","usage":{"input":…,"output":…,"cache_read":…,"cache_write":…},"cost":0.0123,"cost_known":true}
{"type":"export","commits":2,"paths":7}
{"type":"result","subtype":"success","result":"<lastReply>","num_turns":14,"total_cost_usd":0.41,"session_id":"<trace id>"}
```

`subtype` values: `success`, `max_steps`, `interrupted`, `error`,
`export_failed`. A provider 429 surfaces as `error` with the provider's
message, which busybees' `RateLimitedText` / `SessionLimited` already read
for codex/opencode (`session.go:150-190`).

### C9 — cost

**Today.** `LLM.tokenUsage` is cumulative tokens for one conversation
(`core/schema/llm.go:308-309`, `core/llm.go:243-276`); no dollars in the
API. The CLI prices with `modelcatalog.Cost(provider, model, in, out,
cacheRead, cacheWrite)` (`core/modelcatalog/modelcatalog.go:100-116`),
registered for the TUI's live rollup "all models + sub-agents"
(`internal/cmd/dagger/llm.go:148-157`).

**Gap.** `tokenUsage` misses spawned sub-agents (staff workers) and the
small-model calls (titles, compaction). The metric rollup does not.

**Smallest change.** Headless emits `step_finish.cost` from the metric
rollup × `modelcatalog.Cost`, so `costMeter.add` works mid-turn and the
cost cap can stop a dagger turn in flight (unlike claude, which only
reports at the end, `backend.go:129-135`). Uncatalogued/local models →
`cost_known:false`. Subscription OAuth (`ANTHROPIC_AUTH_TOKEN`,
`OPENAI_CODEX_AUTH_TOKEN`, `core/llm.go:1432-1477`) yields a notional
cost, the same as claude's `total_cost_usd` under a subscription.

### C10 — resume id

**Today.** `dagger agent -r <trace>` restores a session from the engine's
retained archive, falling back to Dagger Cloud (`agent.go:45-125,221-233`);
the restored archive's anchors own the workspace and provider
(`agent.go:102-104`). That is a direct match: `ClaudeID = trace id`.

**Gap.** Between rounds busybees changes the branch (review fixes, rebases),
but a restored agent keeps its archived workspace. The interactive fix is
Ctrl+U (`ResetWorkspace`, `session_agent.go:1264-1300`).

**Smallest change.** `--resume <trace> --reload-workspace` = restore, then
`ResetWorkspace` before the prompt. Also needed: resume must work
non-interactively (the bare `-r` picker is already gated on
`canPromptForInit`, `agent.go:62`). Archives must be retained on the engine
bees uses or published to Cloud, else resume fails "before saying
anything", which busybees already retries fresh (`session.go:71-75`).

### C5/C8 — the `bees` MCP server and the `done` tool

**Today.** `LLM.withMCPServer(name, service)` (`core/schema/llm.go:222-227,682-695`)
dials the service over **stdio** only (`core/mcp.go:146-164`,
`core/mcpclient.go:17-94`). `dagger mcp` is the inverse (serves modules
as an MCP server, `internal/cmd/dagger/mcp.go`). busybees' server must run
on the host: `done` writes `outcome.json` and the GitHub tools append
`touched-issues.txt` into the host session dir
(`bb:internal/session/touched.go:12-39`), and mail/notes live in
`BEES_STATE_DIR` (`bb:internal/mcpserver/mcpserver.go:49-69`). busybees
already knows how to run it as `bees mcp serve --listen <addr>` with a
per-session bearer token for container sessions
(`bb:core/agent/container.go:124-171`, `bb:core/mcphost/lease.go:62-104`).

**Smallest change (dagger).** An HTTP (streamable) transport for
`withMCPServer`, e.g. `withMCPServer(name, service, url: String,
bearerToken: Secret)` where `service` is a `Host.service` tunnel
(`core/schema/host.go:111`) to the host loopback port, started with
`svcs.StartBindings` exactly as `http(experimentalServiceHost:)` does
(`core/schema/http.go:244-265`). The CLI flag `--mcp-url
bees=http://127.0.0.1:PORT/mcp --mcp-bearer-env bees=BEES_MCP_TOKEN`
builds the tunnel and an `env://` secret. ~200-300 LOC plus one
integration test.

**Zero-engine-change fallback.** A stdio↔HTTP bridge container as the
`Service` (`withServiceBinding` to the host tunnel), e.g. a
`bees mcp bridge` subcommand built for linux. Works today, adds an image
to maintain, and still needs the CLI flag.

**Role MCP servers.** Only URL entries can be bridged this way; stdio
entries from `[roles.*.mcp]` run commands on the host, which the engine
cannot do. The dagger backend refuses stdio entries other than the
built-in one (which busybees always runs as HTTP for it).

### C17 — env, credentials, gh/git

- Provider credentials are read from the **client's** env by the engine
  (`LLMRouter.loadConfig` with a client `getenv`, `core/llm.go:1381-1530`):
  `ANTHROPIC_*`, `OPENAI_*`, `OPENAI_CODEX_*`, `GEMINI_*`, `LOCAL_*`. The
  `dagger` descriptor's `ProviderEnv` is that list plus `DAGGER_*`,
  `_EXPERIMENTAL_DAGGER_RUNNER_HOST` (already `HostEnv` has `DAGGER_*`,
  `DOCKER_*`, `bb:internal/session/session.go:317-326`).
- `BEES_*` env on the CLI process is harmless but mostly unused: tools run
  in the engine, not as children of the CLI. What needs it is the host
  MCP server, which gets it from `HostMCP.Env` (`session.go:305-307`).
- Git: `Workspace.snapshot` captures the worktree with the client's git
  (`hack/designs/workspace-git.md:70-137`); `export` writes commits + edits
  back with a checked ref transaction (`workspace-git.md:355-406`);
  `GitRef.push` pushes engine-side with the owning client's credentials
  (`workspace-git.md:294-353`). So the developer's "commit, push, open PR"
  steps become tool calls (e.g. the `committer` / `contributor` modules
  this repo installs, `dagger.toml:192-203`) rather than shell commands —
  the busybees prompts (`developer.md:50-51`) need a dagger variant.
- **VCS denial** (no `VCSAccess`): busybees shadows `gh`/`git` on PATH
  (`bb:core/agent/grants.go:73-74,713-740`). For dagger the equivalent is
  "compose no VCS-capable modules, pass no GH token, `--no-export` of
  commits". Export writes `.git` refs, so a non-VCS role must not export
  commits — needs a `--export=edits` mode or refusal.

### C18 — skills

`LLM.withSkills(directory)` exists (`core/schema/llm.go:228-235`): skills
are directories with a `SKILL.md`. `--skills-dir <dir>` → `withSkills(
host.directory(dir))` for each prepared dir (`backend.go:184-195`).
busybees' prepared dirs are Claude plugin dirs; whether they hold
`skills/<name>/SKILL.md` in the shape `withSkills` discovers ("anywhere
in the tree") needs a check, but it looks compatible.

### C13 — restricted (read-only) mode

**Today.** No tool allowlist/denylist on `LLM` (no `withBlockedFunction`
or equivalent in `core/schema/llm.go:64-313`). Core builtins with no
modules composed are read-only trace/log/artifact/skill readers
(`core/mcp.go:2743-2952`, `core/llm_skills.go:511-532`); file tools come
from installed modules (e.g. `editor`, `dagger.toml:180-181`).

**Structural floor.** Writes land in the in-engine overlay and reach the
host only via `export` (`hack/designs/workspace-agents.md:22-26`), so
`--no-export` already guarantees *no host writes*. That is stronger than
claude's tool flags for the filesystem, but weaker on network/exec: a
composed module can run containers with network.

**Smallest change.** Mirror codex: `Restricted{Supported: true,
FollowUp: true, ReadServer: true}` and run
`dagger agent --headless --no-agents --no-export --mcp-url bees-read=…`
— no modules composed, only busybees' own read server over the worktree
(`bb:core/agent/readserver.go:71`, given to codex the same way,
`backend.go:342-363`). Needs only `--no-agents` (compose nothing; today
FILTERS cannot select the empty set) plus the HTTP MCP transport. A later
`LLM.withToolFilter(allow:)` would let it use `editor`'s read tools
instead.

### C14/C15 — grants and placements

The dagger CLI process does almost nothing on the host: capture
(read the worktree with git), the export (the only host write), the
host-service tunnel, and env lookups. Tools run in engine containers.

| Placement | Declaration | Why |
|---|---|---|
| `none` | supported, `WritableTools` refused ("grant `*`") | no per-tool allowlist yet |
| `none`+confine | unsupported | the CLI needs the engine socket/docker, `~/.cache/dagger`, `~/.config/dagger`; Landlock/Seatbelt profiles would need those paths and the engine is root anyway |
| `claude`(+confine) | refused in `Validate` | Claude Code's sandbox runs claude alone (`profile.go:98-100`) |
| `container` | refused initially | dagger-in-docker-in-engine; little gain |
| `sbx` | later | `bb:core/agent/sbxdagger.go` already installs the Dagger CLI in the sandbox and forwards the host engine (`startDagger`, lines 55-73); add `SbxTemplates["dagger"]` and relax `verifyDagger`'s sbx-only rule (`grants.go:419-435`) so `sandbox_dagger_engine` also selects the engine for `agent = "dagger"` on the host |

Note the inversion: for claude/codex the sandbox protects the host from
*tools*; for dagger the tools are already isolated in the engine, and the
remaining host surface is the CLI's export. `Grants.Mounts` for `none`
stay `/` rw (`bb:internal/session/session.go:418-421`) because the CLI's
export is a host write wherever the worktree is; `DaggerEngine` should
become a grant for this backend in every placement, since the engine is
root on the host (`sbxdagger.go:131-134`).

### C11 — `bees kill`

The pid file works as-is (`session.go:455-459`). For the ps scan, argv
`dagger agent --headless --name bees-<session> --system-prompt-file
<sessions dir>/<session>/system-prompt.md` carries both the `--name
bees-` session marker (`bb:internal/session/session.go:73`) and the
sessions-dir path the scan scopes by (`procs.go:29-41`) → `ArgvMarker:
true`. Add `"dagger"` to `procs.AgentExecutables`
(`procs.go:89`); `isAgentCommand` checks only the first two words
(`procs.go:451-461`), so `dagger agent …` matches. Killing the CLI ends the
engine client session, which should cancel the loop server-side; the engine
itself (shared, long-lived) is not a session process and must not be
killed. The archive is left unsealed and remains best-effort resumable
(`agent.go:283-292`).

**Loss on kill/timeout:** edits live in the overlay until export, so a
SIGKILLed or timed-out dagger session leaves *nothing* in the worktree
(claude leaves partial edits on disk). Mitigation: busybees sends SIGTERM
first for this backend (headless interrupts, exports, prints `result`),
SIGKILL after `WaitDelay`; or the trace resume recovers the work.

### C16 — workspace binding and untracked files

`snapshotWorkspace` (`agent.go:381-397`) tries a frozen capture and falls
back to the live workspace with a warning. Untracked files need an
interactive Include/Drop decision, and "a noninteractive call that needs
approval fails" (`workspace-git.md:110-122`). busybees worktrees are
normally clean (session files live outside the worktree), but leftovers
from crashed rounds are not rare. Hence `--untracked include|drop`.
Without a frozen snapshot, `export` falls back to the overlay-at-host-root
behavior (`workspace-git.md:399-402`), which still works for a live local
worktree but is not resumable.

Agent composition is from the workspace's installed `@agent` modules
(`agent.go:359-379`, `workspace-agents.md:95-132`). A target repo without
a `dagger.toml` gets a bare LLM with no file tools. busybees needs a way
to inject its kit without editing the repo: `dagger agent -m <module>`
style extra agent modules (or a bees-owned `@agent` module installed via a
`--env` overlay). That kit should include a "run a command in the role's
`sandbox_image` with the workspace mounted, return the Changeset" tool, or
developers cannot run tests in repos without Dagger checks.

## 3. Sketch: `bb:core/agent/dagger.go`

```go
// Descriptor, appended to Backends (backends.go:204).
{
    Name:        AgentDagger, // "dagger"
    Credentials: []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN",
        "OPENAI_API_KEY", "OPENAI_CODEX_AUTH_TOKEN", "GEMINI_API_KEY", "LOCAL_BASE_URL"},
    ProviderEnv: []string{"ANTHROPIC_*", "OPENAI_*", "GEMINI_*", "LOCAL_*",
        "DAGGER_*", "_EXPERIMENTAL_DAGGER_RUNNER_HOST", "DOCKER_*"},
    ArgvMarker:  true,
    Restricted:  &RestrictedCapabilities{Supported: true, FollowUp: true, ReadServer: true},
    WritableTools: supportNowhere(fmt.Sprintf("grant %q", ToolsAll)),
    bin:  func(r *Runner) string { return r.DaggerBin },
    impl: daggerBackend{},
}

func (daggerBackend) command(ctx, r, b, req, paths) (bin, args, stdin, env, err) {
    bin = b.executable(r)
    args = []string{"agent", "--headless", "--progress=plain",
        "--name", r.namePrefix() + req.Name,
        "--system-prompt-file", paths.systemPrompt,   // path-bearing argv marker
        "--prompt-file", "-"}                         // task on stdin
    if m := req.Profile.Model; m != ""  { args += "--model", m }
    if e := req.Profile.Effort; e != "" { args += "--effort", e }
    if n := req.Profile.MaxTurns; n > 0 { args += "--max-steps", n }
    if req.ResumeID != "" { args += "--resume", req.ResumeID, "--reload-workspace" }
    if d := req.Profile.Dagger; d != nil { env += {EnvDaggerRunnerHost, d.Engine} } // sbxdagger.go:40
    switch {
    case paths.restricted:
        args += "--no-agents", "--no-export"
        args += "--mcp-url", ReadServerName+"="+paths.read.url,
                "--mcp-bearer-env", ReadServerName+"="+readServerTokenEnv
        env  += {readServerTokenEnv, paths.read.token}
    default:
        for name, e := range paths.mcp {           // built-in arrives as a URL entry
            if e.URL == "" { return error("dagger: MCP server %q is stdio; only URL servers reach the engine") }
            args += "--mcp-url", name+"="+e.URL
            if e.BearerTokenEnv != "" { args += "--mcp-bearer-env", name+"="+e.BearerTokenEnv }
        }
        for _, d := range skillDirs(ctx, r, req) { args += "--skills-dir", d }
        args += "--untracked", "drop"
        args = append(args, req.Profile.DaggerAgents...) // FILTERS, e.g. "editor", "bees-kit"
    }
    return bin, args, req.Prompt, env, nil
}

func (daggerBackend) consume(r, stdout, transcript, cost) (*streamEnd, *RateLimit, error) {
    var end *streamEnd; var sid, last string; turns := 0
    err := r.tee(stdout, transcript, func(line []byte, typ string) {
        switch typ {
        case "session":     sid = ev.SessionID           // trace id
        case "text":        last = ev.Text
        case "step_finish": turns++; if ev.CostKnown { cost.add(ev.Cost) }
        case "result":      end = &streamEnd{SessionID: ev.SessionID, Result: ev.Result,
                                Subtype: ev.Subtype, NumTurns: ev.NumTurns, IsError: ev.Subtype != "success"}
        }
    })
    if end == nil { return nil, nil, err }              // runner counts turns (C7)
    if end.SessionID == "" { end.SessionID = sid }
    if end.Result == "" { end.Result = last }
    return end, nil, err
}
```

Runner-side changes around it:
- **Host MCP without a box.** Today the built-in server is started as HTTP
  only by the container/sbx boxes (`container.go:99-122`, `sbx.go:141`).
  Factor `container.startServer` into a runner helper and use it when
  `be.Name == AgentDagger` regardless of placement; `mcpEntries` then
  yields the URL entry with `BearerTokenEnv = BEES_MCP_TOKEN`. The server
  pid file and orphan cleanup already exist (`procs.WriteServerPID`).
- **Engine URL reachability.** The engine reaches the host loopback only
  through `Host.service` tunnels, which the CLI builds from `--mcp-url`;
  busybees keeps listening on `127.0.0.1` (`lease.go:62-70`).
- **Cancel.** For this backend `cmd.Cancel` sends SIGTERM to the group and
  relies on `WaitDelay` for SIGKILL (`session.go:407-416`).
- **Validate** refuses `sandbox` ∈ {claude, container} and confine for
  `agent = "dagger"`; `verifyDagger` accepts `Profile.Dagger` for this
  backend outside sbx.
- **Config/doc/doctor**: `agent = "dagger"` in profiles
  (`bb:docs/configuration.md:757-791`), a `dagger_agents` list (FILTERS),
  a doctor check for `dagger version` and a reachable engine,
  `review` providers (`bb:internal/review/agent.go:193-220` derives them
  from descriptors), `SbxTemplates` later.
- **Prompts**: a dagger flavor of `developer.md` / assemble prompts where
  "git push / gh pr create" become tool calls.

## 4. What busybees gains, and what it loses

**Gains**
- **Isolation without a sandbox mode.** The CLI host process only
  captures, tunnels and exports; every tool runs in engine containers; the
  overlay makes "no host write until export" structural. Closest to
  busybees' `container` mode without maintaining images per agent CLI.
- **Trace archive + Cloud traces.** Every session is a trace: replayable
  transcript, `dagger agent -r <trace>` to inspect or continue *any* bees
  session by hand, `trace_url` to link from GitHub comments; resume works
  across machines via Cloud (`agent.go:140-164`).
- **Caching.** Tool execs (builds, tests, `check`) share the engine cache
  across concurrent sessions and rounds; best-of-N attempts on one issue
  reuse each other's builds. LLM calls are per-session
  (`core/schema/llm.go:22-23`, `PerSessionInput`), so no accidental
  answer reuse.
- **Workspace modules as tools.** A repo's own `@check`/`@generate`
  functions and installed agent modules become the agent's tools; a
  bees-owned `@agent` kit gives every repo the same tool surface regardless
  of provider (Anthropic, OpenAI incl. Codex subscription, Gemini, local).
- **Mid-turn cost cap** from per-step cost events (claude cannot,
  `backend.go:129-135`), sub-agent costs included.
- **Engine-side git**: commits/pushes are pure operations with receipts
  (`workspace-git.md:294-353`); `export` merges cleanly into a worktree
  that moved.

**Losses / can't do (yet)**
- **Built-in tool grants** (`--tools`/`allowedTools` equivalents): none
  until an LLM tool filter exists → `WritableTools` unsupported
  everywhere, like pi.
- **Restricted mode** depends on `--no-agents` + HTTP MCP (read server);
  network/exec restrictions inside modules are not expressible.
- **Stdio MCP servers** from the role table cannot run (host commands).
- **Partial work on kill/timeout** stays in the overlay, not the worktree.
- **Confinement** (`confine = true`) is not applicable; the engine is
  root-equivalent, so it is a bigger grant than any agent CLI.
- **Rate limits / fallback model**: no structured `rate_limit_event`, no
  in-session model fallback; both handled by the caller, as for codex.
- **Prompt portability**: shell-centric prompts must be rewritten as tool
  usage; without a shell-in-container tool, repos without Dagger checks
  cannot run their tests.
- **Untracked-file capture** needs an explicit headless policy.

## 5. Ordering and rough sizing

1. **dagger: `dagger agent --headless`** (prompt/system-prompt files,
   `--name`, `--model`, `--effort`, `--max-steps`, `--no-export`,
   `--untracked`, `--no-agents`, SIGTERM → interrupt + export + result).
   Reuses `LLMSession`/`sessionAgent`. ~500-700 LOC + tests. *Unblocks a
   prototype with `agent = "dagger"` minus MCP.*
2. **dagger: JSONL event stream** (`session`, `text`, `tool_use`,
   `step_finish` with cost, `export`, `result`) from the telemetry DB +
   `modelcatalog.Cost`. ~300-500 LOC.
3. **dagger: HTTP transport for `withMCPServer`** (+ `--mcp-url` /
   `--mcp-bearer-env`, `Host.service` tunnel, `env://` secret). Public API
   change → schema snapshot + SDL diff. ~200-300 LOC + integration test.
   *Unblocks `done` and the whole bees tool surface.*
4. **busybees: `core/agent/dagger.go` + descriptor** (placement `none`,
   grants `*`), host-MCP-without-box refactor, SIGTERM cancel,
   `procs.AgentExecutables`, doctor, config/docs, fake-`dagger` contract
   tests alongside `contract_test.go`. ~800-1200 LOC incl. tests.
5. **dagger: `--resume --reload-workspace`** headless (restore + reset).
   ~150 LOC. busybees passes `ResumeID`. *FollowUp for reviews.*
6. **busybees: restricted mode** via `--no-agents` + read server; add
   `dagger` to review providers. ~150 LOC.
7. **bees `@agent` kit module** (shell-in-`sandbox_image`, gh via the
   session token, commit/push) and `dagger agent -m` style injection.
   Size depends on reuse of `editor`/`committer`/`contributor`.
8. **Later**: `LLM` tool allowlist (→ `WritableTools`, finer restricted
   mode), `sbx` placement via `sbxdagger.go`, structured rate-limit events.

Steps 1-4 are the minimum for a useful developer role (~2-3 weeks of
focused work across both repos); 5-6 make reviewers and multi-round roles
work; 7 decides whether it is pleasant on repos that are not already
Dagger workspaces.

## Open questions

- Do `GitRef.push` delegated-push approvals ever prompt
  (`workspace-git.md:344-349`)? Headless must never block on one.
- Should the headless export also run on failure (`IsError`) so a failed
  developer turn leaves its work for the next round, as claude does?
- Is a bees-owned `@agent` kit better shipped as a module referenced by
  bees config, or should busybees require targets to install it in
  `dagger.toml`?
- The cheaper inverse: give existing claude/codex sessions the repo's
  Dagger tools by adding `dagger mcp` as a role MCP server
  (`internal/cmd/dagger/mcp.go`). It gets caching and workspace modules
  without a new backend, but none of the trace/resume/isolation benefits.
