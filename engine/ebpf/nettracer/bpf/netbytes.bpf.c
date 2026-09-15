//go:build ignore

#include "common.h"
#include <bpf/bpf_endian.h>

char LICENSE[] SEC("license") = "GPL";

#define ETH_P_8021Q 0x8100
#define ETH_P_8021AD 0x88A8
#define ETH_P_ARP 0x0806
#define ETH_P_IP 0x0800
#define ETH_P_IPV6 0x86DD
#define TC_ACT_OK 0
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

static __always_inline void add_bytes(struct __sk_buff *skb, __u8 direction, __u8 scope)
{
    struct counter_key key = {
        .ifindex = skb->ifindex,
        .direction = direction,
        .scope = scope,
    };
    __u64 zero = 0;
    __u64 *bytes = bpf_map_lookup_elem(&byte_counters, &key);
    if (!bytes) {
        bpf_map_update_elem(&byte_counters, &key, &zero, BPF_NOEXIST);
        bytes = bpf_map_lookup_elem(&byte_counters, &key);
    }
    if (bytes)
        *bytes += skb->len;
}

static __always_inline int classify(struct __sk_buff *skb, __u8 direction)
{
    __u16 proto;
    __u32 offset = 12;
    if (bpf_skb_load_bytes(skb, offset, &proto, sizeof(proto)) < 0)
        return SCOPE_EXTERNAL;
    offset = 14;

    if (proto == bpf_htons(ETH_P_8021Q) || proto == bpf_htons(ETH_P_8021AD)) {
        if (bpf_skb_load_bytes(skb, offset + 2, &proto, sizeof(proto)) < 0)
            return SCOPE_EXTERNAL;
        offset += 4;
    }

    /* ARP is link-local CNI control traffic and cannot leave the bridge. */
    if (proto == bpf_htons(ETH_P_ARP))
        return SCOPE_INTERNAL;

    if (proto == bpf_htons(ETH_P_IP)) {
        struct ipv4_lpm_key key = {.prefixlen = 32};
        __u32 addr_offset = offset + (direction == DIR_TX ? 16 : 12);
        if (bpf_skb_load_bytes(skb, addr_offset, &key.addr, sizeof(key.addr)) < 0)
            return SCOPE_EXTERNAL;
        return bpf_map_lookup_elem(&internal_v4, &key) ? SCOPE_INTERNAL : SCOPE_EXTERNAL;
    }

    if (proto == bpf_htons(ETH_P_IPV6)) {
        struct ipv6_lpm_key key = {.prefixlen = 128};
        __u32 addr_offset = offset + (direction == DIR_TX ? 24 : 8);
        if (bpf_skb_load_bytes(skb, addr_offset, key.addr, sizeof(key.addr)) < 0)
            return SCOPE_EXTERNAL;
        return bpf_map_lookup_elem(&internal_v6, &key) ? SCOPE_INTERNAL : SCOPE_EXTERNAL;
    }

    /* Anything not positively identified as Dagger-local is billable. */
    return SCOPE_EXTERNAL;
}

SEC("tc")
int count_ingress(struct __sk_buff *skb)
{
    /* Host-veth ingress is traffic transmitted by the container. */
    add_bytes(skb, DIR_TX, classify(skb, DIR_TX));
    return TC_ACT_OK;
}

SEC("tc")
int count_egress(struct __sk_buff *skb)
{
    /* Host-veth egress is traffic received by the container. */
    add_bytes(skb, DIR_RX, classify(skb, DIR_RX));
    return TC_ACT_OK;
}
