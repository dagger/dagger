#!/bin/sh

set -o errexit
set -o nounset

#########################################################################################################################################
# DISCLAIMER																																																														#
# Copied from https://github.com/moby/moby/blob/ed89041433a031cafc0a0f19cfe573c31688d377/hack/dind#L28-L37															#
# Permission granted by Akihiro Suda <akihiro.suda.cz@hco.ntt.co.jp> (https://github.com/k3d-io/k3d/issues/493#issuecomment-827405962)	#
# Moby License Apache 2.0: https://github.com/moby/moby/blob/ed89041433a031cafc0a0f19cfe573c31688d377/LICENSE														#
#########################################################################################################################################
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
  echo "[$(date -Iseconds)] [CgroupV2 Fix] Evacuating Root Cgroup ..."
  mkdir -p /sys/fs/cgroup/init
  controllers=$(sed -e 's/ / +/g' -e 's/^/+/' <"/sys/fs/cgroup/cgroup.controllers")
  # Retry, as moby does since https://github.com/moby/moby/commit/c43aa0b6aa7c88343f0951ba9a39c69aa51c54ef:
  # a process that joins the root group after the move makes the write fail.
  attempt=1
  until
    # move the processes from the root group to the /init group,
    # otherwise writing subtree_control fails with EBUSY.
    xargs -rn1 < /sys/fs/cgroup/cgroup.procs > /sys/fs/cgroup/init/cgroup.procs || :
    # enable controllers; unlike sed, echo reports why a write failed
    echo "$controllers" >"/sys/fs/cgroup/cgroup.subtree_control"
  do
    if [ "$attempt" -ge 10 ]; then
      echo "[$(date -Iseconds)] [CgroupV2 Fix] Failed to enable controllers after $attempt attempts" >&2
      for f in cgroup.type cgroup.controllers cgroup.subtree_control; do
        echo "$f: $(cat "/sys/fs/cgroup/$f")" >&2
      done
      for pid in $(head -n 20 /sys/fs/cgroup/cgroup.procs); do
        echo "still in root group: $pid $(tr '\0' ' ' <"/proc/$pid/cmdline" 2>/dev/null)" >&2
      done
      exit 1
    fi
    attempt=$((attempt + 1))
    sleep 0.1
  done
  echo "[$(date -Iseconds)] [CgroupV2 Fix] Done (attempt $attempt)"
fi

exec "$@"
