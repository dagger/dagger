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

/* Only the context type is needed. The connect programs do not inspect its
 * fields; they use it to obtain the socket cookie. */
struct bpf_sock_addr {
    __u32 user_family;
};

#define DIR_RX 0
#define DIR_TX 1
#define SCOPE_INTERNAL 0
#define SCOPE_EXTERNAL 1

#define OWNER_DAGGERLAND 1
#define OWNER_USERLAND 2

struct counter_key {
    __u32 ifindex;
    __u8 direction;
    __u8 scope;
    __u16 pad;
};

struct owner_counter_key {
    __u8 owner;
    __u8 direction;
    __u8 scope;
    __u8 pad;
};

struct unattributed_counter_key {
    __u8 direction;
    __u8 scope;
    __u16 pad;
};

struct unattributed_counter_value {
    __u64 bytes;
    __u64 packets;
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

/* Connections are pooled within one ownership class. The socket cookie stays
 * stable for the lifetime of a connection, including ingress packets. */
struct {
    __uint(type, BPF_MAP_TYPE_LRU_HASH);
    __uint(max_entries, 16384);
    __type(key, __u64);
    __type(value, __u8);
} socket_owners SEC(".maps");

/* Container execs are attributed by cgroup instead of by socket. */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 16384);
    __type(key, __u64);
    __type(value, __u8);
} cgroup_owners SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_HASH);
    __uint(max_entries, 16);
    __type(key, struct owner_counter_key);
    __type(value, __u64);
} owner_byte_counters SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_HASH);
    __uint(max_entries, 4);
    __type(key, struct unattributed_counter_key);
    __type(value, struct unattributed_counter_value);
} unattributed_counters SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u8);
} enforce_ownership SEC(".maps");

/* The cgroup mount may expose a parent of the engine's actual cgroup. Limit
 * connect enforcement to the engine process; owned child cgroups are checked
 * independently and unrelated sibling processes must remain unaffected. */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 16);
    __type(key, __u32);
    __type(value, __u8);
} protected_tgids SEC(".maps");

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

    /* Anything not positively identified as Dagger-local is external. */
    return SCOPE_EXTERNAL;
}

/* cgroup_skb runs at the socket's L3 boundary, so there is no Ethernet
 * header. This is the authoritative engine-and-descendants measurement. */
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

static __always_inline __u8 packet_owner(struct __sk_buff *skb, __u8 direction)
{
    __u64 cgroup_id = bpf_skb_cgroup_id(skb);
    __u8 *owner = bpf_map_lookup_elem(&cgroup_owners, &cgroup_id);
    if (owner)
        return *owner;

    __u64 cookie = bpf_get_socket_cookie(skb);
    if (cookie) {
        owner = bpf_map_lookup_elem(&socket_owners, &cookie);
        if (owner)
            return *owner;
    }

    /* Some cgroup skb paths expose the attachment cgroup instead of the leaf
     * cgroup which created the socket. On the first process-driven egress
     * packet, copy the current leaf cgroup owner to the socket. Retransmits
     * and ingress packets then use the stable socket cookie. */
    if (direction == DIR_TX) {
        cgroup_id = bpf_get_current_cgroup_id();
        owner = bpf_map_lookup_elem(&cgroup_owners, &cgroup_id);
        if (owner) {
            if (cookie)
                bpf_map_update_elem(&socket_owners, &cookie, owner, BPF_ANY);
            return *owner;
        }
    }
    return 0;
}

static __always_inline int add_owner_bytes(struct __sk_buff *skb, __u8 direction)
{
    __u8 scope = classify_l3(skb, direction);
    __u8 owner = packet_owner(skb, direction);
    if (!owner) {
        struct unattributed_counter_key unattributed_key = {
            .direction = direction,
            .scope = scope,
        };
        struct unattributed_counter_value zero = {};
        struct unattributed_counter_value *counter =
            bpf_map_lookup_elem(&unattributed_counters, &unattributed_key);
        if (!counter) {
            bpf_map_update_elem(&unattributed_counters, &unattributed_key,
                                &zero, BPF_NOEXIST);
            counter = bpf_map_lookup_elem(&unattributed_counters,
                                          &unattributed_key);
        }
        if (counter) {
            counter->bytes += skb->len;
            counter->packets++;
        }

        /* The ingress hook runs before TCP assigns the first SYN to a
         * listener, and SYN-ACK can run before accept(2), so those packets do
         * not always expose an owned socket. Count unattributed packets
         * conservatively as userland and keep the separate audit counter.
         * New unowned outbound sockets are rejected by the connect hooks. */
        owner = OWNER_USERLAND;
    }

    struct owner_counter_key key = {
        .owner = owner,
        .direction = direction,
        .scope = scope,
    };
    __u64 zero = 0;
    __u64 *bytes = bpf_map_lookup_elem(&owner_byte_counters, &key);
    if (!bytes) {
        bpf_map_update_elem(&owner_byte_counters, &key, &zero, BPF_NOEXIST);
        bytes = bpf_map_lookup_elem(&owner_byte_counters, &key);
    }
    if (bytes)
        *bytes += skb->len;
    return 1;
}

static __always_inline int allow_owned_connect(struct bpf_sock_addr *ctx)
{
    __u32 zero_key = 0;
    __u8 *enforce = bpf_map_lookup_elem(&enforce_ownership, &zero_key);
    if (!enforce || !*enforce)
        return 1;

    __u64 cgroup_id = bpf_get_current_cgroup_id();
    if (bpf_map_lookup_elem(&cgroup_owners, &cgroup_id))
        return 1;

    __u64 cookie = bpf_get_socket_cookie(ctx);
    if (cookie && bpf_map_lookup_elem(&socket_owners, &cookie))
        return 1;

    __u32 tgid = bpf_get_current_pid_tgid() >> 32;
    return bpf_map_lookup_elem(&protected_tgids, &tgid) ? 0 : 1;
}

SEC("cgroup/connect4")
int enforce_connect4(struct bpf_sock_addr *ctx)
{
    return allow_owned_connect(ctx);
}

SEC("cgroup/connect6")
int enforce_connect6(struct bpf_sock_addr *ctx)
{
    return allow_owned_connect(ctx);
}

SEC("cgroup/sendmsg4")
int enforce_sendmsg4(struct bpf_sock_addr *ctx)
{
    return allow_owned_connect(ctx);
}

SEC("cgroup/sendmsg6")
int enforce_sendmsg6(struct bpf_sock_addr *ctx)
{
    return allow_owned_connect(ctx);
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

SEC("cgroup_skb/ingress")
int count_cgroup_ingress(struct __sk_buff *skb)
{
    return add_owner_bytes(skb, DIR_RX);
}

SEC("cgroup_skb/egress")
int count_cgroup_egress(struct __sk_buff *skb)
{
    return add_owner_bytes(skb, DIR_TX);
}
