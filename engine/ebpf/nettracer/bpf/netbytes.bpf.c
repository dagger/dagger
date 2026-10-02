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
 * linux/bpf.h; fields through gso_size are needed for packet accounting. */
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

struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_HASH);
    __uint(max_entries, 4096);
    __type(key, struct operation_counter_key);
    __type(value, __u64);
} operation_byte_counters SEC(".maps");

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
                                      __u8 scope, __u32 l3_bytes)
{
    struct counter_key key = {
        .ifindex = skb->ifindex,
        .direction = direction,
        .scope = scope,
    };
    __u64 *bytes = bpf_map_lookup_elem(&byte_counters, &key);
    if (bytes)
        *bytes += l3_bytes;
}

/* TCX precedes IP receive-side trimming. Exclude Ethernet padding, but do not
 * cap a GSO buffer to a single segment's IP length. This helper is TCX-only:
 * cgroup_skb already receives trimmed L3 buffers. */
static __always_inline __u32 tcx_ip_bytes(struct __sk_buff *skb, __u32 offset)
{
    if (skb->len <= offset)
        return 0;
    __u32 available = skb->len - offset;
    if (skb->gso_size)
        return available;

    __u8 header[6];
    if (bpf_skb_load_bytes(skb, offset, header, sizeof(header)) < 0)
        return 0;
    __u32 length;
    if ((header[0] >> 4) == 4) {
        length = ((__u32)header[2] << 8) | header[3];
        if (length < (header[0] & 0xf) * 4)
            return 0;
    } else {
        length = ((__u32)header[4] << 8) | header[5];
        /* Zero may identify a jumbogram. Without parsing its hop-by-hop
         * option, omit it rather than mistake trailing bytes for payload. */
        if (!length && available > 40)
            return 0;
        length += 40;
    }
    return length <= available ? length : 0;
}

/* Validate a complete IP header before using its remote address. Reading only
 * the source address can succeed on a truncated receive-side header. */
static __always_inline int classify_ip(struct __sk_buff *skb, __u8 direction,
                                       __u16 proto, __u32 offset)
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
        return bpf_map_lookup_elem(&internal_v4, &key) ?
            SCOPE_INTERNAL : SCOPE_EXTERNAL;
    }

    if (proto == bpf_htons(ETH_P_IPV6)) {
        __u8 header[40];
        if (bpf_skb_load_bytes(skb, offset, header, sizeof(header)) < 0 ||
            (header[0] >> 4) != 6)
            return -1;
        struct ipv6_lpm_key key = {.prefixlen = 128};
        __builtin_memcpy(key.addr, &header[direction == DIR_TX ? 24 : 8],
                         sizeof(key.addr));
        return bpf_map_lookup_elem(&internal_v6, &key) ?
            SCOPE_INTERNAL : SCOPE_EXTERNAL;
    }

    return -1;
}

static __always_inline int classify(struct __sk_buff *skb, __u8 direction,
                                    __u32 *l3_offset)
{
    __u16 proto;
    __u32 offset = 12;
    if (bpf_skb_load_bytes(skb, offset, &proto, sizeof(proto)) < 0)
        return -1;
    offset = 14;

    if (proto == bpf_htons(ETH_P_8021Q) ||
        proto == bpf_htons(ETH_P_8021AD)) {
        if (bpf_skb_load_bytes(skb, offset + 2, &proto, sizeof(proto)) < 0)
            return -1;
        offset += 4;
    }

    *l3_offset = offset;

    /* Match cgroup_skb accounting by excluding non-IP link-layer traffic. */
    return classify_ip(skb, direction, proto, offset);
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
    }
    /* Both the engine's exact cgroup and the dedicated operation subtree
     * contribute to this aggregate. The parent hook skips the subtree, so
     * each packet is counted once. Workload cgroups remain excluded. */
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
        add_bytes(skb, DIR_TX, scope, tcx_ip_bytes(skb, l3_offset));
    return TCX_NEXT;
}

SEC("tc")
int count_egress(struct __sk_buff *skb)
{
    /* Host-veth egress is traffic received by the container. */
    __u32 l3_offset = 0;
    int scope = classify(skb, DIR_RX, &l3_offset);
    if (scope >= 0)
        add_bytes(skb, DIR_RX, scope, tcx_ip_bytes(skb, l3_offset));
    return TCX_NEXT;
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

/* Attached only at the engine-owned subprocess subtree, inherited by each
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
