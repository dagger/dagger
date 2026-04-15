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
Installation

Adopting Dagger
├── Quickstart
├── Core Concepts (Workspaces, Modules, Functions, Checks)
├── Set Up Your Project
├── Secrets
├── Caching
├── Observability
├── CI Integration
└── Engine & Runtime

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
├── .NET Edition (placeholder)
├── Java Edition (placeholder)
├── Rust Edition (placeholder)
└── Elixir Edition (placeholder)

Reference
├── CLI (generated)
├── Workspace Configuration
├── Module Configuration
├── Container Runtimes
├── Upgrading to Workspaces
```

### Installation

Top-level peer to everything. Prerequisite to all other sections.

### Adopting Dagger

Everything you do to make Dagger work for your team. Ranges from one-time getting-started to platform configuration. A platform engineer lands here and sees everything they need to roll out Dagger. A solo dev does Quickstart + Set Up Your Project and moves on — the rest is there when they need it.

**Quickstart:** Clone `dagger/hello-dagger`, install `dagger/eslint`, `dagger/vitest`, `dagger/prettier`, run `dagger check`, `dagger login`, cloud checks. No code written. Minimal narration — let the product speak. Includes Dagger Cloud setup (motivated by "want to see what happened?" after first successful check, then progresses to cloud engines and Cloud Checks for automated CI).

**Set Up Your Project:** Bridge from quickstart to real project via `dagger install github.com/dagger/intro`. The intro module provides red checks with guidance, creating a `dagger check` → edit → `dagger check` feedback loop. The product teaches, not the docs.

**Secrets:** Managing secrets when using Dagger. Providers (env, file, cmd, Vault, 1Password, AWS), safeguards, URI schemes. Showcase page — Dagger does a lot here.

**Caching:** Understanding and configuring cache behavior. Layer caching, volume caching, function call caching. Cache busting, shared caches.

**Observability:** Tracing, debugging, TUI. Configuring OTel backends. Dagger Cloud Traces. Reading and understanding traces.

**CI Integration:** The standard path is Cloud Checks — managed CI with no runners to configure. For teams evaluating incrementally or running alongside existing CI, there's "hybrid mode": call `dagger check` from any CI platform. Hybrid mode is presented as a temporary bridge, not a permanent architecture.

**Engine & Runtime:** Engine configuration, custom runners, proxies, custom CAs. Configure-once infrastructure.

### Using Dagger

Day-to-day usage organized by verbs — the actual things you do. Pure operations, no theory. Core Concepts lives in Adopting Dagger because "what are these things?" is an adopting question, not a using question.

- **Checking your code** — `dagger check`. Local, cloud (`--cloud`), automated (Cloud Checks). Filtering, selecting.
- **Generating code** — `dagger generate`. Changesets, review.
- **Shipping your code** — `dagger ship`. Publishing, releasing, deploying.
- **Running dev services** — `dagger up`. Service discovery, parallel startup.

Dagger Cloud is not a separate section — it's a capability woven into each verb (local → cloud → automated).

### Developing Modules

Three-stage user journey: project-specific module → team-shared → general-purpose/reusable.

**What users see: Editions.** The sidebar shows one guide per language. Users pick their language and get a complete, self-contained guide. There is no "core guide" visible to users.

```
Developing Modules
├── Base Edition (Dang)
├── Go Edition
├── TypeScript Edition
├── Python Edition
├── .NET Edition (placeholder)
├── Java Edition (placeholder)
├── Rust Edition (placeholder)
└── Elixir Edition (placeholder)
```

**What maintainers see: Sources + derived editions.** Editions are derived artifacts, built from two source files: a core guide (patterns and concepts, with Dang snippets) and an SDK guide (language-specific setup, idioms, and snippet overrides). The source layout:

```
extending/
  core-guide.mdx              ← patterns, concepts, structure (Dang snippets)
  sdk-guides/
    dang.mdx                  ← Dang SDK specifics
    go.mdx                    ← Go SDK specifics
    typescript.mdx            ← TypeScript SDK specifics
    python.mdx                ← Python SDK specifics
    ...
  editions/
    dang.mdx                  ← derived from core-guide + sdk-guides/dang
    go.mdx                    ← derived from core-guide + sdk-guides/go
    typescript.mdx            ← derived (placeholder until built)
    ...
```

The sidebar points to `editions/`. The raw core guide and SDK guides are never in the sidebar — they're source files, not user-facing pages.

**Generated-file convention.** Every edition carries a header:

```
<!-- THIS FILE IS GENERATED. DO NOT EDIT DIRECTLY. -->
<!-- Source: extending/core-guide.mdx + extending/sdk-guides/dang.mdx -->
<!-- To rebuild: dagger call build-docs (not yet implemented — manually assembled for now) -->
```

This enforces the discipline: **never edit an edition directly — edit the core guide or the SDK guide.** The edition is a derived artifact. The header is aspirational until the builder exists, but the convention is real from day one. When the real builder ships, the header becomes true instead of aspirational — zero refactoring.

**Why not multi-language tabs in a single guide:**
- **Single source of truth.** The core guide stays pure — one set of concepts, one set of examples. No N-way tab maintenance.
- **Better end product.** An edition is a complete document in your language, not a tabbed patchwork.
- **Composable.** Adding a new SDK means writing one eligible SDK guide, not touching every example in the core guide.
- **Community-friendly.** Third-party SDKs (Rust, Elixir, etc.) can add their own editions without touching core docs.

**Core guide sections:**

- **When to Develop a Module** — Should you write one, or install something? The spectrum from project-specific to general-purpose.
- **Choosing an SDK** — Dang (no codegen, pure DSL, fastest path) vs Go/Python/TypeScript (full language power, existing libraries).
- **Designing for Artifacts** — API surface as artifacts (nouns, not verbs). Selectable, filterable, composable.
- **Workspace Access** — Lazy file access through the Workspace API. Push to the leaves. Filter at the call.
- **Collections** — Keyed sets of related objects with standard algebra (keys, list, get, subset, batch).
- **Verbs (Checks, Generators, Ship)** — Annotating functions as check/generate/ship handlers tied to artifacts.
- **Configuration** — Constructor args with defaults. Workspace config (`config.*`). Progressive disclosure.
- **Testing** — How to test modules. (Placeholder — content TBD.)

**SDK guide requirements (for eligibility):** Each SDK guide must implement a structure and snippet convention that allows the builder to cherry-pick language-specific examples into the core guide's structure and stitch in SDK-specific sections. The exact convention is TBD — the Dang edition bootstraps the pattern.

### Reference

- **CLI** — Generated, no manual work needed.
- **Workspace Configuration** — `.dagger/config.toml` schema.
- **Module Configuration** — `dagger.json` schema.
- **Container Runtimes** — Docker, Podman, nerdctl, Apple Container.
- **Upgrading to Workspaces** — For existing users. Linked from CLI error messages and support channels.

Engine & Runtime, CI Integrations moved to Adopting Dagger. Secrets provider reference (URI schemes, AWS query params) may live in both Adopting Dagger (user-facing) and Reference (schema-level).

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
- Features section — **migrates**, not killed. Content moves to:
  - `secrets.mdx` → Adopting Dagger > Secrets
  - `caching.mdx` → Adopting Dagger > Caching
  - `observability.mdx` → Adopting Dagger > Observability
  - `services.mdx` → Using Dagger > Running dev services
  - `programmability.mdx` → Core Concepts / Developing Modules (split)
  - `sandbox.mdx` → Core Concepts (security model)
  - `reusability.mdx` → Core Concepts > Modules
  - `shell.mdx` → Using Dagger or Adopting Dagger (TBD)
  - `llm.mdx` → Using Dagger or Adopting Dagger (TBD)
  - `local-defaults.mdx` → Adopting Dagger or Reference (TBD)
- Use Cases, FAQ pages — kill (low value, not maintained)
- Cookbook — folded into SDK guides under Developing Modules

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

- Adopting Dagger pages (Secrets, Caching, Observability, CI Integration, Engine & Runtime) — migrate from Features
- Using Dagger verb pages (checking, generating, shipping, running services)
- Developing Modules editions (Dang first, then Go/TypeScript/Python)
- Edition builder tool (`dagger call build-docs`)
- Testing section in core guide
- Sidebar restructure to match final hierarchy (Installation → Adopting Dagger → Using Dagger → Developing Modules → Reference)
- Agent skills (setup-ci, use-dagger, write-module)
- Intro module (`github.com/dagger/intro`)
- Use-case specific pages (e2e tests, etc.)
- "Joining a team" onboarding path
- Additional editions (community SDKs: .NET, Java, Rust, Elixir)
- Module-bundled skills
