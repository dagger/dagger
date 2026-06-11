---
name: dagger-pr-review
description: |
  Perform thorough code review of pull requests in the dagger/dagger repository.
  Use whenever the user asks to review a PR, a branch, a diff, or specific commits in
  this repo — including casual phrasings like "look at this PR", "what do you think of
  #1234", "review my branch", or "check this change before I merge". Covers design
  review, bug hunting, code style, and the repo's contribution checklist (codegen,
  changie, DCO, tests). Produces a structured report plus draft review comments ready
  to post with `gh`.
---

# Dagger PR Review

Review PRs in dagger/dagger with three goals, in priority order:

1. **Design** — does this change fit Dagger's architecture, API conventions, and long-term direction?
2. **Correctness** — bugs, edge cases, races, error handling, behavior regressions.
3. **Style** — consistency with the codebase. The maintainer cares a lot about style; do not hand-wave it as "nitpicking". Style findings are first-class, just labeled at lower severity.

The review must be evidence-based. Every finding cites a file and line from the diff. Never report a problem you have not verified by reading the actual code — if you suspect an issue but can't confirm it from the available context, phrase it as a question, not a finding.

## Step 1 — Gather context

Identify what you're reviewing:

**Given a PR number or URL** (requires `gh`):

```bash
gh pr view <num> --repo dagger/dagger --json title,body,author,baseRefName,headRefName,files,labels
gh pr diff <num> --repo dagger/dagger
# Linked issues and discussion give design intent:
gh pr view <num> --repo dagger/dagger --json body --jq .body   # look for "Fixes #..."
gh api repos/dagger/dagger/pulls/<num>/comments --paginate     # existing review comments — don't repeat them
```

**Given a local branch**:

```bash
git log --oneline upstream/main..HEAD    # or origin/main
git diff upstream/main...HEAD --stat
git diff upstream/main...HEAD
```

For either mode, also read the *surrounding code*, not just the diff. A diff only shows what changed; bugs and design problems usually live in how the change interacts with code that didn't change. For each non-trivial hunk, open the full file (locally or via `gh api repos/dagger/dagger/contents/<path>?ref=<sha>`) and read the enclosing function/type. Trace callers of modified functions when the signature or behavior changed (`grep -rn` in a local checkout is fine).

## Step 2 — Triage by area and load the right reference

Map the changed paths to areas, and read the matching reference file(s) in `references/` before reviewing those hunks:

| Paths | Area | Reference |
|---|---|---|
| `engine/`, `core/`, `dagql/`, `cmd/engine`, `auth/`, `network/` | Engine (Go) | `references/go.md` |
| `cmd/codegen/`, `**/dagger.gen.go`, codegen templates | Codegen | `references/go.md` + repo skill `skills/dagger-codegen` |
| `sdk/typescript/` | TypeScript SDK | `references/typescript.md` |
| `sdk/go/`, `sdk/python/`, `sdk/elixir/`, etc. | Other SDKs | `references/go.md` for Go; general lenses otherwise |
| `docs/` | Docs | general lenses; check examples actually compile/run |
| `.changes/`, `CHANGELOG` | Release notes | checklist section below |

If the repo checkout includes `skills/` (cache-expert, dagger-codegen, engine-dev-testing), consult the relevant one for domain knowledge — e.g. read `skills/cache-expert/SKILL.md` before reviewing anything that touches cache keys or DAG operations.

## Step 3 — Review with three lenses

### Design lens

Before judging line-level code, articulate (to yourself) what the PR is trying to do and whether this is the right shape for it:

- Does the change belong at this layer? (engine vs codegen vs SDK vs CLI — a common smell is fixing in one SDK what should be fixed in codegen or the engine so all SDKs benefit)
- Does it follow the immutable-DAG model? Operations take immutable inputs and produce immutable outputs; "mutation" is a new node. Anything that introduces hidden mutable state or non-determinism threatens caching.
- API surface changes: is naming consistent with existing API conventions? Is it additive or breaking? Breaking changes need a `Breaking` changie entry and strong justification.
- Cross-SDK consistency: a feature added to one SDK should usually have a plan for the others (or live in codegen).
- Is there a simpler design that achieves the same? Flag complexity that isn't paying for itself.
- Scope: does the PR mix unrelated changes that should be split?

### Correctness lens

Hunt for bugs deliberately rather than waiting for them to jump out. For each hunk ask: what inputs/states make this go wrong?

- Error paths: swallowed errors, errors that lose context, cleanup not running on failure, partial state on early return.
- Concurrency: shared state without synchronization, goroutine/promise lifetimes, context cancellation propagation, channel deadlocks.
- Edge cases: empty/nil inputs, zero values, unicode/path edge cases (Windows paths come up in SDKs), very large inputs.
- Behavior changes hidden as refactors: does any caller depend on the old behavior? Check callers.
- Cache correctness: does the change alter inputs to an operation without altering its cache key (stale results) or add nondeterminism to a cached operation?
- Tests: do new tests actually exercise the fix (would they fail on main)? Is a bugfix missing a regression test? Are integration tests added in the right harness (`core/integration`, SDK test suites)?

### Style lens

General rules for all languages (language-specific rules live in the reference files):

- Match the surrounding file's conventions even when multiple styles would be acceptable in isolation.
- Names: precise, consistent with neighboring code, no abbreviations the codebase doesn't already use.
- Comments explain *why*, not *what*; stale comments contradicting the code are findings.
- No dead code, commented-out code, or leftover debug logging.
- Generated files (`dagger.gen.go`, `client.gen.ts`) are reviewed only for "was the generator run and committed?" — never style-review generated content, but DO verify the hand-written template/codegen change that produced it.

## Step 4 — Repo contribution checklist

Verify the mechanical requirements from CONTRIBUTING.md; report any miss as a finding:

- **Generated files**: if the PR touches API definitions, codegen, or GraphQL schema, `dagger generate` output must be committed (look for matching `*.gen.*` changes; mismatched or missing regeneration is a common failure).
- **Changie**: user-facing changes need a `.changes/unreleased/*.yaml` entry with the right kind (Breaking/Added/Changed/Deprecated/Removed/Fixed/Experimental/Security/Dependencies). Internal refactors don't.
- **DCO**: every commit has `Signed-off-by` matching the author.
- **Commit messages**: imperative mood, concise subject, body explains why (per chris.beams.io guidance referenced in CONTRIBUTING.md).
- **Lint**: would `dagger checks *:lint` pass? Apply the lint rules from the reference files mentally; flag obvious violations.
- **Docs**: user-visible behavior or API changes should update `docs/` when relevant.

## Step 5 — Produce the output

Always produce **both** of the following.

### A. Review report (in chat)

```
# PR Review: <title> (#<num>)

**Summary**: 2-4 sentences — what the PR does and your overall assessment.
**Recommendation**: Approve / Approve with nits / Request changes — and why in one line.

## Design
<findings or "No design concerns.">

## Bugs & correctness
<findings>

## Style
<findings>

## Checklist
<only the items that failed or need attention; say "Checklist clean" otherwise>
```

Each finding: `**[severity]** path/to/file.go:123 — description`, with a short code excerpt when it aids understanding and a concrete suggested fix whenever you have one. Severities:

- `blocker` — must fix before merge (bug, breaking change without justification, security)
- `major` — should fix; design or correctness concern with real impact
- `minor` — style, naming, small improvements
- `question` — needs the author's input; you couldn't verify

Order findings by severity, not file order. If the PR is good, say so plainly — do not invent findings to look thorough. A review with zero findings and a clear "why I'm confident" is a valid output.

### B. Draft review comments (ready to post)

For each finding worth an inline comment, emit a ready-to-run `gh` command block the user can execute after editing:

```bash
gh pr review <num> --repo dagger/dagger --comment --body "..."        # top-level
# Inline comments via the API:
gh api repos/dagger/dagger/pulls/<num>/comments \
  -f body="<comment text>" \
  -f commit_id="<head sha>" \
  -f path="core/foo.go" \
  -F line=123 -f side=RIGHT
```

Comment text style: direct, specific, kind. Lead with the issue, then the suggestion. Use GitHub suggestion blocks (```suggestion fenced blocks) for small concrete fixes. Phrase `question` items as genuine questions. Never post anything yourself — drafting only; the user posts.

For local-branch reviews where there's no PR yet, skip the `gh` commands and present the inline comments as `path:line — text` so they can be pasted later.

## Calibration

- Don't pad: 3 real findings beat 15 padded ones. Skip anything Prettier/eslint/golangci would auto-catch unless it indicates the author didn't run the linters at all (then report that once, as a checklist finding).
- Large PRs (>1000 lines changed): review commit-by-commit if commits are well-structured, and say explicitly which parts got lighter coverage.
- When you disagree with a design but it's defensible, present the trade-off and your preference rather than a demand.
