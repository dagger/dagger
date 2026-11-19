#!/bin/sh
# #15226 with a Go module run through a Dang module entrypoint, as the Go SDK
# generates with dangEntrypoint (not a cake demo). The same workload as
# fixtures/15226-go, against grower/ here: its Grower is the same Go code,
# dispatched by entrypoint/main.dang execing the module's binary with the
# call request on stdin, instead of the legacy Go runtime reading its call
# through currentFunctionCall. Measure it with measureWorkload, e.g.
#   memoryCake.measureWorkload(pr: "15226-go-dang", ref: ...,
#     workload: <this directory>,
#     focus: "moduleFunctionReceiver|convertedReceivers", env: ["LINGER=90"])
#
# The engine passes the receiver JSON to the entrypoint's call() as an
# argument, in process (core/sdk/dang/v2/entrypoint.go), so no
# currentFunctionCall or FunctionCall.parent result keeps it, and #15226's
# bound holds. The entrypoint's own copies are another matter: the decoded
# argument (#15224's cache), the exec's stdin request and the result file's
# contents are each a copy of the state per call.
#
# Before returning, it writes what the session's dagql cache holds:
# results per type.field (all and session-owned) to /out/cache-fields.tsv,
# completed arbitrary entries per key prefix to /out/cache-arbitrary.tsv.
set -eu

CALLS=${CALLS:-150}
GROW=${GROW:-16384}

# The main object's functions are on Query too: the first grow is the
# constructor's.
q='{'
i=0
while [ "$i" -lt "$CALLS" ]; do
  q="$q grow(n: $GROW) {"
  i=$((i + 1))
done
q="$q size"
i=0
while [ "$i" -lt "$CALLS" ]; do
  q="$q }"
  i=$((i + 1))
done
q="$q }"
echo "$q" | dagger api query -m "$(pwd)/grower"

# Seconds to wait, the session still open, before the live heap. Right after
# the query, the client runtimes of many calls are still closed-retained,
# behind their exec's nested client, and each holds its *FunctionCall with
# the receiver JSON; after ~90 s they are reclaimed, leaving what the
# session's caches and results keep.
sleep "${LINGER:-0}"

if [ -d /out ]; then
  snap=$(mktemp)
  curl -sSf --max-time 300 http://dagger-engine:6060/debug/dagql/cache -o "$snap" || true
  # <type.field> <results> <session-owned results>
  jq -r '
    ([(.session_results // [])[].shared_result_ids[]] | map({key: tostring, value: true}) | from_entries) as $owned
    | .results
    | map({f: ((.type_name // "?") + "." + (.result_call.field // "?")),
           owned: ($owned[(.shared_result_id | tostring)] // false)})
    | group_by(.f)
    | map([.[0].f, length, (map(select(.owned)) | length)] | @tsv)
    | .[]' "$snap" | sort -t "$(printf '\t')" -k2 -nr >/out/cache-fields.tsv || true
  # <arbitrary key prefix> <completed entries>
  jq -r '(.completed_arbitrary_calls // []) | map(.call_key | sub(":.*"; "")) | group_by(.) | map([.[0], length] | @tsv) | .[]' \
    "$snap" >/out/cache-arbitrary.tsv || true
  rm -f "$snap"
fi
