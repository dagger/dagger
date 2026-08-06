# Rust SDK contributor guidance

## Mission and scope

Elevate the Dagger Rust SDK to stable, Go-level capability while making its public API
feel native to Rust. These instructions apply to `sdk/rust/**`. Repository-wide
instructions and [the shared SDK contribution guide](../CONTRIBUTING.md) also apply;
when guidance differs, follow the more specific rule without weakening a
repository-wide requirement.

Read [CONTRIBUTING.md](CONTRIBUTING.md) for the human-facing workflow and
[ARCHITECTURE.md](ARCHITECTURE.md) before changing component boundaries.

## Sources of truth

Use the executable completeness contract's peer-authority model:

- The Dagger engine schema and protocol define the public wire surface.
- Target-compatible `sdk-sdk` checks define common lifecycle behaviour only within
  their explicit check, target, and platform scope.
- The pinned Go SDK and tests define behaviour outside the harness scope and remain
  reference evidence where peer scopes overlap.
- Rust policy defines ownership, lifetimes, naming, error handling, safety, and
  ergonomics. Cross-SDK consistency does not justify an unidiomatic Rust API.
- Existing Rust code and historical proposals, including pull request #12229, are
  evidence. Let them inform the design, not direct it.

No authority has blanket precedence. Preserve compatible overlap and fail or document a
genuine incompatibility instead of silently selecting one source. The reviewed selections,
classifications, and evidence rules live in [completeness/README.md](completeness/README.md).

## Architecture boundaries

- `dagger-sdk` owns the public client, session lifecycle, query construction, and
  user-facing errors.
- `dagger-codegen` owns schema-to-Rust translation and generated API shape.
- `dagger-bootstrap` is orchestration for code generation, not a second SDK surface.
- `crates/dagger-sdk/src/gen.rs` is generated. Never edit it directly; modify the
  generator or templates and regenerate it.
- Keep generated types thin. Handwritten behaviour belongs outside `gen.rs` unless it
  must be emitted consistently for every generated type.
- Module support should reuse the SDK's transport and generated model rather than
  creating a parallel client stack.

## Rust standards

- Rust edition 2024 is the current language contract.
- The development toolchain is pinned in `rust-toolchain.toml`; the workspace MSRV is
  declared in `Cargo.toml`. Both currently target Rust 1.97.1. Do not change either as
  a side effect of unrelated work.
- Run stable `rustfmt` and Clippy from the pinned toolchain.
- Prefer typed public errors. Context-rich general errors are acceptable in internal
  build and code-generation tooling.
- Avoid `unwrap` and `panic` in library paths. When an invariant makes failure
  impossible, use `expect` with a message explaining that invariant.
- Comments explain why a decision is necessary, not what the next line does.
- Prefer explicit ownership and borrowing over pervasive cloning or shared `Arc`
  state. Shared ownership must represent an actual lifecycle requirement.
- New dependencies are design decisions. Reuse the standard library or an existing
  dependency where that remains clear and maintainable.
- Avoid sleeps in tests; synchronize on the condition the test needs to observe.
- Do not interrupt Rust compilation merely because it is quiet. Allow at least five
  minutes before treating a build or test as stalled.

## Code documentation

The Rust SDK mirrors contracts owned by the engine schema, the scoped official SDK
conformance harness, and the definitive Go implementation. The reasoning behind a
non-obvious mapping or invariant must survive in the source; requiring the next human
or agent to reconstruct it from those authorities is an avoidable correctness risk.

- Give every module a `//!` introduction that explains what it owns, its architectural
  boundary, and the invariants a future editor must preserve.
- Document public items with `///` when callers need guarantees, validation rules,
  failure behaviour, evidence scope, or lifecycle assumptions that the type signature
  cannot express.
- Add inline comments for correctness-critical decisions: canonical ordering and
  hashing domains, target/evidence containment, precedence, ownership, retry or
  idempotency assumptions, concurrency boundaries, and deliberate trade-offs. Explain
  why the decision is necessary, not what the following line does.
- Cite the pinned schema, harness check, Go symbol/test, or approved specification when
  behaviour is ground-truthed and the anchor prevents accidental reinterpretation.
- Keep obvious control flow, signature paraphrases, and ceremonial section comments out
  of the source. Property tests carry one invariant sentence and the required
  `// Feature: <name>, Property N: <text>` tag.

Missing module context or missing explanation for non-obvious correctness logic is a
defect, even when formatting, compilation, and tests pass.

## Security and dependency posture

- Unsafe Rust is denied at workspace level. A future exception requires the narrowest
  possible `#[allow(unsafe_code)]`, a `// SAFETY:` justification at every unsafe block,
  and tests that exercise the boundary.
- Run all Cargo operations with `--locked` when a lockfile is present. Dependency
  resolution changes are deliberate reviewable changes, never a CI side effect.
- `cargo deny check` must pass. Active advisories, unapproved licenses, wildcard
  dependencies, unknown registries, and unknown Git sources fail the check.
- Any advisory exception must name the advisory, explain why it is unreachable, link
  the upstream remediation, and be removed as soon as a fixed dependency is available.
- New Git dependencies require a pinned revision and explicit source-policy review.
- Never expose session tokens, registry credentials, environment secrets, or sensitive
  host paths in errors, tracing, fixtures, snapshots, or generated code.

## Generated code and parity

Regenerate with `dagger generate -y` from the repository root. Inspect generated output
and keep generator changes together with their expected output.

For work represented in the Go SDK:

- locate the corresponding Go implementation and tests before designing the Rust API;
- port behavioural coverage and edge cases, not Go syntax or ownership patterns;
- add code-generator tests for generated shape and engine-backed integration tests for
  observable behaviour;
- record intentional behavioural differences explicitly in code or pull-request
  rationale.

## Verification

Run from `sdk/rust` before pushing:

```console
cargo fmt --all --check
cargo check --workspace --all-features --locked
cargo test --workspace --all-features --locked
cargo clippy --workspace --all-targets --all-features --locked -- -D warnings
RUSTDOCFLAGS="-D warnings" cargo doc --workspace --all-features --no-deps --locked
cargo deny check
```

Also run `dagger generate -y` and the relevant repository Dagger checks when the change
touches code generation, engine integration, examples, or release automation. State
every command actually run in the pull request and explain any omission.

## Worktree and Git safety

- Treat pre-existing changes as user work. Do not discard, overwrite, or include them
  without explicit authorization.
- Never use destructive restoration commands such as `git reset --hard`, `git clean
  -f`, or broad `git checkout`/`git restore` operations without approval of the exact
  command and target.
- Keep commits coherent. Do not mix dependency movement, generated churn, formatting,
  or unrelated cleanup into feature work.
- Follow the root contribution guide for Changie fragments, licensing, commit quality,
  and DCO sign-off.

## Commit attribution — recognise agent work (required)

Kiro, Claude Code, and Codex may do a substantial share of work in this repository, and
that contribution is recognised in the history—never flattened into a lone human
author. The human operator stays the Git `author`; agents are credited with required
commit trailers:

- **`Co-authored-by: <Agent> <email>`** — one line for every agent that authored part
  of the change, including code, documentation, tests, or specifications.
- **`Assisted-by: <Agent> <email>`** — one line for every agent that assisted without
  primary authorship, including review, verification, research, or pairing.

Both trailers are required whenever their role applies. Credit every participating
agent. A commit with genuine agent involvement and no corresponding attribution trailer
is incomplete.

Canonical identities:

- `Kiro <kiro@kiro.dev>`
- `Claude <noreply@anthropic.com>`
- `Codex <codex@openai.com>`

Trailers go at the end of the message after a blank line. Dagger separately requires a
human DCO sign-off; an agent must never receive a `Signed-off-by` trailer.

```text
feat(sdk/rust): add module entrypoint discovery

<why-this-design-is-correct body>

Co-authored-by: Codex <codex@openai.com>
Signed-off-by: Its Me <its.me@workingwith.ai>
```

Pull-request descriptions must name participating agents and distinguish authorship
from assistance. Do not include model names or task transcripts unless they materially
help review.

## Definition of done

A change is complete when its public design is idiomatic Rust, relevant Go behaviour is
accounted for, generated output is current, focused and regression tests pass, required
documentation and release notes are present, and agent attribution plus human DCO
sign-off are recorded.
