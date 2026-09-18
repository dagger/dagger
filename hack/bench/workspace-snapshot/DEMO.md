# Workspace snapshot demo

## The story

Traditional filesync treats a plain Git clone as an ordinary directory. For a
large repository, that means sending both the worktree and `.git` to the
engine.

The candidate recognizes a Git workspace and creates a synthetic commit for
its current contents. The commit is parented by the tracked upstream commit.
The client then acts as a Git `upload-pack` server: after the engine fetches
the common base, native Git negotiation sends only the missing local changes
and untracked files.

This demo compares the two paths over two topologies:

| Topology | Expected winner | Why |
| --- | --- | --- |
| Local Docker | Filesync | Copying over a local socket is cheaper than fetching GitHub. |
| Dagger Cloud | Client Git server | The client uploads only its delta; Cloud fetches and caches the common base. |

## 1. Create the fixture

Run from the repository root:

```console
hack/bench/workspace-snapshot/demo-setup.sh --reset
```

The setup script makes an independent `dagger/dagger` clone at `origin/main`.
It deliberately avoids hardlinks so `.git` has its real size. It then creates
the representative local work:

- 64 modified tracked files × 256 KiB;
- 128 untracked files × 128 KiB;
- 33.6 MB of local changes in total.

The script prints the worktree size, `.git` size, base commit, and final
`git status`. On the measured checkout, the worktree was roughly 200 MB and
`.git` roughly 450 MB. This is the key setup: filesync sees the whole plain
clone, while Git negotiation can exclude the advertised `origin/main` base.

The shared query asks for the root directory digest, which forces complete
materialization, and reads an untracked probe file to verify correctness.

## 2. Prepare matching CLIs and engines

```console
hack/bench/workspace-snapshot/demo-images.sh
```

This pulls `v1.0.0-beta.13` as the traditional-filesync baseline, builds the
candidate from the current checkout with `engine-dev`, and extracts the CLI
embedded in each engine image. Using matching CLIs matters because the Git
transport adds session RPCs that the baseline does not know about.

For a Cloud demo, the candidate engine must also be available to Cloud. If the
shared `tibor` tag does not already contain this commit, prepare and publish it
with:

```console
hack/bench/workspace-snapshot/demo-images.sh --publish
```

That publishes `ghcr.io/dagger/engine:tibor`; Cloud uses its
`dagger.namespace-images.com/engine:tibor` counterpart.

## 3. Show the local tradeoff

Set short names used by the visible Dagger commands:

```console
DEMO_ROOT=${WORKSPACE_SNAPSHOT_DEMO_ROOT:-/tmp/workspace-snapshot-demo}
DEMO_REPO=$DEMO_ROOT/dagger
FILESYNC_IMAGE=registry.dagger.io/engine:v1.0.0-beta.13
CANDIDATE_IMAGE=localhost/dagger-workspace-candidate:latest
export DAGGER_LEAVE_OLD_ENGINE=1
```

Reset the baseline engine, then run the visible filesync command. Run the
`dagger-filesync` command a second time without resetting to
measure the unchanged workspace:

```console
hack/bench/workspace-snapshot/demo-reset.sh filesync
export DAGGER_ENGINE="image+docker://$FILESYNC_IMAGE?container=dagger-demo-filesync&volume=dagger-demo-filesync&cleanup=false"

time "$DEMO_ROOT/dagger-filesync" --silent -W "$DEMO_REPO" \
  api query -M <"$DEMO_ROOT/query.graphql"
```

Reset the candidate engine and do the same: run this Dagger command twice,
without resetting between the cold and unchanged measurements:

```console
hack/bench/workspace-snapshot/demo-reset.sh candidate
export DAGGER_ENGINE="image+docker://$CANDIDATE_IMAGE?container=dagger-demo-candidate&volume=dagger-demo-candidate&cleanup=false"

time "$DEMO_ROOT/dagger-candidate" --silent -W "$DEMO_REPO" \
  api query -M <"$DEMO_ROOT/query.graphql"
```

What to explain:

- Both arms print the same probe contents and materialize the same user
  worktree. Their root digests differ because the baseline includes `.git`,
  while the candidate intentionally reconstructs only the worktree.
- Filesync wins locally because all traffic stays on the machine.
- The candidate's cold path must fetch `origin/main` from GitHub before it can
  negotiate the local delta. That network fetch is wasted work in this
  topology.

## 4. Show the Cloud speedup

Log in once:

```console
unset DAGGER_ENGINE
dagger login
```

Start both Cloud engines with a trivial query before measuring the workspace:

```console
hack/bench/workspace-snapshot/demo-prewarm.sh filesync
hack/bench/workspace-snapshot/demo-prewarm.sh candidate
```

Prewarming separates image provisioning from workspace transfer. The prewarm
query asks only for `defaultPlatform`; it does not request `currentWorkspace`,
transfer the checkout, or fetch the Git base. The two arms use isolated local
state directories but retain the normal Dagger config for Cloud credentials.

Time the visible Cloud filesync command. Run it twice; the first result is cold
workspace admission and the second is the unchanged workspace:

```console
export XDG_STATE_HOME="$DEMO_ROOT/cloud-filesync/state"
unset _EXPERIMENTAL_DAGGER_CLOUD_ENGINE_IMAGE

time "$DEMO_ROOT/dagger-filesync" --silent --engine cloud -W "$DEMO_REPO" \
  api query -M <"$DEMO_ROOT/query.graphql"
```

Then run the visible Cloud candidate command twice. The experimental variable
selects the candidate image when `--engine cloud` provisions the engine:

```console
export XDG_STATE_HOME="$DEMO_ROOT/cloud-candidate/state"
export _EXPERIMENTAL_DAGGER_CLOUD_ENGINE_IMAGE=dagger.namespace-images.com/engine:tibor

time "$DEMO_ROOT/dagger-candidate" --silent --engine cloud -W "$DEMO_REPO" \
  api query -M <"$DEMO_ROOT/query.graphql"
```

What to explain:

- Filesync uploads the large plain checkout from the presenter machine.
- On the first candidate call, Cloud fetches `origin/main` near the engine,
  then negotiates and fetches only missing objects from the client Git server.
- On the unchanged second call, the engine can reuse the remote base and the
  content-addressed workspace snapshot.
- This is a remote-engine admission optimization. It is not a claim that a
  GitHub fetch should beat a local socket copy.

## Reference result

The current upload-pack prototype produced:

| Topology | Arm | Cold | Warm unchanged |
| --- | --- | ---: | ---: |
| Local Docker | Filesync | 9.158 s | 1.684 s |
| Local Docker | Client Git server | 37.261 s | 1.876 s |
| Dagger Cloud | Filesync | >300 s | Not run after timeout |
| Dagger Cloud | Client Git server | 14.897 s | 5.026 s |

The preceding thin-bundle prototype measured 45.074 s / 1.792 s locally and
15.087 s / 4.494 s on Cloud. Upload-pack is somewhat better on the local cold
path, but effectively equivalent to the bundle on Cloud. The large win comes
from avoiding remote filesync of `.git`, not from choosing bundle versus
upload-pack for the delta.

## Code tour

- `engine/session/git/git_snapshot.go` builds the synthetic commit, discovers
  the tracked upstream, and serves the commit with `git upload-pack`.
- `engine/session/git/git.proto` transports the bidirectional Git protocol and
  exact remote/base metadata. The old bundle RPC remains as compatibility
  fallback.
- `core/git_hostdir.go` fetches the common base and lets native Git negotiate
  the remaining objects directly with the client.
- `core/schema/workspace.go` and `core/schema/host.go` select and cache this
  path for an unfiltered Git workspace root, retaining filesync as fallback.

## Repeatable benchmark

The live demo intentionally exposes each step. For repeated, order-rotated
measurements, use `run.py`; it performs the same four arms, retains raw outputs,
and writes `RESULTS.md`:

```console
hack/bench/workspace-snapshot/run.py \
  --source . \
  --output /tmp/workspace-snapshot-benchmark \
  --rounds 3 \
  --timeout 300 \
  --prewarm-cloud \
  --arm local-filesync=registry.dagger.io/engine:v1.0.0-beta.13 \
  --arm local-git-server=localhost/dagger-workspace-candidate:latest \
  --cloud-arm cloud-filesync=registry.dagger.io/engine:v1.0.0-beta.13 \
  --cloud-arm cloud-git-server=localhost/dagger-workspace-candidate:latest,dagger.namespace-images.com/engine:tibor
```
