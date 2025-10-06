package network

// DomainSuffix is an extra domain suffix appended to every session's unique
// domain. It is used to identify which domains to propagate from a parent
// session's /etc/resolv.conf to a child (nested Dagger) session's network
// config so that nested code can reach services in the parent.
const DomainSuffix = ".dagger.local"

// DefaultName is a short name for the engine's container network. It is used
// for interface name.
const DefaultName = "dagger"

// DefaultCIDR is the default address range to use for networked containers.
const DefaultCIDR = "10.87.0.0/16"
