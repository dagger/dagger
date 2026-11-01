#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source_root=$(git -C "$script_dir" rev-parse --show-toplevel)

topology=local
rounds=${WORKSPACE_SNAPSHOT_ROUNDS:-1}
timeout=${WORKSPACE_SNAPSHOT_TIMEOUT:-600}
output=
cli=${WORKSPACE_SNAPSHOT_CLI:-}

image=${WORKSPACE_SNAPSHOT_IMAGE:-}

usage() {
  cat <<'EOF'
Benchmark one dagger/dagger workspace using filesync with .git, filesync
without .git, and Git bundle. Runs locally by default; pass --engine cloud for
Dagger Cloud.

Usage:
  benchmark.sh [--engine cloud] [options]

Options:
  --engine local|cloud       Engine topology (default: local)
  --rounds N                 Number of order-rotated rounds (default: 1)
  --timeout SECONDS          Timeout for each query (default: 600)
  --output PATH              Results directory (default: timestamped /tmp path)
  --image IMAGE              Engine image: local Docker ref, or Cloud-pullable ref
  --cli PATH                 Matching native CLI (default: build from this checkout)
  -h, --help                 Show this help

All three arms use this one native CLI and engine image. The benchmark creates
one fixture and uses it for every arm.
EOF
}

require_value() {
  local option=$1
  local value=${2:-}
  if [[ -z $value ]]; then
    echo "$option requires a value" >&2
    exit 2
  fi
}

while (($#)); do
  case $1 in
    --engine)
      require_value "$1" "${2:-}"
      topology=$2
      shift 2
      ;;
    --rounds)
      require_value "$1" "${2:-}"
      rounds=$2
      shift 2
      ;;
    --timeout)
      require_value "$1" "${2:-}"
      timeout=$2
      shift 2
      ;;
    --output)
      require_value "$1" "${2:-}"
      output=$2
      shift 2
      ;;
    --image)
      require_value "$1" "${2:-}"
      image=$2
      shift 2
      ;;
    --cli)
      require_value "$1" "${2:-}"
      cli=$2
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

case $topology in
  local|cloud) ;;
  *)
    echo "--engine must be local or cloud" >&2
    exit 2
    ;;
esac

if [[ -z $image ]]; then
  if [[ $topology == cloud ]]; then
    echo "Cloud mode requires --image with a registry image that Dagger Cloud can pull" >&2
    exit 2
  fi
  image=localhost/dagger-workspace-candidate:latest
fi

[[ $rounds =~ ^[1-9][0-9]*$ ]] || {
  echo "--rounds must be a positive integer" >&2
  exit 2
}
[[ $timeout =~ ^[1-9][0-9]*$ ]] || {
  echo "--timeout must be a positive integer" >&2
  exit 2
}

if [[ -z $output ]]; then
  output=/tmp/workspace-snapshot-3way-${topology}-$(date +%Y%m%d-%H%M%S)
fi

cli_tmp=
if [[ -z $cli ]]; then
  cli_tmp=$(mktemp -d "${TMPDIR:-/tmp}/workspace-snapshot-cli.XXXXXX")
  trap 'rm -rf -- "$cli_tmp"' EXIT
  cli=$cli_tmp/dagger
  echo "building native CLI from $source_root"
  (cd "$source_root" && go build -o "$cli" ./cmd/dagger)
elif [[ ! -x $cli ]]; then
  echo "--cli must name an executable native Dagger CLI: $cli" >&2
  exit 2
fi

args=(
  --source "$source_root"
  --output "$output"
  --rounds "$rounds"
  --timeout "$timeout"
)

if [[ $topology == local ]]; then
  args+=(
    --arm "filesync-with-git=$cli,$image,filesync-with-git"
    --arm "filesync-without-git=$cli,$image,filesync-without-git"
    --arm "git-bundle=$cli,$image,git-bundle"
  )
else
  args+=(
    --prewarm-cloud
    --cloud-arm "filesync-with-git=$cli,$image,filesync-with-git"
    --cloud-arm "filesync-without-git=$cli,$image,filesync-without-git"
    --cloud-arm "git-bundle=$cli,$image,git-bundle"
  )
fi

echo "topology: $topology"
echo "fixture:  one dagger/dagger clone shared by all arms"
echo "output:   $output"
echo "CLI:       $cli"
echo "engine:    $image"
echo "cases:     filesync-with-git, filesync-without-git, git-bundle"

"$script_dir/run.py" "${args[@]}"
