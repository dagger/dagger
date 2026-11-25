package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sddaemon "github.com/coreos/go-systemd/v22/daemon"
	"github.com/gofrs/flock"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/sys/unix"
)

const (
	supervisorTestMode = "_DAGGER_TEST_SUPERVISOR_MODE"
	supervisorTestRoot = "_DAGGER_TEST_SUPERVISOR_ROOT"
)

// Run a real child process so Go's fatal panic and exit behavior are exercised.
func TestEngineSupervisorProcess(t *testing.T) {
	mode := os.Getenv(supervisorTestMode)
	if mode == "" {
		return
	}
	root := os.Getenv(supervisorTestRoot)
	fd, err := strconv.Atoi(os.Getenv(engineStatusFDEnv))
	require.NoError(t, err)
	engineStatusPipe = os.NewFile(uintptr(fd), "test-engine-status")
	unix.CloseOnExec(fd)
	// The helper process owns this descriptor; process exit closes it.
	attempt := os.Getenv(engineCacheResetEnv)
	file, err := os.OpenFile(filepath.Join(root, "attempts"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = fmt.Fprintln(file, attempt)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	if mode != "no root" {
		require.NoError(t, reportEngineStatus(engineStatus{Root: root}))
	}

	switch mode {
	case "normal":
		require.NoError(t, reportEngineStatus(engineStatus{Notify: sddaemon.SdNotifyReady}))
		require.NoError(t, reportEngineStatus(engineStatus{Notify: sddaemon.SdNotifyStopping}))
		fmt.Println("normal engine output")
		return
	case "extra fd":
		file := os.NewFile(3, "inherited-fd")
		data, err := io.ReadAll(file)
		require.NoError(t, err)
		require.Equal(t, "inherited", string(data))
		require.NoError(t, file.Close())
		return
	case "exit":
		os.Exit(7)
	case "killed":
		require.NoError(t, unix.Kill(os.Getpid(), unix.SIGKILL))
		select {}
	case "unrelated panic":
		panic("unrelated engine bug")
	case "signal":
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, unix.SIGTERM)
		require.NoError(t, reportEngineStatus(engineStatus{Notify: sddaemon.SdNotifyReady}))
		fmt.Println("waiting for signal")
		select {
		case <-signals:
			fmt.Println("received SIGTERM")
			os.Exit(17)
		case <-time.After(5 * time.Second):
			t.Fatal("supervisor did not forward SIGTERM")
		}
	}

	path := filepath.Join(root, "worker", "containerdmeta.db")
	if mode == "snapshotter" {
		path = filepath.Join(root, "worker", "snapshots", "metadata.db")
	}
	if mode == "repeat" && attempt == "1" {
		writeCorruptSupervisorDB(t, path)
	}
	if mode == "after ready" {
		require.NoError(t, reportEngineStatus(engineStatus{Notify: sddaemon.SdNotifyReady}))
	}
	// Mirror startup's state lock: the supervisor must wait for process death
	// before it can acquire the lock and remove either database or snapshots.
	lock := flock.New(filepath.Join(root, "dagger-engine.lock"))
	locked, err := lock.TryLock()
	require.NoError(t, err)
	require.True(t, locked)
	defer lock.Close()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	db, err := bolt.Open(path, 0600, &bolt.Options{NoFreelistSync: true})
	require.NoError(t, err)
	if attempt == "0" || mode == "repeat" {
		// freepages can return while its goroutine is unwinding the panic.
		// Keep the child alive so the fatal panic is fully reported.
		time.Sleep(5 * time.Second)
		t.Fatal("corrupt database unexpectedly opened")
	}
	require.NoError(t, db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucket([]byte("after-reset"))
		return err
	}))
	require.NoError(t, db.Close())
	require.NoError(t, reportEngineStatus(engineStatus{Notify: sddaemon.SdNotifyReady}))
	fmt.Println("engine recovered")
}

func TestEngineSupervisor(t *testing.T) {
	for _, tc := range []struct {
		mode  string
		code  int
		reset bool
	}{
		{mode: "normal"},
		{mode: "exit", code: 7},
		{mode: "killed", code: 137},
		{mode: "containerd", reset: true},
		{mode: "snapshotter", reset: true},
		{mode: "repeat", code: 2, reset: true},
		{mode: "unrelated panic", code: 2},
		{mode: "no root", code: 2},
		{mode: "after ready", code: 2},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "worker", "containerdmeta.db")
			if tc.mode == "snapshotter" {
				path = filepath.Join(root, "worker", "snapshots", "metadata.db")
			}
			writeCorruptSupervisorDB(t, path)
			marker := filepath.Join(root, "worker", "old-snapshot")
			require.NoError(t, os.WriteFile(marker, []byte("old"), 0600))
			queryDB := filepath.Join(root, "dagql-cache.db")
			require.NoError(t, os.WriteFile(queryDB, []byte("old query cache"), 0600))
			var stdout, stderr bytes.Buffer
			code, err := superviseEngine(supervisorTestChild(tc.mode, root), make(chan os.Signal), &stdout, &stderr)
			require.NoError(t, err)
			require.Equal(t, tc.code, code, stderr.String())
			attempts, err := os.ReadFile(filepath.Join(root, "attempts"))
			require.NoError(t, err)
			if tc.reset {
				require.Equal(t, "0\n1\n", string(attempts), "retry exactly once and mark the recovered engine")
				require.NoFileExists(t, marker)
				require.NoFileExists(t, queryDB)
				require.Contains(t, stderr.String(), "self identifies as 0")
				require.Contains(t, stderr.String(), "go.etcd.io/bbolt.(*DB).freepages")
				if tc.code == 0 {
					require.Contains(t, stdout.String(), "engine recovered")
				}
			} else {
				require.Equal(t, "0\n", string(attempts))
				require.FileExists(t, marker)
				require.FileExists(t, queryDB)
			}
		})
	}
}

func TestEngineSupervisorForwardsSignals(t *testing.T) {
	root := t.TempDir()
	signals := make(chan os.Signal, 1)
	stdout := &supervisorSignalWriter{ready: make(chan struct{})}
	var stderr bytes.Buffer
	done := make(chan struct{})
	var code int
	var err error
	go func() {
		defer close(done)
		code, err = superviseEngine(supervisorTestChild("signal", root), signals, stdout, &stderr)
	}()
	select {
	case <-stdout.ready:
		signals <- unix.SIGTERM
	case <-time.After(5 * time.Second):
		t.Fatal("child never became ready")
	}
	<-done
	require.NoError(t, err)
	require.Equal(t, 17, code)
	require.Contains(t, stdout.String(), "received SIGTERM")
}

func TestEngineSupervisorNotifiesSystemd(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "notify.sock")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	require.NoError(t, err)
	defer conn.Close()
	t.Setenv("NOTIFY_SOCKET", path)
	code, err := superviseEngine(supervisorTestChild("normal", root), make(chan os.Signal), &bytes.Buffer{}, &bytes.Buffer{})
	require.NoError(t, err)
	require.Zero(t, code)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	for _, want := range []string{sddaemon.SdNotifyReady, sddaemon.SdNotifyStopping} {
		buf := make([]byte, 128)
		n, err := conn.Read(buf)
		require.NoError(t, err)
		require.Equal(t, want, string(buf[:n]))
	}
}

func TestEngineSupervisorPreservesExtraFiles(t *testing.T) {
	root := t.TempDir()
	read, write, err := os.Pipe()
	require.NoError(t, err)
	defer read.Close()
	_, err = write.WriteString("inherited")
	require.NoError(t, err)
	require.NoError(t, write.Close())
	var stderr bytes.Buffer
	code, err := superviseEngine(func() *exec.Cmd {
		cmd := supervisorTestChild("extra fd", root)()
		cmd.ExtraFiles = []*os.File{read}
		return cmd
	}, make(chan os.Signal), &bytes.Buffer{}, &stderr)
	require.NoError(t, err)
	require.Zero(t, code, stderr.String())
}

func TestEngineSupervisorLaunchFailure(t *testing.T) {
	code, err := superviseEngine(func() *exec.Cmd {
		return exec.Command(filepath.Join(t.TempDir(), "missing-engine"))
	}, make(chan os.Signal), &bytes.Buffer{}, &bytes.Buffer{})
	require.ErrorContains(t, err, "start engine")
	require.Equal(t, 1, code)
}

func TestResetEngineCacheRequiresLock(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "worker", "keep")
	require.NoError(t, os.MkdirAll(filepath.Dir(marker), 0700))
	require.NoError(t, os.WriteFile(marker, nil, 0600))
	lock := flock.New(filepath.Join(root, "dagger-engine.lock"))
	locked, err := lock.TryLock()
	require.NoError(t, err)
	require.True(t, locked)
	defer lock.Close()
	require.ErrorContains(t, resetEngineCache(root), "state is locked")
	require.FileExists(t, marker)
}

func TestIsBoltCorruptionPanic(t *testing.T) {
	const assertion = "panic: assertion failed: Page expected to be: 502, but self identifies as 0\n\ngoroutine 17 [running]:\n"
	const boltFrame = "go.etcd.io/bbolt/internal/common.(*Page).FastCheck(...)\n"
	for _, tc := range []struct {
		name  string
		log   string
		match bool
	}{
		{"page assertion", assertion + boltFrame, true},
		{"freepages invariant", "panic: freepages: failed to get all reachable pages\n\ngoroutine 1 [running]:\ngo.etcd.io/bbolt.(*DB).freepages(...)\n", true},
		{"unrelated assertion", assertion + "example.com/engine.assert(...)\n", false},
		{"bbolt in another goroutine", assertion + "example.com/engine.assert(...)\n\ngoroutine 2 [runnable]:\n" + boltFrame, false},
		{"unrelated panic in bbolt", "panic: unrelated bug\n\ngoroutine 17 [running]:\n" + boltFrame, false},
		{"quoted log", "WARN error=\"panic: assertion failed: page 502\"\n" + boltFrame, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.match, isBoltCorruptionPanic([]byte(tc.log)))
		})
	}
}

func TestEnginePanicTail(t *testing.T) {
	var tail enginePanicTail
	_, err := tail.Write(bytes.Repeat([]byte("x"), enginePanicTailSize*2))
	require.NoError(t, err)
	_, err = tail.Write([]byte("panic"))
	require.NoError(t, err)
	require.Len(t, tail.Bytes(), enginePanicTailSize)
	require.True(t, bytes.HasSuffix(tail.Bytes(), []byte("panic")))
}

func supervisorTestChild(mode, root string) func() *exec.Cmd {
	return func() *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestEngineSupervisorProcess$")
		cmd.Env = append(os.Environ(), supervisorTestMode+"="+mode, supervisorTestRoot+"="+root)
		return cmd
	}
}

type supervisorSignalWriter struct {
	buffer bytes.Buffer
	ready  chan struct{}
	once   sync.Once
}

func (w *supervisorSignalWriter) String() string { return w.buffer.String() }

func (w *supervisorSignalWriter) Write(p []byte) (int, error) {
	n, err := w.buffer.Write(p)
	if strings.Contains(w.String(), "waiting for signal") {
		w.once.Do(func() { close(w.ready) })
	}
	return n, err
}

func writeCorruptSupervisorDB(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	db, err := bolt.Open(path, 0600, &bolt.Options{NoFreelistSync: true})
	require.NoError(t, err)
	require.NoError(t, db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucket([]byte("test"))
		if err != nil {
			return err
		}
		return bucket.Put([]byte("key"), bytes.Repeat([]byte("value"), 2000))
	}))
	var offset int64
	require.NoError(t, db.View(func(tx *bolt.Tx) error {
		offset = int64(tx.Bucket([]byte("test")).Root()) * int64(db.Info().PageSize)
		return nil
	}))
	require.NoError(t, db.Close())
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = file.WriteAt(make([]byte, 8), offset)
	require.NoError(t, err)
	require.NoError(t, file.Close())
}
