# Dagger shell workload for sizing dehydration of a live session's results
# (cmd/engine/debug.go /debug/dagql/dehydrate). Run it against a dev engine
# with its debug server on :6060, e.g. `dagger -c "$(cat dehydrate-workload.sh)"`.
#
# The engine fetches its own debug endpoints through `http`. The pass runs
# detached (async=1) and after a delay, while the session is idle in a sleep,
# so that it never races the session's own calls. The shell is lazy: every
# value assigned is used, or its pipeline never runs. mode=dropall (instead of
# the default pass) measures the upper bound and breaks the engine.
RUN=run1
h=$(http "http://127.0.0.1:6060/debug/dagql/heap?rate=4096&save=start&phase=start-$RUN" | contents)
.echo "HEAP-START $h"
d=$(directory)
for i in {1..1000}; do
  d=$($d | with-new-file "f$i" "content of file $i in run $RUN")
done
base=$(container | from alpine:3.20 | with-directory /data $d)
c=$base
for j in {1..40}; do
  c=$($c | with-new-file "/in$j" "exec $j run $RUN" | with-exec -- sh -c "cat /in$j > /out$j && ls /data | wc -l >> /out$j")
done
d1=$($d | digest)
c1=$($c | file /out3 | contents)
e1=$($c | directory /data | digest)
.echo "EVAL-DONE $d1"
r=$(http "http://127.0.0.1:6060/debug/dagql/dehydrate?async=1&delay=20s&save=$RUN&phase=$RUN" | contents)
.echo "STARTED $r"
s=$(container | from alpine:3.20 | with-exec -- sh -c "sleep 60; echo $RUN" | stdout)
.echo "SLEPT $s"
r2=$(http "http://127.0.0.1:6060/debug/dagql/dehydrate/last?phase=$RUN" | contents)
.echo "DEHYDRATE $r2"
d2=$($d | digest)
c2=$($c | file /out3 | contents)
e2=$($c | directory /data | digest)
.echo "CMP-D $d1 $d2"
.echo "CMP-C $c1 / $c2"
.echo "CMP-E $e1 $e2"
h=$(http "http://127.0.0.1:6060/debug/dagql/heap?save=end&phase=end-$RUN" | contents)
.echo "HEAP-END $h"
# Heap profiles: go tool pprof -top -diff_base=http://<engine>:6060/debug/dagql/heapprofile?name=start http://<engine>:6060/debug/dagql/heapprofile?name=$RUN-before
