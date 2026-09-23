package core

import (
	"fmt"
	"strings"
)

type upVerifyBounds struct {
	prepare, ready, probe, shutdown int
}

func upVerifyScript(upArgs, url, expected, success string, bounds upVerifyBounds) string {
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	args := []string{"set --"}
	for _, arg := range strings.Fields(upArgs) {
		args = append(args, quote(arg))
	}
	return strings.Join(args, " ") + "\n" + fmt.Sprintf(`
PREPARE_TIMEOUT=%d
READY_TIMEOUT=%d
PROBE_TIMEOUT=%d
SHUTDOWN_TIMEOUT=%d
URL=%s
EXPECTED=%s
SUCCESS=%s
DAGGER_PID=
STATE=$(mktemp -d)

stop_dagger() {
	[ -n "$DAGGER_PID" ] || return 0
	STARTED=$(date +%%s)
	# A separate process group lets cleanup kill both the watchdog and its
	# sleep. The watchdog bounds the parent's wait even if dagger ignores TERM.
	setsid sh -c 'sleep "$1"; touch "$2"; kill -KILL "$3" 2>/dev/null' sh \
		"$SHUTDOWN_TIMEOUT" "$STATE/shutdown-timeout" "$DAGGER_PID" </dev/null >/dev/null 2>&1 &
	WATCHDOG_PID=$!
	kill -TERM "$DAGGER_PID" 2>/dev/null || :
	wait "$DAGGER_PID" 2>/dev/null || :
	DAGGER_PID=
	kill -KILL "$WATCHDOG_PID" 2>/dev/null || :
	kill -KILL "-$WATCHDOG_PID" 2>/dev/null || :
	wait "$WATCHDOG_PID" 2>/dev/null || :
	if [ -f "$STATE/shutdown-timeout" ]; then
		echo "FAIL: dagger up shutdown timed out after $(($(date +%%s) - STARTED))s (budget ${SHUTDOWN_TIMEOUT}s)"
		return 1
	fi
}
cleanup() {
	STATUS=$?
	trap - EXIT
	stop_dagger || STATUS=1
	rm -rf "$STATE"
	exit "$STATUS"
}
trap cleanup EXIT

STARTED=$(date +%%s)
echo "START: module-loading preparation (budget ${PREPARE_TIMEOUT}s)"
timeout -s KILL "$PREPARE_TIMEOUT" dagger up -l "$@"
STATUS=$?
ELAPSED=$(($(date +%%s) - STARTED))
if [ "$STATUS" -ne 0 ]; then
	echo "FAIL: module-loading preparation failed after ${ELAPSED}s (budget ${PREPARE_TIMEOUT}s, exit $STATUS)"
	exit 1
fi
echo "DONE: module-loading preparation after ${ELAPSED}s"

dagger up "$@" &
DAGGER_PID=$!
STARTED=$(date +%%s)
DEADLINE=$((STARTED + READY_TIMEOUT))
while :; do
	REMAINING=$((DEADLINE - $(date +%%s)))
	if [ "$REMAINING" -le 0 ]; then
		echo "FAIL: service readiness timed out after $(($(date +%%s) - STARTED))s (budget ${READY_TIMEOUT}s)"
		exit 1
	fi
	PROBE=$PROBE_TIMEOUT
	[ "$PROBE" -le "$REMAINING" ] || PROBE=$REMAINING
	if timeout -s KILL "$PROBE" wget -q --spider "$URL" 2>/dev/null; then
		break
	fi
	REMAINING=$((DEADLINE - $(date +%%s)))
	[ "$REMAINING" -gt 0 ] || continue
	PAUSE=2
	[ "$PAUSE" -le "$REMAINING" ] || PAUSE=$REMAINING
	sleep "$PAUSE"
done

STARTED=$(date +%%s)
BODY=$(timeout -s KILL "$PROBE_TIMEOUT" wget -qO- "$URL" 2>/dev/null)
STATUS=$?
if [ "$STATUS" -ne 0 ]; then
	echo "FAIL: service response read failed after $(($(date +%%s) - STARTED))s (budget ${PROBE_TIMEOUT}s, exit $STATUS)"
	exit 1
fi
echo "$BODY" | grep -qi "$EXPECTED" || {
	echo "FAIL: expected $EXPECTED in response, got: $BODY"
	exit 1
}
stop_dagger || exit 1
echo "$SUCCESS"
`, bounds.prepare, bounds.ready, bounds.probe, bounds.shutdown, quote(url), quote(expected), quote(success))
}
