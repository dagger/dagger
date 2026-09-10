---
name: docs-writing-style
description: Writing style for Dagger documentation prose in docs/current_docs. Use when writing, rewriting, or reviewing any docs page (guides, reference, config, getting started) to get the voice, scope, naming, and accuracy right. Complements docs/STYLE_GUIDE.md, which covers mechanics like titles, pronouns, lists, and snippets.
---

# Docs Writing Style

How Dagger docs prose should read. `docs/STYLE_GUIDE.md` covers the mechanics:
Word Case titles, sentence-case headings, no personal pronouns outside
guides (the quickstart and `guides/` are companion journeys and may use
"you"), list and bold rules, snippet layout. Follow it. This skill
covers what the style guide does not: voice, scope, naming, and accuracy.

## Voice

**Write it the way an engineer would say it.** Read each sentence aloud. If it
would sound odd said across a desk, rewrite it. The most common failure is a
sentence that is technically true but leads with the wrong thing.

**Explain how the pieces fit. Do not present design as limitation.** A sentence
that opens with what a tool cannot do reads as "unsupported" even when the next
sentence explains the supported path. Lead with what the reader needs to
provide and why.

| Reads as a limitation | Reads as design |
| --- | --- |
| The Go module cannot start a database. It does not know the project has one. The way to close that gap is to hand it a base container. | End-to-end tests often need a database running, so the Go module needs to be given a test runtime that already has those services configured. That is what its `base` setting is for. |

**One idea per sentence.** A sentence with two clauses joined by "and" or a
semicolon is usually two sentences. Short does not mean clipped: a sentence
beats a label with a colon.

**No hedging and no throat-clearing.** Cut "note that" and "it is worth
mentioning". State the fact.

## Patterns that read as machine-written

These show up in AI-drafted prose and reviewers spot them immediately. Search
for each before sending a page.

- **Balanced triads and tidy parallel clauses.** Three parallel items in every
  list, a "not X but Y" or "A, B, and C" cadence in every paragraph. Real
  explanations have as many items as the subject has. If a list has three
  entries, check whether the third was added for rhythm.
- **Meta-commentary that narrates significance instead of stating a fact.**
  "This is worth spelling out", "and it matters", "importantly", "the key
  insight is". Delete the narration and keep the fact. If the fact does not
  stand on its own, it was not worth the sentence.
- **Closing lines that assert nothing checkable.** "Your data exists to power
  your experience, nothing else." "This is how Dagger is meant to be used." A
  paragraph ends when its last fact is stated. Do not append a flourish.
- **Marketing intensifiers.** Seamless, effortless, powerful, robust,
  delightful, elegant, simple. Say what the thing does. If it is fast, give
  the number in a table or leave it out.
- **Sentences opening with "Simply" or "Just".** They tell the reader a step is
  easy instead of showing the step. Start with the verb.

## Scope

**A page is about its subject, not about the tools it uses.** A guide to
daggerizing a Go project is not a guide to `dagger module settings`. Show a
command once, where the reader needs it, in the form they will type. Do not
enumerate its other forms, flags, or edge cases. Link to the reference page for
the full surface.

**Do not teach the reader how to read output.** Skip verbosity flags and
TUI-navigation advice. A person at the terminal will drill into the TUI on their
own, and an agent gets the report. Show the summary the command prints and move
on.

**Say only what the page's reader needs at that step.** A sentence that is true
but that no reader of this page would act on belongs on a different page or
nowhere. "List-valued settings are edited in `dagger.toml` directly" was cut
from the Go guide for this reason: it was also inaccurate, and even the accurate
version was a detail about the settings command, not about the Go project.

**Reference pages are reference.** A page under `reference/` says what a thing
is, what it exposes, and what its settings mean. Narrative ("first do this, then
that, here is why") belongs in a guide, and the reference page links to it. When
a guide starts owning a story, trim the matching reference page toward pure
reference.

## Naming

**Use names a real project would use.** A module in an example is
`test-services`, not `myapp` or `example`. A function is `goTestBase`, not
`myFunction`. The name should read naturally inside the commands and config the
reader will type, such as `test-services:go-test-base`.

**Name things by purpose, not by ownership.** "A module for test services" says
what it is. "A module for the project" says only whose it is.

**Sidebar labels match page titles.** Do not set a `label` in `sidebars.ts`
that abbreviates the title. "Go" under "Guides" is too broad; the page is
"Daggerize a Go Project". Omit the label so the sidebar inherits the title.

**Use current, canonical command forms and refs.** Official modules install
from `dagger.io/<module>` and SDKs from `dagger.io/sdk/<sdk>`. Module verbs live
under `dagger module` (`install`, `init <sdk> --name`, `settings`). Confirm
against `docs/current_docs/reference/cli/index.mdx` after every merge of main.

## Accuracy

**Describe only what is printed.** Every output block comes from a real run
with the current CLI. Never point the reader at something the report omits,
such as a `skipped` count that is only printed when nonzero.

**State constraints where the reader meets them.** If two settings are mutually
exclusive, say so in the section that introduces the second one, and say what
happens when both are set. If a value is a floor rather than a requirement, say
"1.26 or newer". If a workflow is unaffected by a setting (lint runs in its own
pinned image), say so next to the setting.

**Examples are complete.** A config example the reader is meant to compare
against their own file must include everything the tooling wrote, including
tables the page never asked them to add. A partial example makes the reader
wonder whether their extra tables are a mistake.

**Side effects get a sentence.** When a command touches a file the page has not
introduced (`dagger.lock` keeping pins for images no longer used), explain it
at that point so the first surprise is self-explanatory.

**Verify against source, not memory.** Before describing a module's setting,
read the module's source. Before describing CLI behavior, run it. Docs written
from recollection of how something used to work are the main source of drift.

**Aspirational only by decision.** Docs describe the 1.0 target, which may be
ahead of the released tooling. That is fine when the maintainer has decided it
(for example describing `dagger.io/` refs before the beta that resolves them
ships). It is not fine to guess. Say in the PR what is ahead of the release.

## Rewrites seen in review

| Before | After | Why |
| --- | --- | --- |
| "To see individual tests, add verbosity: `dagger check go:test-all -vvv`" | (removed) | Teaching output navigation, not the subject |
| "Look at the summary line for skipped tests. If that count is not zero..." | "The summary adds a `skipped` count next to `passed` whenever a test skipped. If that count appears..." | Zero is not printed |
| "Create a module for the project" | "Create a module for test services" | Purpose, not ownership |
| `myapp` / `myapp:go-test-base` | `test-services` / `test-services:go-test-base` | A name someone would use |
| Sidebar label "Go" | (no label; inherits "Daggerize a Go Project") | Label too broad |
| "The image matches the Go module's default... helper binaries need Go 1.26" | "Use the same release you would otherwise put in `version`. Keep it at Go 1.26 or newer" | Floor versus requirement was ambiguous |

## Process

- Run `dagger check markdown-lint:lint` and `dagger check docs:check` (the
  full site build, which catches broken links) with the pin from `hack/build`:
  `DAGGER_X_RELEASE=$(grep -o 'X_RELEASE=[^ ]*' hack/build | sed 's/X_RELEASE=\${DAGGER_X_RELEASE:-//; s/}//')`.
- After merging main, grep the edited pages for moved links and renamed
  commands. Upstream moves reference pages and renames CLI verbs without
  touching every page that mentions them.
- Commit only when asked, with DCO signoff (`git commit -s`).
