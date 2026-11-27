#!/bin/sh
# Generates /d: $DIRS directories of 100 small files each (DIRS is prepended
# by workload.sh).
set -eu
mkdir -p /d
cd /d
i=0
while [ "$i" -lt "$DIRS" ]; do
  mkdir "d$i"
  j=0
  while [ "$j" -lt 100 ]; do
    echo "file $i/$j" >"d$i/f$j"
    j=$((j + 1))
  done
  i=$((i + 1))
done
