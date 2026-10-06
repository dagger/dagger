#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
runtime_dir=${XDG_RUNTIME_DIR:-/tmp}/dagger-workspace-snapshot-slides-${UID}
pid_file=$runtime_dir/server.pid
log_file=$runtime_dir/server.log
slide=$script_dir/benchmark-slide.html
server=$script_dir/slide-server.py
port=${WORKSPACE_SNAPSHOT_SLIDES_PORT:-8080}
url=http://localhost:$port/

[[ $port =~ ^[1-9][0-9]*$ && $port -le 65535 ]] || {
  echo "invalid slide server port: $port" >&2
  exit 2
}

running_pid() {
  [[ -f "$pid_file" ]] || return 1

  local pid
  pid=$(<"$pid_file")
  [[ $pid =~ ^[0-9]+$ ]] || return 1
  kill -0 "$pid" 2>/dev/null || return 1
  local command
  command=$(ps -p "$pid" -o args= 2>/dev/null) || return 1
  [[ $command == *"$server $slide --port $port"* ]] || return 1
  printf '%s\n' "$pid"
}

start() {
  local pid
  if pid=$(running_pid); then
    echo "slides already running at $url (pid $pid)"
    return
  fi

  rm -f "$pid_file"
  mkdir -p "$runtime_dir"
  nohup python3 "$server" "$slide" --port "$port" \
    >"$log_file" 2>&1 &
  pid=$!
  printf '%s\n' "$pid" >"$pid_file"

  for _ in {1..50}; do
    if ! kill -0 "$pid" 2>/dev/null; then
      rm -f "$pid_file"
      echo "failed to start slides server; see $log_file" >&2
      return 1
    fi
    if python3 - "$url" <<'PY'
import sys
import urllib.request

with urllib.request.urlopen(sys.argv[1], timeout=0.2) as response:
    raise SystemExit(0 if response.status == 200 else 1)
PY
    then
      if ! kill -0 "$pid" 2>/dev/null; then
        rm -f "$pid_file"
        echo "failed to start slides server; see $log_file" >&2
        return 1
      fi
      echo "slides running at $url (pid $pid)"
      return
    fi
    sleep 0.1
  done

  kill "$pid" 2>/dev/null || true
  rm -f "$pid_file"
  echo "slides server did not become ready; see $log_file" >&2
  return 1
}

stop() {
  local pid
  if ! pid=$(running_pid); then
    rm -f "$pid_file"
    echo "slides are not running"
    return
  fi

  kill "$pid"
  for _ in {1..50}; do
    if ! kill -0 "$pid" 2>/dev/null; then
      rm -f "$pid_file"
      echo "stopped slides server (pid $pid)"
      return
    fi
    sleep 0.1
  done

  echo "slides server did not stop cleanly (pid $pid)" >&2
  return 1
}

case ${1:-} in
  start) start ;;
  stop) stop ;;
  *)
    echo "usage: $0 start|stop" >&2
    exit 2
    ;;
esac
