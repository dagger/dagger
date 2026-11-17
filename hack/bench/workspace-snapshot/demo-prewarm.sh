#!/usr/bin/env bash
set -euo pipefail

demo_root=${WORKSPACE_SNAPSHOT_DEMO_ROOT:-/tmp/workspace-snapshot-demo}
cloud_candidate_image=${WORKSPACE_SNAPSHOT_CLOUD_CANDIDATE_IMAGE:-dagger.namespace-images.com/engine:tibor}

case ${1:-} in
  filesync)
    cli=$demo_root/dagger-filesync
    cloud_env=()
    ;;
  candidate)
    cli=$demo_root/dagger-candidate
    cloud_env=("_EXPERIMENTAL_DAGGER_CLOUD_ENGINE_IMAGE=$cloud_candidate_image")
    ;;
  *)
    echo "usage: $0 filesync|candidate" >&2
    exit 2
    ;;
esac

[[ -x "$cli" ]] || {
  echo "matching CLI missing; run demo-images.sh first" >&2
  exit 1
}

mkdir -p "$demo_root/cloud-$1"
echo "prewarming Cloud $1 engine without loading currentWorkspace"
time env \
  XDG_STATE_HOME="$demo_root/cloud-$1/state" \
  "${cloud_env[@]}" \
  "$cli" --silent --engine cloud api query -M <<<'{ defaultPlatform }'
