# Modules v2

Modules v2 is the umbrella for all changes to the way you develop, configure,
and operate Dagger modules.

## Components

| Component | Status | Doc |
| --- | --- | --- |
| [Workspace](./workspace.md) | API + plumbing shipped; configuration in progress | workspace.md |
| [Lockfile](./lockfile.md) | In progress | lockfile.md |
| [Artifacts](./artifacts.md) | Designed | artifacts.md |
| [Execution Plans](./plans.md) | Designed | plans.md |
| [Collections](./collections.md) | Designed; prior prototype exists (`collections` branch) | collections.md |
| [Provenance](./provenance.md) | Designed (high level) | provenance.md |
| [Stdlib](./stdlib.md) | Stage 1 in progress | stdlib.md |

## Dependency Graph

```
                                         Stdlib stage 1
                                              │
Workspace API (done) ─────────────────────────┤
        │                                     │
        ├──────────────────┐                  ▼
        ▼                  ▼             Stdlib stage 2
Workspace plumbing    Lockfile                │
     (done)                │                  │
        │                  ▼                  │
    Artifacts    Workspace configuration      │
        │                                     │
        ▼                                     │
 Execution Plans                              │
        │                                     │
        ▼                                     │
  Collections ────────────────────────────────┤
        │                                     │
        ▼                                     ▼
    Provenance                           Stdlib stage 3
```

**Main track:** Workspace plumbing → Artifacts → Execution Plans → Collections
→ Provenance. Artifacts establishes the general selector model first:
module-level dimensions plus synthesized non-collection selector dimensions
such as `check`. Collections plug in later as keyed dimension providers and
batch/subset semantics on top of that base.

**Config track:** Lockfile → Workspace configuration. Independent of the main
track.

**Stdlib track:** Progresses in stages with gateway dependencies:
- **Stage 1** — regular modules, no new infrastructure needed
- **Stage 2** — modules adopt Workspace API (receive workspace, read files/dirs)
- **Stage 3** — modules expose collections (GoModules, GoTests, etc.)

## Design Principles

- **Engine smart, CLI dumb.** In doubt, push logic into the engine.
- **API is UX.** Type names, function names, and breakdown matter.
- **One filter model.** `--<dimension>=<value>`, repeatable, across all commands.
- **Introspection-driven.** The CLI is a generic client — no per-workspace
  codegen. Dimensions, filters, and actions are discovered at runtime.
- **Plans, not VMs.** Verbs compile to inspectable finite DAGs. No loops,
  conditionals, or variables.

## Related Documents

- [../artifacts-on-collections-report.md](../artifacts-on-collections-report.md) —
  design exploration transcript (Artifacts API, Plans, filter model)
- [../workspace-artifacts.md](../workspace-artifacts.md) — baseline artifacts design
- [../workspace-artifacts-transcript-guide.md](../workspace-artifacts-transcript-guide.md) —
  reading guide for the full design transcript
