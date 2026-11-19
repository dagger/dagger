#!/bin/sh
# #15226 with a Go module (not a cake demo: see fixtures/15226 for the one
# the cake runs). Measure it with measureWorkload, e.g.
#   memoryCake.measureWorkload(pr: "15226-go", ref: ...,
#     workload: <this directory>,
#     focus: "moduleFunctionReceiver|convertedReceivers")
#
# CALLS chained calls of grower's `grow` (grower/, a Go module), each adding
# GROW bytes to the object's state: every call converts a new receiver to
# JSON (moduleFunctionReceiverJSON), ~178 MiB at the defaults. Bounding
# module-receiver-sdk-input's cache (#15226) does not free it here: the Go
# runtime reads its call with currentFunctionCall { parent ... }
# (DispatchInputs), and the session keeps a FunctionCall.currentFunctionCall
# result (the *FunctionCall, with the receiver JSON as Parent) and a
# JSON.parent result (the same bytes) per call until it ends.
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
