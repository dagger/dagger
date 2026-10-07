//go:build ignore

#include "common.h"
#include <bpf/bpf_endian.h>

char LICENSE[] SEC("license") = "GPL";

#define ETH_P_IP 0x0800
#define ETH_P_IPV6 0x86DD
#define BPF_F_NO_PREALLOC (1U << 0)
/* Deepest absolute cgroup level searched for the workload parent. Levels
 * count from the host's cgroup root, not from a cgroup namespace's. */
#define MAX_WORKLOAD_PARENT_LEVEL 64
/* Every network namespace numbers its loopback device 1. */
#define LOOPBACK_IFINDEX 1

/* The repository's compact vmlinux.h intentionally omits the socket-buffer
 * context exposed to cgroup_skb programs. Keep this prefix in sync with
 * linux/bpf.h. */
struct __sk_buff {
    __u32 len;
    __u32 pkt_type;
    __u32 mark;
    __u32 queue_mapping;
    __u32 protocol;
    __u32 vlan_present;
    __u32 vlan_tci;
    __u32 vlan_proto;
    __u32 priority;
    __u32 ingress_ifindex;
    __u32 ifindex;
    __u32 tc_index;
    __u32 cb[5];
    __u32 hash;
    __u32 tc_classid;
    __u32 data;
    __u32 data_end;
    __u32 napi_id;
    __u32 family;
    __u32 remote_ip4;
    __u32 local_ip4;
    __u32 remote_ip6[4];
    __u32 local_ip6[4];
    __u32 remote_port;
    __u32 local_port;
    __u32 data_meta;
    __u64 flow_keys;
    __u64 tstamp;
    __u32 wire_len;
    __u32 gso_segs;
    __u64 sk;
    __u32 gso_size;
};

#define DIR_RX 0
#define DIR_TX 1
#define SCOPE_INTERNAL 0
#define SCOPE_EXTERNAL 1

struct cgroup_counter_key {
    __u8 direction;
    __u8 scope;
    __u16 pad;
};

struct operation_counter_key {
    __u64 cgroup_id;
    __u8 direction;
    __u8 scope;
    __u16 pad;
    __u32 reserved;
};

struct ipv4_lpm_key {
    __u32 prefixlen;
    __u32 addr;
};

struct ipv6_lpm_key {
    __u32 prefixlen;
    __u8 addr[16];
};

struct {
    __uint(type, BPF_MAP_TYPE_LPM_TRIE);
    __uint(map_flags, BPF_F_NO_PREALLOC);
    __uint(max_entries, 64);
    __type(key, struct ipv4_lpm_key);
    __type(value, __u8);
} internal_v4 SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_LPM_TRIE);
    __uint(map_flags, BPF_F_NO_PREALLOC);
    __uint(max_entries, 64);
    __type(key, struct ipv6_lpm_key);
    __type(value, __u8);
} internal_v6 SEC(".maps");

/* Addresses that are not globally reachable, per the IANA special-purpose
 * registries. A value of 0 marks a more specific, globally reachable
 * exception inside a non-public range. */
struct {
    __uint(type, BPF_MAP_TYPE_LPM_TRIE);
    __uint(map_flags, BPF_F_NO_PREALLOC);
    __uint(max_entries, 32);
    __type(key, struct ipv4_lpm_key);
    __type(value, __u8);
} nonpublic_v4 SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_LPM_TRIE);
    __uint(map_flags, BPF_F_NO_PREALLOC);
    __uint(max_entries, 32);
    __type(key, struct ipv6_lpm_key);
    __type(value, __u8);
} nonpublic_v6 SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_HASH);
    __uint(max_entries, 4);
    __type(key, struct cgroup_counter_key);
    __type(value, __u64);
} cgroup_byte_counters SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_HASH);
    __uint(max_entries, 4096);
    __type(key, struct operation_counter_key);
    __type(value, __u64);
} operation_byte_counters SEC(".maps");

/* Keyed by a workload's own cgroup (a child of the workload parent), so
 * processes in nested cgroups below it, in the workload's network namespace,
 * count toward that workload. The engine reserves each workload's keys; the
 * programs never create entries. */
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_HASH);
    __uint(max_entries, 16384);
    __type(key, struct operation_counter_key);
    __type(value, __u64);
} workload_byte_counters SEC(".maps");

/* Each workload's own network namespace, keyed by its cgroup ID. Sockets in
 * it are classified against this engine's networks. Processes in nested
 * cgroups that run in their own namespaces, such as a nested engine's
 * containers, are on networks this engine does not know, so only their
 * traffic to public addresses counts, as external. */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 4096);
    __type(key, __u64);
    __type(value, __u64);
} workload_netns_cookies SEC(".maps");

/* Set when the workload parent is deeper than MAX_WORKLOAD_PARENT_LEVEL, so
 * the engine reports workload accounting unavailable instead of zeros. */
struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u64);
} workload_parent_too_deep SEC(".maps");

/* The cgroup whose children are workloads (e.g. /exec), where the workload
 * programs are attached once. */
struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u64);
} workload_parent_cgroup_id SEC(".maps");

/* cgroup_skb programs include descendant cgroups. Restrict engine totals to
 * sockets owned by the engine's exact cgroup. */
struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u64);
} engine_cgroup_id SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u64);
} engine_netns_cookie SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u32);
} engine_loopback_ifindex SEC(".maps");

/* remote_in reports whether a packet's remote address matches the given
 * tries with a nonzero value: 1 if it does, 0 if not, and -1 for non-IP or
 * malformed packets.
 * Validate a complete IP header before using its remote address. Reading only
 * the source address can succeed on a truncated receive-side header. */
static __always_inline int remote_in(struct __sk_buff *skb, __u8 direction,
                                     __u16 proto, __u32 offset,
                                     void *v4, void *v6)
{
    if (proto == bpf_htons(ETH_P_IP)) {
        __u8 header[20];
        if (bpf_skb_load_bytes(skb, offset, header, sizeof(header)) < 0)
            return -1;
        __u32 header_len = (header[0] & 0xf) * 4;
        if ((header[0] >> 4) != 4 || header_len < sizeof(header))
            return -1;
        /* Include options in the completeness check. */
        __u8 last;
        if (bpf_skb_load_bytes(skb, offset + header_len - 1, &last, 1) < 0)
            return -1;
        struct ipv4_lpm_key key = {.prefixlen = 32};
        __builtin_memcpy(&key.addr, &header[direction == DIR_TX ? 16 : 12],
                         sizeof(key.addr));
        __u8 *match = bpf_map_lookup_elem(v4, &key);
        return match && *match ? 1 : 0;
    }

    if (proto == bpf_htons(ETH_P_IPV6)) {
        __u8 header[40];
        if (bpf_skb_load_bytes(skb, offset, header, sizeof(header)) < 0 ||
            (header[0] >> 4) != 6)
            return -1;
        struct ipv6_lpm_key key = {.prefixlen = 128};
        __builtin_memcpy(key.addr, &header[direction == DIR_TX ? 24 : 8],
                         sizeof(key.addr));
        __u8 *match = bpf_map_lookup_elem(v6, &key);
        return match && *match ? 1 : 0;
    }

    return -1;
}

static __always_inline int classify_ip(struct __sk_buff *skb, __u8 direction,
                                       __u16 proto, __u32 offset)
{
    int internal = remote_in(skb, direction, proto, offset,
                             &internal_v4, &internal_v6);
    if (internal < 0)
        return -1;
    return internal ? SCOPE_INTERNAL : SCOPE_EXTERNAL;
}

/* cgroup_skb runs at the socket's L3 boundary, so there is no Ethernet
 * header. The cgroup ID check below excludes descendant workloads. */
static __always_inline int classify_l3(struct __sk_buff *skb, __u8 direction)
{
    return classify_ip(skb, direction, skb->protocol, 0);
}

static __always_inline int add_cgroup_bytes(struct __sk_buff *skb,
                                             __u8 direction, int operation)
{
    __u32 zero_key = 0;
    __u64 socket_cgroup = bpf_skb_cgroup_id(skb);
    if (!operation) {
        __u64 *cgroup_id = bpf_map_lookup_elem(&engine_cgroup_id, &zero_key);
        if (!cgroup_id || socket_cgroup != *cgroup_id)
            return 1;
    }

    __u64 *netns_cookie =
        bpf_map_lookup_elem(&engine_netns_cookie, &zero_key);
    if (!netns_cookie || bpf_get_netns_cookie(skb) != *netns_cookie)
        return 1;

    __u32 *loopback_ifindex =
        bpf_map_lookup_elem(&engine_loopback_ifindex, &zero_key);
    if (loopback_ifindex && skb->ifindex == *loopback_ifindex)
        return 1;

    int scope = classify_l3(skb, direction);
    if (scope < 0)
        return 1;
    if (operation) {
        struct operation_counter_key key = {
            .cgroup_id = socket_cgroup,
            .direction = direction,
            .scope = scope,
        };
        __u64 *bytes = bpf_map_lookup_elem(&operation_byte_counters, &key);
        if (bytes)
            *bytes += skb->len;
        return 1;
    }
    /* Engine totals cover only the engine's own cgroup. Sibling helpers
     * are attributed to their operation spans, just like executor workloads. */
    struct cgroup_counter_key key = {
        .direction = direction,
        .scope = scope,
    };
    __u64 zero = 0;
    __u64 *bytes = bpf_map_lookup_elem(&cgroup_byte_counters, &key);
    if (!bytes) {
        bpf_map_update_elem(&cgroup_byte_counters, &key, &zero, BPF_NOEXIST);
        bytes = bpf_map_lookup_elem(&cgroup_byte_counters, &key);
    }
    if (bytes)
        *bytes += skb->len;
    return 1;
}

SEC("cgroup_skb/ingress")
int count_cgroup_ingress(struct __sk_buff *skb)
{
    return add_cgroup_bytes(skb, DIR_RX, 0);
}

SEC("cgroup_skb/egress")
int count_cgroup_egress(struct __sk_buff *skb)
{
    return add_cgroup_bytes(skb, DIR_TX, 0);
}

/* Attached only at the engine-owned subprocess subtrees, inherited by each
 * command's child cgroup. No per-command link or packet event stream needed. */
SEC("cgroup_skb/ingress")
int count_operation_ingress(struct __sk_buff *skb)
{
    return add_cgroup_bytes(skb, DIR_RX, 1);
}

SEC("cgroup_skb/egress")
int count_operation_egress(struct __sk_buff *skb)
{
    return add_cgroup_bytes(skb, DIR_TX, 1);
}

/* The workload owning a socket: the child of the workload parent on the
 * socket cgroup's path. Zero when the socket is outside every workload. */
static __always_inline __u64 workload_cgroup_id(struct __sk_buff *skb)
{
    __u32 zero_key = 0;
    __u64 *parent = bpf_map_lookup_elem(&workload_parent_cgroup_id, &zero_key);
    if (!parent || !*parent)
        return 0;
    for (int level = 0; level < MAX_WORKLOAD_PARENT_LEVEL; level++) {
        __u64 ancestor = bpf_skb_ancestor_cgroup_id(skb, level);
        if (!ancestor)
            return 0;
        if (ancestor == *parent)
            return bpf_skb_ancestor_cgroup_id(skb, level + 1);
    }
    /* Every socket these programs see is below the parent, so reaching the
     * bound means the parent is deeper than it. */
    __u64 *too_deep = bpf_map_lookup_elem(&workload_parent_too_deep, &zero_key);
    if (too_deep)
        *too_deep = 1;
    return 0;
}

static __always_inline int add_workload_bytes(struct __sk_buff *skb,
                                              __u8 direction)
{
    /* Traffic within a workload's own network namespace is not network
     * use. */
    if (skb->ifindex == LOOPBACK_IFINDEX)
        return 1;
    __u64 workload = workload_cgroup_id(skb);
    if (!workload)
        return 1;
    __u64 *netns_cookie = bpf_map_lookup_elem(&workload_netns_cookies, &workload);
    if (!netns_cookie)
        return 1;
    int scope;
    if (bpf_get_netns_cookie(skb) == *netns_cookie) {
        scope = classify_l3(skb, direction);
        if (scope < 0)
            return 1;
    } else {
        /* A socket in a namespace nested in the workload, such as a nested
         * engine's container, is on networks this engine does not know. Only
         * traffic to the public internet is known to be external; skip the
         * rest, including this engine's own networks, rather than
         * misclassify it. */
        if (remote_in(skb, direction, skb->protocol, 0,
                      &internal_v4, &internal_v6) != 0 ||
            remote_in(skb, direction, skb->protocol, 0,
                      &nonpublic_v4, &nonpublic_v6) != 0)
            return 1;
        scope = SCOPE_EXTERNAL;
    }
    struct operation_counter_key key = {
        .cgroup_id = workload,
        .direction = direction,
        .scope = scope,
    };
    __u64 *bytes = bpf_map_lookup_elem(&workload_byte_counters, &key);
    if (bytes)
        *bytes += skb->len;
    return 1;
}

/* Attached once at the workload parent cgroup and inherited by every
 * workload's cgroup, so starting or stopping a workload attaches nothing. */
SEC("cgroup_skb/ingress")
int count_workload_ingress(struct __sk_buff *skb)
{
    return add_workload_bytes(skb, DIR_RX);
}

SEC("cgroup_skb/egress")
int count_workload_egress(struct __sk_buff *skb)
{
    return add_workload_bytes(skb, DIR_TX);
}
