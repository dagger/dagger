# llm-workspace-stacked — handoff

The tidied, stack-ready rewrite of `llm-workspace-fresh`: 109 commits squashed
and reordered into 40, in five slices ready to become stacked PRs. Based on the
current head of **#13600 `workspace-overlay-export`** (`e55008800`, which
already includes the cherry-picked lock-reads fix). Written 2026-07-13 for a
fresh session whose job is: open the stacked PRs, then run a regenerate for
each PR (see §4).

## 1. The stack

Each slice builds green on the one below it. `⛳` marks a PR head.

```
                e55008800  (base: #13600 workspace-overlay-export)
PR A — engine & TUI fixes (independent; could even target main)
     1  3487ae343  fix(clientdb): cap SQLite pool at one connection
     2  ef2ed559c  fix(engine): batch client DB log writes
     3  022aaaf30  fix(dagql): bound parallel resolution fan-out
     4  77e2e4433  fix(dagui): let verbosity expand rolled-up spans
  ⛳ 5  a5c391f2b  feat(trace): fetch whole trace at high verbosity
PR B — Workspace file APIs
     6  697bb96f3  feat(workspace): add Workspace.glob API
  ⛳ 7  a401ef883  feat(workspace): add Workspace.search API
PR C — LLM foundation: providers, config, session CLI, TUI
     8  16dd8da20  feat(llm): add config, CLI, and Anthropic OAuth
     9  c18af1f88  feat(llm): content-block model, API, and SDKs
    10  b287cd589  feat(llm): add OpenAI Codex provider
    11  51ff724c5  feat(cli): add LLM session save/resume/interject
    12  71b4ee99b  feat(tui): render LLM diffs, queue, status, branch
    13  4493ea6ee  fix(sdk/typescript): keep LLM acronym raw
    14  92a70d188  feat(llm): round-trip extended thinking
    15  7c873145f  feat(llm): trace HTTP and stream display live
    16  9719f56ef  fix(llm): guard handle-form digests, fix setup UX
    17  111d32220  fix(llm): decode inputs, detect streaming mode
    18  661bcc383  feat(llm): route local models through a c2h tunnel
  ⛳19  35d33c5ec  chore(lint): resolve golangci-lint findings
PR D — the LLM ⇄ Workspace rework: object tools, Env removal, @agent
    20  07387bc36  feat(llm)!: bind the LLM to a Workspace
    21  270ba445c  feat(core): resolve module context via workspace
    22  90e125980  feat(llm)!: generate tools from bound objects, eliminate Env
    23  a0a0ec338  feat(core): add Query.currentNode
    24  b8fc7d6b4  feat(llm): add skill discovery and reading tools
    25  2064b0c3b  fix(llm): add context tokens, fix cache accounting
    26  80ad08b41  feat(cli): rework the LLM session UX
    27  dd37d7b89  feat(tui): surface LLM conversations like checks
    28  3d5bf612c  llm: installable @agent plugins via dagger agent
    29  00b7041cc  test: cover @agent discovery and composition
    30  4707ef3ad  fix(tui): agent prompt log visibility and duration display
    31  da026307f  workspace: run group leaves on the bound workspace
    32  06862409c  core: carry the bound workspace across module calls
    33  793345b37  core: rebind LLM workspace when a tool returns one
    34  a739a5430  chore(lint): resolve golangci-lint findings
    35  9d47c5136  chore: regen SDKs, docs, and toolchains
  ⛳36  31c8b75c2  docs(design): add workspace-agents as-built design doc
tip — dev tooling (probably not a PR, or a final small one)
    37  d91fc18cd  docs(skills): add tui-surfacing, extend tui-qa
    38  6db861670  skills: add dagger-llm-workspace development skill
    39  fe9093644  add tui-qa agent module
  ⛳40  66394f7a2  docs(design): add overlay sparse-read design doc
```

Commit 22 deliberately absorbs the whole intermediate "Dang as the LLM's tool
scheme" era: the object-tools rework deleted that layer wholesale, so the
squash presents only the final form. `hack/designs/workspace-agents.md`
(commit 36) is the as-built design doc replacing the retired working docs
(`LLM_PLUGINS.md`, `WORKSPACE_GENERATE_SYNC.md`, `hack/designs/llm-object-tools.md`,
the old `HANDOFF.md`, `ideas.md` — all stripped from history).

## 2. Verification done

- **Tree identity**: the tip tree is byte-identical to the original
  `llm-workspace-fresh` HEAD (see `backup/llm-workspace-fresh-2026-07-13`)
  except for intended deltas: working docs removed, the new design doc, ~22
  code comments repointed from the dead docs to it, and the fixes below.
- **Fixes made during the tidy** (each inside the commit that introduces the
  code): `core/workspace_context.go` call to `EnsureWorkspaceModules` updated
  for the `bestEffort` arg the #13600 rebase introduced (the original branch
  HEAD did not compile); a `codexAPIError` helper moved from commit 10 to 15 so
  every commit in PR C compiles; two pre-existing gofmt misses folded into the
  lint commits.
- **Builds**: `go build ./...` is clean at all five PR heads; `gofmt -l` clean
  at tip.
- **Generate**: the tip (= PR D head + tip commits) is `dagger generate`-clean
  across go-sdk/docs/python/typescript/rust/php/elixir/engine-dev/go/golang/
  markdown-lint.

## 3. Two findings that gate the PRs

1. **The base PR #13600 is generate-dirty (~14k lines).** The vendored module
   clients (core/integration testdata, toolchains, modules, viztest) and SDK
   client libraries were never regenerated for the overlay/export schema types
   (`RemoteGitMirror`, `HTTPState`, `ClientFilesyncMirror`, …). Any PR based on
   it fails `check-generated` before our changes even enter the picture.
   Branch **`regen-workspace-overlay-base`** (`023c77784`, one commit on the
   base) holds the exact `dagger generate` output — cherry-pick it into
   #13600 like the lock fix. PRs A/B/C then only need their own deltas.
2. **`dagger generate` lies at older checkouts** (why per-PR regen must be done
   carefully, or in CI): the `*-sdk-dev` toolchains' generate cache key does
   not include the engine schema — e.g. `GoSdkDev.workspaceDir` includes only
   `sdk/go` + `cmd/codegen`, and the engine-dev service is addressed by the
   stable name `"sdk"`. Once a generate has run at one checkout, running it at
   an older one cache-hits (`go-sdk:generate DONE [0.3s]`) and replays results
   from the wrong schema. Fresh CI checkouts are immune. Locally, bust the key
   by leaving the previous run's generated-file changes dirty in the worktree
   when re-running (the generated files are inside the input sets), or prune
   the engine cache. This toolchain cache-key bug deserves its own fix on main.

## 4. Next session's job

1. Cherry-pick `regen-workspace-overlay-base` into #13600 and push.
2. Rebase this branch onto the new #13600 head (the regen commit will make
   commit 35 partially redundant; expect generated-file conflicts — resolve by
   re-running generate at the affected heads).
3. Open the stacked PRs at the ⛳ heads: A → B → C → D (and decide whether the
   tip's dev tooling ships as a small fifth PR or stays local).
4. Per PR, run `dagger generate -y <all generators except changelog>` at the PR
   head and commit the delta as the PR's final commit. Expected deltas: A none;
   B glob/search in SDK clients + docs schema; C the LLM API surface; D none
   (commit 35 already reconciles; verify).

## 5. Ref inventory

- `llm-workspace-stacked` — this branch (tip = §1 log + this doc).
- `llm-workspace-tidy` — same tip, minus this doc; working branch of the tidy.
- `backup/llm-workspace-fresh-2026-07-13` — untouched original branch.
- `base/workspace-overlay-export` — local pin of #13600's head (`e55008800`).
- `regen-workspace-overlay-base` — the base regen cherry-pick candidate (§3.1).
- `tmp-regen` — scratch, safe to delete.
