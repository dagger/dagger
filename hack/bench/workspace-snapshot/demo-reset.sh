#!/usr/bin/env bash
set -euo pipefail

case ${1:-} in
  filesync) engine_name=dagger-demo-filesync ;;
  candidate) engine_name=dagger-demo-candidate ;;
  *)
    echo "usage: $0 filesync|candidate" >&2
    exit 2
    ;;
esac

docker rm -fv "$engine_name" >/dev/null 2>&1 || true
docker volume rm -f "$engine_name" >/dev/null 2>&1 || true
echo "reset local engine and volume: $engine_name"
