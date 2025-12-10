/* SPDX-License-Identifier: (LGPL-2.1 OR BSD-2-Clause) */
/*
 * Minimal vmlinux.h for overlay in-use tracer
 *
 * This contains only the types needed for this specific tracer.
 * For a full vmlinux.h, generate from your kernel's BTF:
 *   bpftool btf dump file /sys/kernel/btf/vmlinux format c > vmlinux.h
 */

#ifndef __VMLINUX_H__
#define __VMLINUX_H__

/* Basic integer types */
typedef unsigned char __u8;
typedef short int __s16;
typedef short unsigned int __u16;
typedef int __s32;
typedef unsigned int __u32;
typedef long long int __s64;
typedef long long unsigned int __u64;
typedef __u8 u8;
typedef __s16 s16;
typedef __u16 u16;
typedef __s32 s32;
typedef __u32 u32;
typedef __s64 s64;
typedef __u64 u64;

typedef int pid_t;

/* Byte-order types needed by bpf_helper_defs.h */
typedef __u16 __be16;
typedef __u32 __be32;
typedef __u64 __be64;
typedef __u16 __le16;
typedef __u32 __le32;
typedef __u64 __le64;
typedef __u32 __wsum;

/* BPF map types */
enum bpf_map_type {
    BPF_MAP_TYPE_UNSPEC = 0,
    BPF_MAP_TYPE_HASH = 1,
    BPF_MAP_TYPE_ARRAY = 2,
    BPF_MAP_TYPE_PROG_ARRAY = 3,
    BPF_MAP_TYPE_PERF_EVENT_ARRAY = 4,
    BPF_MAP_TYPE_PERCPU_HASH = 5,
    BPF_MAP_TYPE_PERCPU_ARRAY = 6,
    BPF_MAP_TYPE_STACK_TRACE = 7,
    BPF_MAP_TYPE_CGROUP_ARRAY = 8,
    BPF_MAP_TYPE_LRU_HASH = 9,
    BPF_MAP_TYPE_LRU_PERCPU_HASH = 10,
    BPF_MAP_TYPE_LPM_TRIE = 11,
    BPF_MAP_TYPE_ARRAY_OF_MAPS = 12,
    BPF_MAP_TYPE_HASH_OF_MAPS = 13,
    BPF_MAP_TYPE_DEVMAP = 14,
    BPF_MAP_TYPE_SOCKMAP = 15,
    BPF_MAP_TYPE_CPUMAP = 16,
    BPF_MAP_TYPE_XSKMAP = 17,
    BPF_MAP_TYPE_SOCKHASH = 18,
    BPF_MAP_TYPE_CGROUP_STORAGE = 19,
    BPF_MAP_TYPE_REUSEPORT_SOCKARRAY = 20,
    BPF_MAP_TYPE_PERCPU_CGROUP_STORAGE = 21,
    BPF_MAP_TYPE_QUEUE = 22,
    BPF_MAP_TYPE_STACK = 23,
    BPF_MAP_TYPE_SK_STORAGE = 24,
    BPF_MAP_TYPE_DEVMAP_HASH = 25,
    BPF_MAP_TYPE_STRUCT_OPS = 26,
    BPF_MAP_TYPE_RINGBUF = 27,
    BPF_MAP_TYPE_INODE_STORAGE = 28,
    BPF_MAP_TYPE_TASK_STORAGE = 29,
};

/* BPF map update flags */
#define BPF_ANY     0
#define BPF_NOEXIST 1
#define BPF_EXIST   2
#define BPF_F_LOCK  4

/* Architecture-specific pt_regs for kprobes */
#if defined(__TARGET_ARCH_x86)
struct pt_regs {
    unsigned long r15;
    unsigned long r14;
    unsigned long r13;
    unsigned long r12;
    unsigned long bp;
    unsigned long bx;
    unsigned long r11;
    unsigned long r10;
    unsigned long r9;
    unsigned long r8;
    unsigned long ax;
    unsigned long cx;
    unsigned long dx;
    unsigned long si;
    unsigned long di;
    unsigned long orig_ax;
    unsigned long ip;
    unsigned long cs;
    unsigned long flags;
    unsigned long sp;
    unsigned long ss;
};
#elif defined(__TARGET_ARCH_arm64)
/* arm64 uses user_pt_regs in libbpf's bpf_tracing.h */
struct user_pt_regs {
    unsigned long long regs[31];
    unsigned long long sp;
    unsigned long long pc;
    unsigned long long pstate;
};
struct pt_regs {
    struct user_pt_regs user_regs;
    unsigned long long orig_x0;
    signed long syscallno;
    unsigned long long orig_addr_limit;
    unsigned long long pmr_save;
    unsigned long long stackframe[2];
    unsigned long long lockdep_hardirqs;
    unsigned long long exit_rcu;
};
#else
#error "Unsupported architecture"
#endif

/* Tracepoint context for syscall enter */
struct trace_event_raw_sys_enter {
    unsigned short common_type;
    unsigned char common_flags;
    unsigned char common_preempt_count;
    int common_pid;
    long int id;
    unsigned long args[6];
};

/* Tracepoint context for syscall exit */
struct trace_event_raw_sys_exit {
    unsigned short common_type;
    unsigned char common_flags;
    unsigned char common_preempt_count;
    int common_pid;
    long int id;
    long int ret;
};

/* Namespace common */
struct ns_common {
    unsigned int inum;
};

/* Mount namespace */
struct mnt_namespace {
    struct ns_common ns;
};

/* Namespace proxy - holds pointers to various namespaces */
struct nsproxy {
    struct mnt_namespace *mnt_ns;
};

/* Task struct - the process descriptor */
struct task_struct {
    pid_t pid;
    pid_t tgid;
    struct nsproxy *nsproxy;
    char comm[16];
};

/* Quick string - used for dentry names */
struct qstr {
    union {
        struct {
            u32 hash;
            u32 len;
        };
        u64 hash_len;
    };
    const unsigned char *name;
};

/* Directory entry - represents a path component */
struct dentry {
    unsigned int d_flags;
    void *d_seq_padding;      /* seqcount_spinlock_t */
    void *d_hash_padding;     /* struct hlist_bl_node */
    struct dentry *d_parent;
    struct qstr d_name;
    /* ... more fields we don't need */
};

#endif /* __VMLINUX_H__ */
