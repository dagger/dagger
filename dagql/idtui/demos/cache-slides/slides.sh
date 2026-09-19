#!/usr/bin/env bash
set -euo pipefail

readonly port=8080
readonly host=127.0.0.1
readonly slides_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly runtime_dir="${XDG_RUNTIME_DIR:-/tmp}"
readonly pid_file="${runtime_dir}/dagger-cache-slides-${UID}-${port}.pid"
readonly log_file="${runtime_dir}/dagger-cache-slides-${UID}-${port}.log"

usage() {
  echo "Usage: $0 {start|stop}"
}

is_slides_server() {
  local server_pid="$1"
  [[ "$server_pid" =~ ^[0-9]+$ ]] || return 1
  [[ -r "/proc/${server_pid}/cmdline" ]] || return 1

  local command_line
  command_line="$(tr '\0' ' ' <"/proc/${server_pid}/cmdline")"
  [[ "$command_line" == *"python3 -m http.server ${port} --bind ${host} --directory ${slides_dir}"* ]]
}

start() {
  if [[ -f "$pid_file" ]]; then
    local existing_pid
    existing_pid="$(<"$pid_file")"
    if is_slides_server "$existing_pid"; then
      echo "Slides already running at http://localhost:${port} (PID ${existing_pid})"
      return
    fi
    rm -f "$pid_file"
  fi

  nohup python3 -m http.server "$port" \
    --bind "$host" \
    --directory "$slides_dir" \
    >"$log_file" 2>&1 </dev/null &
  local server_pid=$!
  echo "$server_pid" >"$pid_file"

  sleep 0.25
  if ! kill -0 "$server_pid" 2>/dev/null; then
    rm -f "$pid_file"
    echo "Failed to start slides server. See ${log_file}" >&2
    return 1
  fi

  echo "Slides running at http://localhost:${port} (PID ${server_pid})"
}

stop() {
  if [[ ! -f "$pid_file" ]]; then
    echo "Slides are not running"
    return
  fi

  local server_pid
  server_pid="$(<"$pid_file")"
  if is_slides_server "$server_pid"; then
    kill "$server_pid"
    for _ in {1..20}; do
      kill -0 "$server_pid" 2>/dev/null || break
      sleep 0.1
    done
    if kill -0 "$server_pid" 2>/dev/null; then
      echo "Slides server did not stop (PID ${server_pid})" >&2
      return 1
    fi
  fi
  rm -f "$pid_file"
  echo "Slides stopped"
}

case "${1:-}" in
  start) start ;;
  stop) stop ;;
  *) usage; exit 2 ;;
esac
