#!/bin/sh
# PR #15223 "dang: bound prepared programs kept per session".
#
# A Dang function call runs its module from a prepared (parsed and
# type-checked) program, cached as a session-owned entry keyed by the
# module's source ID, schema ID and mount directory. An edited module gets a
# new source ID and a remounted one a new directory, so a session keeps one
# prepared program per variant it ever called, until it ends. Each holds the
# module's syntax and the API types it inferred.
#
# Workload: in one session, VARIANTS copies of a Dang module with FUNCS
# functions (gen.sh), each differing only by a comment so each has its own
# source ID, and call `ping` on each with `dagger call -m`.
set -eu

VARIANTS=${VARIANTS:-40}
FUNCS=${FUNCS:-300}

work=$(mktemp -d)
i=0
while [ "$i" -lt "$VARIANTS" ]; do
  sh ./gen.sh "$work/v$i" "$i" "$FUNCS"
  dagger call -m "$work/v$i" ping >/dev/null
  i=$((i + 1))
  echo "variants: $i/$VARIANTS"
done
