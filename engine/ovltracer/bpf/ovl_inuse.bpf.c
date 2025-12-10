//go:build ignore

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
#include <bpf/bpf_core_read.h>

char LICENSE[] SEC("license") = "GPL";

#define MAX_PATH_LEN 256
#define MAX_DATA_LEN 512

/* Event sent to userspace when EBUSY occurs */
struct event {
    __u64 timestamp_ns;
    __u32 mntns;
    __u32 tgid;
    char comm[16];
    char inuse_path[MAX_PATH_LEN];    /* The path that's already in use */
    char mount_src[MAX_PATH_LEN];     /* What we tried to mount */
    char mount_dst[MAX_PATH_LEN];     /* Where we tried to mount it */
    char mount_data[MAX_DATA_LEN];    /* Mount options (contains lowerdir/upperdir/workdir) */
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

/* Helper to get mount namespace inum */
static __always_inline __u32 get_mntns(struct task_struct *task)
{
    return BPF_CORE_READ(task, nsproxy, mnt_ns, ns.inum);
}

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
 * The main event: catch ovl_report_in_use (called when EBUSY happens)
 *
 * This function is called by overlayfs when it detects a directory is
 * already in use by another overlay mount. The second argument is the
 * path string that's in use.
 *
 * Note: The exact symbol name may vary by kernel. Common variants:
 *   - ovl_report_in_use
 *   - ovl_report_in_use.isra.0
 *   - ovl_report_in_use.constprop.0
 * ======================================================================== */

SEC("kprobe/ovl_report_in_use")
int BPF_KPROBE(kp_ovl_report_in_use, void *ofs, const char *path)
{
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    struct event *e;
    struct task_struct *task;
    struct mount_args *margs;

    e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
    if (!e)
        return 0;

    task = (struct task_struct *)bpf_get_current_task();

    e->timestamp_ns = bpf_ktime_get_ns();
    e->mntns = get_mntns(task);
    e->tgid = pid_tgid >> 32;
    bpf_get_current_comm(&e->comm, sizeof(e->comm));

    /* Read the in-use path from kernel memory */
    bpf_probe_read_kernel_str(e->inuse_path, sizeof(e->inuse_path), path);

    /* Retrieve the mount args we saved at syscall entry */
    margs = bpf_map_lookup_elem(&mount_args_map, &pid_tgid);
    if (margs) {
        __builtin_memcpy(e->mount_src, margs->src, sizeof(e->mount_src));
        __builtin_memcpy(e->mount_dst, margs->dst, sizeof(e->mount_dst));
        __builtin_memcpy(e->mount_data, margs->data, sizeof(e->mount_data));
    }

    bpf_ringbuf_submit(e, 0);
    return 0;
}
