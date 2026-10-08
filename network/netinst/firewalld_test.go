package netinst

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The probe runs at engine start, so a system bus whose connection never
// completes must not stall it past the timeout. godbus does not apply the
// context while dialing.
func TestFirewalldRunningStalledBus(t *testing.T) {
	// A listener with a backlog of 0 whose one pending connection is taken:
	// the next connect hangs.
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	require.NoError(t, err)
	t.Cleanup(func() { syscall.Close(fd) })
	require.NoError(t, syscall.Bind(fd, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}))
	require.NoError(t, syscall.Listen(fd, 0))
	sa, err := syscall.Getsockname(fd)
	require.NoError(t, err)
	addr := fmt.Sprintf("127.0.0.1:%d", sa.(*syscall.SockaddrInet4).Port)
	first, err := net.DialTimeout("tcp", addr, time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { first.Close() })
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", fmt.Sprintf("tcp:host=127.0.0.1,port=%d", sa.(*syscall.SockaddrInet4).Port))

	start := time.Now()
	require.False(t, firewalldRunning(context.Background(), 200*time.Millisecond))
	require.Less(t, time.Since(start), 2*time.Second)
}

func TestFirewalldRunningNoBus(t *testing.T) {
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "missing"))
	require.False(t, firewalldRunning(context.Background(), time.Second))
}
