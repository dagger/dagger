//go:build ignore

#include "common.h"

char LICENSE[] SEC("license") = "GPL";

/* Execs measured at once, and threads waiting for a CPU at once. A thread
 * whose wait finds the waiting map full isn't counted. */
#define MAX_EXECS 4096
#define MAX_WAITING 65536
#define TASK_RUNNING 0

/* The kernel structures this program reads, as CO-RE views: libbpf matches
 * each to the running kernel's type of the name before the triple
 * underscore, so only the fields named here must exist. */
struct kernfs_node___execsched {
    u64 id;
} __attribute__((preserve_access_index));

struct cgroup_subsys_state___execsched {
    struct cgroup_subsys_state___execsched *parent;
} __attribute__((preserve_access_index));

struct cgroup___execsched {
    struct cgroup_subsys_state___execsched self;
    struct kernfs_node___execsched *kn;
} __attribute__((preserve_access_index));

struct css_set___execsched {
    struct cgroup___execsched *dfl_cgrp;
} __attribute__((preserve_access_index));

struct task_struct___execsched {
    unsigned int __state;
    pid_t pid;
    pid_t tgid;
    struct css_set___execsched *cgroups;
} __attribute__((preserve_access_index));

struct linux_binprm___execsched {
    const char *filename;
} __attribute__((preserve_access_index));

/* What the program measured of one exec, keyed by the ID of its resource
 * cgroup. Times are bpf_ktime_get_ns's, 0 until seen. The workload runs in
 * the resource cgroup's "container" child: its first process is crun's
 * child, which execs the engine's init (/.init); the init then starts its
 * session helper (/.init-session) and the workload. The exec's mount
 * helpers run in leaves under its "sshfs" child. */
struct exec_sched {
    /* When crun's child was created in the container cgroup. */
    u64 container_start_ns;
    /* The first exec in the container cgroup, the init's. */
    u64 init_exec_ns;
    /* The first exec there by another process, of a file other than the
     * engine's own (/.init...): the workload's. */
    u64 workload_exec_ns;
    /* When the workload's and the init's processes exited (their main
     * threads). */
    u64 workload_exit_ns;
    u64 init_exit_ns;
    /* The time the exec's threads, workload and mount helpers alike, waited
     * runnable for a CPU, how many waits ended, and the time they ran. */
    u64 runq_wait_ns;
    u64 runq_waits;
    u64 oncpu_ns;
    u32 init_tgid;
    u32 workload_tgid;
};

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, MAX_EXECS);
    __type(key, u64);
    __type(value, struct exec_sched);
} execs SEC(".maps");

/* A thread's exec (the ID of its resource cgroup, 0 when none), and since
 * when it has been waiting for a CPU, or running on one. */
struct exec_since {
    u64 since_ns;
    u64 exec_id;
};

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, MAX_WAITING);
    __type(key, u32);
    __type(value, struct exec_since);
} waiting SEC(".maps");

/* Each CPU's running thread. */
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, u32);
    __type(value, struct exec_since);
} running SEC(".maps");

static __always_inline struct cgroup___execsched *
cgroup_parent(struct cgroup___execsched *cg)
{
    struct cgroup_subsys_state___execsched *css = BPF_CORE_READ(cg, self.parent);
    if (!css)
        return NULL;
    return (void *)css - bpf_core_field_offset(struct cgroup___execsched, self);
}

/* The measured exec a task belongs to: its cgroup or one of the two above it
 * (the exec's resource cgroup holds the container cgroup and the sshfs
 * cgroup, which holds a leaf per mount helper). depth is how far up the exec
 * was found: 1 for the container cgroup. NULL for a task outside every
 * measured exec. */
static __always_inline struct exec_sched *
exec_of(struct task_struct___execsched *task, u64 *exec_id, int *depth)
{
    struct cgroup___execsched *cg = BPF_CORE_READ(task, cgroups, dfl_cgrp);
#pragma unroll
    for (int i = 0; i < 3 && cg; i++) {
        u64 id = BPF_CORE_READ(cg, kn, id);
        struct exec_sched *e = bpf_map_lookup_elem(&execs, &id);
        if (e) {
            *exec_id = id;
            *depth = i;
            return e;
        }
        cg = cgroup_parent(cg);
    }
    return NULL;
}

/* A thread of a measured exec starts waiting for a CPU. */
static __always_inline void start_wait(struct task_struct___execsched *p, u64 now)
{
    u32 tid = BPF_CORE_READ(p, pid);
    if (!tid)
        return;
    u64 exec_id;
    int depth;
    if (!exec_of(p, &exec_id, &depth))
        return;
    struct exec_since w = {.since_ns = now, .exec_id = exec_id};
    bpf_map_update_elem(&waiting, &tid, &w, BPF_ANY);
}

/* The tracepoints' arguments are declared as the CO-RE views of their kernel
 * types; BPF_PROG only casts them. */

SEC("tp_btf/sched_wakeup")
int BPF_PROG(on_wakeup, struct task_struct___execsched *p)
{
    start_wait(p, bpf_ktime_get_ns());
    return 0;
}

SEC("tp_btf/sched_wakeup_new")
int BPF_PROG(on_wakeup_new, struct task_struct___execsched *p)
{
    start_wait(p, bpf_ktime_get_ns());
    return 0;
}

SEC("tp_btf/sched_switch")
int BPF_PROG(on_switch, unsigned int preempt, struct task_struct___execsched *prev,
             struct task_struct___execsched *next)
{
    u64 now = bpf_ktime_get_ns();

    /* A thread switched out while still runnable waits again from now. */
    if (BPF_CORE_READ(prev, __state) == TASK_RUNNING)
        start_wait(prev, now);

    /* The thread switched in stops waiting. */
    u32 tid = BPF_CORE_READ(next, pid);
    struct exec_since *w = bpf_map_lookup_elem(&waiting, &tid);
    if (w) {
        struct exec_sched *e = bpf_map_lookup_elem(&execs, &w->exec_id);
        if (e && now > w->since_ns) {
            __sync_fetch_and_add(&e->runq_wait_ns, now - w->since_ns);
            __sync_fetch_and_add(&e->runq_waits, 1);
        }
        bpf_map_delete_elem(&waiting, &tid);
    }

    /* The CPU's time so far goes to the exec of the thread switched out. */
    u32 zero = 0;
    struct exec_since *r = bpf_map_lookup_elem(&running, &zero);
    if (!r)
        return 0;
    if (r->exec_id && now > r->since_ns) {
        struct exec_sched *e = bpf_map_lookup_elem(&execs, &r->exec_id);
        if (e)
            __sync_fetch_and_add(&e->oncpu_ns, now - r->since_ns);
    }
    u64 exec_id = 0;
    int depth;
    if (!tid || !exec_of(next, &exec_id, &depth))
        exec_id = 0;
    r->exec_id = exec_id;
    r->since_ns = now;
    return 0;
}

SEC("tp_btf/sched_process_fork")
int BPF_PROG(on_fork, struct task_struct___execsched *parent,
             struct task_struct___execsched *child)
{
    u64 exec_id;
    int depth;
    struct exec_sched *e = exec_of(child, &exec_id, &depth);
    if (e && depth == 1 && !e->container_start_ns)
        e->container_start_ns = bpf_ktime_get_ns();
    return 0;
}

/* Whether an exec'd file is one of the engine's own, /.init or
 * /.init-session. */
static __always_inline int engine_file(struct linux_binprm___execsched *bprm)
{
    char name[7] = {};
    bpf_probe_read_kernel_str(name, sizeof(name), BPF_CORE_READ(bprm, filename));
    return name[0] == '/' && name[1] == '.' && name[2] == 'i' && name[3] == 'n' &&
           name[4] == 'i' && name[5] == 't';
}

SEC("tp_btf/sched_process_exec")
int BPF_PROG(on_exec, struct task_struct___execsched *p, pid_t old_pid,
             struct linux_binprm___execsched *bprm)
{
    u64 exec_id;
    int depth;
    struct exec_sched *e = exec_of(p, &exec_id, &depth);
    if (!e || depth != 1)
        return 0;
    u32 tgid = BPF_CORE_READ(p, tgid);
    u64 now = bpf_ktime_get_ns();
    if (!e->init_exec_ns) {
        e->init_exec_ns = now;
        e->init_tgid = tgid;
        return 0;
    }
    if (!e->workload_exec_ns && tgid != e->init_tgid && !engine_file(bprm)) {
        e->workload_exec_ns = now;
        e->workload_tgid = tgid;
    }
    return 0;
}

SEC("tp_btf/sched_process_exit")
int BPF_PROG(on_exit, struct task_struct___execsched *p)
{
    u32 pid = BPF_CORE_READ(p, pid);
    u32 tgid = BPF_CORE_READ(p, tgid);
    if (pid != tgid)
        return 0;
    u64 exec_id;
    int depth;
    struct exec_sched *e = exec_of(p, &exec_id, &depth);
    if (!e || depth != 1)
        return 0;
    u64 now = bpf_ktime_get_ns();
    if (e->workload_tgid == tgid && !e->workload_exit_ns)
        e->workload_exit_ns = now;
    if (e->init_tgid == tgid && !e->init_exit_ns)
        e->init_exit_ns = now;
    return 0;
}
