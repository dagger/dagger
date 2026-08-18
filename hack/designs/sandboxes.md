# Sandboxes: last-resort arbitrary exec for workspace agents

Proposal for giving workspace agents access to arbitrary container sandboxes —
an `exec` tool that runs an argv in any container with the workspace mounted —
without turning it into the honeypot that a generic `bash` tool becomes.
Builds on the object-tools architecture in
[workspace-agents.md](workspace-agents.md).

## 1. Problem

1. **No escape hatch** — Agents get curated tools (checks, generators, editor
   tools, purpose-built modules like engine-lab). When a task goes off the
   beaten path — install a package, run one specific test binary, poke at a
   CLI — there is no way to just run a command, and the agent either gives up
   or contorts (e.g. `webFetch` abuse, writing throwaway check functions).
2. **The obvious fix is a honeypot** — A generic `bash`/`exec` tool reliably
   out-competes better tools: models reach for it first, ignore the ergonomic
   curated surface, and lose the structured results (trace reports, changeset
   summaries, check rows) that the curated tools produce. `modules/editor`
   carries a commented-out `bash` sketch that was shelved for exactly this.
3. **Sandboxes can't be named** — The natural tool shape is
   `exec(args: [String!]!, sandbox: Container!)`, but a required object-typed
   arg disqualifies a method from becoming a tool
   (`objectToolEligible`, `core/llm_object_tools.go`): the model has no
   handle to pass. So today a sandbox arg can't even be expressed.
4. **Sandbox definitions have no home or lifecycle** — There is no convention
   for where reusable dev containers live, no way for the model to discover
   them, and no loop for the model to write and maintain them.

## 2. Design at a glance

One new module and two small core APIs:

| Piece | What | Where |
|---|---|---|
| `sandbox` module | `exec` / `changes` / `listSandboxes` tools + guidance prompt | `modules/sandbox/main.dang` (Dang) |
| Addressable args | required `Container!` (etc.) tool args render as **address strings**, lifted via `Query.address` in the session context | `core/llm_object_tools.go` |
| Discovery | `Workspace.addresses(type: "Container")` lists module functions loadable as sandboxes | `core/schema/workspace.go` |

Everything else already exists: `Query.address(value).container` resolves both
image refs and `module:function` refs (`core/schema/address.go`, including
demand-loading installed-but-unloaded modules); `Changeset` returns auto-apply
to the workspace (`MCP.routeObjectMethodResult`); `editor.install`/`editor.reload`
already give the model the write-a-module-and-recompose loop; the CLI already
lifts strings into `Container!` flags via the exact same address scheme
(`internal/cmd/dagger/flags.go`).

The lifecycle this enables:

* **Trivial task** → `exec(sandbox: "golang:1.26", args: ["go", "vet", "./..."])`
  — any image ref works, nothing to set up.
* **Recurring need** → the model writes a `Container!`-returning function
  (in an existing module when one fits, else a `sandboxes` module it
  maintains), reloads, and uses its address: `exec(sandbox: "sandboxes:go", ...)`.
* **Discovery** → `listSandboxes` prints every `Container!`-returning function
  across installed modules, plus the active sessions.

## 3. The `sandbox` module

```graphql
"""
Run commands in arbitrary container sandboxes, with the workspace mounted.
LAST RESORT: prefer purpose-built tools (checks, generators, module tools).
"""
type Sandbox {
  """
  Run an argv in a sandbox container with the workspace at /workspace.

  LAST RESORT — reach for this only when no purpose-built tool can do the
  job (e.g. running one test directly when checks can't be filtered that
  far). Prefer checks for tests/lints, generators for codegen, and module
  tools for everything they cover.
  """
  exec(
    """
    The sandbox to run in: an image ref like "golang:1.26", a module
    function like "sandboxes:go", or any other container address.
    """
    sandbox: Container!,
    """The command to run, as argv. No shell by default; use ["sh", "-c", ...] explicitly if needed."""
    args: [String!]!,
    """
    Named session to run in. Empty runs one-shot: container state (installed
    packages, env, files outside /workspace) is discarded after the call.
    A name persists the container across calls under that name.
    """
    session: String! = "",
    """Discard the named session's stored state before running."""
    fresh: Boolean! = false,
  ): Sandbox!

  """
  Pull the workspace changes a sandbox made: the diff of its /workspace
  against the tree as it was mounted, applied to the workspace.
  """
  changes(session: String! = ""): Changeset!

  """
  List available sandboxes: Container-returning module functions usable as
  `sandbox` addresses, and this session's active sandbox sessions.
  """
  listSandboxes: String!
}
```

Implementation notes (all patterns proven by existing modules):

* **Mounting.** Each call copies (`withDirectory`, not a mount — mounts don't
  diff) `ws.directory("/")` from the auto-injected `Workspace!` to
  `/workspace` and sets the workdir there, so the sandbox always sees the
  *current* tree including the model's pending edits — the per-call
  re-injection lesson from `modules/engine-lab` (`withSource`, and the stale
  tree bug class its comment documents).
* **Output.** `withExec(args, expect: ReturnType.ANY)`; print the exit code
  when nonzero and a tail-truncated stdout/stderr (the engine-lab `tail`
  pattern). A failing command is a result, not an error.
* **Caching.** `@cache(policy: FunctionCachePolicy.Never)` on `exec` and
  `changes`, plus a `Random.string` nonce env var to bust the exec cache —
  the mandatory pair for live tools (engine-lab's NOTE explains why).
* **State.** Session containers and their "before" trees live in private
  `let` maps (`Map[Container!]!` / `Map[Directory!]!` keyed by session name);
  `exec` returns `Sandbox!` so the rebind-on-self-return rule persists them
  (engine-object handles in Dang state serialize as IDs — the
  `modules/staff` pattern). One-shot calls still record before/after under an
  implicit `""` session so `changes` works on the last run.
* **Freshness rule.** A continued session re-copies the current workspace
  into `/workspace` *only when doing so cannot destroy work*: if the
  session's `/workspace` still differs from the tree mounted into it
  (un-extracted changes), the container tree is kept and the model is nudged
  to run `changes` (or pass `fresh: true`). If clean, re-copy — the session
  keeps up with workspace edits automatically. One wrinkle, found by the
  prototype: `changes` cannot advance its own session's baseline — a
  `Changeset!` return doesn't rebind module state (single return channel
  again) — so `exec` self-heals instead: a "dirty" session whose
  `/workspace` already matches the *current* workspace tree is clean by
  definition (its extraction was applied) and re-syncs.
* **Change nudge.** After every exec, if `/workspace` diverged from the
  mounted tree, print a one-line diffstat: `sandbox changed 3 file(s) —
  call changes() to pull them into the workspace`. The model never has to
  remember to check.
* **Composition.** `agent(base: LLM!): LLM! @agent` binds `currentNode` and
  adds the guidance prompt (§6). Installable/removable per workspace like
  any agent module; not installed by default anywhere.
* **Possible ergonomics, not in v1:** a `workdir` arg (relative to
  `/workspace`) to remove the most common reason for `sh -c "cd x && ..."`;
  an env-var arg for the same reason. Add on evidence, not speculation.

### Why two tools, not one

A tool has exactly one return channel (`MCP.routeObjectMethodResult`): return
`Sandbox!` and the state rebinds; return `Changeset!` and it overlays the
workspace. `exec` needs the former (sessions), extraction needs the latter, so
they are separate tools with the before/after containers held per-session in
between. This is also the right shape for guidance: sandbox effects reach the
workspace only through an explicit, changeset-summarized step — `exec` can
never silently edit the tree.

### Why argv, not a shell string

`args: [String!]!` mirrors `withExec` and keeps quoting sane; `["sh", "-c",
...]` is available when a pipeline is genuinely needed. The mild friction is
intentional (§6).

## 4. Addressable arguments (core)

The tool layer's eligibility rule changes from "no required object args" to
"no required object args **that can't be expressed as an address**":

* `objectToolEligible` (`core/llm_object_tools.go`): a required arg whose
  `@expectedType` names an *address-liftable* type no longer disqualifies the
  method. Other object types still do, as do lists of objects (`[Container!]!`
  — element-wise lifting is a possible follow-up).
* `objectMethodSchema`: such args render as strings described
  `(Container address)` — extending the existing `(Container ID)` convention
  for optional object args — ideally with per-type syntax hints ("an image
  ref like `golang:1.26`, an installed module function like `sandboxes:go`,
  or a Container ID from a previous tool result").
* Dispatch (`callObjectMethod`): a string supplied for an object-typed arg is
  first tried as an ID (today's behavior — IDs passed between tools keep
  working, and an encoded ID can never collide with an address); if the ID
  decode fails, it is lifted: `Query.address(value)` → `Address.container`
  (or the field matching the expected type), and the resulting object's ID
  becomes the argument. When both attempts fail, the error reports both.

This is a straight port of what the CLI already does for flags —
`internal/cmd/dagger/flags.go` resolves a `Container!` flag as
`c.Address(v.address).Container().Sync(ctx)` — so the tool schema and the CLI
speak the same address language.

### The liftable set is a capability decision

The CLI lifts nine types (Container, Directory, File, Secret, Service,
Socket, GitRepository, GitRef, Volume), but a CLI flag is human-typed;
a tool arg is **model-typed**, and several `Address` decoders resolve
strings into capabilities the model doesn't otherwise hold:

* `Address.secret` resolves `env://GITHUB_TOKEN`, `file://...`, `op://...` —
  lifting it lets the model *mint* secrets by guessing URIs instead of only
  receiving handles it was given.
* `Address.directory`/`.file`/`.socket` fall back to **host** paths
  (`host.directory`, `host.unixSocket`), and `.service`/git types reach the
  host's network and local repos for non-remote addresses — all outside the
  workspace boundary that agent reads/writes are otherwise scoped to
  ([workspace-agents.md](workspace-agents.md) §1).

`Container` has no host fallback — image refs pull from registries, bare
refs resolve installed modules — so **v1 lifts `Container` only**. The
mechanism is general (a per-type allowlist); each further type gets its own
capability review before joining (e.g. Directory/File restricted to git
URLs, Secret probably never without consent machinery).

Two semantics worth stating: `Address.container` is registered
`WithInput(dagql.PerCallInput)`, so an address re-resolves fresh on every
tool call — two execs against `"alpine:latest"` may observe different images
(matching CLI flag behavior, unlike a stable model-passed ID) — and the
resolution `Select` should run non-internal so that e.g. an image pull
renders in the trace as part of the tool call rather than vanishing.

### Resolution context is the point

The lifting **must** happen in the tool layer, not inside the module. A
module-side `sandbox: String!` + `address(sandbox).container` resolves against
the *module's own* schema: `resolveModuleRef` reads
`dagql.CurrentDagqlServer(ctx)`, and `demandLoadInstalledModule` explicitly
refuses to load workspace siblings for module clients (the
`md.ClientID != ws.ClientID` gate — modules only see their declared deps, by
design). Validated against a from-source engine: from inside the module,
`"sidecar:ctr"` falls through to image resolution and fails, while a self-ref
(`"sandbox:demo"`) resolves fine. Object-tool dispatch, by contrast, selects
against the session's workspace-served schema (`buildObjectMethodSelector`
runs on the MCP's `srv`) — the same visibility a workspace CLI user has,
which is exactly right: the *model* is the caller, the module is just the
implementation. (The prototype also sharpened the CLI picture: even a host
client's `Container!` flag resolves module refs only in *workspace-mode*
invocations — module refs are a workspace concept, and agent sessions are
workspace-scoped by construction.)

This change matters beyond sandboxes: every type admitted to the allowlist
makes a whole class of module functions tool-eligible (a required
`Directory!` arg expressible as a git URL, a `Service!` as a module ref) —
the tool schema converges on the CLI's expressiveness, at whatever pace the
capability reviews allow.

## 5. Discovery (core)

```graphql
extend type Workspace {
  """
  Addresses loadable from the workspace's installed modules: functions whose
  return type matches `type` and whose required args (beyond an auto-injected
  Workspace) are none, rendered as bare "module:function" references.
  """
  addresses(type: String!): [WorkspaceAddress!]!
}

type WorkspaceAddress {
  """The address value, e.g. "sandboxes:go"."""
  value: String!
  """The function's doc string."""
  description: String!
}
```

* Implementation follows the `Workspace.checks`/`generators`/`services`
  rollup precedent: walk installed modules' main objects via the mod tree,
  which already records each function's return type and description
  (`ModTreeNode.Children`, `core/modtree.go`), and keep zero-required-arg
  functions returning `type`. New field, so it is view-gated like its
  siblings (`View(AfterVersion(...))`, see `internal-docs/version-gating.md`).
* Scope matches what `resolveModuleRef` can load today: top-level functions
  only (single `module:function` segment), entrypoint modules excluded (their
  functions are hoisted and not addressable — `demandLoadInstalledModule`
  already errors on this case with an explanation).
* `listSandboxes` = `ws.addresses(type: "Container")` rendered as
  `value — description` lines, a reminder that any image ref or address works
  too, and the module's live session table.

**Why not enum options on the `sandbox` arg**: baking discovered addresses
into the tool schema as `enum` would exclude arbitrary image refs, and would
go stale mid-session the moment the model writes a new sandbox function.
Discovery is a tool result, not a schema constraint.

Until this landed, a v0 `listSandboxes` introspected in-module via
`ws.modules` → `ws.moduleSource(...).asModule` → object type defs — workable
(a module client *may* load and introspect sibling workspace modules through
the explicit Workspace API; only the implicit address-string demand-load is
capability-gated) but heavy, since it loaded every module. The core rollup
replaced it.

## 6. Guidance: keeping the honeypot covered

The tool is deliberately fenced with three kinds of pressure:

**Prompt.** The module's system prompt states the ladder explicitly:

> You have a sandbox escape hatch: `exec` runs a command in any container
> with the workspace mounted. It is a LAST RESORT, and almost always the
> wrong first move:
> - Tests and lints: use checks (`check`, `listChecks`) — sandbox `go test`
>   only when checks can't select what you need (e.g. one test, `-run`) and
>   the iteration saving is significant.
> - Codegen and formatting: use generators.
> - Anything a module tool covers: use the module tool — it returns
>   structured results a raw command doesn't.
> Reaching for exec is a signal: if no tool covered the need, note it; if
> you exec the same setup twice, graduate it into a sandbox definition (§
> "maintaining sandboxes") instead of re-installing packages per call.

**Friction.** No default sandbox (every call names one), argv instead of a
shell string, and changes reach the workspace only through the explicit
`changes` step with a changeset summary. Trivial when genuinely needed,
annoying as a default hammer.

**Visibility.** Every exec is an ordinary span in the trace with its full
command line; sandbox use is auditable, and a session that leaned on `exec`
for things checks covered is visible in review.

## 7. Writing and maintaining sandboxes

The model owns its sandboxes as ordinary module code:

* **Where.** In an existing module when one plausibly owns the concern
  (e.g. a `go` toolchain module gains a `devContainer`); otherwise in a
  workspace-local `sandboxes` module the model creates and maintains — the
  catalog of that workspace's dev environments.
* **How.** A `Container!`-returning function, ideally wolfi-based for cheap
  package installs:

  ```dang
  type Sandboxes {
    """Go development: compilers, gopls, delve; module cache mounted."""
    go: Container! {
      wolfi.container(packages: ["go", "gopls", "delve", "git"])
        .withMountedCache("/go/pkg/mod", cacheVolume("go-mod"))
        .withEnvVariable("GOMODCACHE", "/go/pkg/mod")
    }
  }
  ```

* **Reload loop.** Already exists: `editor.install(ref)` adds a module to the
  workspace config (`Workspace.withModule`) and recomposes the conversation
  in place; `editor.reload` recomposes against the current tree (pending
  edits included) after editing an existing one (`modules/editor/main.dang`).
  After either, the new address resolves immediately — `Address.container`
  demand-loads installed modules that this session hasn't loaded yet.
* **Trust.** Sandbox definitions are module code and exec runs in engine
  containers: the same trust envelope as any module function the agent can
  already call. The workspace is copied in, never written directly; the only
  way back is the reviewed `Changeset`. No host access, no privileged flags
  in v1 (a sandbox needing nesting or root capabilities is a purpose-built
  module's job, e.g. engine-lab).

## 8. Alternatives considered

* **A generic `bash` builtin** — the honeypot (§1); also unownable: no
  per-workspace curation, no prompt, no lifecycle. Rejected; the shelved
  editor `bash` sketch is its tombstone.
* **`exec` on the editor module** — couples the escape hatch to the editing
  toolset; a separate module keeps it opt-in per workspace, separately
  promptable, and removable without losing the editor.
* **Binding a `Container` via `withTools`** — the generic object-tools path
  technically exposes `withExec` etc., but Container's surface is huge,
  uncurated, and full of object-typed args; none of the workspace mounting,
  session, or changeset semantics fall out. Not a substitute.
* **Module-side `sandbox: String!` resolution** — broken for sibling module
  refs (§4); served as the v0 stopgap until addressable args landed, with
  image refs (and the module's own catalog via self-ref) still working.
  Since removed from the module.
* **Implicit sessions keyed by address** — every exec on `"golang:1.26"`
  continuing prior state reads nicely but hides state, and made the
  before/after bookkeeping for `changes` ambiguous. Explicit `session`
  names are one arg and no surprises.
* **Auto-applying changes on every exec** — impossible in one tool (single
  return channel) and undesirable anyway: it would make `exec` a stealth
  editor with no changeset review point.

## 9. Phasing

1. **v0, module only (no core changes)** *(landed, since superseded)*:
   `modules/sandbox` with `sandbox: String!`, in-module
   `address(...).container` resolution — image refs and the module's own
   catalog worked; sibling module refs failed with a pointed error, and v0
   discovery annotated them as such. Validated the exec/session/changes
   semantics.
2. **v1, addressable args** *(landed)*: the `core/llm_object_tools.go`
   change; the module's arg became `sandbox: Container!` and sibling refs
   work. The change stands alone and benefits every module.
3. **v2, discovery** *(landed)*: `Workspace.addresses(type:)` +
   `listSandboxes` wired to it (dropping the v0 introspection fallback).

## Status

Implemented. The pieces, in dependency order:

* `core/llm_object_tools.go` — addressable tool args (§4): eligibility,
  `(Container address)` schema rendering with per-type syntax hints,
  ID-first decode with address lifting at dispatch, non-internal lift
  tracing, and the Container-only capability allowlist. Covered by unit
  tests and a replay integration test driving a lifted `exec` tool through
  a live engine (`core/integration/llm_object_tools_test.go`).
* `Workspace.addresses(type:)` + `WorkspaceAddress` (§5) — view-gated like
  their workspace siblings, best-effort module loading, with integration
  tests (`core/integration/workspace_addresses_test.go`).
* `modules/sandbox/main.dang` — the module in its v1/v2 shape:
  `exec(sandbox: Container!, ...)` resolved at the caller's boundary,
  `listSandboxes` wired to `Workspace.addresses`, guidance prompt per §6.
  Validated end-to-end against a from-source engine:
  image refs and sibling module refs (`sidecar:ctr`) both lift, `changes`
  extraction applies, sessions persist across chained execs, and discovery
  lists siblings. `modules/sandbox/testdata/ws/` is the manual QA
  workspace.

Remaining:

* Integration coverage of `modules/sandbox` itself (freshness rule,
  changes extraction, dirty-session self-heal) through a live agent
  session — the lifting mechanism is integration-tested; the module is
  covered by manual QA only.
* Installation stays per-workspace opt-in by design (§3, §7); nothing
  registers the module by default.
