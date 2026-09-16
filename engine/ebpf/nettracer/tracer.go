// Package nettracer accounts for container traffic at TC hooks on the
// host-side veth. It separates traffic whose remote address belongs to a
// Dagger-managed bridge from traffic leaving those bridges.
package nettracer

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/engine/ebpf/internal/ebpfutil"
	"github.com/dagger/dagger/engine/netaccounting"
	"github.com/dagger/dagger/engine/realm"
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

// Owner identifies the trusted source of a network connection.
type Owner = netaccounting.Owner

const (
	OwnerDaggerland = netaccounting.OwnerDaggerland
	OwnerUserland   = netaccounting.OwnerUserland
)

type counterKey struct {
	Ifindex   uint32
	Direction uint8
	Scope     uint8
	Pad       uint16
}

type ipv4LPMKey struct {
	PrefixLen uint32
	Address   uint32
}

type ipv6LPMKey struct {
	PrefixLen uint32
	Address   [16]byte
}

type ownerCounterKey struct {
	Owner     uint8
	Direction uint8
	Scope     uint8
	Pad       uint8
}

// Sample is a cumulative snapshot for one veth.
type Sample struct {
	InternalRX uint64
	InternalTX uint64
	ExternalRX uint64
	ExternalTX uint64
}

// RealmSample is a cumulative network-layer snapshot for one realm.
type RealmSample struct {
	InternalRX uint64
	InternalTX uint64
	ExternalRX uint64
	ExternalTX uint64
}

// UnattributedSample reports traffic that reached the engine cgroup without
// an explicit socket or cgroup realm.
type UnattributedSample struct {
	InternalRXBytes   uint64
	InternalRXPackets uint64
	InternalTXBytes   uint64
	InternalTXPackets uint64
	ExternalRXBytes   uint64
	ExternalRXPackets uint64
	ExternalTXBytes   uint64
	ExternalTXPackets uint64
}

// Tracer owns the programs and maps shared by all CNI veths.
type Tracer struct {
	objs netbytesObjects
	cpus int
	mu   sync.Mutex

	cgroupIngress         link.Link
	cgroupEgress          link.Link
	realmEnforcementLinks []link.Link
	cgroupEnabled         bool
	cgroupErr             error
	clearTagger           func()
}

var activeTracer atomic.Pointer[Tracer]
var cgroupRegistrations atomic.Uint64
var cgroupRegistrationErrors atomic.Uint64
var cgroupRegistrationErrno atomic.Int64

func New() (*Tracer, error) {
	// This SCHED_CLS program does not use CO-RE, so unlike the diagnostic
	// tracers it does not require kernel BTF.
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
	// the CNI link. Keeping these prefixes in the same trie avoids extra parsing
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
	activeTracer.Store(t)
	t.clearTagger = netaccounting.RegisterSocketTagger(t)
	if err := t.addCurrentNetworkPrefixes(); err != nil {
		t.cgroupErr = err
		return t, nil //nolint:nilerr // Per-container TC accounting still works.
	}
	cgroupPath, err := currentCgroupPath()
	if err != nil {
		t.cgroupErr = err
		return t, nil //nolint:nilerr // Per-container TC accounting still works.
	}
	ingress, err := link.AttachCgroup(link.CgroupOptions{
		Path:    cgroupPath,
		Attach:  ebpf.AttachCGroupInetIngress,
		Program: objs.CountCgroupIngress,
	})
	if err != nil {
		t.cgroupErr = err
		return t, nil //nolint:nilerr // Per-container TC accounting still works.
	}
	t.cgroupIngress = ingress
	egress, err := link.AttachCgroup(link.CgroupOptions{
		Path:    "/sys/fs/cgroup",
		Attach:  ebpf.AttachCGroupInetEgress,
		Program: objs.CountCgroupEgress,
	})
	if err != nil {
		_ = ingress.Close()
		t.cgroupIngress = nil
		t.cgroupErr = err
		return t, nil //nolint:nilerr // Per-container TC accounting still works.
	}
	t.cgroupEgress = egress
	t.cgroupEnabled = true
	return t, nil
}

// EnableRealmEnforcementFromEnv enables enforcement after engine startup has
// completed. Before clients can connect, bootstrap traffic is audit-only.
func EnableRealmEnforcementFromEnv() error {
	if !realmEnforcementEnabled() {
		return nil
	}
	t := activeTracer.Load()
	if t == nil || !t.cgroupEnabled {
		return nil
	}
	tgid := uint32(os.Getpid())
	one := uint8(1)
	if err := t.objs.ProtectedTgids.Put(tgid, one); err != nil {
		return fmt.Errorf("protecting engine network realm: %w", err)
	}
	cgroupPath, err := currentCgroupPath()
	if err != nil {
		return fmt.Errorf("locating realm enforcement cgroup: %w", err)
	}
	programs := []struct {
		program *ebpf.Program
		attach  ebpf.AttachType
	}{
		{t.objs.EnforceConnect4, ebpf.AttachCGroupInet4Connect},
		{t.objs.EnforceConnect6, ebpf.AttachCGroupInet6Connect},
		{t.objs.EnforceSendmsg4, ebpf.AttachCGroupUDP4Sendmsg},
		{t.objs.EnforceSendmsg6, ebpf.AttachCGroupUDP6Sendmsg},
	}
	for _, program := range programs {
		attached, err := link.AttachCgroup(link.CgroupOptions{
			Path:    cgroupPath,
			Attach:  program.attach,
			Program: program.program,
		})
		if err != nil {
			for _, attached := range t.realmEnforcementLinks {
				_ = attached.Close()
			}
			t.realmEnforcementLinks = nil
			return fmt.Errorf("attaching network realm enforcement: %w", err)
		}
		t.realmEnforcementLinks = append(t.realmEnforcementLinks, attached)
	}
	return t.SetRealmEnforcement(true)
}

func realmEnforcementEnabled() bool {
	switch strings.ToLower(os.Getenv("_EXPERIMENTAL_DAGGER_NETWORK_REALM_ENFORCE")) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
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
	if t.clearTagger != nil {
		t.clearTagger()
	}
	var errs []error
	if t.cgroupIngress != nil {
		errs = append(errs, t.cgroupIngress.Close())
	}
	if t.cgroupEgress != nil {
		errs = append(errs, t.cgroupEgress.Close())
	}
	for _, attached := range t.realmEnforcementLinks {
		errs = append(errs, attached.Close())
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

// OwnerAccountingAvailable reports whether the engine cgroup programs are
// attached. Missing support is reported explicitly instead of returning zero
// counters that look authoritative.
func OwnerAccountingAvailable() bool {
	t := activeTracer.Load()
	return t != nil && t.cgroupEnabled
}

// OwnerAccountingError explains why engine-wide accounting is unavailable.
func OwnerAccountingError() error {
	t := activeTracer.Load()
	if t == nil {
		return errors.New("engine cgroup network accounting is not initialized")
	}
	return t.cgroupErr
}

// SampleRealms returns cumulative engine-wide packet counters for both
// realms.
func SampleRealms() (map[realm.Realm]RealmSample, error) {
	t := activeTracer.Load()
	if t == nil || !t.cgroupEnabled {
		return nil, errors.New("engine cgroup network accounting is unavailable")
	}
	daggerland, err := t.SampleRealm(realm.Daggerland)
	if err != nil {
		return nil, err
	}
	userland, err := t.SampleRealm(realm.Userland)
	if err != nil {
		return nil, err
	}
	return map[realm.Realm]RealmSample{
		realm.Daggerland: daggerland,
		realm.Userland:   userland,
	}, nil
}

// SetRealmEnforcement controls whether the engine may open an unattributed TCP
// or UDP connection. Packet audit counters are updated in both modes.
func (t *Tracer) SetRealmEnforcement(enforce bool) error {
	key := uint32(0)
	var value uint8
	if enforce {
		value = 1
	}
	return t.objs.EnforceOwnership.Put(key, value)
}

// RealmEnforcementEnabled reports whether unattributed engine connects are
// rejected.
func RealmEnforcementEnabled() (bool, error) {
	t := activeTracer.Load()
	if t == nil || !t.cgroupEnabled {
		return false, errors.New("engine cgroup network accounting is unavailable")
	}
	key := uint32(0)
	var value uint8
	if err := t.objs.EnforceOwnership.Lookup(key, &value); err != nil {
		return false, err
	}
	return value != 0, nil
}

// SampleUnattributed returns cumulative counters for packets without a realm.
func SampleUnattributed() (UnattributedSample, error) {
	t := activeTracer.Load()
	if t == nil || !t.cgroupEnabled {
		return UnattributedSample{}, errors.New("engine cgroup network accounting is unavailable")
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	var sample UnattributedSample
	for direction := directionRX; direction <= directionTX; direction++ {
		for scope := scopeInternal; scope <= scopeExternal; scope++ {
			key := netbytesUnattributedCounterKey{Direction: direction, Scope: scope}
			values := make([]netbytesUnattributedCounterValue, t.cpus)
			if err := t.objs.UnattributedCounters.Lookup(key, &values); err != nil {
				if errors.Is(err, ebpf.ErrKeyNotExist) {
					continue
				}
				return UnattributedSample{}, err
			}
			var bytes, packets uint64
			for _, value := range values {
				bytes += value.Bytes
				packets += value.Packets
			}
			switch {
			case direction == directionRX && scope == scopeInternal:
				sample.InternalRXBytes, sample.InternalRXPackets = bytes, packets
			case direction == directionTX && scope == scopeInternal:
				sample.InternalTXBytes, sample.InternalTXPackets = bytes, packets
			case direction == directionRX && scope == scopeExternal:
				sample.ExternalRXBytes, sample.ExternalRXPackets = bytes, packets
			case direction == directionTX && scope == scopeExternal:
				sample.ExternalTXBytes, sample.ExternalTXPackets = bytes, packets
			}
		}
	}
	return sample, nil
}

// SetSocketOwner attributes all packets on conn to owner. Callers must use
// separate connection pools for different owners.
func (t *Tracer) SetSocketOwner(conn syscall.Conn, owner Owner) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return fmt.Errorf("getting raw socket: %w", err)
	}
	var cookie uint64
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		cookie, socketErr = unix.GetsockoptUint64(int(fd), unix.SOL_SOCKET, unix.SO_COOKIE)
	}); err != nil {
		return fmt.Errorf("accessing socket: %w", err)
	}
	if socketErr != nil {
		return fmt.Errorf("getting socket cookie: %w", socketErr)
	}
	value := uint8(owner)
	if err := t.objs.SocketOwners.Put(cookie, value); err != nil {
		return fmt.Errorf("registering socket owner: %w", err)
	}
	return nil
}

// RegisterCgroupRealm attributes sockets in cgroupPath to realm. The returned
// cleanup removes the mapping after the cgroup has stopped.
func RegisterCgroupRealm(cgroupPath string, networkRealm realm.Realm) (_ func() error, rerr error) {
	defer func() {
		if rerr != nil {
			cgroupRegistrationErrors.Add(1)
			var errno syscall.Errno
			if errors.As(rerr, &errno) {
				cgroupRegistrationErrno.Store(int64(errno))
			}
		}
	}()
	t := activeTracer.Load()
	if t == nil {
		return func() error { return nil }, nil
	}
	handle, _, err := unix.NameToHandleAt(unix.AT_FDCWD, cgroupPath, 0)
	if err != nil {
		return nil, fmt.Errorf("getting cgroup ID for %s: %w", cgroupPath, err)
	}
	handleBytes := handle.Bytes()
	if len(handleBytes) < 8 {
		return nil, fmt.Errorf("getting cgroup ID for %s: handle is %d bytes", cgroupPath, len(handleBytes))
	}
	id := binary.NativeEndian.Uint64(handleBytes[:8])
	value := uint8(networkRealm)
	if err := t.objs.CgroupOwners.Put(id, value); err != nil {
		return nil, fmt.Errorf("registering cgroup owner: %w", err)
	}
	cgroupRegistrations.Add(1)
	return func() error {
		err := t.objs.CgroupOwners.Delete(id)
		if errors.Is(err, ebpf.ErrKeyNotExist) {
			return nil
		}
		return err
	}, nil
}

// CgroupRegistrationStats reports attempted child-cgroup realm setup.
func CgroupRegistrationStats() (registered, failed uint64, lastErrno int64) {
	return cgroupRegistrations.Load(), cgroupRegistrationErrors.Load(), cgroupRegistrationErrno.Load()
}

// PrepareCommand starts a subprocess directly in a temporary realm cgroup.
// Descendant helper processes inherit that realm. The cleanup must run after
// cmd has exited.
func PrepareCommand(cmd *exec.Cmd, networkRealm realm.Realm) (func() error, error) {
	t := activeTracer.Load()
	if t == nil || !t.cgroupEnabled {
		return func() error { return nil }, nil
	}
	cgroupPath, err := os.MkdirTemp("/sys/fs/cgroup", "dagger-network-")
	if err != nil {
		return nil, fmt.Errorf("creating command cgroup: %w", err)
	}
	unregister, err := RegisterCgroupRealm(cgroupPath, networkRealm)
	if err != nil {
		_ = os.Remove(cgroupPath)
		return nil, err
	}
	cgroup, err := os.Open(cgroupPath)
	if err != nil {
		_ = unregister()
		_ = os.Remove(cgroupPath)
		return nil, fmt.Errorf("opening command cgroup: %w", err)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = new(syscall.SysProcAttr)
	}
	cmd.SysProcAttr.UseCgroupFD = true
	cmd.SysProcAttr.CgroupFD = int(cgroup.Fd())
	return func() error {
		return errors.Join(cgroup.Close(), unregister(), os.Remove(cgroupPath))
	}, nil
}

// SetSocketOwnerBeforeConnect is suitable for net.Dialer.ControlContext. It
// registers the socket before connect so TCP and TLS setup are classified.
func (t *Tracer) SetSocketOwnerBeforeConnect(raw syscall.RawConn, owner Owner) error {
	var cookie uint64
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		cookie, socketErr = unix.GetsockoptUint64(int(fd), unix.SOL_SOCKET, unix.SO_COOKIE)
	}); err != nil {
		return err
	}
	if socketErr != nil {
		return socketErr
	}
	return t.objs.SocketOwners.Put(cookie, uint8(owner))
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

// Attachment accounts for one host-side veth.
type Attachment struct {
	tracer  *Tracer
	ifindex int
	ingress link.Link
	egress  link.Link
}

func (t *Tracer) Attach(ifindex int) (_ *Attachment, rerr error) {
	ingress, err := link.AttachTCX(link.TCXOptions{
		Interface: ifindex,
		Program:   t.objs.CountIngress,
		Attach:    ebpf.AttachTCXIngress,
	})
	if err != nil {
		return nil, fmt.Errorf("attaching TCX ingress to interface %d: %w", ifindex, err)
	}
	defer func() {
		if rerr != nil {
			ingress.Close()
		}
	}()
	egress, err := link.AttachTCX(link.TCXOptions{
		Interface: ifindex,
		Program:   t.objs.CountEgress,
		Attach:    ebpf.AttachTCXEgress,
	})
	if err != nil {
		return nil, fmt.Errorf("attaching TCX egress to interface %d: %w", ifindex, err)
	}
	return &Attachment{tracer: t, ifindex: ifindex, ingress: ingress, egress: egress}, nil
}

// AttachInterface discovers the interface and its bridge prefixes before
// attaching the accounting programs. It must be called in the network
// namespace containing the host-side veth.
func (t *Tracer) AttachInterface(name string) (*Attachment, error) {
	dev, err := netlink.LinkByName(name)
	if err != nil {
		return nil, fmt.Errorf("looking up veth %s: %w", name, err)
	}
	if err := t.AddInternalPrefixesForInterface(dev.Attrs().Index); err != nil {
		return nil, err
	}
	return t.Attach(dev.Attrs().Index)
}

func (a *Attachment) Sample() (Sample, error) {
	a.tracer.mu.Lock()
	defer a.tracer.mu.Unlock()
	var sample Sample
	for direction := directionRX; direction <= directionTX; direction++ {
		for scope := scopeInternal; scope <= scopeExternal; scope++ {
			key := counterKey{Ifindex: uint32(a.ifindex), Direction: direction, Scope: scope}
			values := make([]uint64, a.tracer.cpus)
			if err := a.tracer.objs.ByteCounters.Lookup(key, &values); err != nil {
				if errors.Is(err, ebpf.ErrKeyNotExist) {
					continue
				}
				return Sample{}, err
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

// SampleRealm returns the authoritative cumulative packet count for a realm
// across the engine cgroup and all descendant cgroups.
func (t *Tracer) SampleRealm(networkRealm realm.Realm) (RealmSample, error) {
	if !t.cgroupEnabled {
		return RealmSample{}, errors.New("engine cgroup network accounting is unavailable")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var sample RealmSample
	for direction := directionRX; direction <= directionTX; direction++ {
		for scope := scopeInternal; scope <= scopeExternal; scope++ {
			key := ownerCounterKey{Owner: uint8(networkRealm), Direction: direction, Scope: scope}
			values := make([]uint64, t.cpus)
			if err := t.objs.OwnerByteCounters.Lookup(key, &values); err != nil {
				if errors.Is(err, ebpf.ErrKeyNotExist) {
					continue
				}
				return RealmSample{}, err
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

func (a *Attachment) Close() error {
	var errs []error
	if a.ingress != nil {
		errs = append(errs, a.ingress.Close())
	}
	if a.egress != nil {
		errs = append(errs, a.egress.Close())
	}
	for direction := directionRX; direction <= directionTX; direction++ {
		for scope := scopeInternal; scope <= scopeExternal; scope++ {
			key := counterKey{Ifindex: uint32(a.ifindex), Direction: direction, Scope: scope}
			if err := a.tracer.objs.ByteCounters.Delete(key); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
