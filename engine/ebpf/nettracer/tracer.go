//go:build linux && (386 || amd64 || arm64)

// Package nettracer accounts for engine, subprocess and executor workload
// traffic at cgroup socket-buffer hooks.
package nettracer

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/engine/ebpf/internal/ebpfutil"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -cflags "-O2 -g -Wall -Werror -D__TARGET_ARCH_x86 -I../bpf" -target amd64 netbytes ./bpf/netbytes.bpf.c
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -cflags "-O2 -g -Wall -Werror -D__TARGET_ARCH_arm64 -I../bpf" -target arm64 netbytes ./bpf/netbytes.bpf.c

const (
	directionRX uint8 = iota
	directionTX
)

const (
	scopeInternal uint8 = iota
	scopeExternal
)

type ipv4LPMKey struct {
	PrefixLen uint32
	Address   uint32
}

type ipv6LPMKey struct {
	PrefixLen uint32
	Address   [16]byte
}

type cgroupCounterKey struct {
	Direction uint8
	Scope     uint8
	Pad       uint16
}

// Sample is a cumulative snapshot for one workload or subprocess.
type Sample struct {
	InternalRX uint64
	InternalTX uint64
	ExternalRX uint64
	ExternalTX uint64
}

// EngineSample is a cumulative engine-cgroup network-layer snapshot.
type EngineSample struct {
	InternalRX uint64
	InternalTX uint64
	ExternalRX uint64
	ExternalTX uint64
}

// Tracer owns the programs and maps shared by the engine, its subprocesses
// and executor workloads.
type Tracer struct {
	objs netbytesObjects
	cpus int
	mu   sync.Mutex

	workloadMu     sync.Mutex
	workloadParent string
	workloadLinks  []link.Link

	cgroupIngress link.Link
	cgroupEgress  link.Link
	cgroupEnabled bool
	cgroupErr     error
	commands      *commandAccounting
	commandErr    error
}

var activeTracer atomic.Pointer[Tracer]

// Active returns the process-wide tracer used by network providers.
func Active() *Tracer {
	return activeTracer.Load()
}

func New() (*Tracer, error) {
	// These cgroup_skb programs do not use CO-RE, so unlike the diagnostic
	// tracers they do not require kernel BTF.
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("removing memlock limit: %w", err)
	}
	var objs netbytesObjects
	if err := loadNetbytesObjects(&objs, nil); err != nil {
		return nil, ebpfutil.WrapVerifierError(err, "loading network accounting BPF objects")
	}
	cpus, err := ebpf.PossibleCPU()
	if err != nil {
		objs.Close()
		return nil, fmt.Errorf("querying possible CPUs: %w", err)
	}
	t := &Tracer{objs: objs, cpus: cpus}
	// IPv6 neighbor discovery and other link-local control traffic never leaves
	// the local link. Keeping these prefixes in the same trie avoids extra parsing
	// in the packet hot path.
	for _, prefix := range []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("::1/128"),
		netip.MustParsePrefix("fe80::/10"),
		netip.MustParsePrefix("ff02::/16"),
	} {
		if err := t.AddInternalPrefix(prefix); err != nil {
			objs.Close()
			return nil, fmt.Errorf("registering link-local prefix %s: %w", prefix, err)
		}
	}
	finishUnavailable := func(err error) (*Tracer, error) {
		t.cgroupErr = err
		activeTracer.Store(t)
		return t, nil
	}
	if err := t.addCurrentNetworkPrefixes(); err != nil {
		return finishUnavailable(err)
	}
	cgroupPath, err := currentCgroupPath()
	if err != nil {
		return finishUnavailable(err)
	}
	if err := t.configureEngineBoundary(cgroupPath); err != nil {
		return finishUnavailable(err)
	}
	ingress, err := link.AttachCgroup(link.CgroupOptions{
		Path:    cgroupPath,
		Attach:  ebpf.AttachCGroupInetIngress,
		Program: objs.CountCgroupIngress,
	})
	if err != nil {
		return finishUnavailable(err)
	}
	t.cgroupIngress = ingress
	egress, err := link.AttachCgroup(link.CgroupOptions{
		Path:    cgroupPath,
		Attach:  ebpf.AttachCGroupInetEgress,
		Program: objs.CountCgroupEgress,
	})
	if err != nil {
		_ = ingress.Close()
		t.cgroupIngress = nil
		return finishUnavailable(err)
	}
	t.cgroupEgress = egress
	t.cgroupEnabled = true
	t.commands, t.commandErr = t.newCommandAccounting(cgroupPath)
	activeTracer.Store(t)
	return t, nil
}

func (t *Tracer) configureEngineBoundary(cgroupPath string) error {
	cgroupID, err := cgroupID(cgroupPath)
	if err != nil {
		return err
	}
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("opening engine netns socket: %w", err)
	}
	defer unix.Close(fd)

	cookie, err := unix.GetsockoptUint64(fd, unix.SOL_SOCKET, unix.SO_NETNS_COOKIE)
	if err != nil {
		return fmt.Errorf("getting engine netns cookie: %w", err)
	}
	loopback, err := netlink.LinkByName("lo")
	if err != nil {
		return fmt.Errorf("looking up engine loopback interface: %w", err)
	}
	key := uint32(0)
	if err := t.objs.EngineCgroupId.Put(key, cgroupID); err != nil {
		return fmt.Errorf("setting engine cgroup ID: %w", err)
	}
	if err := t.objs.EngineNetnsCookie.Put(key, cookie); err != nil {
		return fmt.Errorf("setting engine netns cookie: %w", err)
	}
	ifindex := uint32(loopback.Attrs().Index)
	if err := t.objs.EngineLoopbackIfindex.Put(key, ifindex); err != nil {
		return fmt.Errorf("setting engine loopback interface: %w", err)
	}
	return nil
}

// addCurrentNetworkPrefixes discovers the bridge subnet containing the engine
// container. Requiring the default route to use a veth makes host networking
// fail closed instead of treating a host LAN as a Dagger-managed network.
func (t *Tracer) addCurrentNetworkPrefixes() error {
	links := map[int]netlink.Link{}
	for _, family := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		routes, err := netlink.RouteList(nil, family)
		if err != nil {
			return fmt.Errorf("listing default routes: %w", err)
		}
		for _, route := range routes {
			if route.LinkIndex == 0 {
				continue
			}
			if route.Dst != nil {
				ones, _ := route.Dst.Mask.Size()
				if ones != 0 {
					continue
				}
			}
			dev, err := netlink.LinkByIndex(route.LinkIndex)
			if err != nil {
				return fmt.Errorf("looking up default route interface: %w", err)
			}
			if dev.Type() != "veth" {
				return fmt.Errorf("default route interface %s is %s, not veth", dev.Attrs().Name, dev.Type())
			}
			links[route.LinkIndex] = dev
		}
	}
	if len(links) == 0 {
		return errors.New("no veth default route found")
	}
	for _, dev := range links {
		addrs, err := netlink.AddrList(dev, netlink.FAMILY_ALL)
		if err != nil {
			return fmt.Errorf("listing addresses for %s: %w", dev.Attrs().Name, err)
		}
		for _, addr := range addrs {
			if addr.IPNet == nil || addr.IP.IsLoopback() || addr.IP.IsUnspecified() {
				continue
			}
			ip, ok := netip.AddrFromSlice(addr.IPNet.IP)
			if !ok {
				continue
			}
			ones, _ := addr.IPNet.Mask.Size()
			if err := t.AddInternalPrefix(netip.PrefixFrom(ip.Unmap(), ones).Masked()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *Tracer) Close() error {
	activeTracer.CompareAndSwap(t, nil)
	var errs []error
	errs = append(errs, t.closeWorkloads())
	if t.commands != nil {
		errs = append(errs, t.commands.Close())
	}
	if t.cgroupIngress != nil {
		errs = append(errs, t.cgroupIngress.Close())
	}
	if t.cgroupEgress != nil {
		errs = append(errs, t.cgroupEgress.Close())
	}
	errs = append(errs, t.objs.Close())
	return errors.Join(errs...)
}

func currentCgroupPath() (string, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		controllers, path, ok := strings.Cut(line, "::")
		if !ok || controllers != "0" {
			continue
		}
		path = filepath.Clean("/" + path)
		return filepath.Join("/sys/fs/cgroup", path), nil
	}
	return "", errors.New("unified cgroup entry not found")
}

func cgroupID(path string) (uint64, error) {
	handle, _, err := unix.NameToHandleAt(unix.AT_FDCWD, path, 0)
	if err != nil {
		return 0, fmt.Errorf("getting cgroup ID for %s: %w", path, err)
	}
	data := handle.Bytes()
	if len(data) < 8 {
		return 0, fmt.Errorf(
			"getting cgroup ID for %s: handle is %d bytes",
			path,
			len(data),
		)
	}
	return binary.NativeEndian.Uint64(data[:8]), nil
}

// EngineAccountingAvailable reports whether the engine cgroup programs are
// attached. Missing support is reported explicitly instead of returning zero
// counters that look authoritative.
func EngineAccountingAvailable() bool {
	t := activeTracer.Load()
	return t != nil && t.cgroupEnabled && fallbackCommands.Load() == nil
}

// EngineAccountingError explains why engine-wide accounting is unavailable.
func EngineAccountingError() error {
	t := activeTracer.Load()
	if t == nil {
		return errors.New("engine cgroup network accounting is not initialized")
	}
	if fallbackCommands.Load() != nil {
		return errors.New("subprocess cgroups lack network accounting; engine aggregate is incomplete")
	}
	return t.cgroupErr
}

// SampleEngine returns cumulative packet counters for sockets in the engine's
// exact cgroup and its dedicated sibling subprocess subtrees, in the engine network
// namespace. Module and withExec cgroups remain excluded.
func SampleEngine() (EngineSample, error) {
	t := activeTracer.Load()
	if t == nil || !t.cgroupEnabled || fallbackCommands.Load() != nil {
		return EngineSample{}, errors.New("engine cgroup network accounting is unavailable")
	}
	return t.sampleEngine()
}

// AddInternalPrefixesForInterface discovers the bridge containing ifindex and
// classifies all addresses routed directly by that bridge as internal.
func (t *Tracer) AddInternalPrefixesForInterface(ifindex int) error {
	dev, err := netlink.LinkByIndex(ifindex)
	if err != nil {
		return fmt.Errorf("looking up veth index %d: %w", ifindex, err)
	}
	masterIndex := dev.Attrs().MasterIndex
	if masterIndex == 0 {
		return fmt.Errorf("veth %s has no bridge master", dev.Attrs().Name)
	}
	bridge, err := netlink.LinkByIndex(masterIndex)
	if err != nil {
		return fmt.Errorf("looking up bridge for %s: %w", dev.Attrs().Name, err)
	}
	addrs, err := netlink.AddrList(bridge, netlink.FAMILY_ALL)
	if err != nil {
		return fmt.Errorf("listing bridge %s addresses: %w", bridge.Attrs().Name, err)
	}
	var errs []error
	for _, addr := range addrs {
		if addr.IPNet == nil {
			continue
		}
		prefix, ok := netip.AddrFromSlice(addr.IPNet.IP)
		if !ok {
			continue
		}
		ones, _ := addr.IPNet.Mask.Size()
		if err := t.AddInternalPrefix(netip.PrefixFrom(prefix.Unmap(), ones).Masked()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (t *Tracer) AddInternalPrefix(prefix netip.Prefix) error {
	one := uint8(1)
	if prefix.Addr().Is4() {
		addr := prefix.Addr().As4()
		key := ipv4LPMKey{PrefixLen: uint32(prefix.Bits()), Address: binary.NativeEndian.Uint32(addr[:])}
		return t.objs.InternalV4.Put(key, one)
	}
	addr := prefix.Addr().As16()
	return t.objs.InternalV6.Put(ipv6LPMKey{PrefixLen: uint32(prefix.Bits()), Address: addr}, one)
}

// AddInternalPrefixesForVeth classifies the addresses of the bridge that
// the named host-side veth is attached to as internal. It must be called in
// the network namespace containing the veth.
func (t *Tracer) AddInternalPrefixesForVeth(name string) error {
	dev, err := netlink.LinkByName(name)
	if err != nil {
		return fmt.Errorf("looking up veth %s: %w", name, err)
	}
	return t.AddInternalPrefixesForInterface(dev.Attrs().Index)
}

func (t *Tracer) sampleEngine() (EngineSample, error) {
	if !t.cgroupEnabled {
		return EngineSample{}, errors.New("engine cgroup network accounting is unavailable")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var sample EngineSample
	for direction := directionRX; direction <= directionTX; direction++ {
		for scope := scopeInternal; scope <= scopeExternal; scope++ {
			key := cgroupCounterKey{Direction: direction, Scope: scope}
			values := make([]uint64, t.cpus)
			if err := t.objs.CgroupByteCounters.Lookup(key, &values); err != nil {
				if errors.Is(err, ebpf.ErrKeyNotExist) {
					continue
				}
				return EngineSample{}, err
			}
			var total uint64
			for _, value := range values {
				total += value
			}
			switch {
			case direction == directionRX && scope == scopeInternal:
				sample.InternalRX = total
			case direction == directionTX && scope == scopeInternal:
				sample.InternalTX = total
			case direction == directionRX && scope == scopeExternal:
				sample.ExternalRX = total
			case direction == directionTX && scope == scopeExternal:
				sample.ExternalTX = total
			}
		}
	}
	return sample, nil
}
