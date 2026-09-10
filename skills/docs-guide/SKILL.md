---
name: docs-guide
description: Author or review a platform guide in docs/current_docs/guides ("Daggerize a Go Project", Go + Compose, TypeScript + Playwright, and similar). Use when asked to write, extend, or review a guide that takes a whole project type from installing an official module to Checks running on every push.
---

# Docs Guides

How to write a guide under `docs/current_docs/guides/`. The exemplar is
`docs/current_docs/guides/go/index.mdx`. Read it before writing a new one and
match its shape.

## What a guide is for

A guide answers one question: "I have a project of type X. How do I daggerize
it, all the way up?" It is the end-to-end path for one platform, from installing
the official module to Checks running on every push.

Guides are a parallel axis to the rest of the docs:

- Getting Started and Configuration teach Dagger concepts in a linear order.
- Reference pages describe one module, command, or config file.
- A guide cuts across all of that for a single platform so the reader never has
  to assemble the journey themselves.

Once a guide owns the narrative for a platform, the matching pages under
`reference/modules/` become pure reference. Each guide directory is shaped so it
can ship as a distributable skill: `index.mdx` is the entry document, branch
pages and snippets are its resources.

## Layout

- One directory per platform: `guides/<platform>/`.
- `index.mdx` is the trunk, a linear progression: install the module, configure
  it, run generators, extend it with a small module of the reader's own, wire
  that module in, run every Check, run on every push, next steps.
- Branch pages (`guides/go/compose.mdx`, `guides/go/playwright.mdx`) exist only
  for a section that is conditional on project shape. A branch opens at a named
  trunk step and rejoins the trunk. Do not split a linear journey across pages.
- Set `pagination_next: null` in frontmatter so prev/next does not imply an
  order between guides.
- Module code lives in `guides/<platform>/snippets/<module-name>/` and is
  embedded with a code-import fence, never pasted inline:

  ````markdown
  ```dang file=./snippets/test-services/main.dang
  ```
  ````

- Register the page in `docs/sidebars.ts` under the "Guides" category as
  `{ type: "doc", id: "guides/<platform>/index" }` with no `label`, so the
  sidebar inherits the page title. Add a row to `guides/index.mdx`.

## How a guide is written

**For the reader's own project, not an example project.** No lab repository,
no sample app to clone, no "we will build a greetings API". The guide assumes
any project of that type and states the conventions the reader's code should
follow, such as reading a service address from an environment variable and
skipping when it is unset. Test code and application code never appear.

**The only code is Dagger module code, in Dang.** Everything else is dagger
commands, `dagger.toml`, and prose explaining how the pieces fit together. The
module the reader writes gets a name a real project would use (`test-services`,
not `myapp`) and stays deliberately minimal. Say why it is minimal.

**The official module does the heavy lifting.** Lean on the platform module and
its settings first. The reader's own module exists only to supply what the
official module cannot know about the project, and module wiring connects the
two. State that division plainly and frame it as how the pieces fit, never as a
limitation ("the Go module cannot start services" reads as unsupported; "the Go
module needs to be given a test runtime with those services configured, which
is what `base` is for" reads as design).

**Focused on the platform, not on the CLI.** It is not a tutorial for
`dagger module settings` and it does not teach verbosity flags. Show each
command once, where the reader needs it, and trust the TUI and the report for
the rest. Link to reference pages for the full command surface.

**Prose follows `docs/STYLE_GUIDE.md` and the `docs-writing-style` skill.**
Verb-first title ("Daggerize a Go Project"), "you" is allowed because a
guide is a companion journey, one idea per sentence, code identifiers only when the reader has to
go there. The writing-style skill covers voice, scope, naming, and accuracy.

## What review will check

Every guide in this section has been reviewed by reading the official module's
source and diffing the guide against it. Write to that standard:

- **Verify every claim against the module source.** Clone the module (for
  example `github.com/dagger/go`) and read its `.dang` files and helpers before
  describing a setting. Constraints the reader would otherwise discover only by
  reading that source belong in the guide: settings that are mutually exclusive,
  workflows a setting does not affect (lint runs in its own pinned image),
  what is mounted by default, whether a version is a floor or a requirement.
- **Config examples are complete.** The reader should be able to diff their
  `dagger.toml` against the guide. Include every table the tooling writes,
  including ones the guide never asked the reader to add, such as the
  `[sdks.<name>]` scope tables written by `dagger module init`.
- **Output shown is output printed.** Every command output block comes from a
  real run against the current CLI. Never tell the reader to look for something
  the report omits (a zero `skipped` count is not printed).
- **Side effects get a sentence.** Files the tooling touches, such as
  `dagger.lock` keeping stale image pins, are explained where the reader first
  meets them so the first surprise is self-explanatory.
- **Sentences read the way an engineer would say them.** Rename anything
  generic, cut any paragraph that hedges, and reword any sentence that sounds
  like a caveat when it is really an explanation of how the pieces fit.
- **Use the canonical, current command forms and module refs.** As of Sep 2026:
  `dagger module install dagger.io/<module>`, `dagger module install
  dagger.io/sdk/<sdk>`, `dagger module init <sdk> --name <name>`,
  `dagger module settings <module> [key] [value]` (`-u` unsets),
  `dagger check [-l]`, `dagger generate`, `dagger up -l`, `dagger api call`.
  Confirm against `docs/current_docs/reference/cli/index.mdx` after merging
  main, since the CLI surface moves.

## Verification workflow

Do this before sending a guide for review, and again after every merge of main.

1. **Scaffold a throwaway project** of the target type in the scratchpad, with
   a test that skips when its service variable is unset. `git init` it.
2. **Get a CLI that matches the docs.** The docs describe upstream main and the
   next beta. The brew CLI and older betas reject newer refs and commands.
   Options, in order of preference:
   - The latest published beta: `DAGGER_X_RELEASE=v1.0.0-beta.N dagger ...`
   - A dev build of the checkout: `./hack/build`, then
     `./hack/with-dev dagger ...` from any directory. Only main commits with
     published archives work with `--x-release <sha>`; a dev build is the
     fallback when the docs are ahead of every published build.
3. **Run the guide top to bottom** in the scratch project, exactly as written,
   and paste real output into the output blocks: `dagger check -l`, the settings
   table, the final `dagger check`, the resulting `dagger.toml`.
4. **Exercise the failure paths the guide mentions**: set the mutually exclusive
   settings together, unset the wiring and confirm the skips return, change a
   version and inspect `dagger.lock`.
5. **Run the docs checks** from the repo root with the pin hack/build uses:

   ```bash
   X=$(grep -o 'X_RELEASE=[^ ]*' hack/build | sed 's/X_RELEASE=\${DAGGER_X_RELEASE:-//; s/}//')
   DAGGER_X_RELEASE=$X dagger check markdown-lint:lint
   DAGGER_X_RELEASE=$X dagger check docs:check   # full site build, catches broken links
   ```

6. **After merging main**, grep the guide for moved pages and renamed commands,
   and rerun steps 3 and 5. Upstream moves reference pages (for example
   `reference/modules/playwright.mdx` to `reference/modules/js/`) and renames
   commands without touching guides.

## Section skeleton

Use these H2s for a trunk page, adapting names to the platform:

1. `How the pieces fit together`: the official module, its settings, the
   reader's small module, module wiring. One bullet each. Say which projects
   need only the first two.
2. `Install the <platform> module`: install command, `dagger check -l`, the
   first `dagger check`, what each Check does, how a skipped test appears.
3. `Configure the <platform> module`: settings table, one `settings` command,
   the settings that come up most often with their constraints, a complete
   `[modules.<name>.settings]` example.
4. `Run generators`: only if the module has a generator.
5. `Give the tests the services they need` (or the platform's equivalent
   extension point): the one-line reason the official module needs to be given
   something, the conventions on the test side, `### Create a module for
   <purpose>`, `### Wire it into the <platform> module` with the full
   `dagger.toml`, and a proof step that removes the wiring.
6. `Run every Check`: real output, then the commit step naming `dagger.toml`,
   `dagger.lock`, and `.dagger`, with a sentence on what the lock file holds.
7. `Run it on every push`: Cloud Checks and existing CI, by link.
8. `Next steps`: reference pages and sibling branch guides.

## Checklist before opening the PR

- [ ] No example project, no application or test code, only Dang module code
- [ ] Every setting described was checked against the module source
- [ ] Every output block was pasted from a real run with the current CLI
- [ ] The final `dagger.toml` is complete, including SDK scope tables
- [ ] Commands use the current `dagger module ...` forms and `dagger.io/` refs
- [ ] Sidebar entry has no `label`; `guides/index.mdx` has a row
- [ ] `markdown-lint:lint` and `docs:check` pass
- [ ] Reference pages for the platform link to the guide, and their narrative
      paragraphs were trimmed toward pure reference
