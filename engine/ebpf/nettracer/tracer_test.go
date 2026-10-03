//go:build linux && (386 || amd64 || arm64)

package nettracer

import (
	"encoding/binary"
	"errors"
	"math"
	"net/netip"
	"os"
	"testing"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/rlimit"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestTCProgramsClassifyAndContinue(t *testing.T) {
	_ = rlimit.RemoveMemlock()
	var objs netbytesObjects
	if err := loadNetbytesObjects(&objs, nil); err != nil {
		if os.Getenv("DAGGER_TEST_EBPF") != "1" && (errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES)) {
			t.Skipf("cannot load BPF programs: %v", err)
		}
		require.NoError(t, err)
	}
	t.Cleanup(func() { require.NoError(t, objs.Close()) })

	cpus, err := ebpf.PossibleCPU()
	require.NoError(t, err)
	tracer := &Tracer{objs: objs, cpus: cpus}
	// BPF_PROG_TEST_RUN uses the loopback interface (ifindex 1).
	_, err = tracer.reserveCounters(1)
	require.NoError(t, err)
	require.NoError(t, tracer.AddInternalPrefix(
		netip.MustParsePrefix("10.0.0.0/24"),
	))
	paddedIPv4 := ipv4EthernetPacket("10.0.0.2", "192.0.2.1", false)
	binary.BigEndian.PutUint16(paddedIPv4[16:18], 40)
	paddedIPv6 := ipv6EthernetPacket("fd00::2", "2001:db8::1")
	binary.BigEndian.PutUint16(paddedIPv6[18:20], 1)

	tests := []struct {
		name      string
		program   *ebpf.Program
		packet    []byte
		direction uint8
		scope     uint8
		bytes     uint64
	}{
		{"tx internal", objs.CountIngress, ipv4EthernetPacket("10.0.0.2", "10.0.0.3", false), directionTX, scopeInternal, 46},
		{"tx external", objs.CountIngress, ipv4EthernetPacket("10.0.0.2", "192.0.2.1", false), directionTX, scopeExternal, 46},
		{"tx excludes ipv4 padding", objs.CountIngress, paddedIPv4, directionTX, scopeExternal, 40},
		{"tx excludes ipv6 padding", objs.CountIngress, paddedIPv6, directionTX, scopeExternal, 41},
		{"tx external vlan", objs.CountIngress, ipv4EthernetPacket("10.0.0.2", "192.0.2.1", true), directionTX, scopeExternal, 46},
		{"tx external ipv6", objs.CountIngress, ipv6EthernetPacket("fd00::2", "2001:db8::1"), directionTX, scopeExternal, 46},
		{"rx internal", objs.CountEgress, ipv4EthernetPacket("10.0.0.3", "10.0.0.2", false), directionRX, scopeInternal, 46},
		{"rx external", objs.CountEgress, ipv4EthernetPacket("192.0.2.1", "10.0.0.2", false), directionRX, scopeExternal, 46},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := counterTotal(t, tracer, test.direction, test.scope)
			result, _, err := test.program.Test(test.packet)
			require.NoError(t, err)
			require.Equal(t, uint32(math.MaxUint32), result)
			require.Equal(
				t,
				before+test.bytes,
				counterTotal(t, tracer, test.direction, test.scope),
			)
		})
	}

	t.Run("arp is excluded", func(t *testing.T) {
		packet := make([]byte, 60)
		binary.BigEndian.PutUint16(packet[12:14], 0x0806)
		before := counterTotal(t, tracer, directionTX, scopeInternal)
		result, _, err := objs.CountIngress.Test(packet)
		require.NoError(t, err)
		require.Equal(t, uint32(math.MaxUint32), result)
		require.Equal(t, before, counterTotal(t, tracer, directionTX, scopeInternal))
	})
}

func TestCounterReservationIsAtomic(t *testing.T) {
	_ = rlimit.RemoveMemlock()
	var objs netbytesObjects
	if err := loadNetbytesObjects(&objs, nil); err != nil {
		if os.Getenv("DAGGER_TEST_EBPF") != "1" && (errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES)) {
			t.Skipf("cannot load BPF programs: %v", err)
		}
		require.NoError(t, err)
	}
	t.Cleanup(func() { require.NoError(t, objs.Close()) })

	cpus, err := ebpf.PossibleCPU()
	require.NoError(t, err)
	tracer := &Tracer{objs: objs, cpus: cpus}
	values := make([]uint64, cpus)
	for i := range 4093 {
		key := counterKey{Ifindex: uint32(i + 1)}
		require.NoError(t, objs.ByteCounters.Put(key, values))
	}

	const reservedIfindex = 5000
	_, err = tracer.reserveCounters(reservedIfindex)
	require.Error(t, err)
	for direction := directionRX; direction <= directionTX; direction++ {
		for scope := scopeInternal; scope <= scopeExternal; scope++ {
			key := counterKey{
				Ifindex: reservedIfindex, Direction: direction, Scope: scope,
			}
			require.ErrorIs(t, objs.ByteCounters.Lookup(key, &values), ebpf.ErrKeyNotExist)
		}
	}
}

func ipv4EthernetPacket(source, dest string, vlan bool) []byte {
	l3Offset := 14
	if vlan {
		l3Offset += 4
	}
	packet := make([]byte, l3Offset+46)
	if vlan {
		binary.BigEndian.PutUint16(packet[12:14], 0x8100)
		binary.BigEndian.PutUint16(packet[16:18], 0x0800)
	} else {
		binary.BigEndian.PutUint16(packet[12:14], 0x0800)
	}
	packet[l3Offset] = 0x45
	binary.BigEndian.PutUint16(packet[l3Offset+2:l3Offset+4], uint16(len(packet)-l3Offset))
	sourceBytes := netip.MustParseAddr(source).As4()
	destBytes := netip.MustParseAddr(dest).As4()
	copy(packet[l3Offset+12:l3Offset+16], sourceBytes[:])
	copy(packet[l3Offset+16:l3Offset+20], destBytes[:])
	return packet
}

func ipv6EthernetPacket(source, dest string) []byte {
	const l3Offset = 14
	packet := make([]byte, l3Offset+46)
	binary.BigEndian.PutUint16(packet[12:14], 0x86dd)
	packet[l3Offset] = 0x60
	binary.BigEndian.PutUint16(packet[l3Offset+4:l3Offset+6], uint16(len(packet)-l3Offset-40))
	sourceBytes := netip.MustParseAddr(source).As16()
	destBytes := netip.MustParseAddr(dest).As16()
	copy(packet[l3Offset+8:l3Offset+24], sourceBytes[:])
	copy(packet[l3Offset+24:l3Offset+40], destBytes[:])
	return packet
}

func counterTotal(
	t *testing.T,
	tracer *Tracer,
	direction, scope uint8,
) uint64 {
	t.Helper()
	iterator := tracer.objs.ByteCounters.Iterate()
	var key counterKey
	values := make([]uint64, tracer.cpus)
	var total uint64
	for iterator.Next(&key, &values) {
		if key.Direction == direction && key.Scope == scope {
			for _, value := range values {
				total += value
			}
		}
	}
	require.NoError(t, iterator.Err())
	return total
}
