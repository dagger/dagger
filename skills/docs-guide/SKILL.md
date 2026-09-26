---
name: docs-guide
description: Author or review a platform guide in docs/current_docs/getting-started/platform-guides ("Daggerize a Go Project", Go + Compose, TypeScript + Playwright, and similar). Use when asked to write, extend, or review a guide that takes a whole project type from installing an official module to every Check passing in a runtime the project controls.
---

# Docs Guides

How to write a guide under `docs/current_docs/getting-started/platform-guides/`.
The exemplar is `getting-started/platform-guides/go/index.mdx`, the first guide
of this kind. Read it
before writing a new one and match its shape, keeping the sections that apply
to what the platform's official module exposes and dropping the ones that do
not. This skill describes the framework; the Go guide is the worked instance of
every part of it, and the section skeleton below names the Go section that
shows each kind.

## What a guide is for

A guide answers one question: "I have a project of type X. How do I daggerize
it, all the way up?" It is the end-to-end path for one platform, from installing
the official module to every Check passing, including the ones that need a
runtime or services of the project's own.

Platform guides are the last part of Getting Started and continue from the
Quickstart:

- The Quickstart gets any project to its first passing Checks: `dagger init`,
  the recommended modules, settings, generators, `dagger check`.
- A platform guide picks up from there for one kind of project and goes as far
  as that platform needs.
- Reference pages describe one module, command, or config file. A guide links
  to them for the full surface so the reader never has to assemble the journey
  themselves.

Once a guide owns the narrative for a platform, the matching pages under
`reference/modules/` become pure reference. Each guide directory is shaped so it
can ship as a distributable skill: `index.mdx` is the entry document, branch
pages and snippets are its resources.

## Layout

- One directory per platform: `getting-started/platform-guides/<platform>/`.
- `index.mdx` is the trunk, a linear progression: install the module, configure
  it, add any companion module the platform splits out, run generators, extend
  it with a small module of the reader's own, wire that module in, run every
  Check, next steps. That order is both the order to
  daggerize in and increasing complexity, so most readers finish early. Say so
  in the intro and in "What this guide covers": name the point where a
  project with plain tests is done, and tell the reader to stop there.
- Branch pages (`go/compose.mdx`, `go/playwright.mdx`) exist only
  for a section that is conditional on project shape. A branch opens at a named
  trunk step and rejoins the trunk. Do not split a linear journey across pages.
- Set `pagination_next: null` in frontmatter so prev/next does not imply an
  order between guides.
- Module code lives in `<platform>/snippets/<module-name>/` and is
  embedded with a code-import fence, never pasted inline:

  ````markdown
  ```dang file=./snippets/<module-name>/main.dang
  ```
  ````

- Register the page in `docs/sidebars.ts` in the "Platform Guides" category,
  the last item of "Getting Started", as
  `{ type: "doc", id: "getting-started/platform-guides/<platform>/index" }`
  with no `label`, so the sidebar inherits the page title. Add a row to
  `getting-started/platform-guides/index.mdx`.

## How a guide is written

**Continues from the Quickstart.** The prerequisite is the Quickstart, not
Install, and the reader is assumed to have set up Cloud Checks after it, so a
guide has no "run it on every push" or CI section. The reader already has a
`dagger.toml`, and `dagger module recommend` may have installed some of the
guide's modules. Keep each install command, and say what the reader sees when
the module is already there: Dagger reports `Module "<name>" is already
installed`, exits cleanly, and changes nothing. Do not re-teach what the
Quickstart covered. Go: the prerequisite paragraph and "Install the Dagger
module for Go".

**Open with what the guide covers, not how it works.** A reader at the top does
not know what they are getting into yet. Give a short numbered list of the
steps, one line each, and say which steps most projects need. Put the detail
about each piece (what the official module discovers, what wiring does, why the
reader's module is small) in the section that introduces that piece. Go: "What
this guide covers".

**Name the official module for someone who knows the platform but not Dagger.**
"The Go module" means a `go.mod` to a Go user. Introduce it as "the Dagger
module for <platform>", then define the name it is installed under, in code
font, as the shorthand for the rest of the page (the `go` module), and keep the
platform's own term for the platform's own thing.

**For the reader's own project, not an example project.** No lab repository,
no sample app to clone, no "we will build a greetings API". The guide assumes
any project of that type and states the conventions the reader's code should
follow, such as reading a service address from an environment variable and
skipping when it is unset. Test code and application code never appear.

**The only code is Dagger module code, in Dang.** Everything else is dagger
commands, `dagger.toml`, and prose explaining how the pieces fit together. The
one exception is a platform-native directive or config line when the section
is about that line: show it on its own, never the code around it. The module
the reader writes is the project's dev module. `dagger module init <sdk>` with
no name creates `<project>-dev` as the workspace entrypoint, and a reader who
already has a dev module adds to it, so write `<project>-dev` as the placeholder
in commands and config. Say that the snippet's type name stands in for the one
`dagger module init` generated, since a mismatched type fails to load. Functions
get names that say what they supply to the official module (`testRuntime`) and
stay deliberately minimal. Say why they are minimal. When the guide extends that
module later, add a second snippet directory for the
extended version rather than editing the first one in prose.

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
  workflows a setting does not affect (a companion module that runs in its own
  image ignores the platform module's version), what is mounted by default,
  whether a version is a floor or a requirement, and any convention the module
  reads from the project's source, such as a directive that declares inputs.
- **Config examples are complete.** The reader should be able to diff their
  `dagger.toml` against the guide. Include every table the tooling writes,
  including ones the guide never asked the reader to add, such as the
  `[sdks.<name>]` scope tables written by `dagger module init`.
- **Output shown is output printed.** Every command output block comes from a
  real run against the current CLI. Never tell the reader to look for something
  the report omits: zero counts are not printed, and a cached re-run prints no
  per-test section at all, so capture test output on a cache miss.
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
  `dagger check [-l]`, `dagger generate`, `dagger start -l`, `dagger api call`.
  Confirm against `docs/current_docs/reference/cli/index.mdx` after merging
  main, since the CLI surface moves.

## Verification workflow

Do this before sending a guide for review, and again after every merge of main.

1. **Scaffold a throwaway project** of the target type in the scratchpad that
   follows every convention the guide asks of the reader's code, such as a test
   that skips when its service variable is unset. `git init` it.
2. **Use a CLI that matches the docs.** The docs describe upstream main and the
   next beta, and older CLIs reject newer refs and commands. Run `dagger` as it
   is on your PATH. Maintainers pin the release they want with
   `DAGGER_X_RELEASE` in their own shell, so never set or override it in a
   command. When the docs are ahead of every published build, use a dev build
   of the checkout: `./hack/build`, then `./hack/with-dev dagger ...` from any
   directory.
3. **Run the guide top to bottom** in the scratch project, exactly as written,
   and paste real output into the output blocks: `dagger check -l`, the settings
   table, the final `dagger check`, the resulting `dagger.toml`. Headless runs
   need `-y` on `dagger module init` and any other command that returns a
   changeset. The settings table truncates to the terminal width, so capture
   it under a wide pseudo-terminal.
4. **Exercise every failure path the guide mentions**, so its failure text is
   real: settings it calls mutually exclusive, a type name it says must match,
   a directive or setting it says is required. Confirm each fails the
   way the guide says.
5. **Run the docs checks** from the repo root:

   ```bash
   dagger check markdown-lint:lint
   dagger check docs:check   # full site build, catches broken links
   ```

6. **After merging main**, grep the guide for moved pages and renamed commands,
   and rerun steps 3 and 5. Upstream moves reference pages (for example
   `reference/modules/playwright.mdx` to `reference/modules/js/`) and renames
   commands without touching guides.

## Section skeleton

Use these H2s for a trunk page, adapting names to the platform. Sections 4, 6,
and 7 are kinds, not fixed titles: include one section for each thing the
platform actually has, and none for things it does not. Each entry ends with
the Go guide section that shows it.

1. `What this guide covers`: a numbered list of the steps, one short line each,
   with no mechanism. Say which steps most projects need and that the reader
   stops when the Checks cover their project. Then the prerequisite paragraph
   linking the Quickstart. Go: "What this guide covers".
2. `Install the Dagger module for <platform>`: what the module is and the name
   it is installed under, the install command, what the reader sees if the
   Quickstart already installed it, `dagger check -l`, the first `dagger
   check`, what each Check does, and how a Check that passes but is incomplete
   appears (for example a skipped test). Go: "Install the Dagger module for
   Go".
3. `Configure the <name> module`: settings table, one `settings` command,
   the settings that come up most often with their constraints, including how
   to select which of the project's units each workflow covers and how to mount
   files the module does not find on its own, and a complete
   `[modules.<name>.settings]` example. Go: "Configure the `go` module".
4. One section per companion module the platform splits out (a linter, a
   formatter): install it, show the Check list gaining a Check, its settings,
   and how it differs from the platform module. Go: "Lint the project",
   which installs one of two linter modules and says how the other differs.
5. `Run generators`: only if the module has a generator. Go: "Run generators".
6. One section per convention the official module reads from the project's own
   source, such as a directive that declares extra inputs: what the module
   discovers on its own, the convention and its rules, the failure without it,
   and when to prefer it over a setting. Go: "Declare the files a test or
   generator reads", for `//go:test:include` and `//go:generate:include`.
7. One section per extension point the official module exposes through a
   wireable setting, in the order a reader needs them. For a runtime container:
   the one-line reason the module needs to be given one, `### Create a
   <purpose> module` with a function that starts from a published image and
   adds what the project plausibly needs, a command proving it, and `### Wire
   it into the <name> module` with the full `dagger.toml` and the sentence on
   what wiring does. For services:
   the conventions on the test side, extend the same module, and a short
   wiring step for readers who skipped the previous section. Go: "Provide the
   runtime the tests and generators run in" adds a `testRuntime` function to
   the project's dev module and wires it; "Give the tests the services they
   need" adds Postgres to that same module as a second snippet stage.
8. `Run every Check`: real output, then the commit step naming `dagger.toml`,
   `dagger.lock`, and `.dagger`, with a sentence on what the lock file holds.
   Go: "Run every Check".
9. `Next steps`: reference pages, companion module references, and sibling
   branch guides. Go: "Next steps".

## Checklist before opening the PR

- [ ] No example project, no application or test code, only Dang module code
      plus bare platform directive lines where a section is about that directive
- [ ] Every setting described was checked against the module source
- [ ] Every output block was pasted from a real run with the current CLI
- [ ] The final `dagger.toml` is complete, including SDK scope tables
- [ ] Commands use the current `dagger module ...` forms and `dagger.io/` refs
- [ ] Prerequisite is the Quickstart, and each install says what an
      already-installed module prints
- [ ] Sidebar entry has no `label`; `platform-guides/index.mdx` has a row
- [ ] `markdown-lint:lint` and `docs:check` pass
- [ ] Reference pages for the platform link to the guide, and their narrative
      paragraphs were trimmed toward pure reference
