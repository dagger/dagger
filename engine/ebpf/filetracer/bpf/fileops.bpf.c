//go:build ignore

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
#include <bpf/bpf_core_read.h>

char LICENSE[] SEC("license") = "GPL";

#define MAX_PATH_LEN 256

/* File operation types */
#define OP_CREATE 1
#define OP_DELETE 2
#define OP_STAT   3

/* openat flags */
#define O_CREAT 0100

/* Event sent to userspace */
struct file_event {
    __u64 timestamp_ns;
    __u64 inode;        /* inode number (for STAT success) */
    __s32 error;        /* negative errno (for STAT failure), 0 on success */
    __u32 tgid;
    __u32 op;
    char comm[16];
    char path[MAX_PATH_LEN];
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

/* Context saved between sys_enter and sys_exit for stat calls */
struct stat_ctx {
    char path[MAX_PATH_LEN];
    __u64 statbuf;  /* pointer to userspace struct stat */
};

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 10240);
    __type(key, __u64);
    __type(value, struct stat_ctx);
} stat_ctx_map SEC(".maps");

/* Check if current process matches target comm */
static __always_inline int should_trace(void)
{
    __u32 zero = 0;
    char *target = bpf_map_lookup_elem(&target_comm, &zero);
    if (!target || target[0] == 0)
        return 0;

    char comm[16];
    bpf_get_current_comm(&comm, sizeof(comm));

    #pragma unroll
    for (int i = 0; i < 16; i++) {
        if (target[i] != comm[i])
            return 0;
        if (target[i] == 0)
            break;
    }

    return 1;
}

/* Helper to emit a simple event (CREATE, DELETE) */
static __always_inline void emit_simple_event(__u32 op, const char *path)
{
    struct file_event *e;

    e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
    if (!e)
        return;

    e->timestamp_ns = bpf_ktime_get_ns();
    e->inode = 0;
    e->error = 0;
    e->tgid = bpf_get_current_pid_tgid() >> 32;
    e->op = op;
    bpf_get_current_comm(&e->comm, sizeof(e->comm));

    if (path)
        bpf_probe_read_user_str(e->path, sizeof(e->path), path);

    bpf_ringbuf_submit(e, 0);
}

/* ========================================================================
 * CREATE: openat with O_CREAT
 * ======================================================================== */

SEC("tp/syscalls/sys_enter_openat")
int tp_sys_enter_openat(struct trace_event_raw_sys_enter *ctx)
{
    if (!should_trace())
        return 0;

    int flags = (int)ctx->args[2];
    if (!(flags & O_CREAT))
        return 0;

    const char *pathname = (const char *)ctx->args[1];
    emit_simple_event(OP_CREATE, pathname);

    return 0;
}

/* ========================================================================
 * DELETE: unlinkat
 * NOTE: Traced system-wide (no comm filter) for debugging
 * ======================================================================== */

SEC("tp/syscalls/sys_enter_unlinkat")
int tp_sys_enter_unlinkat(struct trace_event_raw_sys_enter *ctx)
{
    /* No comm filter - trace all deletes system-wide */
    const char *pathname = (const char *)ctx->args[1];
    emit_simple_event(OP_DELETE, pathname);

    return 0;
}

/* ========================================================================
 * STAT: newfstatat (covers stat, lstat, fstatat)
 *
 * We need both enter and exit to capture the path at entry and the
 * return value (success/error) plus inode at exit.
 *
 * int newfstatat(int dirfd, const char *pathname, struct stat *statbuf, int flags)
 *
 * struct stat contains st_ino at a known offset.
 * ======================================================================== */

SEC("tp/syscalls/sys_enter_newfstatat")
int tp_sys_enter_newfstatat(struct trace_event_raw_sys_enter *ctx)
{
    if (!should_trace())
        return 0;

    __u64 pid_tgid = bpf_get_current_pid_tgid();
    struct stat_ctx sctx = {};

    const char *pathname = (const char *)ctx->args[1];
    bpf_probe_read_user_str(sctx.path, sizeof(sctx.path), pathname);
    sctx.statbuf = ctx->args[2];

    bpf_map_update_elem(&stat_ctx_map, &pid_tgid, &sctx, BPF_ANY);

    return 0;
}

SEC("tp/syscalls/sys_exit_newfstatat")
int tp_sys_exit_newfstatat(struct trace_event_raw_sys_exit *ctx)
{
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    struct stat_ctx *sctx;
    struct file_event *e;
    long ret = ctx->ret;

    sctx = bpf_map_lookup_elem(&stat_ctx_map, &pid_tgid);
    if (!sctx)
        return 0;

    e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
    if (!e)
        goto cleanup;

    e->timestamp_ns = bpf_ktime_get_ns();
    e->tgid = pid_tgid >> 32;
    e->op = OP_STAT;
    bpf_get_current_comm(&e->comm, sizeof(e->comm));
    __builtin_memcpy(e->path, sctx->path, sizeof(e->path));

    if (ret == 0) {
        /* Success - read inode from statbuf
         * struct stat layout: st_ino is typically at offset 8 on 64-bit */
        __u64 inode = 0;
        /* st_ino offset in struct stat: after st_dev (8 bytes) */
        bpf_probe_read_user(&inode, sizeof(inode), (void *)(sctx->statbuf + 8));
        e->inode = inode;
        e->error = 0;
    } else {
        /* Error - ret is negative errno */
        e->inode = 0;
        e->error = (__s32)ret;
    }

    bpf_ringbuf_submit(e, 0);

cleanup:
    bpf_map_delete_elem(&stat_ctx_map, &pid_tgid);
    return 0;
}

/* ========================================================================
 * STAT: statx (newer stat interface)
 *
 * int statx(int dirfd, const char *pathname, int flags,
 *           unsigned int mask, struct statx *statxbuf)
 *
 * struct statx has stx_ino at offset 80.
 * ======================================================================== */

SEC("tp/syscalls/sys_enter_statx")
int tp_sys_enter_statx(struct trace_event_raw_sys_enter *ctx)
{
    if (!should_trace())
        return 0;

    __u64 pid_tgid = bpf_get_current_pid_tgid();
    struct stat_ctx sctx = {};

    const char *pathname = (const char *)ctx->args[1];
    bpf_probe_read_user_str(sctx.path, sizeof(sctx.path), pathname);
    sctx.statbuf = ctx->args[4];  /* statx uses arg[4] for statxbuf */

    bpf_map_update_elem(&stat_ctx_map, &pid_tgid, &sctx, BPF_ANY);

    return 0;
}

SEC("tp/syscalls/sys_exit_statx")
int tp_sys_exit_statx(struct trace_event_raw_sys_exit *ctx)
{
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    struct stat_ctx *sctx;
    struct file_event *e;
    long ret = ctx->ret;

    sctx = bpf_map_lookup_elem(&stat_ctx_map, &pid_tgid);
    if (!sctx)
        return 0;

    e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
    if (!e)
        goto cleanup;

    e->timestamp_ns = bpf_ktime_get_ns();
    e->tgid = pid_tgid >> 32;
    e->op = OP_STAT;
    bpf_get_current_comm(&e->comm, sizeof(e->comm));
    __builtin_memcpy(e->path, sctx->path, sizeof(e->path));

    if (ret == 0) {
        /* Success - read inode from statxbuf
         * struct statx: stx_ino is at offset 80 */
        __u64 inode = 0;
        bpf_probe_read_user(&inode, sizeof(inode), (void *)(sctx->statbuf + 80));
        e->inode = inode;
        e->error = 0;
    } else {
        e->inode = 0;
        e->error = (__s32)ret;
    }

    bpf_ringbuf_submit(e, 0);

cleanup:
    bpf_map_delete_elem(&stat_ctx_map, &pid_tgid);
    return 0;
}
