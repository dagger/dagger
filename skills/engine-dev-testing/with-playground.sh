#!/bin/sh

# Playground wrapper for testing dev engine changes.
# Designed to be called from Claude Code's Bash tool with run_in_background: true.
# Poll with TaskOutput to watch progress (heartbeat prints every 30s).
#
# If running synchronously instead, you MUST set Bash tool timeout >= 360000ms
# (the default 120s WILL kill the process mid-build).

if [ -z "$1" ]; then
  echo "Usage: $0 '<inner-command>'"
  echo "Example: $0 'dagger version'"
  exit 1
fi

if [ -n "$DAGGER_CLOUD_ENGINE" ] && [ "$DAGGER_CLOUD_ENGINE" != "0" ] && [ "$DAGGER_CLOUD_ENGINE" != "false" ] && [ -z "$DAGGER_MODULE" ]; then
  echo "WARNING: Running remotely via dagger cloud. Consider setting DAGGER_MODULE to a remote git branch to skip local file uploads (but git push first!)."
  echo "example: DAGGER_MODULE=github.com/dagger/dagger@upstream-branch $0 ..."
fi

# Timeout in seconds (default: 5 minutes). Set PLAYGROUND_TIMEOUT=0 to disable.
PLAYGROUND_TIMEOUT="${PLAYGROUND_TIMEOUT:-300}"

# Capture stdout and stderr SEPARATELY.
# Dagger cleanly separates them:
#   stdout = query result (the string returned by combined-output = inner command output)
#   stderr = progress trace (dots during build, full error trace on failure)
# Merging them (2>&1) causes the error trace to push inner command output out of view.
RESULT_FILE=$(mktemp "${TMPDIR:-/tmp}/playground-result.XXXXXX")
TRACE_FILE=$(mktemp "${TMPDIR:-/tmp}/playground-trace.XXXXXX")

# Clean up ALL child processes and temp files on exit
cleanup() {
  [ -n "$DAGGER_PID" ] && kill "$DAGGER_PID" 2>/dev/null
  [ -n "$WATCHDOG_PID" ] && kill "$WATCHDOG_PID" 2>/dev/null
  [ -n "$HEARTBEAT_PID" ] && kill "$HEARTBEAT_PID" 2>/dev/null
  rm -f "$RESULT_FILE" "$TRACE_FILE"
}
trap cleanup EXIT

echo "=== Playground: starting (timeout: ${PLAYGROUND_TIMEOUT}s) ==="

# Build the dagger engine playground, using the installed system dagger,
# with pre-downloaded sample source code for convenience,
# then execute the given inner command and capture the output.
#
# --progress=dots: compact progress during build (dots to stderr), with full
# error trace on failure (via frontendPretty in reportOnly mode). This gives
# us minimal noise on success and full crash context on failure.
#
# stdout (query result = inner command output) → RESULT_FILE
# stderr (progress trace + error summary)      → TRACE_FILE
dagger --progress=dots call \
  engine-dev \
  playground \
  with-directory --path=src/dagger --source=https://github.com/dagger/dagger#main \
  with-directory --path=src/demo-react-app --source=https://github.com/kpenfound/demo-react-app#main \
  with-new-file --path=/tmp/inner.sh --contents="$1" --permissions=0755 \
  with-exec --args=sh --args=/tmp/inner.sh \
  combined-output >"$RESULT_FILE" 2>"$TRACE_FILE" &

DAGGER_PID=$!

# Start watchdog timer (unless timeout is disabled)
if [ "$PLAYGROUND_TIMEOUT" -gt 0 ] 2>/dev/null; then
  (sleep "$PLAYGROUND_TIMEOUT" && kill "$DAGGER_PID" 2>/dev/null) &
  WATCHDOG_PID=$!
fi

# Heartbeat every 30s. Both output streams go to files, so these messages
# are the only direct stdout during the build — they confirm the process
# is alive for Claude (and the user watching).
(
  ELAPSED=0
  while kill -0 "$DAGGER_PID" 2>/dev/null; do
    sleep 30
    ELAPSED=$((ELAPSED + 30))
    echo "[playground: ${ELAPSED}s elapsed, still running...]"
  done
) &
HEARTBEAT_PID=$!

# Wait for dagger to finish (or be killed by watchdog)
wait "$DAGGER_PID"
EXIT_CODE=$?

echo ""

# --- Display results ---
# Inner command output first — this is what the caller cares about most.
RESULT_LINES=$(wc -l < "$RESULT_FILE" | tr -d ' ')
if [ "$RESULT_LINES" -gt 0 ]; then
  echo "=== Inner command output (${RESULT_LINES} lines) ==="
  cat "$RESULT_FILE"
else
  echo "=== Inner command output: (empty — pipeline may have failed before execution) ==="
fi

# Show progress trace on failure. It contains:
# - dots/X indicators showing which pipeline steps succeeded/failed
# - full error trace summary (from frontendPretty reportOnly mode)
# - panic stack traces if the engine crashed
# On success, the trace is just dots — not useful, so we skip it.
if [ "$EXIT_CODE" -ne 0 ]; then
  echo ""
  TRACE_LINES=$(wc -l < "$TRACE_FILE" | tr -d ' ')
  TRACE_TAIL=100
  if [ "$TRACE_LINES" -gt "$TRACE_TAIL" ]; then
    echo "=== Progress trace (last ${TRACE_TAIL} of ${TRACE_LINES} lines) ==="
    tail -"$TRACE_TAIL" "$TRACE_FILE"
  else
    echo "=== Progress trace (${TRACE_LINES} lines) ==="
    cat "$TRACE_FILE"
  fi

  # Extract panics if present but outside the tail window
  if grep -q 'panic:' "$TRACE_FILE" 2>/dev/null; then
    PANIC_IN_TAIL=$(tail -"$TRACE_TAIL" "$TRACE_FILE" | grep -c 'panic:' 2>/dev/null || true)
    if [ "${PANIC_IN_TAIL:-0}" -eq 0 ] 2>/dev/null; then
      echo ""
      echo "=== PANIC (extracted from earlier in trace) ==="
      grep -B 2 -A 40 'panic:' "$TRACE_FILE" | head -60
      echo "=== END PANIC ==="
    fi
  fi
fi

# --- Final status ---
echo ""
if [ "$EXIT_CODE" -eq 143 ]; then
  echo "=== TIMEOUT: killed after ${PLAYGROUND_TIMEOUT}s ==="
  exit 124
elif [ "$EXIT_CODE" -eq 0 ]; then
  echo "=== Playground: SUCCESS ==="
else
  echo "=== Playground: FAILED (exit code ${EXIT_CODE}) ==="
fi

exit "$EXIT_CODE"
