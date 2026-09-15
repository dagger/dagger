#!/usr/bin/env sh

export DAGGER_NO_NAG=1
export LOOPS_BEFORE_GC=10
export WAIT_BETWEEN_RUNS=5

while true
do
  for i in $(seq 1 $LOOPS_BEFORE_GC)
  do
    echo
    echo "LOOP $i/$LOOPS_BEFORE_GC BEFORE MANUAL GC"

    echo
    # We are using a date that will always be different so that we cache bust on every call
    # We write 1MB of data to disk to see the effects of cache pruning on metrics
    dagger -c "container | from alpine | with-exec dd if=/dev/zero of=$(date -Iseconds) bs=1M count=1"

    echo
    echo "SLEEPING FOR ${WAIT_BETWEEN_RUNS}s..."
    sleep $WAIT_BETWEEN_RUNS
  done

  echo
  echo "RUNNING MANUAL GC..."
  dagger core engine local-cache prune
done
