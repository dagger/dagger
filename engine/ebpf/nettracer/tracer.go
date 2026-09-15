// Package nettracer accounts for container traffic at TC hooks on the
// host-side veth. It separates traffic whose remote address belongs to a
// Dagger-managed bridge from traffic leaving those bridges.
package nettracer

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"sync"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
	"github.com/vishvananda/netlink"

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
	scopeUnknown
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

// Sample is a cumulative snapshot for one veth.
type Sample struct {
	InternalRX uint64
	InternalTX uint64
	ExternalRX uint64
	ExternalTX uint64
	UnknownRX  uint64
	UnknownTX  uint64
}

// Tracer owns the programs and maps shared by all CNI veths.
type Tracer struct {
	objs netbytesObjects
	cpus int
	mu   sync.Mutex
}

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
	return &Tracer{objs: objs, cpus: cpus}, nil
}

func (t *Tracer) Close() error { return t.objs.Close() }

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
	for direction := uint8(directionRX); direction <= directionTX; direction++ {
		for scope := uint8(scopeInternal); scope <= scopeUnknown; scope++ {
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
			case direction == directionRX && scope == scopeUnknown:
				sample.UnknownRX = total
			case direction == directionTX && scope == scopeUnknown:
				sample.UnknownTX = total
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
	for direction := uint8(directionRX); direction <= directionTX; direction++ {
		for scope := uint8(scopeInternal); scope <= scopeUnknown; scope++ {
			key := counterKey{Ifindex: uint32(a.ifindex), Direction: direction, Scope: scope}
			if err := a.tracer.objs.ByteCounters.Delete(key); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
