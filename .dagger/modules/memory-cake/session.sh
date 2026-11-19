#!/bin/sh
# Runs under `dagger run` (see driver.sh): DAGGER_SESSION_PORT/TOKEN name the
# open session, so `dagger` commands in the workload join it. The live
# profile is taken here, before returning, while that session is still open.
set -u
cd /fixture
sh ./workload.sh >/out/workload.log 2>&1
echo $? >/out/workload.exit
curl -sSf --max-time 300 "http://dagger-engine:6060/debug/pprof/heap?gc=1" -o /out/live.pb.gz
curl -sSf --max-time 120 http://dagger-engine:6060/debug/vars -o /out/live-vars.json
curl -sSf --max-time 120 http://dagger-engine:6060/debug/client-lifecycle -o /out/live-clients.json
true
