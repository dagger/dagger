# Reduce Avoidable QEMU Execution in Engine Release Builds

## Status

Proposed. Awaiting Erik's final review before implementation.

## Top-line goal

Make the existing multi-platform engine release graph more reliable by removing
two unnecessary target-platform executions and updating Dagger's bundled QEMU
fallback, without changing release orchestration, test coverage, supported
platforms, the Python SDK payload contract and behavior, or native-runner
topology. Updating QEMU does change the engine's bundled emulator files as
described below.

## Motivation

The engine release builds both `linux/amd64` and `linux/arm64` variants. When a
variant does not match the engine host, any `Container.withExec` in that target
container can run through binfmt or Dagger's bundled QEMU. This is useful for
true compatibility tests, but release artifact construction should not use it
for architecture-independent generation or declarative filesystem changes.

The current graph has two small but important examples:

1. Python SDK packaging executes target-platform `uv`, `uvx`, and Python to
   create a codegen zipapp whose only currently declared runtime dependency is
   the architecture-independent `graphql-core` package.
2. Final engine-image assembly executes target-platform `ln` to create one
   symlink that Dagger can create directly in the filesystem graph.

The first path is also adjacent to the July 16, 2026 release failure: the
`linux/arm64` Python SDK build completed `uv export`, then the dependent
`uvx shiv` packaging path exited with status 139. That establishes avoidable
target execution, but not QEMU as the proven root cause. Removing the target
execution is therefore the primary reliability change. Updating the bundled
QEMU fallback from 7.1.0 to 10.2.3 is a separate defense-in-depth change with
an independent rollback boundary.

## Goals

1. Run Python codegen dependency export and shiv packaging on the engine's
   build platform for every requested engine target.
2. Continue bundling the correct target-platform `uv` and `uvx` binaries in
   each Python SDK payload without executing those target binaries.
3. Create `/usr/bin/buildctl -> /usr/bin/dial-stdio` declaratively rather than
   by running target-platform `ln`.
4. Update Dagger's embedded BuildKit-compatible QEMU binaries from QEMU 7.1.0
   to QEMU 10.2.3 using an immutable image digest.
5. Preserve the current engine image variants, SDK payload layout, release
   entry points, and publication behavior.
6. Keep the QEMU update independently revertible from the emulation-removal
   changes.

## Non-goals

This change will not:

- change GitHub Actions workflows, including the macOS QEMU setup;
- change tests, test matrices, or existing cross-platform `uname` coverage;
- restructure TypeScript, Bun, Node, npm, or Go SDK packaging;
- change Alpine/Wolfi BusyBox setup;
- change changelog tooling;
- introduce native per-architecture release runners or cross-node builders;
- change Python, uv, shiv, Bun, Go, or package dependency versions;
- modify dependency manifests, lockfiles, generated Dagger bindings, or release
  configuration;
- use `dag.CurrentWorkspace()`; release source continues to arrive through the
  existing explicit `Workspace` input;
- claim that QEMU 10.2.3 fixes the July exit 139 without evidence from CI.

## Current graph

### Python SDK content

`Builder.pythonSDKContent` currently applies `build.platform` to both Docker
builds:

```text
sdk/python/runtime/images/base --platform=TARGET --target=base
sdk/python/runtime/images/uv   --platform=TARGET --target=uv
```

For an amd64 engine host building an arm64 engine variant, the graph is:

```text
amd64 engine host
  |
  +-- resolve arm64 Python 3.14 rootfs
  +-- resolve arm64 uv rootfs
  |
  +-- copy arm64 uv/uvx into SDK rootfs/dist             [file operation]
  |
  +-- put arm64 uv/uvx in arm64 Python /usr/local/bin
  +-- run arm64 `uv export ...`                          [QEMU/binfmt]
  +-- run arm64 `uvx shiv==1.0.8 ...`                    [QEMU/binfmt]
  +-- take /codegen and put it in SDK rootfs/dist        [file operation]
  |
  +-- export target SDK OCI content                      [no target execution]
```

The generated `/codegen` is a shiv zipapp with entry point
`codegen.cli:main`. The mounted local `codegen` package is Python source and
declares only `graphql-core>=3.2.3`, which publishes a `py3-none-any` wheel.
Neither the local package nor that dependency contains native extensions or
platform-conditioned files today. The mounted directory has no `uv.lock`, and
the command does not pass `--frozen`, so this design does not claim that the
resolved version is locked by the parent SDK's `uv.lock`. The target platform
is relevant to the uv binaries bundled beside the zipapp, but not to the
current zipapp generation commands.

### Engine-image symlink

After copying the engine, helpers, runc, tini, CNI plugins, and bundled QEMU
binaries into the target rootfs, `Builder.Engine` currently calls:

```go
WithExec([]string{"ln", "-s", "/usr/bin/dial-stdio", "/usr/bin/buildctl"})
```

The operation changes filesystem metadata only, but because the container is
`build.platform`, it starts the target `ln` binary and can invoke QEMU.

### Bundled QEMU

`consts.QemuBinImage` currently points at an immutable 2023
`tonistiigi/binfmt` BuildKit image containing QEMU 7.1.0. `Builder.qemuBins`
resolves that image for the engine target platform, enumerates its rootfs, and
copies its `/buildkit-qemu-*` files into `/usr/local/bin` in the final engine.

These binaries are Dagger's fallback when the host is neither native for a
requested platform nor already configured with a matching binfmt handler.

## Proposed graph

### BUILDPLATFORM/TARGETPLATFORM boundary

For Python SDK construction, use separate build-platform and target-platform uv
rootfs values:

```text
BUILDPLATFORM branch
  |
  +-- Python 3.14 base for current engine platform
  +-- uv image for current engine platform
  +-- host-native `uv export`
  +-- host-native `uvx shiv`
  +-- architecture-independent /codegen
  |
  +-----------------------------+
                                |
TARGETPLATFORM branch           |
  |                             |
  +-- uv image for target       |
  +-- copy target uv/uvx -------+-- assemble target SDK rootfs
                                +-- copy /codegen
                                +-- export target OCI content
```

No target binary executes in this Python SDK construction path. Each target
still receives its matching uv binaries.

For the engine symlink, replace the exec vertex with a filesystem vertex:

```text
target engine container
  +-- withSymlink(target=/usr/bin/dial-stdio,
                  linkName=/usr/bin/buildctl)
```

## Detailed implementation

### 1. Split Python build tools from target payload tools

File: `toolchains/engine-dev/build/sdk.go`

Function: `(*Builder).pythonSDKContent`

#### 1.1 Separate platform-free Python source from target payload assembly

Keep the existing two Docker build contexts, but name the source directories so
the uv context can produce both a build-platform and target-platform result:

```go
pythonImageSource := build.source.Directory("sdk/python/runtime/images/base")
uvImageSource := build.source.Directory("sdk/python/runtime/images/uv")
```

Construct the existing filtered SDK source as a platform-free directory before
adding target uv files:

```go
pySrc := dag.Directory().WithDirectory(
    "/",
    build.source.Directory("sdk/python"),
    dagger.DirectoryWithDirectoryOpts{
        Include: []string{
            "pyproject.toml",
            "uv.lock",
            "src/**/*.py",
            "src/**/*.typed",
            "codegen/",
            "runtime/",
            "LICENSE",
            "README.md",
        },
        Exclude: []string{
            "src/dagger/_engine/",
            "src/dagger/provisioning/",
        },
    },
)
```

The include/exclude contract is unchanged. Naming this value separately is
semantically important: the codegen exec must mount
`pySrc.Directory("codegen")` before any target-platform value is added to the
directory's provenance.

#### 1.2 Build the Python execution environment for BUILDPLATFORM

Create the Python base without setting `DirectoryDockerBuildOpts.Platform`:

```go
buildBase := pythonImageSource.DockerBuild(dagger.DirectoryDockerBuildOpts{
    Target: "base",
})
```

An omitted platform means the Docker build uses the current Dagger query/engine
platform. The Dockerfile is a single `FROM python:3.14-slim` stage and has no
target-dependent build logic.

Do not add a `buildPlatform` field to `Builder`. The build graph only needs the
current query platform here, and an unset platform already expresses that
semantics. `build.platform` remains the explicit artifact target.

#### 1.3 Resolve uv separately for execution and payload

Produce two containers from the same pinned uv Dockerfile:

```go
buildUV := uvImageSource.DockerBuild(dagger.DirectoryDockerBuildOpts{
    Target: "uv",
})

targetUV := uvImageSource.DockerBuild(dagger.DirectoryDockerBuildOpts{
    Platform: build.platform,
    Target:   "uv",
})
```

The image reference and digest in
`sdk/python/runtime/images/uv/Dockerfile` remain unchanged. Manifest selection
chooses the appropriate architecture for each branch.

#### 1.4 Select target uv without executing it

Reserve `targetUV.Rootfs()` only for the later SDK rootfs assembly. The target
branch remains a manifest/rootfs selection and contains no exec vertex.

The final assembly will retain the existing filtered copy:

```go
pySrc.WithDirectory("dist", targetUV.Rootfs(), dagger.DirectoryWithDirectoryOpts{
    Include: []string{"uv*"},
})
```

This preserves the current output contract:

- `dist/uv` has the target engine architecture;
- `dist/uvx` has the target engine architecture;
- existing filenames, permissions, and locations remain unchanged.

#### 1.5 Run export and shiv from platform-free source with build-platform uv

Keep the existing codegen source mount, commands, flags, paths, TLS setting, and
shiv version. Change only the container/rootfs supplying the executables:

```go
codegen := buildBase.
    WithWorkdir("/src").
    WithDirectory(
        "/usr/local/bin",
        buildUV.Rootfs(),
        dagger.ContainerWithDirectoryOpts{Include: []string{"uv*"}},
    ).
    WithMountedDirectory("", pySrc.Directory("codegen")).
    WithEnvVariable("UV_NATIVE_TLS", "true").
    WithExec([]string{
        "uv", "export",
        "--no-hashes",
        "--no-editable",
        "--package", "codegen",
        "-o", "/requirements.txt",
    }).
    WithExec([]string{
        "uvx", "shiv==1.0.8",
        "--reproducible",
        "--compressed",
        "-e", "codegen.cli:main",
        "-o", "/codegen",
        "-r", "/requirements.txt",
    }).
    File("/codegen")
```

The `uv*` filter avoids copying the uv image's unrelated `io/` root entry into
the throwaway execution container.

Only after `codegen` is defined, assemble the target payload:

```go
rootfs := pySrc.
    WithDirectory("dist", targetUV.Rootfs(), dagger.DirectoryWithDirectoryOpts{
        Include: []string{"uv*"},
    }).
    WithFile("dist/codegen", codegen)
```

Do not copy `buildUV` into the final payload. Do not execute `targetUV`.

#### 1.6 Caching behavior

`pythonSDKContent` is evaluated once per `Builder`, and release builds create a
builder for each target. The build-platform codegen subgraph is expected to
have the same inputs and operations for amd64 and arm64 target builders.
Because `codegen` depends on `pySrc`, `buildBase`, and `buildUV` but not
`targetUV` or `rootfs`, `build.platform` is absent from its full provenance.
The resulting recipe identity is eligible for exact Dagger cache reuse and
in-flight deduplication across target builders; this does not guarantee reuse
across independently isolated engine/cache environments.

This design intentionally avoids changing `sdkContentF` or moving codegen into
a new release-wide orchestration parameter. Such a refactor would enlarge the
change for little benefit; graph identity already expresses the desired
deduplication.

#### 1.7 Maintained invariant

The correctness condition for keeping codegen architecture-independent is:

> The local codegen package and the dependency closure embedded by shiv must
> contain only platform-independent Python files and `py3-none-any` wheels, and
> build-platform marker resolution must produce the same runtime dependency
> set required by every target platform.

That condition holds for the current local `codegen -> graphql-core`
dependency chain. If a future change introduces a platform marker,
platform-specific file, wheel, or native extension, its review must either
prove equivalent marker resolution, preserve a universal zipapp, explicitly
produce target-specific zipapps, or move validation to native target hardware.
This proposal does not add a new automated policy check for that future case.

### 2. Create the buildctl symlink declaratively

File: `toolchains/engine-dev/build/builder.go`

Function: `(*Builder).Engine`

Replace:

```go
ctr = ctr.
    WithExec([]string{"ln", "-s", "/usr/bin/dial-stdio", "/usr/bin/buildctl"}).
    WithDirectory(distconsts.EngineDefaultStateDir, dag.Directory())
```

with:

```go
ctr = ctr.
    WithSymlink("/usr/bin/dial-stdio", "/usr/bin/buildctl").
    WithDirectory(distconsts.EngineDefaultStateDir, dag.Directory())
```

The engine-dev generated bindings already provide `Container.WithSymlink`; no
schema, core, or generated-code changes are necessary. The argument order is
`target`, then `linkName`, matching the desired absolute link:

```text
/usr/bin/buildctl -> /usr/bin/dial-stdio
```

`Container.WithSymlink` updates the rootfs snapshot and clears the prior image
reference without executing a process. It handles the same absolute target and
link path used by `ln -s`.

### 3. Update the bundled BuildKit QEMU image

File: `toolchains/engine-dev/consts/consts.go`

Constant: `QemuBinImage`

Replace:

```go
QemuBinImage = "tonistiigi/binfmt@sha256:e06789462ac7e2e096b53bfd9e607412426850227afeb1d0f5dfa48a731e0ba5"
```

with:

```go
QemuBinImage = "tonistiigi/binfmt@sha256:6014c1e52b8e51a67fbf76f691ffbe20ac0204c31c2f086df3e8ef3ce134b488"
```

The candidate is the multi-platform `buildkit-v10.2.3` image from
`tonistiigi/binfmt` release `buildkit/v10.2.3-66`, published June 8, 2026. It
contains QEMU 10.2.3 and preserves the `/buildkit-qemu-*` layout expected by
`Builder.qemuBins`.

The new BuildKit image also contains `/buildkit-qemu-loongarch64`, which is not
present in the currently pinned payload. `Builder.qemuBins` copies every rootfs
entry, so this update copies that binary into applicable engine images and
increases their layer size in addition to updating existing emulators. It is
currently inert for Dagger's explicit fallback: containerd normalizes the
architecture as `loong64`, while `getEmulator` has no mapping from `loong64` to
the binary suffix `loongarch64`. Adding that mapping or filtering the binary is
outside this proposal because either would mix the version bump with a change
in emulator-selection policy.

Registry inspection established the expected payload change before
implementation:

- amd64 image variant: 53.1 MB to 81.1 MB uncompressed, an increase of 28.0 MB;
- arm64 image variant: 51.8 MB to 76.9 MB uncompressed, an increase of 25.1 MB;
- each new variant contains nine flat `/buildkit-qemu-*` entries, versus eight
  in the old variant;
- the amd64 variant omits `buildkit-qemu-x86_64`, while the arm64 variant omits
  `buildkit-qemu-aarch64`; this native-architecture omission is expected in
  both the old and new images;
- the added ninth entry is `buildkit-qemu-loongarch64`;
- the new binaries are static PIE executables, while the old binaries are
  plain static executables; both sets are unstripped.

Validation must confirm these expected per-variant entry sets and size changes,
not rediscover them without a baseline.

Do not use the normal `tonistiigi/binfmt:latest` image. That is the mutable
binfmt-registration image with `/usr/bin/qemu-*`, not the BuildKit-form image
whose rootfs Dagger copies into the engine.

No code change is required in `Builder.qemuBins`: it will continue resolving
the pinned image for `build.platform`, enumerating the available emulator files,
and copying them into `/usr/local/bin`.

## Exact implementation file scope

| File | Change |
| --- | --- |
| `toolchains/engine-dev/build/sdk.go` | Split build-platform Python/uv execution from target-platform uv payload selection. |
| `toolchains/engine-dev/build/builder.go` | Replace target `ln -s` exec with `Container.WithSymlink`. |
| `toolchains/engine-dev/consts/consts.go` | Pin the BuildKit-form QEMU 10.2.3 image digest. |

Expected untouched areas include `.github/workflows`, all test files,
TypeScript/Bun SDK construction, Go SDK construction, changelog tooling,
Wolfi/Alpine construction, release orchestration, dependency manifests,
lockfiles, and generated bindings.

The durable design document itself is a fourth worktree file:
`hack/designs/qemu-ci-reduction-quick-wins.md`. The three-file limit applies to
the later product-code implementation diff, not to the complete worktree diff
that includes this reviewed design artifact.

## Validation plan

No new tests or test-matrix changes are proposed. Validation uses focused
artifact inspection followed by the existing CI and release checks.

### Static review

Before running Dagger:

1. Confirm `build.platform` occurs only on `targetUV` in the Python portion.
2. Confirm `pySrc`, `buildBase`, `buildUV`, and the full transitive provenance
   of `codegen` have no target-platform dependency.
3. Confirm the final SDK rootfs is assembled only after `codegen` is defined
   and reads bundled uv binaries only from `targetUV`.
4. Confirm no `WithExec` remains for the buildctl symlink.
5. Confirm the QEMU digest is immutable and is the BuildKit image variant.
6. Confirm the product-code implementation diff contains only the three files
   in the table above, in addition to this design document.

### Focused local validation

Use Dagger's normal progress UI and avoid broad `go test ./...` or tests through
`bin/dagger`.

1. Run `dagger call engine-dev release-dry-run`. This is intentionally the
   selected end-to-end build because it constructs the default amd64 and arm64
   engine variants through the release graph being changed. Do not substitute
   a broad `go test ./...` invocation or run tests through `bin/dagger`.
2. Inspect the graph/trace for the arm64 variant on an amd64 host:
   - `uv export` and `uvx shiv` execute on the host/build platform;
   - target uv selection is a file/rootfs operation, not an exec;
   - the former `ln -s` exec vertex is absent.
3. Inspect both SDK artifacts:
   - `dist/codegen` is present and executable;
   - `dist/uv` and `dist/uvx` have the expected target ELF architecture;
   - capture and compare the generated `/requirements.txt` and zipapp package
     inventory before and after the change; take the captures close together
     because resolution is unlocked, and investigate or normalize any resolved
     version difference before attributing it to the platform change;
   - the codegen zipapp has the expected entry point and the same file-level
     structure as before.
4. Inspect the engine rootfs:
   - `/usr/bin/buildctl` is a symlink;
   - it resolves to `/usr/bin/dial-stdio`;
   - `/usr/bin/dial-stdio` remains executable.
5. Inspect the amd64 and arm64 manifests of the pinned QEMU image and confirm
   the pre-recorded entry-count, native-architecture omission, uncompressed
   size, and ELF-format expectations above. Confirm the expected emulator
   binaries report QEMU 10.2.3 and that the inert loongarch64 binary is copied
   deliberately rather than mistaken for an unrelated rootfs entry.

`release-dry-run` is available through the current engine-dev module API. ELF
architecture, symlink, zipapp, QEMU root-entry, and exec-platform assertions
require manual artifact or trace inspection around that run; they are not all
exposed as dedicated module fields. Use trace/artifact inspection rather than
adding temporary product logging or changing tests.

### CI validation

Run the repository's existing checks normally. In particular, rely on the
existing engine release/dry-run, integration, SDK, and publish-related coverage
that is already selected by CI. Do not add a repetition matrix or special QEMU
stress workflow.

Because checked-in GitHub PR triggers are transitioning to native CI, confirm
on the implementation PR that the `engine-dev` release-dry-run check is
actually scheduled. Do not infer pre-merge coverage solely from the commented
workflow triggers.

Success means:

- both amd64 and arm64 engine artifacts build;
- existing SDK and platform checks pass;
- existing CI exercises the produced engine and SDK artifacts to the same
  extent it did before this change; this proposal does not claim that current
  provision jobs directly execute embedded `dist/codegen`;
- no new exit 139, exec-format, missing-file, or symlink failure appears;
- the QEMU image resolves for each supported engine target.

## Delivery and rollback

Keep the behavioral removal and QEMU version update as independent commits:

1. `engine-dev: avoid emulation in Python SDK and image symlink setup`
2. `engine-dev: update bundled QEMU to 10.2.3`

This provides two useful rollback boundaries:

- A Python or symlink artifact regression can be reverted without downgrading
  QEMU.
- An emulator regression can be reverted by restoring one constant without
  reintroducing the eliminated Python and `ln` executions.

If CI fails after the QEMU bump with an emulator-specific regression, first
revert only the QEMU commit. If Python payload contents or buildctl resolution
regress, revert or correct the first commit based on the failing artifact.

No implementation commit, push, PR, or release-state change occurs until Erik
approves this design. Any later commits must carry:

```text
Signed-off-by: Erik Sipsma <erik@sipsma.dev>
```

They must contain no agent attribution.

## Acceptance criteria

The implementation is complete when all of the following are true:

1. Python SDK construction for a non-native target does not execute
   target-platform uv, uvx, Python, or shiv; amd64-to-arm64 is the primary
   validation case.
2. A target engine image still contains target-architecture `dist/uv` and
   `dist/uvx` plus a working `dist/codegen` zipapp.
3. Engine image assembly creates `/usr/bin/buildctl` without a target-platform
   exec and the link resolves to `/usr/bin/dial-stdio`.
4. Final engine images contain the expected BuildKit-form QEMU 10.2.3 fallback
   binaries.
5. Existing relevant CI passes without test or workflow modifications.
6. The product-code implementation diff is limited to the three files listed
   above; the reviewed design document remains the separate durable artifact.

## Final approval gate

Erik must review and approve this document before implementation begins.
