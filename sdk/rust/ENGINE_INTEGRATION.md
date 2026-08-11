# Rust SDK engine integration

This document is the maintainer contract for building, exercising, and auditing the
built-in Rust SDK. It complements [ARCHITECTURE.md](ARCHITECTURE.md): that document
describes the public client, while this one describes the private engine-packaged
compiler, adapter, runtime image, and exact-target evidence workflow.

## Runtime build audit

The Rust build was reviewed against the definitive Go SDK runtime and representative
module-backed SDKs in preparation for the exact-target matrix. The comparison is about
observable build hygiene, not copying another language's implementation shape.

| Contract | Reviewed source | Rust decision |
| --- | --- | --- |
| Current and legacy generation are distinct | `core/sdk/go_sdk.go`, `sdk/python/runtime/main.go`, and `sdk/typescript/runtime/main.go` | A missing introspection file selects checked committed generation; a present file selects private legacy generation. Neither mode silently falls through to the other. |
| Dependency selection is lock-aware | Go's build path, Python's locked `uv sync`, and the TypeScript package-manager paths | Runtime verification requires `Cargo.lock`, checks it with `cargo metadata --locked`, and builds with `cargo build --locked`. The generated protocol binary also declares every crate it names directly. |
| Mutable inputs arrive late | Go's scoped module mount and Python's late source mount | Schema, project, and request inputs are mounted after the immutable tool and policy layers. Engine content and toolchain caches therefore do not depend on test-only or unrelated SDK source. |
| Build caches are not runtime state | Go removes module/build cache mounts; Java promotes one JAR into a fresh JRE image | Cargo registry, Git, and target caches exist only on the builder. The final image starts from a fresh digest-pinned distroless base and receives only the stripped binary and canonical provenance. |
| Credentials cannot become artifacts | Go brackets the build with SSH socket attachment/removal | Rust control JSON, provenance, cache keys, and generated files contain no credentials. Unsafe Cargo output is replaced by a bounded typed diagnostic, and no build socket, secret, Cargo home, or cache is copied into the runtime image. Ambient credential forwarding is deliberately not inferred from another SDK. |
| Runtime shape is explicit | Go and Java set a fixed entrypoint and workdir | Rust clears inherited default arguments, installs one fixed entrypoint, and uses `/scratch`, matching the engine's `core.RuntimeWorkdirPath`. |
| Platform and executable are engine-owned | Go selects its fixed runtime output; Java promotes the resolved application JAR | Rust selects only the engine platform's reviewed target triple and the manifest-owned `dagger-module` binary. Callers cannot supply a command, target directory, executable, or workdir. |

`toolchains/rust-sdk-dev/internal/enginefree` keeps the source-level audit executable.
If one of the reviewed SDKs changes its build contract, that test directs maintainers
back to this table so the Rust decision is reconsidered rather than drifting silently.

## Verification authority and feature layering

The release matrix in [MAINTAINING.md](MAINTAINING.md), the Rust development module,
and the Rust security workflow remain the definitive SDK verification contract.
Feature checkpoints add evidence for a newly introduced boundary; they do not replace,
fork, or reinterpret that contract. A feature is complete only when its implementation
continues to satisfy the canonical Rust checks and its additional evidence passes.

Completed Feature checkpoints are absorbed into the ordinary unit, integration,
generation, conformance, and security suites. Do not replay earlier Feature workflows
serially at every later checkpoint. The development progression is:

1. While iterating, run the narrowest crate, package, fixture, or named integration
   case that exercises the changed boundary.
2. At an implementation checkpoint, run the relevant canonical Rust checks plus only
   the current Feature's focused evidence.
3. At Feature implementation closure, run the complete canonical Rust matrix and pure
   contract harness once, directly through Cargo and Go, without Dagger orchestration.
4. At SDK sign-off, run the exact-engine evidence matrix. At release, additionally run
   the publication, security, generated-client, and live-conformance gates prescribed
   by `MAINTAINING.md`; no Feature-specific recipe may weaken them.

`engine-unit`, `engine-content`, `engine-integration`, and `engine-evidence` below are
therefore the SDK-sign-off branch. They reproduce or prove boundaries beyond ordinary
Cargo and Go checks, but are subordinate to the canonical SDK build and release
instructions. None is an ordinary local checkpoint gate.

## Engine-free development workflow

Use the narrowest owning package while iterating. Feature 5 implementation closure runs
the canonical Rust commands from `sdk/rust/AGENTS.md` directly, plus focused direct Go
compile/static tests for changed ABI-adapter packages. The high-signal contract suites
are:

```console
cd sdk/rust
cargo test -p dagger-codegen --test engine_operations --test visible_schema_properties --locked
cargo test -p dagger-sdk-engine --locked
cargo test -p dagger-sdk-completeness --locked

cd ../../toolchains/engine-dev
GOCACHE=/tmp/dagger-rust-go-cache go test . ./build -count=1

cd ../rust-sdk-dev
GOCACHE=/tmp/dagger-rust-go-cache go test . ./internal/enginefixture ./internal/enginefree -count=1

cd ../../sdk/rust/runtime
GOCACHE=/tmp/dagger-rust-go-cache go test ./internal/metadata -count=1
```

These tests execute the production Rust schema compiler, all four operation selectors
and renderers, project/runtime plans, protocol model, and evidence model against
deterministic in-memory or temporary-filesystem fixtures. They do not construct or
execute a Dagger engine. Engine-dependent completeness rows remain Partial when this
workflow passes.

The root engine adapter packages contain Linux-only implementation files, so a macOS
checkpoint compiles their tests for the engine platform without executing them:

```console
cd ../../..
GOOS=linux GOARCH=amd64 GOCACHE=/tmp/dagger-rust-go-cache \
  go test -c -o /tmp/dagger-rust-core-sdk.test ./core/sdk
GOOS=linux GOARCH=amd64 GOCACHE=/tmp/dagger-rust-go-cache \
  go test -c -o /tmp/dagger-rust-core-schema.test ./core/schema
GOOS=linux GOARCH=amd64 GOCACHE=/tmp/dagger-rust-go-cache \
  go test -c -o /tmp/dagger-rust-cli.test ./internal/cmd/dagger
```

Linux maintainers may run the corresponding engine-free unit packages directly. The
local contract is compile/static compatibility for these ABI packages plus behavioral
ownership in Rust and the portable toolchain tests; an exact engine remains a sign-off
concern.

### Change-triggered generation

Generation is not part of the continuous local checkpoint loop. Do not invoke a Dagger
generator for documentation, fixtures, Rust internals, or implementation-only Go
changes. Refresh bindings only when their owning Dagger module API/schema changes, and
then do it once with that module's scoped changeset, inspect the exact diff, and return
to direct compile/static checks. A later SDK-sign-off reproducibility pass is separate
from local implementation closure.

## SDK sign-off workflow

Run these commands from the repository root only when collecting exact-engine sign-off
evidence. A focused case is useful for sign-off diagnosis; the complete matrix is the
admission gate.

```console
./bin/dagger api call -m toolchains/rust-sdk-dev engine-unit
./bin/dagger api call -m toolchains/rust-sdk-dev engine-content manifest-digest
./bin/dagger api call -m toolchains/rust-sdk-dev engine-content engine-integration --cases operations
./bin/dagger api call -m toolchains/rust-sdk-dev engine-content engine-evidence
```

- `engine-unit` reproduces Rust engine-tool, completeness-boundary, Go adapter, and
  focused source-graph tests inside the sign-off Dagger graph.
- `engine-content` builds one target-bound OCI content object. Its manifest and
  descriptor digests are evidence coordinates, not a promise that another runner can
  recover the object's bytes.
- `engine-integration` accepts only the documented closed case names. Multiple cases
  fan out from the same retained content object inside one Dagger graph.
- `engine-evidence` requires every positive and negative case to pass before it emits
  an observation. A failure, skip, unknown case, wrong digest, or incomplete set is an
  atomic rejection.

The closed case inventory is:

| Case | Boundary proved |
| --- | --- |
| `resolution` | Canonical built-in selection, idempotent installation, and pre-fallback shorthand rejection |
| `init-empty` | New Cargo package, lockfile, toolchain, starter source, and checked generation |
| `init-existing` | Semantic Cargo adoption and byte-preservation of caller-owned source |
| `init-no-generate` | Initialization without accidental generated publication |
| `operations` | Distinct library, module, client, and entrypoint selector observations plus the real module and client generator hooks over engine-visible schemas |
| `runtime-checked` | Checked-generation runtime registration plus overlapping scalar calls |
| `runtime-legacy` | Private legacy regeneration, registration, invocation, and unchanged host source |
| `negative-generated-lock-toolchain` | Missing generation, stale lockfile, and incompatible toolchain rejection |
| `negative-path-ownership` | Lexical escape, symlink escape, and unknown generated-file ownership rejection |
| `negative-redaction` | Credential-bearing immutable-dependency rejection without secret rendering |

The checked target is declared in `sdk/rust/completeness/target.json`. The packaged
dependency descriptor may select the canonical crates.io release or a credential-free
fork at a full immutable Git revision. Refresh the descriptor, target, schema snapshot,
runtime policy, and generated bindings together; a mixed target must fail before Cargo
runs.

For an unpublished development dependency, add both
`--engine-repository <credential-free-fork-url>` and
`--sdk-dependency-revision <full-reachable-revision>` before `engine-content`. The
current workspace commit remains the engine build's provenance; it may be local and
unpublished. The separate dependency revision must already be reachable from the fork
and contain entry-identical build inputs for `sdk/rust/crates/dagger-sdk`: its Cargo
manifest, Rust source, and packaged assets. The builder checks that identity
bidirectionally before the expensive engine path and records the revision in the
packaged descriptor. Omit both options only when the canonical registry dependency is
actually published; a local commit, mutable branch, or tag is never a dependency
coordinate.

This is a development build of the local engine and packaged built-in SDK whose
generated Cargo projects resolve the public `dagger-sdk` package from the fork commit.
It does not publish a crate, create a tag, or replace the immutable checked Dagger
target; those remain separate release and target-refresh operations.

The repository coordinate must be an HTTPS URL with a host and no embedded user,
password, query, or fragment. The revision must be exactly one lowercase 40-character
commit SHA. A branch may advertise that commit, but the branch name itself is never
passed to the build.

Before a fork-backed run:

1. Push the selected dependency commit to a branch in the intended fork. It need not be
   the current workspace `HEAD` when the public package inputs are unchanged.
2. Run `git rev-parse <local-dependency-ref>` locally and `git ls-remote
   <credential-free-fork-url> refs/heads/<branch>` remotely. Confirm that both commands
   report the same 40-character commit SHA. This distinguishes a locally committed
   revision from one the Dagger Git source can actually fetch.
3. Prove that the build-relevant public package inputs have not changed between that
   revision and the local workspace:

   ```console
   git diff --exit-code <full-reachable-revision> -- \
     sdk/rust/crates/dagger-sdk/Cargo.toml \
     sdk/rust/crates/dagger-sdk/src \
     sdk/rust/crates/dagger-sdk/assets
   ```

4. Pass the same immutable coordinates to every `engine-content` invocation in the
   build. `engine-unit` has no engine content and therefore needs neither coordinate:

   ```console
   ./bin/dagger api call -m toolchains/rust-sdk-dev \
     --engine-repository <credential-free-fork-url> \
     --sdk-dependency-revision <full-reachable-revision> \
     engine-content manifest-digest

   ./bin/dagger api call -m toolchains/rust-sdk-dev \
     --engine-repository <credential-free-fork-url> \
     --sdk-dependency-revision <full-reachable-revision> \
     engine-content engine-integration --cases operations

   ./bin/dagger api call -m toolchains/rust-sdk-dev \
     --engine-repository <credential-free-fork-url> \
     --sdk-dependency-revision <full-reachable-revision> \
     engine-content engine-evidence
   ```

If the public package has changed, publish the source commit to the intended fork before
running the integration case. Do not substitute the local workspace commit merely to
make Cargo resolution proceed: engine provenance and generated-project dependency
provenance are deliberately separate contracts. Invalid coordinates, an unreachable
commit, or any addition, removal, or byte difference in `Cargo.toml`, `src/**`, or
`assets/**` fails before the expensive engine build. A successful build records the
canonical HTTPS repository and exact commit in its dependency descriptor.

## Integration fixture preflight

Do not use an engine run to discover fixture semantics. Before invoking
`engine-integration`, write down and review the complete transition for the selected
case:

1. List the initial workspace/module files and both configuration formats.
2. Record every command's mutation, workspace cwd, module-source root, output root,
   generation mode, and runtime-load precondition.
3. Ground each transition in the corresponding core or established SDK fixture. In
   particular, inspect checked-versus-legacy generation and generator scoping.
4. Confirm that schema consumers receive an already-loadable module. A checked module
   cannot supply its runtime schema while simultaneously bootstrapping the committed
   bindings needed to load that runtime; use a separate stable schema fixture.
5. Respect the SDK's reflected surface. Feature 5 deliberately has no client
   initializer: declare the Rust generator in a stable schema module's `clients`
   configuration and export only that module's `generatedContextChangeset`. This
   traverses the engine-owned `ClientGenerator.GenerateClient` hook directly. Do not
   invent an `api client init` command or run the complete workspace generator group;
   Feature 7 will own user-facing client initialization.
6. Assert exact commands, config anchors, paths, and forbidden sequencing in
   `toolchains/rust-sdk-dev/internal/enginefree`.
7. Inspect the Dagger graph for broad generator discovery, repeated content/engine
   construction, unrelated SDK source, and unbounded fan-out.
8. Run the engine-free audit before the one focused case:

   ```console
   cd toolchains/rust-sdk-dev
   go test ./internal/enginefree -count=1
   ```

Only a fixture that passes this review proceeds to an engine-backed run. A failed
focused case returns to the preflight model first; repeatedly changing the fixture and
using a multi-minute engine build as the next assertion is not the development loop.

## Local triage

Start with the owning direct Cargo test and its deterministic fixture. For an SDK
sign-off failure, reproduce the observed contract in the pure Rust harness before
rerunning only the named engine case. Inspect the stable Rust diagnostic code and
coordinate. Repair generated ownership with scoped generation; do not delete
caller-authored Cargo, source, VCS, or workspace files. Missing or stale locks are
repaired by generation, never by an unlocked runtime build.

The private protocol probe has one registration branch and one scalar invocation. It
proves the nested-session boundary only; it is not a public module authoring API. The
standalone client renderer likewise proves the engine hook without claiming complete
client content.

Before evidence or release review, inspect the runtime image for exactly the installed
binary and provenance additions, confirm that Cargo homes/caches/source are absent, run
the repository Rust security workflow, regenerate scoped outputs twice, and require the
second render to be byte-clean. Evidence may close only the capability-local domains
declared in `completeness/engine-integration-mappings.json`; remaining sibling content
stays visible as a blocker.

When that explicit API/schema predicate is met, the development module's generated Go
bindings have their own smaller generation boundary. Preview and apply only that module
source rather than invoking every workspace generator:

```console
./bin/dagger api call -M -j module-source \
  --ref-string toolchains/rust-sdk-dev --require-kind LOCAL_SOURCE \
  generated-context-changeset modified-paths
./bin/dagger api call -M module-source \
  --ref-string toolchains/rust-sdk-dev --require-kind LOCAL_SOURCE \
  generated-context-changeset export --path .
```

The preview is expected to name only `toolchains/rust-sdk-dev/dagger.gen.go` and the
affected files beneath `toolchains/rust-sdk-dev/internal/dagger`. The unscoped
`dagger generate` command belongs to repository-wide generation and is not part of a
Rust engine-integration checkpoint.
