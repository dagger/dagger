#!/bin/sh
# PR #15224 "dang: bound received values kept per session".
#
# A Dang function call decodes its parent and argument JSON and keeps the
# decoded value, keyed by content, as a session-owned entry until the session
# ends. Every call on a growing object receives its whole state again, so the
# session keeps a decoded copy of every state it saw: quadratic in calls.
# Lists of numbers decode to many small values (~10x their JSON).
#
# Workload: in one session, CALLS chained calls of grower's `grow`, each
# adding GROW numbers to the object's state (grower/, a Dang module).
set -eu

CALLS=${CALLS:-250}
GROW=${GROW:-256}

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
