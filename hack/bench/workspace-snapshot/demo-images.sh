#!/usr/bin/env bash
set -euo pipefail

demo_root=${WORKSPACE_SNAPSHOT_DEMO_ROOT:-/tmp/workspace-snapshot-demo}
source_root=$(git rev-parse --show-toplevel)
filesync_image=${WORKSPACE_SNAPSHOT_FILESYNC_IMAGE:-registry.dagger.io/engine:v1.0.0-beta.13}
candidate_image=${WORKSPACE_SNAPSHOT_CANDIDATE_IMAGE:-localhost/dagger-workspace-candidate:latest}

mkdir -p "$demo_root"

extract_cli() {
  local image=$1
  local output=$2
  local container
  container=$(docker create --entrypoint /bin/true "$image")
  trap 'docker rm -v "$container" >/dev/null 2>&1 || true' RETURN
  docker cp "$container:/usr/local/bin/dagger" "$output"
  chmod +x "$output"
  docker rm -v "$container" >/dev/null
  trap - RETURN
}

docker pull "$filesync_image"
(
  cd "$source_root"
  dagger call engine-dev \
    --ws . \
    load-to-docker \
    --docker /var/run/docker.sock \
    --name "$candidate_image" \
    image
)

extract_cli "$filesync_image" "$demo_root/dagger-filesync"
extract_cli "$candidate_image" "$demo_root/dagger-candidate"

echo "filesync CLI: $($demo_root/dagger-filesync version | head -1)"
echo "candidate CLI: $($demo_root/dagger-candidate version | head -1)"

if [[ ${1:-} == "--publish" ]]; then
  docker tag "$candidate_image" ghcr.io/dagger/engine:tibor
  docker push ghcr.io/dagger/engine:tibor
  echo "Cloud image:  dagger.namespace-images.com/engine:tibor"
fi
