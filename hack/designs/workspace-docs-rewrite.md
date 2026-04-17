# Workspace Docs Rewrite

Design plan for documentation changes required to merge the workspace branch.

## Design Principles

- **Docs are not a crutch.** If users *have* to read too much docs to get anything done, the product isn't intuitive enough. The best product is the one where reading the docs is not necessary.
- **Holistic product design.** CLI, engine, APIs, and docs are designed as a whole. Error messages in the CLI can be changed as part of an overall flow.
- **Diataxis framework.** Tutorials (learning-oriented), how-to guides (task-oriented), explanation (understanding-oriented), reference (information-oriented).
- **Two tiers of users.** Module users (install, configure, call, check — no code) and module developers (write code with an SDK). The distinction is stronger than before.
- **Less content to maintain.** If it's not reference, it should earn its place. Fewer, better pages.

## Concept Changes

| Old Concept | What Happens |
|---|---|
| Toolchain | Dropped. Just "module." |
| Blueprint | Dropped. `entrypoint = true` is a config flag, not a concept. |
| Customizations | Deprecated. Module config via `config.*` in workspace config. |
| `dagger.json` as project config | Moved to `.dagger/config.toml`. `dagger.json` is module-only. |

New core concepts: **Workspaces, Modules, Functions, Checks.**

"Toolchain" can survive as informal language but not as a formal concept.

## Docs Structure

```
Overview

Installation

Adopting Dagger
├── Quickstart
├── Workspace Setup
├── Secrets
├── Observability
├── Triggers
│   ├── GitHub Actions, GitLab, CircleCI, Jenkins, Azure Pipelines,
│   │   AWS CodeBuild, Argo Workflows, Tekton, TeamCity
│   └── (overview: Cloud Checks as standard path; "hybrid mode" as bridge)
├── Scaling
│   ├── Kubernetes
│   └── OpenShift
└── Engine Configuration

Using Dagger
├── Checking your code
├── Generating code
├── Shipping your code
└── Running dev services

Developing Modules
├── Base Edition (Dang)
├── Go Edition
├── TypeScript Edition
├── Python Edition
├── Java Edition (placeholder)
├── PHP Edition (placeholder)
├── Testing
└── Types Reference
    ├── Container, Directory, File, Secret, Service,
    ├── CacheVolume, GitRepository, Env, LLM

Core Concepts
├── Workspaces
├── Modules
├── Artifacts
├── Functions
├── Checks
└── Caching

Reference
├── CLI
│   ├── Command reference (generated)
│   └── Lockfiles
├── Module Configuration (dagger.json)
├── Workspace Configuration (.dagger/config.toml) — TODO
├── Engine schema (engine.json / engine.toml)
├── Cloud
├── Cache
├── LLM
├── Custom Runner
├── Custom CA
├── Proxy
└── Upgrading to Workspaces
```

Core Concepts is a top-level peer, not an Adopting Dagger child. It earns the slot because concepts are reference-shaped (you return to them as lookup), not journey-shaped. Adopting Dagger is the journey; Core Concepts is the mental model that journey builds on.

### Installation

Top-level peer to everything. Prerequisite to all other sections.

### Adopting Dagger

Everything you do to make Dagger work for your team. Ranges from one-time getting-started to platform configuration. A platform engineer lands here and sees everything they need to roll out Dagger. A solo dev does Quickstart + Workspace Setup and moves on — the rest is there when they need it.

**Quickstart:** Clone `dagger/hello-dagger`, install `dagger/eslint`, `dagger/vitest`, `dagger/prettier`, run `dagger check`, `dagger login`, cloud checks. No code written. Minimal narration — let the product speak. Includes Dagger Cloud setup (motivated by "want to see what happened?" after first successful check, then progresses to cloud engines and Cloud Checks for automated CI).

**Workspace Setup:** Bridge from quickstart to real project via `dagger install github.com/dagger/setup`. The setup module provides red checks with guidance, creating a `dagger check` → edit → `dagger check` feedback loop. The product teaches, not the docs.

**Secrets:** Managing secrets when using Dagger. Providers (env, file, cmd, Vault, 1Password, AWS), safeguards, URI schemes. Showcase page — Dagger does a lot here.

**Observability:** Tracing, debugging, TUI. Configuring OTel backends. Dagger Cloud Traces. Reading and understanding traces.

**Triggers:** How Dagger gets triggered from CI. Cloud Checks is the standard path — managed, no runners to configure. For teams evaluating incrementally or running alongside existing CI, there's "hybrid mode": call `dagger check` from any CI platform (GitHub Actions, GitLab, CircleCI, Jenkins, Azure Pipelines, AWS CodeBuild, Argo Workflows, Tekton, TeamCity). Hybrid mode is a temporary bridge, not a permanent architecture.

**Scaling:** Self-hosting the engine at scale. Kubernetes (Helm chart, DaemonSet, auto-scaling) and OpenShift (tainted nodes, tolerations). Presented as second-class to Dagger Cloud's managed engines.

**Engine Configuration:** Engine config, custom runners, proxies, custom CAs. Configure-once infrastructure. Schema-level reference lives in Reference; user-facing "why and how" lives here.

Caching used to live here but moved to Core Concepts — layer/volume/function-call caching is a mental model users return to, not a one-time setup step.

### Using Dagger

Day-to-day usage organized by verbs — the actual things you do. Pure operations, no theory. Core Concepts used to live here (then in Adopting Dagger) and is now a top-level peer: mental-model lookup, not part of any specific journey.

- **Checking your code** — `dagger check`. Local, cloud (`--cloud`), automated (Cloud Checks). Filtering, selecting.
- **Generating code** — `dagger generate`. Changesets, review.
- **Shipping your code** — `dagger ship`. Publishing, releasing, deploying.
- **Running dev services** — `dagger up`. Service discovery, parallel startup.

Dagger Cloud is not a separate section — it's a capability woven into each verb (local → cloud → automated).

### Developing Modules

Three-stage user journey: project-specific module → team-shared → general-purpose/reusable.

**Editions are self-contained.** The sidebar shows one guide per language. Users pick their language and get a complete, self-contained guide. Each edition is its own source file with its own snippet tree — not a derived artifact.

```
Developing Modules
├── Base Edition (Dang)     ← fully fleshed out; the reference implementation
├── Go Edition              ← "not yet available" placeholder + recipes + IDE setup
├── TypeScript Edition      ← "not yet available" placeholder + recipes + IDE setup
├── Python Edition          ← "not yet available" placeholder + recipes + IDE setup
├── Java Edition            ← placeholder
├── PHP Edition             ← placeholder
├── Testing
└── Types Reference         ← shared across editions
```

**What we tried and abandoned: sources + derived editions.** An earlier iteration split the guide into a shared `core-guide.mdx` plus per-SDK `sdk-guides/*.mdx`, with editions marked as "generated — do not edit directly" (aspirational, no builder existed). We killed this in the current pass:

- The builder was never going to get built — too much Docusaurus/MDX snippet choreography for too little payoff.
- "Aspirational generated-file" headers invited exactly the wrong behavior: editors avoided the file, so fixes landed in the wrong place.
- Splitting concepts from language kept the Dang edition accurate but made every other edition look deceptively complete while actually being a near-copy of Dang.

**Replacement plan:** Dang is the reference edition. Go/TS/Python editions are currently placeholders with recipes + IDE setup, and will be fleshed out directly — each with its own prose and snippets, referencing the Dang edition for shared concepts where that saves work. If duplication becomes painful, we revisit — but duplication is cheap and divergence is honest.

**Why not multi-language tabs in a single guide:**
- **Better end product.** An edition is a complete document in your language, not a tabbed patchwork.
- **Composable.** Adding a new SDK means writing one new edition, not touching every example.
- **Community-friendly.** Third-party SDKs can add their own editions without touching core docs.

**Dang edition (reference) sections:**

- **When to Develop a Module** — Should you write one, or install something? The spectrum from project-specific to general-purpose.
- **Choosing an SDK** — Dang (no codegen, pure DSL, fastest path) vs Go/Python/TypeScript (full language power, existing libraries).
- **Designing for Artifacts** — API surface as artifacts (nouns, not verbs). Selectable, filterable, composable.
- **Workspace Access** — Lazy file access through the Workspace API. Push to the leaves. Filter at the call.
- **Collections** — Keyed sets of related objects with standard algebra (keys, list, get, subset, batch).
- **Custom types, enums, interfaces** — Extending the type system.
- **Verbs (Checks, Generators, Ship)** — Annotating functions as check/generate/ship handlers tied to artifacts.
- **Configuration** — Constructor args with defaults. Workspace config (`config.*`). Progressive disclosure.
- **Caching** — How caching behaves in modules. Cross-refs to Core Concepts > Caching.
- **Documentation** — Docstrings, examples, the `dagger shell` introspection story.
- **IDE setup** — Per-edition IDE configuration.
- **Recipes** — Cookbook-style examples (distributed from the old Cookbook).

**Testing** lives as a sibling item in the sidebar rather than inside each edition — same testing story across SDKs.

**Types Reference** is a shared sub-category (Container, Directory, File, Secret, Service, CacheVolume, GitRepository, Env, LLM). Moved out of Reference into Developing Modules so the types live next to the code that uses them.

### Reference

- **CLI** — Command reference (generated) + Lockfiles page.
- **Module Configuration** — `dagger.json` schema.
- **Workspace Configuration** — `.dagger/config.toml` schema. **TODO** — sidebar still has a placeholder comment.
- **Engine** — `engine.json` / `engine.toml` schema.
- **Cloud** — Dagger Cloud config options.
- **Cache** — Cache-specific engine schema.
- **LLM** — LLM provider config schema.
- **Custom Runner** — Running your own engine container.
- **Custom CA** — Custom certificate authority setup.
- **Proxy** — HTTP/HTTPS/NO_PROXY config.
- **Upgrading to Workspaces** — For existing users. Linked from CLI error messages and support channels.

Container Runtimes is gone (dropped — runtime-detection is automatic; edge cases are support-channel material, not docs material). Triggers/Scaling (née CI Integration) moved to Adopting Dagger. Secrets provider reference (URI schemes, AWS query params) may live in both Adopting Dagger (user-facing) and Reference (schema-level).

## Upgrading to Workspaces Page

Standalone page for existing users experiencing the workspace migration. Linked from CLI errors, support threads, blog posts, changelog.

Content:

- Clear "if you're new, skip this" signal
- What changed: `dagger.json` used to serve double duty (module + project config). Now modules (`dagger.json`) and workspaces (`.dagger/config.toml`) are separate.
- What happened to toolchains: now just modules in your workspace. `dagger install` adds them.
- What happened to blueprints: now `entrypoint = true` config flag.
- Migration: backwards compat infers workspace from `dagger.json`. Run `dagger migrate` when ready.
- Command equivalence table (old → new).

## CLI Error Message Changes

### Error 1: `-m` on legacy workspace (hard error)

Current:
```
This module must be migrated to a workspace. Run 'dagger -W <ref>'
```

Proposed (dynamic, based on which fields are present):
```
This module's dagger.json uses <fields>, which have moved to workspaces.
Try: dagger -W <ref>
What changed: https://docs.dagger.io/reference/upgrade-to-workspaces
```

### Error 2: Compat warning (soft, non-blocking)

Current:
```
No workspace config found, inferring from dagger.json. Run 'dagger migrate' soon.
```

Proposed:
```
No workspace config found, inferring from dagger.json.
Run 'dagger migrate' when ready. More info: https://docs.dagger.io/reference/upgrade-to-workspaces
```

## Pages to Kill / Migrate

- All existing quickstarts (basics, CI, toolchain/blueprint, agent, agent-in-project) — **DONE**
- Core concepts: `toolchains.mdx` — **DONE**
- Features section — **DONE**. Section deleted; valuable content transposed (commit `b4c777c2f`). Caching landed in Core Concepts, not Adopting.
- Use Cases, FAQ, Examples pages — **DONE** (killed `introduction/use-cases.mdx`, `examples.mdx`, `faq.mdx`).
- Cookbook — **DONE**. Recipes distributed into per-SDK editions (commit `ed1aa0492`).
- Pre-workspace API guide — **DONE**. `getting-started/api.mdx`, `getting-started/api/clients-{cli,http,sdk}.mdx`, `reference/api/internals.mdx` all killed as an orphan island.
- Custom applications guide (`extending/custom-applications/*`) — **DONE** (killed; the "embed an SDK directly" story wasn't earning its upkeep).
- `reference/glossary.mdx` — **DONE**.
- `reference/troubleshooting.mdx` — **DONE**.
- `reference/best-practices/{monorepos,adopting,contributing}.mdx` — **DONE**.
- `reference/ide-setup.mdx` — **DONE** (content moved into per-edition IDE setup sections).
- `reference/deployment/{kubernetes,openshift}.mdx` — **DONE** (moved under Adopting Dagger > Scaling).
- `getting-started/ci-integrations/*` — **DONE** (moved under Adopting Dagger > Triggers; `github.mdx` misc-tips page killed).
- Container Runtimes — **DONE** (dropped entirely, commit `c2f1c0d09`).
- `extending/core-guide.mdx` + `extending/sdk-guides/*` — **DONE** (sources-derived model abandoned).

Remaining redirects for all the above are captured in `docs/netlify.toml`.

## Agent Skills

Three skills for `dagger/skills/` repo, following the SKILL.md open standard:

```
dagger/skills/
├── setup-ci/SKILL.md      — Detect stack, install modules, configure workspace, verify
├── use-dagger/SKILL.md    — Run checks, generate, ship, debug failures
└── write-module/SKILL.md  — Design and implement a module in Dang
```

Design principles:
- **Task-oriented, not reference-oriented.** Vercel evals show skills work best for procedural workflows. Reference knowledge goes in AGENTS.md.
- **Under 500 lines / 5,000 tokens each.** Detailed material in `references/` subdirectories.
- **Use-case framing for discoverability.** "Set up CI for your project" maximizes marketplace discovery — skill discovery as the new SEO.
- **Own distribution, syndicate to marketplaces.** Git repo is the primitive. Publish to skills.sh and others as amplifiers.
- **CI/CD category is uncontested.** No major CI/CD tool has published official skills. Dagger would own this space.

Future: modules can ship companion skills. `dagger install github.com/dagger/eslint` makes a skill available that teaches agents how to configure and troubleshoot that module.

## Merge MVP

Minimum for merging the workspace branch:

1. **Upgrade page + CLI error messages** — existing users aren't stranded
2. **Quickstart** — new users have a front door
3. **Core Concepts rewrite** — new concepts are explained
4. **Kill old pages** — no contradictory content

Everything else ships after merge.

## Follow-up Work (Post-Merge)

**Done (shipped during this restructure pass):**
- Adopting Dagger pages — Secrets, Observability, Triggers, Scaling, Engine Configuration.
- Features section migration / deletion.
- Dang edition fleshed out (custom types, enums, interfaces, caching, documentation).
- Cookbook distributed into editions.
- Testing promoted to sidebar item.
- Types Reference moved under Developing Modules.
- Sidebar restructure (Overview → Installation → Adopting → Using → Developing → Core Concepts → Reference).
- Orphan cleanup: glossary, troubleshooting, best-practices, ide-setup, use-cases, faq, examples, custom-applications, pre-workspace API pages, container runtimes.

**Still pending:**
- **Workspace Configuration reference page** (`.dagger/config.toml` schema) — intentionally deferred. Tracked in `docs/TODO.md`.
- **Flesh out Go / TypeScript / Python editions** — currently placeholders with IDE setup + recipes. Need full prose covering the Dang edition's section structure, with language-idiomatic snippets.
- **Using Dagger verb pages** (checking, generating, shipping, running services) — exist but light; may need depth pass.
- **"Upgrading to Workspaces" page** — stub exists; need to flesh out with command equivalence table and migration walkthrough.
- Edition builder tool — **abandoned**. Editions are now hand-written per language.
- Agent skills (setup-ci, use-dagger, write-module).
- Setup module (`github.com/dagger/setup`, née `dagger/intro`).
- Use-case specific pages (e2e tests, etc.).
- "Joining a team" onboarding path.
- Additional community-SDK editions (.NET, Rust, Elixir — currently Java and PHP have placeholder pages).
- Module-bundled skills.
