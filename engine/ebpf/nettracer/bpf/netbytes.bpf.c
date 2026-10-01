//go:build ignore

#include "common.h"
#include <bpf/bpf_endian.h>

char LICENSE[] SEC("license") = "GPL";

#define ETH_P_8021Q 0x8100
#define ETH_P_8021AD 0x88A8
#define ETH_P_ARP 0x0806
#define ETH_P_IP 0x0800
#define ETH_P_IPV6 0x86DD
#define TCX_NEXT -1
#define BPF_F_NO_PREALLOC (1U << 0)

/* The repository's compact vmlinux.h intentionally omits the socket-buffer
 * context exposed to SCHED_CLS programs. Keep this prefix in sync with
 * linux/bpf.h; only len and ifindex are accessed below. */
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
};

#define DIR_RX 0
#define DIR_TX 1
#define SCOPE_INTERNAL 0
#define SCOPE_EXTERNAL 1

struct counter_key {
    __u32 ifindex;
    __u8 direction;
    __u8 scope;
    __u16 pad;
};

struct cgroup_counter_key {
    __u8 direction;
    __u8 scope;
    __u16 pad;
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

struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_HASH);
    __uint(max_entries, 4096);
    __type(key, struct counter_key);
    __type(value, __u64);
} byte_counters SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_HASH);
    __uint(max_entries, 4);
    __type(key, struct cgroup_counter_key);
    __type(value, __u64);
} cgroup_byte_counters SEC(".maps");

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

static __always_inline void add_bytes(struct __sk_buff *skb, __u8 direction,
                                      __u8 scope, __u32 l3_offset)
{
    struct counter_key key = {
        .ifindex = skb->ifindex,
        .direction = direction,
        .scope = scope,
    };
    __u64 *bytes = bpf_map_lookup_elem(&byte_counters, &key);
    if (bytes && skb->len > l3_offset)
        *bytes += skb->len - l3_offset;
}

static __always_inline int classify(struct __sk_buff *skb, __u8 direction,
                                    __u32 *l3_offset)
{
    __u16 proto;
    __u32 offset = 12;
    if (bpf_skb_load_bytes(skb, offset, &proto, sizeof(proto)) < 0)
        return SCOPE_EXTERNAL;
    offset = 14;

    if (proto == bpf_htons(ETH_P_8021Q) ||
        proto == bpf_htons(ETH_P_8021AD)) {
        if (bpf_skb_load_bytes(skb, offset + 2, &proto, sizeof(proto)) < 0)
            return SCOPE_EXTERNAL;
        offset += 4;
    }

    *l3_offset = offset;

    if (proto == bpf_htons(ETH_P_IP)) {
        struct ipv4_lpm_key key = {.prefixlen = 32};
        __u32 addr_offset = offset + (direction == DIR_TX ? 16 : 12);
        if (bpf_skb_load_bytes(skb, addr_offset, &key.addr,
                               sizeof(key.addr)) < 0)
            return SCOPE_EXTERNAL;
        return bpf_map_lookup_elem(&internal_v4, &key) ?
            SCOPE_INTERNAL : SCOPE_EXTERNAL;
    }

    if (proto == bpf_htons(ETH_P_IPV6)) {
        struct ipv6_lpm_key key = {.prefixlen = 128};
        __u32 addr_offset = offset + (direction == DIR_TX ? 24 : 8);
        if (bpf_skb_load_bytes(skb, addr_offset, key.addr,
                               sizeof(key.addr)) < 0)
            return SCOPE_EXTERNAL;
        return bpf_map_lookup_elem(&internal_v6, &key) ?
            SCOPE_INTERNAL : SCOPE_EXTERNAL;
    }

    /* Match cgroup_skb accounting by excluding non-IP link-layer traffic. */
    return -1;
}

/* cgroup_skb runs at the socket's L3 boundary, so there is no Ethernet
 * header. The cgroup ID check below excludes descendant workloads. */
static __always_inline int classify_l3(struct __sk_buff *skb, __u8 direction)
{
    if (skb->protocol == bpf_htons(ETH_P_IP)) {
        struct ipv4_lpm_key key = {.prefixlen = 32};
        __u32 addr_offset = direction == DIR_TX ? 16 : 12;
        if (bpf_skb_load_bytes(skb, addr_offset, &key.addr, sizeof(key.addr)) < 0)
            return SCOPE_EXTERNAL;
        return bpf_map_lookup_elem(&internal_v4, &key) ? SCOPE_INTERNAL : SCOPE_EXTERNAL;
    }

    if (skb->protocol == bpf_htons(ETH_P_IPV6)) {
        struct ipv6_lpm_key key = {.prefixlen = 128};
        __u32 addr_offset = direction == DIR_TX ? 24 : 8;
        if (bpf_skb_load_bytes(skb, addr_offset, key.addr, sizeof(key.addr)) < 0)
            return SCOPE_EXTERNAL;
        return bpf_map_lookup_elem(&internal_v6, &key) ? SCOPE_INTERNAL : SCOPE_EXTERNAL;
    }

    return SCOPE_EXTERNAL;
}

static __always_inline int add_cgroup_bytes(struct __sk_buff *skb,
                                             __u8 direction)
{
    __u32 zero_key = 0;
    __u64 *cgroup_id = bpf_map_lookup_elem(&engine_cgroup_id, &zero_key);
    if (!cgroup_id || bpf_skb_cgroup_id(skb) != *cgroup_id)
        return 1;

    __u64 *netns_cookie =
        bpf_map_lookup_elem(&engine_netns_cookie, &zero_key);
    if (!netns_cookie || bpf_get_netns_cookie(skb) != *netns_cookie)
        return 1;

    __u32 *loopback_ifindex =
        bpf_map_lookup_elem(&engine_loopback_ifindex, &zero_key);
    if (loopback_ifindex && skb->ifindex == *loopback_ifindex)
        return 1;

    __u8 scope = classify_l3(skb, direction);
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

SEC("tc")
int count_ingress(struct __sk_buff *skb)
{
    /* Host-veth ingress is traffic transmitted by the container. */
    __u32 l3_offset = 0;
    int scope = classify(skb, DIR_TX, &l3_offset);
    if (scope >= 0)
        add_bytes(skb, DIR_TX, scope, l3_offset);
    return TCX_NEXT;
}

SEC("tc")
int count_egress(struct __sk_buff *skb)
{
    /* Host-veth egress is traffic received by the container. */
    __u32 l3_offset = 0;
    int scope = classify(skb, DIR_RX, &l3_offset);
    if (scope >= 0)
        add_bytes(skb, DIR_RX, scope, l3_offset);
    return TCX_NEXT;
}

SEC("cgroup_skb/ingress")
int count_cgroup_ingress(struct __sk_buff *skb)
{
    return add_cgroup_bytes(skb, DIR_RX);
}

SEC("cgroup_skb/egress")
int count_cgroup_egress(struct __sk_buff *skb)
{
    return add_cgroup_bytes(skb, DIR_TX);
}
