#ifndef DAGGER_EBPF_COMMON_H
#define DAGGER_EBPF_COMMON_H

#include "vmlinux.h"
#include <bpf/bpf_core_read.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>

static __always_inline __u32 get_mntns(struct task_struct *task)
{
    return BPF_CORE_READ(task, nsproxy, mnt_ns, ns.inum);
}

/* Helper to read dentry name + parent (2 levels)
 * Format: "parent/name" or just "name" if no parent
 */
static __always_inline void read_dentry_path(struct dentry *dentry, char *buf, int buflen)
{
    if (!dentry || buflen < 2) {
        buf[0] = 0;
        return;
    }

    char name[48];
    char parent_name[48];
    int have_parent = 0;

    /* Read current dentry name */
    struct qstr d_name;
    bpf_probe_read_kernel(&d_name, sizeof(d_name), &dentry->d_name);
    if (d_name.name) {
        bpf_probe_read_kernel_str(name, sizeof(name), d_name.name);
    } else {
        name[0] = 0;
    }

    /* Read parent dentry name */
    struct dentry *parent;
    bpf_probe_read_kernel(&parent, sizeof(parent), &dentry->d_parent);
    if (parent && parent != dentry) {
        struct qstr pd_name;
        bpf_probe_read_kernel(&pd_name, sizeof(pd_name), &parent->d_name);
        if (pd_name.name) {
            bpf_probe_read_kernel_str(parent_name, sizeof(parent_name), pd_name.name);
            if (parent_name[0] != '/' && parent_name[0] != 0) {
                have_parent = 1;
            }
        }
    }

    /* Build path string */
    int pos = 0;

    if (have_parent) {
        for (int i = 0; i < 47 && parent_name[i] && pos < buflen - 2; i++)
            buf[pos++] = parent_name[i];
        buf[pos++] = '/';
    }

    for (int i = 0; i < 47 && name[i] && pos < buflen - 1; i++)
        buf[pos++] = name[i];

    buf[pos] = 0;
}

#endif
