# Namespace Rust SDK artifact build

This is the complete operator runbook for producing the Dagger Rust SDK artifacts on
the Namespace `dagger-rust-builder-xl` devbox. This is a documentation name, not the
name of any existing builder. Other maintained Rust SDK documents link here instead of
duplicating this procedure.

The devbox is a fast Linux builder, not the source of truth. Its `/workspaces` volume
survives pause and reactivation; Docker containers, images, and volumes might not.
Always repeat every preflight after reconnecting. See Namespace's
[Devbox overview](https://namespace.so/docs/devbox) and
[management guide](https://namespace.so/docs/devbox/managing).

This procedure creates local artifacts only. It does not create a pull request, tag,
GitHub Release, GitHub Actions workflow, crates.io publication, signature, attestation,
or provenance bundle. A manual GitHub Release requires separate direct authorization.

## Documented builder specification

Provision `dagger-rust-builder-xl` as a private, non-ephemeral Namespace Devbox with
this specification:

| Property | Required value |
| --- | --- |
| Platform | Linux/amd64 |
| Namespace size | XL: burstable to 32 vCPU with 64 GB memory |
| Persistent volume | At least 200 GiB, mounted at `/workspaces` |
| Devbox image | Namespace `builtin:agents` |
| Access | Private |
| Privilege | Enabled for the mounted Docker service |
| Repository checkout | Disabled; this runbook performs a fresh exact Git checkout |
| Idle timeout | One hour; the activity marker and SSH session suppress idleness during work |

The XL CPU and memory values come from Namespace's
[machine-size table](https://namespace.so/docs/devbox/managing#machine-sizes), while
the persistent volume setting is documented under
[Volume Size](https://namespace.so/docs/devbox/managing#volume-size). A reproducible
Devbox spec is:

```yaml
name: dagger-rust-builder-xl
image: builtin:agents
size: XL
access_mode: private
volume_size_gb: 200
auto_stop_idle_timeout: 1h
privileged: true
repository:
  disabled: true
```

Create it with `devbox create --from <spec-file>` as described in Namespace's
[spec-file reference](https://namespace.so/docs/devbox/managing#spec-file-reference).
Workspace policy may impose stricter settings; do not weaken one to match this file.

## Security and operating rules

- Use a credential-free HTTPS Git URL. Never put a token, password, credential-bearing
  URL, Docker configuration, or environment dump in a command, log, or document.
- Do not enable shell tracing. Diagnostics must remain credential-safe.
- Use one Namespace Console or SSH session at a time. A second session during
  reactivation or CLI access can contend for the persistent volume.
- Treat historical workspaces and retained custom build helpers as forensic only. Do
  not reuse their CLIs, engines, wrappers, or outputs.
- Keep source checkouts, tools, and outputs below `/workspaces`; never use `/tmp` for an
  artifact that must survive a pause.
- Never transfer a macOS worktree to the builder. Clone with Git on Linux and require
  zero `._*` AppleDouble files.
- Never substitute a bare `cargo package -p dagger-sdk`. The ordinary Dagger `Build`
  packages the unpublished exact macro companion first, applies its local patch while
  packaging `dagger-sdk`, and validates both archives together.
- The proven builder runner is beta.8 because the beta.10 runner can tear down module
  queries exceeding roughly 30 seconds. The runner builds the candidate; it is not the
  engine exported as the candidate artifact.
- Set `_EXPERIMENTAL_DAGGER_RUNNER_HOST` on every Dagger invocation. One omitted setting
  can provision an unintended default engine.
- The Docker socket is mounted in the devbox, but the bundled Docker CLI is outside the
  default `PATH`. Every Dagger invocation must include `/vendor/docker` on `PATH`.
- Remove only the activity marker created by this run. Pause the devbox after verified
  retrieval; never destroy it.

Namespace counts active SSH connections and files under `/.namespace/tasks` as active
work. See [Idleness & Auto-Stop](https://namespace.so/docs/devbox/managing#idleness--auto-stop).

## 1. Fix the immutable inputs

Before using Namespace, push the intended commit to the repository and record its full
lowercase 40-character hash. The commit must contain all approved source,
documentation, generated files, and task state. Local checks must already be green.

On the operator workstation, locate the active Namespace instance and connect to its
workload container. Replace angle-bracketed values; do not copy secrets into them.
The command shape follows the official
[`nsc ssh` reference](https://namespace.so/docs/reference/cli/ssh).

```console
export RUST_SDK_NS_INSTANCE="<namespace-instance-id>"
export RUST_SDK_NS_CONTAINER="<namespace-workload-container>"
nsc list
nsc ssh "$RUST_SDK_NS_INSTANCE" --container_name "$RUST_SDK_NS_CONTAINER"
```

Keep that SSH connection open through artifact generation. Inside the devbox, define
only these task-specific values:

```console
export RUST_SDK_REPOSITORY="https://github.com/<owner>/<repository>.git"
export RUST_SDK_COMMIT="<40-character-commit>"
export RUST_SDK_CHECKOUT="/workspaces/checkouts/dagger-$RUST_SDK_COMMIT"
export RUST_SDK_TOOLING="/workspaces/tooling/$RUST_SDK_COMMIT"
export RUST_SDK_OUTPUT="/workspaces/artifacts/$RUST_SDK_COMMIT"
export RUST_SDK_MARKER="/.namespace/tasks/rust-sdk-artifacts-$RUST_SDK_COMMIT"
export RUST_SDK_RUNNER_NAME="dagger-engine-rust-sdk-beta8-f3fc7d3d"
export RUST_SDK_RUNNER_IMAGE="registry.dagger.io/engine@sha256:f3fc7d3db31ca71fdf828716cac9f1515155d1389ca9c21f09f77d0bb2700e8f"
export RUST_SDK_RUNNER_DIGEST="sha256:f3fc7d3db31ca71fdf828716cac9f1515155d1389ca9c21f09f77d0bb2700e8f"
export RUST_SDK_RUNNER_HOST="docker-container://$RUST_SDK_RUNNER_NAME"
export RUST_SDK_DAGGER="$RUST_SDK_TOOLING/dagger"
```

The repository URL must be credential-free HTTPS. The commit must be the exact pushed
artifact target, not a branch, tag, abbreviated hash, or unpushed local commit.

## 2. Mark the devbox active and create a fresh checkout

Create one specifically named marker. A fresh run requires that neither the marker nor
the checkout already exists:

```console
test ! -e "$RUST_SDK_MARKER"
install -m 600 /dev/null "$RUST_SDK_MARKER"
install -d -m 700 /workspaces/checkouts /workspaces/tooling /workspaces/artifacts
test ! -e "$RUST_SDK_CHECKOUT"
git clone --no-checkout "$RUST_SDK_REPOSITORY" "$RUST_SDK_CHECKOUT"
git -C "$RUST_SDK_CHECKOUT" checkout --detach "$RUST_SDK_COMMIT"
```

Require the exact commit, a clean tree, and no AppleDouble files. The last two commands
must print nothing:

```console
git -C "$RUST_SDK_CHECKOUT" rev-parse HEAD
test "$(git -C "$RUST_SDK_CHECKOUT" rev-parse HEAD)" = "$RUST_SDK_COMMIT"
git -C "$RUST_SDK_CHECKOUT" status --short
test -z "$(git -C "$RUST_SDK_CHECKOUT" status --porcelain=v1)"
find "$RUST_SDK_CHECKOUT" -name '._*' -print
test -z "$(find "$RUST_SDK_CHECKOUT" -name '._*' -print -quit)"
```

Do not continue after a mismatch.

## 3. Revalidate Namespace storage, Docker, and the runner

Check persistent storage and the mounted Docker service:

```console
df -h /workspaces
/vendor/docker/docker version
```

If the named runner container is absent after reactivation, recreate it from the pinned
public image:

```console
/vendor/docker/docker pull "$RUST_SDK_RUNNER_IMAGE"
/vendor/docker/docker run --detach --name "$RUST_SDK_RUNNER_NAME" \
  --restart always --privileged "$RUST_SDK_RUNNER_IMAGE"
```

If the container exists but is stopped, start that exact container:

```console
/vendor/docker/docker start "$RUST_SDK_RUNNER_NAME"
```

In all cases, require the expected image digest and a running state:

```console
/vendor/docker/docker inspect "$RUST_SDK_RUNNER_NAME" \
  --format 'image={{.Image}} running={{.State.Running}} status={{.State.Status}}'
test "$(/vendor/docker/docker inspect "$RUST_SDK_RUNNER_NAME" --format '{{.Image}}')" = \
  "$RUST_SDK_RUNNER_DIGEST"
test "$(/vendor/docker/docker inspect "$RUST_SDK_RUNNER_NAME" --format '{{.State.Running}}')" = "true"
```

Do not assume a retained Docker container is usable merely because `/workspaces`
survived.

## 4. Build and verify the exact Dagger CLI

Build the Linux CLI from the same clean detached commit rather than trusting a retained
binary. The build output stays outside the checkout, and `-mod=readonly` prevents
dependency resolution from editing source manifests:

```console
go version
install -d -m 700 "$RUST_SDK_TOOLING"
cd "$RUST_SDK_CHECKOUT"
go build -mod=readonly -trimpath -o "$RUST_SDK_DAGGER" ./cmd/dagger
sha256sum "$RUST_SDK_DAGGER" > "$RUST_SDK_TOOLING/dagger.SHA256SUMS"
cd "$RUST_SDK_TOOLING"
sha256sum --check dagger.SHA256SUMS
```

The Go version must satisfy the exact checkout's root `go.mod`; do not edit `go.mod` or
`go.sum` to accommodate an older host toolchain.

Even the version check carries the explicit runner host and Docker `PATH`. Require the
reported platform to be `linux/amd64`, the commit to match `RUST_SDK_COMMIT`, and the
tree to be clean:

```console
cd "$RUST_SDK_CHECKOUT"
env PATH=/vendor/docker:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
  _EXPERIMENTAL_DAGGER_RUNNER_HOST="$RUST_SDK_RUNNER_HOST" \
  "$RUST_SDK_DAGGER" version
test -z "$(git status --porcelain=v1)"
```

Stop if the CLI reports another commit, platform, dirty state, or runner host.

## 5. Run the ordinary build and external verification

The artifact directory must be absent at the start. Create it once, empty:

```console
test ! -e "$RUST_SDK_OUTPUT"
install -d -m 700 "$RUST_SDK_OUTPUT"
cd "$RUST_SDK_CHECKOUT"
```

Run the ordinary `Build` with an explicit `linux/amd64` platform and invoke `Verify` on
that result. `Verify` unpacks the two packages into an isolated external Rust consumer,
starts the completed engine produced by the build, performs a query, checks engine
version and revision compatibility, and closes the client cleanly:

```console
env PATH=/vendor/docker:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
  DAGGER_NO_NAG=1 \
  _EXPERIMENTAL_DAGGER_RUNNER_HOST="$RUST_SDK_RUNNER_HOST" \
  "$RUST_SDK_DAGGER" -m .dagger/modules/rust-client-dev api call \
  build --platform=linux/amd64 verify
```

Do not export or checksum artifacts unless this terminal verification succeeds.

## 6. Export exactly three artifacts

Export the package directory from the same exact Build graph. It contains exactly the
two public crates:

```console
env PATH=/vendor/docker:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
  DAGGER_NO_NAG=1 \
  _EXPERIMENTAL_DAGGER_RUNNER_HOST="$RUST_SDK_RUNNER_HOST" \
  "$RUST_SDK_DAGGER" -m .dagger/modules/rust-client-dev api call \
  build --platform=linux/amd64 packages export --path="$RUST_SDK_OUTPUT"
```

Export the completed engine with OCI media types:

```console
env PATH=/vendor/docker:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
  DAGGER_NO_NAG=1 \
  _EXPERIMENTAL_DAGGER_RUNNER_HOST="$RUST_SDK_RUNNER_HOST" \
  "$RUST_SDK_DAGGER" -m .dagger/modules/rust-client-dev api call \
  build --platform=linux/amd64 complete-engine export \
  --media-types=OCI \
  --path="$RUST_SDK_OUTPUT/dagger-engine-v1.0.0-beta.11.rust.1-linux-amd64.oci.tar"
```

These Build calls use the same immutable checkout, platform, module, runner setting,
and content-addressed Dagger graph as verification. They perform no publication.

## 7. Validate the artifact set and create checksums

Require the expected two crate names and one engine archive:

```console
cd "$RUST_SDK_OUTPUT"
test -f dagger-sdk-macros-1.0.0-beta.11.rust.1.crate
test -f dagger-sdk-1.0.0-beta.11.rust.1.crate
test -f dagger-engine-v1.0.0-beta.11.rust.1-linux-amd64.oci.tar
test "$(find . -maxdepth 1 -type f | wc -l)" -eq 3
tar -tf dagger-engine-v1.0.0-beta.11.rust.1-linux-amd64.oci.tar | grep -Fx oci-layout
tar -tf dagger-engine-v1.0.0-beta.11.rust.1-linux-amd64.oci.tar | grep -Fx index.json
```

Create `SHA256SUMS` over only those three files, verify it on the builder, then require
exactly four output files:

```console
sha256sum \
  dagger-sdk-macros-1.0.0-beta.11.rust.1.crate \
  dagger-sdk-1.0.0-beta.11.rust.1.crate \
  dagger-engine-v1.0.0-beta.11.rust.1-linux-amd64.oci.tar \
  > SHA256SUMS
sha256sum --check SHA256SUMS
test "$(find . -maxdepth 1 -type f | wc -l)" -eq 4
find . -maxdepth 1 -type f -printf '%f\n' | sort
```

Finally, revalidate that the source commit remained exact and clean and that no
AppleDouble file appeared. The last two commands must print nothing:

```console
test "$(git -C "$RUST_SDK_CHECKOUT" rev-parse HEAD)" = "$RUST_SDK_COMMIT"
git -C "$RUST_SDK_CHECKOUT" status --short
find "$RUST_SDK_CHECKOUT" -name '._*' -print
```

Exit the devbox SSH session, but leave the owned activity marker in place until local
retrieval is independently verified.

## 8. Download and independently verify all four files

On the operator workstation, define a new absolute local destination. Do not reuse a
directory containing older candidate artifacts:

```console
export RUST_SDK_COMMIT="<same-40-character-commit>"
export RUST_SDK_REMOTE_OUTPUT="/workspaces/artifacts/$RUST_SDK_COMMIT"
export RUST_SDK_LOCAL_OUTPUT="<new-absolute-local-directory>/$RUST_SDK_COMMIT"
mkdir -p "$RUST_SDK_LOCAL_OUTPUT"
chmod 700 "$RUST_SDK_LOCAL_OUTPUT"
```

Download exactly four files from the workload container.

The `--container_name` option is documented in the
[`nsc instance download` reference](https://namespace.so/docs/reference/cli/instance-download).

```console
nsc instance download "$RUST_SDK_NS_INSTANCE" \
  "$RUST_SDK_REMOTE_OUTPUT/dagger-sdk-macros-1.0.0-beta.11.rust.1.crate" \
  "$RUST_SDK_LOCAL_OUTPUT/dagger-sdk-macros-1.0.0-beta.11.rust.1.crate" \
  --container_name "$RUST_SDK_NS_CONTAINER"
nsc instance download "$RUST_SDK_NS_INSTANCE" \
  "$RUST_SDK_REMOTE_OUTPUT/dagger-sdk-1.0.0-beta.11.rust.1.crate" \
  "$RUST_SDK_LOCAL_OUTPUT/dagger-sdk-1.0.0-beta.11.rust.1.crate" \
  --container_name "$RUST_SDK_NS_CONTAINER"
nsc instance download "$RUST_SDK_NS_INSTANCE" \
  "$RUST_SDK_REMOTE_OUTPUT/dagger-engine-v1.0.0-beta.11.rust.1-linux-amd64.oci.tar" \
  "$RUST_SDK_LOCAL_OUTPUT/dagger-engine-v1.0.0-beta.11.rust.1-linux-amd64.oci.tar" \
  --container_name "$RUST_SDK_NS_CONTAINER"
nsc instance download "$RUST_SDK_NS_INSTANCE" \
  "$RUST_SDK_REMOTE_OUTPUT/SHA256SUMS" \
  "$RUST_SDK_LOCAL_OUTPUT/SHA256SUMS" \
  --container_name "$RUST_SDK_NS_CONTAINER"
```

Independently verify the downloaded bytes. Use `sha256sum` on Linux or `shasum` on
macOS:

```console
cd "$RUST_SDK_LOCAL_OUTPUT"
test "$(find . -type f | wc -l)" -eq 4
sha256sum --check SHA256SUMS
```

```console
cd "$RUST_SDK_LOCAL_OUTPUT"
test "$(find . -type f | wc -l)" -eq 4
shasum -a 256 -c SHA256SUMS
```

Only one checksum command is required: choose the one provided by the operator
workstation. A failure invalidates the retrieved candidate.

## 9. Remove the owned marker and pause the devbox

After local checksum verification succeeds, reconnect to the same Namespace devbox and
remove only this run's exact marker:

```console
nsc ssh "$RUST_SDK_NS_INSTANCE" --container_name "$RUST_SDK_NS_CONTAINER"
export RUST_SDK_COMMIT="<same-40-character-commit>"
export RUST_SDK_MARKER="/.namespace/tasks/rust-sdk-artifacts-$RUST_SDK_COMMIT"
test -f "$RUST_SDK_MARKER"
unlink "$RUST_SDK_MARKER"
exit
```

Stop `dagger-rust-builder-xl` from the Namespace Devboxes dashboard or run
`devbox shutdown` and select that documented name. Namespace documents both paths under
[Starting & Stopping](https://namespace.so/docs/devbox/managing#starting--stopping).
Do not run `nsc destroy`: destruction is not pause and is outside this procedure. The
verified remote outputs remain below `/workspaces/artifacts/<exact-commit>/`; the
independently verified copies remain at the chosen local destination.

## Failure and resume rule

If any preflight, build, verification, export, OCI inspection, download, or checksum
fails, stop. Do not publish, mark the candidate complete, or remove the activity marker.
On reconnect, repeat the complete preflight. Preserve a partial output recoverably by
moving only its exact directory aside, then use a new empty
`/workspaces/artifacts/<exact-commit>/` for the next attempt. Never let stale output
validate a later run.
