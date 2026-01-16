//go:build ignore

#include "common.h"

char LICENSE[] SEC("license") = "GPL";

#define MAX_PATH_LEN 128
#define MAX_STACK_DEPTH 16

/* File operation types */
#define OP_MOUNT              1
#define OP_UMOUNT             2
#define OP_UNLINKAT           3
#define OP_MKDIRAT            4
#define OP_STAT               5
#define OP_OVL_WORKDIR_CREATE 6   /* ovl_workdir_create */
#define OP_OVL_WORKDIR_CLEANUP 7  /* ovl_workdir_cleanup */
#define OP_VFS_MKDIR          8   /* vfs_mkdir */
#define OP_VFS_RMDIR          9   /* vfs_rmdir */

/* Event sent to userspace */
struct file_event {
    __u64 timestamp_ns;
    __u64 duration_ns;
    __u64 stack[MAX_STACK_DEPTH]; /* kernel stack trace */
    __s32 error;
    __u32 tgid;
    __u32 op;
    __u32 flags;
    __u32 stack_size;             /* number of stack frames */
    char comm[16];
    char path[MAX_PATH_LEN];      /* target/path */
    char path2[MAX_PATH_LEN];     /* source (for mount) or fstype/options */
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

/* Context saved between sys_enter and sys_exit */
struct syscall_ctx {
    __u64 start_ns;
    __u32 op;
    __u32 flags;
    char path[MAX_PATH_LEN];
    char path2[MAX_PATH_LEN];
};

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 10240);
    __type(key, __u64);
    __type(value, struct syscall_ctx);
} syscall_ctx_map SEC(".maps");

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

/* Helper to emit event at syscall exit */
static __always_inline void emit_exit_event(long ret)
{
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    struct syscall_ctx *ctx;
    struct file_event *e;
    __u64 now = bpf_ktime_get_ns();

    ctx = bpf_map_lookup_elem(&syscall_ctx_map, &pid_tgid);
    if (!ctx)
        return;

    e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
    if (!e)
        goto cleanup;

    e->timestamp_ns = now;
    e->duration_ns = now - ctx->start_ns;
    e->tgid = pid_tgid >> 32;
    e->op = ctx->op;
    e->flags = ctx->flags;
    e->stack_size = 0;
    e->error = (ret < 0) ? (__s32)ret : 0;
    bpf_get_current_comm(&e->comm, sizeof(e->comm));
    __builtin_memcpy(e->path, ctx->path, sizeof(e->path));
    __builtin_memcpy(e->path2, ctx->path2, sizeof(e->path2));

    bpf_ringbuf_submit(e, 0);

cleanup:
    bpf_map_delete_elem(&syscall_ctx_map, &pid_tgid);
}

/* ========================================================================
 * MOUNT SYSCALL - sys_mount(source, target, fstype, flags, data)
 * ======================================================================== */

SEC("tp/syscalls/sys_enter_mount")
int tp_sys_enter_mount(struct trace_event_raw_sys_enter *ctx)
{
    if (!should_trace())
        return 0;

    __u64 pid_tgid = bpf_get_current_pid_tgid();
    struct syscall_ctx sctx = {
        .start_ns = bpf_ktime_get_ns(),
        .op = OP_MOUNT,
        .flags = (__u32)ctx->args[3],
    };

    /* target (mount point) */
    const char *target = (const char *)ctx->args[1];
    if (target)
        bpf_probe_read_user_str(sctx.path, sizeof(sctx.path), target);

    /* data/options - more useful than fstype for debugging */
    const char *data = (const char *)ctx->args[4];
    if (data)
        bpf_probe_read_user_str(sctx.path2, sizeof(sctx.path2), data);

    bpf_map_update_elem(&syscall_ctx_map, &pid_tgid, &sctx, BPF_ANY);
    return 0;
}

SEC("tp/syscalls/sys_exit_mount")
int tp_sys_exit_mount(struct trace_event_raw_sys_exit *ctx)
{
    emit_exit_event(ctx->ret);
    return 0;
}

/* ========================================================================
 * UMOUNT SYSCALL - sys_umount2(target, flags)
 * ======================================================================== */

SEC("tp/syscalls/sys_enter_umount")
int tp_sys_enter_umount(struct trace_event_raw_sys_enter *ctx)
{
    if (!should_trace())
        return 0;

    __u64 pid_tgid = bpf_get_current_pid_tgid();
    struct syscall_ctx sctx = {
        .start_ns = bpf_ktime_get_ns(),
        .op = OP_UMOUNT,
        .flags = (__u32)ctx->args[1],
    };

    const char *target = (const char *)ctx->args[0];
    if (target)
        bpf_probe_read_user_str(sctx.path, sizeof(sctx.path), target);

    bpf_map_update_elem(&syscall_ctx_map, &pid_tgid, &sctx, BPF_ANY);
    return 0;
}

SEC("tp/syscalls/sys_exit_umount")
int tp_sys_exit_umount(struct trace_event_raw_sys_exit *ctx)
{
    emit_exit_event(ctx->ret);
    return 0;
}

/* ========================================================================
 * UNLINKAT SYSCALL - sys_unlinkat(dirfd, pathname, flags)
 * Used for both unlink and rmdir (with AT_REMOVEDIR flag)
 * ======================================================================== */

SEC("tp/syscalls/sys_enter_unlinkat")
int tp_sys_enter_unlinkat(struct trace_event_raw_sys_enter *ctx)
{
    if (!should_trace())
        return 0;

    __u64 pid_tgid = bpf_get_current_pid_tgid();
    struct syscall_ctx sctx = {
        .start_ns = bpf_ktime_get_ns(),
        .op = OP_UNLINKAT,
        .flags = (__u32)ctx->args[2],  /* includes AT_REMOVEDIR for rmdir */
    };

    const char *pathname = (const char *)ctx->args[1];
    if (pathname)
        bpf_probe_read_user_str(sctx.path, sizeof(sctx.path), pathname);

    bpf_map_update_elem(&syscall_ctx_map, &pid_tgid, &sctx, BPF_ANY);
    return 0;
}

SEC("tp/syscalls/sys_exit_unlinkat")
int tp_sys_exit_unlinkat(struct trace_event_raw_sys_exit *ctx)
{
    emit_exit_event(ctx->ret);
    return 0;
}

/* ========================================================================
 * MKDIRAT SYSCALL - sys_mkdirat(dirfd, pathname, mode)
 * ======================================================================== */

SEC("tp/syscalls/sys_enter_mkdirat")
int tp_sys_enter_mkdirat(struct trace_event_raw_sys_enter *ctx)
{
    if (!should_trace())
        return 0;

    __u64 pid_tgid = bpf_get_current_pid_tgid();
    struct syscall_ctx sctx = {
        .start_ns = bpf_ktime_get_ns(),
        .op = OP_MKDIRAT,
        .flags = (__u32)ctx->args[2],  /* mode */
    };

    const char *pathname = (const char *)ctx->args[1];
    if (pathname)
        bpf_probe_read_user_str(sctx.path, sizeof(sctx.path), pathname);

    bpf_map_update_elem(&syscall_ctx_map, &pid_tgid, &sctx, BPF_ANY);
    return 0;
}

SEC("tp/syscalls/sys_exit_mkdirat")
int tp_sys_exit_mkdirat(struct trace_event_raw_sys_exit *ctx)
{
    emit_exit_event(ctx->ret);
    return 0;
}

/* ========================================================================
 * STAT SYSCALL - sys_newfstatat(dirfd, pathname, statbuf, flags)
 * ======================================================================== */

SEC("tp/syscalls/sys_enter_newfstatat")
int tp_sys_enter_newfstatat(struct trace_event_raw_sys_enter *ctx)
{
    if (!should_trace())
        return 0;

    __u64 pid_tgid = bpf_get_current_pid_tgid();
    struct syscall_ctx sctx = {
        .start_ns = bpf_ktime_get_ns(),
        .op = OP_STAT,
        .flags = (__u32)ctx->args[3],
    };

    const char *pathname = (const char *)ctx->args[1];
    if (pathname)
        bpf_probe_read_user_str(sctx.path, sizeof(sctx.path), pathname);

    bpf_map_update_elem(&syscall_ctx_map, &pid_tgid, &sctx, BPF_ANY);
    return 0;
}

SEC("tp/syscalls/sys_exit_newfstatat")
int tp_sys_exit_newfstatat(struct trace_event_raw_sys_exit *ctx)
{
    emit_exit_event(ctx->ret);
    return 0;
}

/* ========================================================================
 * KPROBE CONTEXT FOR DURATION TRACKING
 * ======================================================================== */

struct kprobe_ctx {
    __u64 start_ns;
    __u32 op;
};

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 10240);
    __type(key, __u64);  /* pid_tgid << 8 | op */
    __type(value, struct kprobe_ctx);
} kprobe_ctx_map SEC(".maps");

static __always_inline void save_kprobe_ctx(__u32 op)
{
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u64 key = (pid_tgid << 8) | op;
    struct kprobe_ctx ctx = {
        .start_ns = bpf_ktime_get_ns(),
        .op = op,
    };
    bpf_map_update_elem(&kprobe_ctx_map, &key, &ctx, BPF_ANY);
}

static __always_inline void emit_kprobe_exit_event(__u32 op, long ret)
{
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u64 key = (pid_tgid << 8) | op;
    struct kprobe_ctx *ctx;
    struct file_event *e;
    __u64 now = bpf_ktime_get_ns();

    ctx = bpf_map_lookup_elem(&kprobe_ctx_map, &key);
    if (!ctx)
        return;

    e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
    if (!e)
        goto cleanup;

    e->timestamp_ns = now;
    e->duration_ns = now - ctx->start_ns;
    e->tgid = pid_tgid >> 32;
    e->op = op;
    e->flags = 0;
    e->error = (ret < 0) ? (__s32)ret : 0;
    bpf_get_current_comm(&e->comm, sizeof(e->comm));
    e->path[0] = 0;
    e->path2[0] = 0;

    bpf_ringbuf_submit(e, 0);

cleanup:
    bpf_map_delete_elem(&kprobe_ctx_map, &key);
}

/* ========================================================================
 * KPROBES FOR OVERLAY WORKDIR OPERATIONS
 * ======================================================================== */

/* ovl_workdir_create - called during overlay mount to create work directory */
SEC("kprobe/ovl_workdir_create")
int BPF_KPROBE(kp_ovl_workdir_create)
{
    if (!should_trace())
        return 0;
    save_kprobe_ctx(OP_OVL_WORKDIR_CREATE);
    return 0;
}

SEC("kretprobe/ovl_workdir_create")
int BPF_KRETPROBE(kretp_ovl_workdir_create, int ret)
{
    emit_kprobe_exit_event(OP_OVL_WORKDIR_CREATE, ret);
    return 0;
}

/* ovl_workdir_cleanup - called to cleanup work directory */
SEC("kprobe/ovl_workdir_cleanup")
int BPF_KPROBE(kp_ovl_workdir_cleanup)
{
    if (!should_trace())
        return 0;
    save_kprobe_ctx(OP_OVL_WORKDIR_CLEANUP);
    return 0;
}

SEC("kretprobe/ovl_workdir_cleanup")
int BPF_KRETPROBE(kretp_ovl_workdir_cleanup, int ret)
{
    emit_kprobe_exit_event(OP_OVL_WORKDIR_CLEANUP, ret);
    return 0;
}

/* vfs_mkdir - VFS mkdir, captures full dentry path and stack trace
 * vfs_mkdir(struct mnt_idmap *idmap, struct inode *dir, struct dentry *dentry, umode_t mode)
 * dentry is arg3 (0-indexed: arg2)
 */
SEC("kprobe/vfs_mkdir")
int BPF_KPROBE(kp_vfs_mkdir)
{
    if (!should_trace())
        return 0;

    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u64 key = (pid_tgid << 8) | OP_VFS_MKDIR;
    __u64 now = bpf_ktime_get_ns();
    struct kprobe_ctx kctx = {
        .start_ns = now,
        .op = OP_VFS_MKDIR,
    };
    bpf_map_update_elem(&kprobe_ctx_map, &key, &kctx, BPF_ANY);

    /* Emit event with dentry path and stack trace */
    struct file_event *e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
    if (!e)
        return 0;

    e->timestamp_ns = now;
    e->duration_ns = 0;  /* entry event */
    e->tgid = pid_tgid >> 32;
    e->op = OP_VFS_MKDIR;
    e->flags = 1;  /* 1 = entry */
    e->error = 0;
    bpf_get_current_comm(&e->comm, sizeof(e->comm));

    /* Read dentry path (name + parent) - dentry is 3rd arg (index 2) */
    struct dentry *dentry = (struct dentry *)PT_REGS_PARM3(ctx);
    if (dentry) {
        read_dentry_path(dentry, e->path, sizeof(e->path));
    } else {
        e->path[0] = 0;
    }
    e->path2[0] = 0;

    /* Capture kernel stack trace */
    long stack_size = bpf_get_stack(ctx, e->stack, sizeof(e->stack), 0);
    e->stack_size = (stack_size > 0) ? (stack_size / sizeof(__u64)) : 0;

    bpf_ringbuf_submit(e, 0);
    return 0;
}

SEC("kretprobe/vfs_mkdir")
int BPF_KRETPROBE(kretp_vfs_mkdir, int ret)
{
    emit_kprobe_exit_event(OP_VFS_MKDIR, ret);
    return 0;
}

/* vfs_rmdir - VFS rmdir, captures dentry path and stack trace
 * vfs_rmdir(struct mnt_idmap *idmap, struct inode *dir, struct dentry *dentry)
 * dentry is arg3 (0-indexed: arg2)
 */
SEC("kprobe/vfs_rmdir")
int BPF_KPROBE(kp_vfs_rmdir)
{
    if (!should_trace())
        return 0;

    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u64 key = (pid_tgid << 8) | OP_VFS_RMDIR;
    __u64 now = bpf_ktime_get_ns();
    struct kprobe_ctx kctx = {
        .start_ns = now,
        .op = OP_VFS_RMDIR,
    };
    bpf_map_update_elem(&kprobe_ctx_map, &key, &kctx, BPF_ANY);

    /* Emit event with dentry path and stack trace */
    struct file_event *e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
    if (!e)
        return 0;

    e->timestamp_ns = now;
    e->duration_ns = 0;  /* entry event */
    e->tgid = pid_tgid >> 32;
    e->op = OP_VFS_RMDIR;
    e->flags = 1;  /* 1 = entry */
    e->error = 0;
    bpf_get_current_comm(&e->comm, sizeof(e->comm));

    /* Read dentry path (name + parent) - dentry is 3rd arg (index 2) */
    struct dentry *dentry = (struct dentry *)PT_REGS_PARM3(ctx);
    if (dentry) {
        read_dentry_path(dentry, e->path, sizeof(e->path));
    } else {
        e->path[0] = 0;
    }
    e->path2[0] = 0;

    /* Capture kernel stack trace */
    long stack_size = bpf_get_stack(ctx, e->stack, sizeof(e->stack), 0);
    e->stack_size = (stack_size > 0) ? (stack_size / sizeof(__u64)) : 0;

    bpf_ringbuf_submit(e, 0);
    return 0;
}

SEC("kretprobe/vfs_rmdir")
int BPF_KRETPROBE(kretp_vfs_rmdir, int ret)
{
    emit_kprobe_exit_event(OP_VFS_RMDIR, ret);
    return 0;
}
