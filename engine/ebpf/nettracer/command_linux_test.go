//go:build linux && (386 || amd64 || arm64)

package nettracer

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCommandWaitEmpty(t *testing.T) {
	path := t.TempDir()
	events := filepath.Join(path, "cgroup.events")
	require.NoError(t, os.WriteFile(events, []byte("populated 1\nfrozen 0\n"), 0o600))
	c := &Command{path: path, accounting: &commandAccounting{done: make(chan struct{})}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- c.WaitEmpty(ctx) }()
	select {
	case err := <-finished:
		t.Fatalf("returned while helper is alive: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	// Replace atomically so the reader never observes a partially written file.
	next := filepath.Join(path, "next")
	require.NoError(t, os.WriteFile(next, []byte("populated 0\nfrozen 0\n"), 0o600))
	require.NoError(t, os.Rename(next, events))
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("did not observe the helper exiting")
	}
	require.NoError(t, os.WriteFile(events, []byte("populated 1\n"), 0o600))
	cancel()
	require.ErrorIs(t, c.WaitEmpty(ctx), context.Canceled)
}

// Opt in on a Linux 6.15+ runner with BPF privileges, a writable cgroup v2
// mount and a veth default route (the same requirements as the engine).
// Use engine-dev test --ebpf --pkg ./engine/ebpf/nettracer
// to run the network eBPF tests in a privileged test container.
func TestCommandAccounting(t *testing.T) {
	testCommandAccounting(t, false)
}

func TestExecMountCommandAccounting(t *testing.T) {
	testCommandAccounting(t, true)
}

func testCommandAccounting(t *testing.T, execMount bool) {
	t.Helper()
	if os.Getenv("DAGGER_TEST_EBPF") != "1" {
		t.Skip("set DAGGER_TEST_EBPF=1 on a privileged Linux 6.15+ runner")
	}
	tracer, err := New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, tracer.Close()) })
	require.NoError(t, tracer.commandErr)
	require.NotNil(t, tracer.commands)
	// Use loopback solely as an isolated test peer. Production excludes it.
	require.NoError(t, tracer.objs.EngineLoopbackIfindex.Put(uint32(0), uint32(0)))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, listener.Close()) })
	served := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			served <- err
			return
		}
		defer conn.Close()
		request := make([]byte, 7)
		if _, err := io.ReadFull(conn, request); err != nil {
			served <- err
			return
		}
		_, err = io.Copy(conn, bytes.NewReader(make([]byte, 1<<20)))
		served <- err
	}()
	git := filepath.Join(t.TempDir(), "git")
	executable, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, os.Symlink(executable, git))
	cmd := exec.CommandContext(t.Context(), git, "-test.run=^TestCommandNetworkHelper$")
	cmd.Env = append(os.Environ(), "NETTRACER_HELPER=parent", "NETTRACER_PEER="+listener.Addr().String())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	enginePath, err := currentCgroupPath()
	require.NoError(t, err)
	var command *Command
	if execMount {
		root := filepath.Join(filepath.Dir(enginePath), "exec")
		require.NoError(t, os.MkdirAll(root, 0o755))
		parent, err := os.MkdirTemp(root, "test-mount-")
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, os.Remove(parent)) })
		mounts := filepath.Join(parent, "sshfs")
		require.NoError(t, os.Mkdir(mounts, 0o755))
		t.Cleanup(func() { require.NoError(t, os.Remove(mounts)) })
		command, err = PrepareCommandIn(cmd, mounts)
		require.NoError(t, err)
		require.Equal(t, mounts, filepath.Dir(command.path))
	} else {
		command, err = PrepareCommand(cmd)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(filepath.Dir(enginePath), "git"), filepath.Dir(command.path))
	}
	require.False(t, strings.HasPrefix(command.path, enginePath+"/"))
	t.Cleanup(func() { require.NoError(t, command.Close()) })
	require.True(t, cmd.SysProcAttr.Setpgid)
	require.True(t, cmd.SysProcAttr.UseCgroupFD)
	before, err := SampleEngine()
	require.NoError(t, err)
	require.NoError(t, cmd.Run(), output.String())
	require.NoError(t, <-served)
	require.Contains(t, output.String(), filepath.Base(command.path))
	sample, err := command.Sample()
	require.NoError(t, err)
	require.GreaterOrEqual(t, sample.InternalRX, uint64(1<<20))
	require.Greater(t, sample.InternalTX, uint64(0))
	require.Zero(t, sample.ExternalRX)
	after, err := SampleEngine()
	require.NoError(t, err)
	// The engine-side server sends the large reply; the helper receives it.
	// Its RX bytes must not be added back into the engine's counters.
	require.GreaterOrEqual(t, after.InternalTX-before.InternalTX, uint64(1<<20))
	require.Less(t, after.InternalRX-before.InternalRX, sample.InternalRX)
	require.NoError(t, command.Close())
	_, err = os.Stat(command.path)
	require.True(t, os.IsNotExist(err), "command cgroup should be removed: %v", err)
}

func TestCommandRoot(t *testing.T) {
	for _, tc := range []struct {
		engine, root string
	}{
		{"/sys/fs/cgroup/engine", "/sys/fs/cgroup"},
		{"/sys/fs/cgroup/init", "/sys/fs/cgroup"},
		{"/sys/fs/cgroup/tenant/engine", "/sys/fs/cgroup/tenant"},
		{"/sys/fs/cgroup", ""},
		{"/sys/fs/cgroup/../host", ""},
		{"/outside/engine", ""},
		{"/sys/fs/cgroup/git", ""},
		{"/sys/fs/cgroup/rg", ""},
		{"/sys/fs/cgroup/sshfs", ""},
		{"/sys/fs/cgroup/exec", ""},
	} {
		t.Run(tc.engine, func(t *testing.T) {
			root, err := commandRoot(tc.engine)
			if tc.root == "" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.root, root)
			}
		})
	}
}

func TestCommandPlacementWithoutEBPF(t *testing.T) {
	root := t.TempDir()
	a := &commandAccounting{paths: map[string]string{}, pending: map[string]struct{}{}}
	for _, name := range []string{"git", "rg"} {
		a.paths[name] = filepath.Join(root, name)
		require.NoError(t, os.Mkdir(a.paths[name], 0o755))
	}
	// Helpers are outside engine totals, so unavailable helper hooks must not
	// disable otherwise working engine accounting.
	previousTracer := activeTracer.Swap(&Tracer{cgroupEnabled: true})
	previousCommands := fallbackCommands.Swap(a)
	t.Cleanup(func() {
		activeTracer.Store(previousTracer)
		fallbackCommands.Store(previousCommands)
	})
	require.True(t, EngineAccountingAvailable())
	require.NoError(t, EngineAccountingError())
	for _, name := range []string{"git", "rg"} {
		t.Run(name, func(t *testing.T) {
			paths := map[string]bool{}
			for range 2 {
				cmd := &exec.Cmd{Path: "/usr/bin/" + name, SysProcAttr: &syscall.SysProcAttr{Setpgid: true}}
				command, err := PrepareCommand(cmd)
				require.NoError(t, err)
				require.Equal(t, a.paths[name], filepath.Dir(command.path))
				require.False(t, paths[command.path], "operation cgroups must be unique")
				paths[command.path] = true
				require.True(t, cmd.SysProcAttr.UseCgroupFD)
				require.True(t, cmd.SysProcAttr.Setpgid)
				require.Equal(t, int(command.fd.Fd()), cmd.SysProcAttr.CgroupFD)
				_, err = command.Sample()
				require.ErrorContains(t, err, "network accounting is unavailable")
				require.NoError(t, command.Close())
				require.NoError(t, command.Close())
				_, err = os.Stat(command.path)
				require.True(t, os.IsNotExist(err))
			}
		})
	}
	cmd := &exec.Cmd{Path: "/usr/bin/dnsmasq"}
	_, err := PrepareCommand(cmd)
	require.Error(t, err)
	require.Nil(t, cmd.SysProcAttr, "shared infrastructure stays in the engine cgroup")
	// SSHFS belongs to its owning exec, not a sibling helper category.
	execPath := filepath.Join(root, "exec", "exec-id", "sshfs")
	require.NoError(t, os.MkdirAll(execPath, 0o755))
	sshfs := &exec.Cmd{Path: "/usr/bin/sshfs"}
	mount, err := PrepareCommandIn(sshfs, execPath)
	require.NoError(t, err)
	require.Equal(t, execPath, filepath.Dir(mount.CgroupPath()))
	require.True(t, sshfs.SysProcAttr.UseCgroupFD)
	require.NoError(t, mount.Close())
}

func TestCommandNetworkHelper(t *testing.T) {
	switch os.Getenv("NETTRACER_HELPER") {
	case "parent":
		// The helper's child gets no explicit CgroupFD: inheritance must cover
		// Git's SSH and HTTP subprocesses too.
		child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCommandNetworkHelper$")
		for _, env := range os.Environ() {
			if !strings.HasPrefix(env, "NETTRACER_HELPER=") {
				child.Env = append(child.Env, env)
			}
		}
		child.Env = append(child.Env, "NETTRACER_HELPER=transfer")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		require.NoError(t, child.Run())
	case "transfer":
		path, err := os.ReadFile("/proc/self/cgroup")
		require.NoError(t, err)
		_, err = os.Stdout.Write(path)
		require.NoError(t, err)
		conn, err := net.Dial("tcp", os.Getenv("NETTRACER_PEER"))
		require.NoError(t, err)
		defer conn.Close()
		_, err = io.WriteString(conn, "request")
		require.NoError(t, err)
		n, err := io.Copy(io.Discard, conn)
		require.NoError(t, err)
		require.EqualValues(t, 1<<20, n)
	default:
		t.Skip("subprocess fixture")
	}
}
