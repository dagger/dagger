//go:build ignore

#include "common.h"

char LICENSE[] SEC("license") = "GPL";

#define MAX_PATH_LEN 256
#define MAX_DATA_LEN 512
#define DENTRY_NAME_LEN 64

/* Event sent to userspace when EBUSY occurs */
struct event {
    __u64 timestamp_ns;
    __u32 mntns;
    __u32 tgid;
    char comm[16];
    char dentry_name0[DENTRY_NAME_LEN];  /* The dentry itself */
    char dentry_name1[DENTRY_NAME_LEN];  /* Parent */
    char dentry_name2[DENTRY_NAME_LEN];  /* Grandparent */
    char mount_src[MAX_PATH_LEN];        /* What we tried to mount */
    char mount_dst[MAX_PATH_LEN];        /* Where we tried to mount it */
    char mount_data[MAX_DATA_LEN];       /* Mount options (contains lowerdir/upperdir/workdir) */
};

/* Ring buffer for events */
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 256 * 1024);
} events SEC(".maps");

/* Store mount syscall args at entry, retrieve at EBUSY */
struct mount_args {
    char src[MAX_PATH_LEN];
    char dst[MAX_PATH_LEN];
    char data[MAX_DATA_LEN];
    __u32 mntns;
};

/* Hash map to store mount args keyed by pid_tgid */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 10240);
    __type(key, __u64);
    __type(value, struct mount_args);
} mount_args_map SEC(".maps");

/* Per-CPU array for temporary storage (avoids stack limit) */
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, struct mount_args);
} tmp_mount_args SEC(".maps");

/* Store dentry pointer between kprobe entry and kretprobe
 * We use __u64 instead of struct dentry* for bpf2go compatibility */
struct probe_ctx {
    __u64 dentry_ptr;
};

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 10240);
    __type(key, __u64);
    __type(value, struct probe_ctx);
} trylock_ctx_map SEC(".maps");

/* Separate map for ovl_is_inuse probes */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 10240);
    __type(key, __u64);
    __type(value, struct probe_ctx);
} is_inuse_ctx_map SEC(".maps");

/* ========================================================================
 * Capture mount() syscall arguments at entry
 * ======================================================================== */

SEC("tp/syscalls/sys_enter_mount")
int tp_sys_enter_mount(struct trace_event_raw_sys_enter *ctx)
{
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u32 zero = 0;
    struct task_struct *task;

    /* Use per-CPU array to avoid stack overflow */
    struct mount_args *args = bpf_map_lookup_elem(&tmp_mount_args, &zero);
    if (!args)
        return 0;

    /* Read mount arguments from syscall */
    char *dev_name = (char *)ctx->args[0];
    char *dir_name = (char *)ctx->args[1];
    void *data = (void *)ctx->args[4];

    bpf_probe_read_user_str(args->src, sizeof(args->src), dev_name);
    bpf_probe_read_user_str(args->dst, sizeof(args->dst), dir_name);
    bpf_probe_read_user_str(args->data, sizeof(args->data), data);

    task = (struct task_struct *)bpf_get_current_task();
    args->mntns = get_mntns(task);

    bpf_map_update_elem(&mount_args_map, &pid_tgid, args, BPF_ANY);
    return 0;
}

/* Clean up on syscall exit */
SEC("tp/syscalls/sys_exit_mount")
int tp_sys_exit_mount(struct trace_event_raw_sys_exit *ctx)
{
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    bpf_map_delete_elem(&mount_args_map, &pid_tgid);
    return 0;
}

/* ========================================================================
 * Helper to emit an event given a dentry pointer
 * ======================================================================== */
static __always_inline void emit_inuse_event(__u64 pid_tgid, struct dentry *dentry)
{
    struct event *e;
    struct task_struct *task;
    struct mount_args *margs;
    struct dentry *cur;
    const unsigned char *name;

    if (!dentry)
        return;

    e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
    if (!e)
        return;

    task = (struct task_struct *)bpf_get_current_task();

    e->timestamp_ns = bpf_ktime_get_ns();
    e->mntns = get_mntns(task);
    e->tgid = pid_tgid >> 32;
    bpf_get_current_comm(&e->comm, sizeof(e->comm));

    /* Read up to 3 levels of dentry names - Go will concatenate them */
    cur = dentry;

    /* Level 0 - the dentry itself */
    name = BPF_CORE_READ(cur, d_name.name);
    if (name)
        bpf_probe_read_kernel_str(e->dentry_name0, sizeof(e->dentry_name0), name);

    /* Level 1 - parent */
    cur = BPF_CORE_READ(cur, d_parent);
    if (cur && cur != dentry) {
        name = BPF_CORE_READ(cur, d_name.name);
        if (name)
            bpf_probe_read_kernel_str(e->dentry_name1, sizeof(e->dentry_name1), name);

        /* Level 2 - grandparent */
        struct dentry *parent = cur;
        cur = BPF_CORE_READ(cur, d_parent);
        if (cur && cur != parent) {
            name = BPF_CORE_READ(cur, d_name.name);
            if (name)
                bpf_probe_read_kernel_str(e->dentry_name2, sizeof(e->dentry_name2), name);
        }
    }

    /* Retrieve the mount args we saved at syscall entry */
    margs = bpf_map_lookup_elem(&mount_args_map, &pid_tgid);
    if (margs) {
        __builtin_memcpy(e->mount_src, margs->src, sizeof(e->mount_src));
        __builtin_memcpy(e->mount_dst, margs->dst, sizeof(e->mount_dst));
        __builtin_memcpy(e->mount_data, margs->data, sizeof(e->mount_data));
    }

    bpf_ringbuf_submit(e, 0);
}

/* ========================================================================
 * Catch ovl_inuse_trylock - called when overlayfs tries to lock a dir
 *
 * bool ovl_inuse_trylock(struct dentry *dentry)
 * Returns true if lock acquired (not in use), false if already in use
 * ======================================================================== */

SEC("kprobe/ovl_inuse_trylock")
int BPF_KPROBE(kp_ovl_inuse_trylock, struct dentry *dentry)
{
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    struct probe_ctx pctx = { .dentry_ptr = (__u64)dentry };

    bpf_map_update_elem(&trylock_ctx_map, &pid_tgid, &pctx, BPF_ANY);
    return 0;
}

SEC("kretprobe/ovl_inuse_trylock")
int BPF_KRETPROBE(kretp_ovl_inuse_trylock, int ret)
{
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    struct probe_ctx *pctx;

    /* trylock returns true (non-zero) if lock acquired = NOT in use
     * We care about ret == 0 meaning it was already in use */
    if (ret != 0)
        goto cleanup;

    pctx = bpf_map_lookup_elem(&trylock_ctx_map, &pid_tgid);
    if (!pctx)
        return 0;

    emit_inuse_event(pid_tgid, (struct dentry *)pctx->dentry_ptr);

cleanup:
    bpf_map_delete_elem(&trylock_ctx_map, &pid_tgid);
    return 0;
}

/* ========================================================================
 * Catch ovl_is_inuse - called to check if a dentry is marked in use
 *
 * bool ovl_is_inuse(struct dentry *dentry)
 * Returns true if in use, false if not
 * ======================================================================== */

SEC("kprobe/ovl_is_inuse")
int BPF_KPROBE(kp_ovl_is_inuse, struct dentry *dentry)
{
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    struct probe_ctx pctx = { .dentry_ptr = (__u64)dentry };

    bpf_map_update_elem(&is_inuse_ctx_map, &pid_tgid, &pctx, BPF_ANY);
    return 0;
}

SEC("kretprobe/ovl_is_inuse")
int BPF_KRETPROBE(kretp_ovl_is_inuse, int ret)
{
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    struct probe_ctx *pctx;

    /* is_inuse returns true (non-zero) if the dentry IS in use = conflict */
    if (ret == 0)
        goto cleanup;

    pctx = bpf_map_lookup_elem(&is_inuse_ctx_map, &pid_tgid);
    if (!pctx)
        return 0;

    emit_inuse_event(pid_tgid, (struct dentry *)pctx->dentry_ptr);

cleanup:
    bpf_map_delete_elem(&is_inuse_ctx_map, &pid_tgid);
    return 0;
}
