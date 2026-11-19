#!/bin/sh
# PR #15226 "core: bound receivers a session keeps converted".
#
# Before calling a module function, the engine converts the receiver (the
# parent object) to JSON and keeps it as a session-owned entry, one per
# receiver result, until the session ends. An object never changes in place,
# so every call on an evolving object has a new receiver, and the session
# keeps the JSON of every receiver it called.
#
# Workload: in one session, fill a holder (holder/, a Dang module) with SIZE
# bytes of state, take CALLS distinct results of it (touch(i) returns the
# same state) and call `size` on each. Each touched result is a new receiver
# whose JSON is kept; their content is the same, so the Dang SDK's decoded
# copies (#15224, keyed by content) stay one.
#
# A Go module would not show it: with 150 calls on a growing Go object, the
# fix's branch kept the same 178 MiB of receiver JSON as main. There the JSON
# is held for the session by the function call's own results too: the Go
# runtime reads its call through Query.currentFunctionCall and
# FunctionCall.parent, session-owned dagql results holding the call (and its
# Parent JSON), so bounding the cache frees nothing (see fixtures/15226-go).
set -eu

CALLS=${CALLS:-200}
SIZE=${SIZE:-1048576}

q="{ fill(n: $SIZE) {"
i=0
while [ "$i" -lt "$CALLS" ]; do
  q="$q t$i: touch(i: $i) { size }"
  i=$((i + 1))
done
q="$q } }"
echo "$q" | dagger api query -m "$(pwd)/holder" >/dev/null
