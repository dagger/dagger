package core

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEmulatorMountExecWhileForking execs the emulator mount's source right
// after mounting it while other goroutines start processes, as the engine
// does when it runs many execs at once. A child forked while anything holds
// the emulator open for writing keeps that write fd until the child execs,
// and exec fails with ETXTBSY ("text file busy") in the meantime (#14709).
func TestEmulatorMountExecWhileForking(t *testing.T) {
	trueBin, err := exec.LookPath("true")
	require.NoError(t, err)
	bin, err := os.ReadFile(trueBin)
	require.NoError(t, err)

	// Pad the binary to roughly the size of a QEMU emulator; trailing bytes
	// after the ELF image don't affect exec.
	emulatorPath := filepath.Join(t.TempDir(), "buildkit-qemu-test")
	bin = append(bin, bytes.Repeat([]byte{0}, 8<<20)...)
	require.NoError(t, os.WriteFile(emulatorPath, bin, 0o755))

	emu := &emulator{path: emulatorPath}

	stop := make(chan struct{})
	var forkers sync.WaitGroup
	for range 4 {
		forkers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = exec.Command(trueBin).Run()
			}
		})
	}
	defer func() {
		close(stop)
		forkers.Wait()
	}()

	var busy int
	const attempts = 200
	for range attempts {
		mountable, err := emu.Mount(t.Context(), true)
		require.NoError(t, err)
		mounts, release, err := mountable.Mount()
		require.NoError(t, err)
		require.Len(t, mounts, 1)
		require.Equal(t, "bind", mounts[0].Type)
		require.Contains(t, mounts[0].Options, "ro")

		cmd := exec.Command(mounts[0].Source)
		cmd.Args = []string{"true"} // busybox picks the applet from argv[0]
		err = cmd.Run()
		if err != nil {
			require.ErrorIs(t, err, syscall.ETXTBSY)
			busy++
		}
		require.NoError(t, release())
	}
	require.Zero(t, busy, "%d of %d execs of the emulator failed with ETXTBSY", busy, attempts)
}
