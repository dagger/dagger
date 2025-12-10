//go:build ignore

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
#include <bpf/bpf_core_read.h>

char LICENSE[] SEC("license") = "GPL";

#define MAX_PATH_LEN 256

/* File operation types */
#define OP_CREATE 1
#define OP_RENAME 2
#define OP_DELETE 3

/* openat flags we care about */
#define O_CREAT 0100

/* Event sent to userspace */
struct file_event {
    __u64 timestamp_ns;
    __u32 tgid;
    __u32 op;
    char comm[16];
    char path[MAX_PATH_LEN];
    char path2[MAX_PATH_LEN];  /* for rename: new path */
};

/* Ring buffer for events */
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 256 * 1024);
} events SEC(".maps");

/* Map to store target comm (process name) - set from userspace */
struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, char[16]);
} target_comm SEC(".maps");

/* Check if current process matches target comm */
static __always_inline int should_trace(void)
{
    __u32 zero = 0;
    char *target = bpf_map_lookup_elem(&target_comm, &zero);
    if (!target || target[0] == 0)
        return 0;

    char comm[16];
    bpf_get_current_comm(&comm, sizeof(comm));

    /* Compare comm strings */
    #pragma unroll
    for (int i = 0; i < 16; i++) {
        if (target[i] != comm[i])
            return 0;
        if (target[i] == 0)
            break;
    }

    return 1;
}

/* Helper to emit a file event */
static __always_inline void emit_event(__u32 op, const char *path, const char *path2)
{
    struct file_event *e;

    e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
    if (!e)
        return;

    e->timestamp_ns = bpf_ktime_get_ns();
    e->tgid = bpf_get_current_pid_tgid() >> 32;
    e->op = op;
    bpf_get_current_comm(&e->comm, sizeof(e->comm));

    if (path)
        bpf_probe_read_user_str(e->path, sizeof(e->path), path);
    if (path2)
        bpf_probe_read_user_str(e->path2, sizeof(e->path2), path2);

    bpf_ringbuf_submit(e, 0);
}

/* ========================================================================
 * Trace file creation via openat with O_CREAT
 *
 * int openat(int dirfd, const char *pathname, int flags, mode_t mode)
 * ======================================================================== */

SEC("tp/syscalls/sys_enter_openat")
int tp_sys_enter_openat(struct trace_event_raw_sys_enter *ctx)
{
    if (!should_trace())
        return 0;

    int flags = (int)ctx->args[2];

    /* Only care about file creations */
    if (!(flags & O_CREAT))
        return 0;

    const char *pathname = (const char *)ctx->args[1];
    emit_event(OP_CREATE, pathname, NULL);

    return 0;
}

/* ========================================================================
 * Trace file renames via renameat2
 *
 * int renameat2(int olddirfd, const char *oldpath,
 *               int newdirfd, const char *newpath, unsigned int flags)
 * ======================================================================== */

SEC("tp/syscalls/sys_enter_renameat2")
int tp_sys_enter_renameat2(struct trace_event_raw_sys_enter *ctx)
{
    if (!should_trace())
        return 0;

    const char *oldpath = (const char *)ctx->args[1];
    const char *newpath = (const char *)ctx->args[3];

    emit_event(OP_RENAME, oldpath, newpath);

    return 0;
}

/* Also trace plain renameat (some systems use this instead) */
SEC("tp/syscalls/sys_enter_renameat")
int tp_sys_enter_renameat(struct trace_event_raw_sys_enter *ctx)
{
    if (!should_trace())
        return 0;

    const char *oldpath = (const char *)ctx->args[1];
    const char *newpath = (const char *)ctx->args[3];

    emit_event(OP_RENAME, oldpath, newpath);

    return 0;
}

/* ========================================================================
 * Trace file deletion via unlinkat
 *
 * int unlinkat(int dirfd, const char *pathname, int flags)
 * ======================================================================== */

SEC("tp/syscalls/sys_enter_unlinkat")
int tp_sys_enter_unlinkat(struct trace_event_raw_sys_enter *ctx)
{
    if (!should_trace())
        return 0;

    const char *pathname = (const char *)ctx->args[1];
    emit_event(OP_DELETE, pathname, NULL);

    return 0;
}
