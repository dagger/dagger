package engineutil

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/console"
	runc "github.com/containerd/go-runc"
	"github.com/dagger/dagger/internal/buildkit/executor"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// A TTY process that prints and exits at once must not lose output that is
// still buffered in its terminal when the runtime returns.
func TestCallWithIOKeepsOutputOfQuickTTYProcess(t *testing.T) {
	callReturned := make(chan struct{})
	stdout := &gatedWriter{
		firstWrite: make(chan struct{}),
		// Hold the output copy on its first write until the runtime call has
		// returned, so the rest of the output is still in the terminal then.
		release: func() {
			<-callReturned
			time.Sleep(100 * time.Millisecond)
		},
	}
	process := &executor.ProcessInfo{
		Meta:   executor.Meta{Tty: true},
		Stdout: stdout,
	}

	c := &Client{Opts: &Opts{}}
	err := c.callWithIO(context.Background(), process, nil, procKiller{}, func(_ context.Context, started chan<- int, _ runc.IO, consoleSocket runc.ConsoleSocket, _ string) error {
		started <- os.Getpid()
		pts, err := fakeRuntimeTerminal(consoleSocket)
		if err != nil {
			return err
		}
		defer pts.Close()
		if _, err := pts.Write([]byte("first\n")); err != nil {
			return err
		}
		<-stdout.firstWrite
		if _, err := pts.Write([]byte("second\n")); err != nil {
			return err
		}
		close(callReturned)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "first\r\nsecond\r\n", stdout.String())
}

// Something else holding the terminal open, such as a background job, must
// not keep the exec from returning: draining is bounded.
func TestCallWithIODrainIsBounded(t *testing.T) {
	defaultTimeout := ttyDrainTimeout
	ttyDrainTimeout = 200 * time.Millisecond
	t.Cleanup(func() { ttyDrainTimeout = defaultTimeout })

	stdout := &gatedWriter{firstWrite: make(chan struct{}), release: func() {}}
	process := &executor.ProcessInfo{
		Meta:   executor.Meta{Tty: true},
		Stdout: stdout,
	}

	var heldOpen *os.File
	t.Cleanup(func() {
		if heldOpen != nil {
			heldOpen.Close()
		}
	})
	c := &Client{Opts: &Opts{}}
	start := time.Now()
	err := c.callWithIO(context.Background(), process, nil, procKiller{}, func(_ context.Context, started chan<- int, _ runc.IO, consoleSocket runc.ConsoleSocket, _ string) error {
		started <- os.Getpid()
		pts, err := fakeRuntimeTerminal(consoleSocket)
		if err != nil {
			return err
		}
		// keep the terminal open after the process "exits"
		heldOpen = pts
		if _, err := pts.Write([]byte("printed\n")); err != nil {
			return err
		}
		<-stdout.firstWrite
		return nil
	})
	require.NoError(t, err)
	require.Less(t, time.Since(start), 5*time.Second)
	require.Equal(t, "printed\r\n", stdout.String())
}

// A process that exits nonzero still ran with its terminal, so its output
// must arrive even when receiving the terminal is still pending as the
// runtime returns.
func TestCallWithIOKeepsTTYOfNonzeroExit(t *testing.T) {
	stdout := &gatedWriter{firstWrite: make(chan struct{}), release: func() {}}
	process := &executor.ProcessInfo{
		Meta:   executor.Meta{Tty: true},
		Stdout: stdout,
	}

	c := &Client{Opts: &Opts{}}
	err := c.callWithIO(context.Background(), process, nil, procKiller{}, func(_ context.Context, started chan<- int, _ runc.IO, consoleSocket runc.ConsoleSocket, _ string) error {
		started <- os.Getpid()
		go func() {
			// the terminal reaches the engine only after the runtime returned
			time.Sleep(100 * time.Millisecond)
			pts, err := fakeRuntimeTerminal(consoleSocket)
			if err != nil {
				return
			}
			defer pts.Close()
			pts.Write([]byte("command output\n"))
		}()
		return &runc.ExitError{Status: 42}
	})
	var exitErr *runc.ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, "command output\r\n", stdout.String())
}

// Resizes reach the process's terminal directly: one sent before the terminal
// exists, as the initial size is, and one sent while the process runs.
func TestCallWithIOResizesTTY(t *testing.T) {
	resizes := make(chan executor.WinSize, 1)
	resizes <- executor.WinSize{Rows: 35, Cols: 160}
	stdout := &gatedWriter{firstWrite: make(chan struct{}), release: func() {}}
	process := &executor.ProcessInfo{
		Meta:   executor.Meta{Tty: true},
		Stdout: stdout,
		Resize: resizes,
	}

	c := &Client{Opts: &Opts{}}
	err := c.callWithIO(context.Background(), process, nil, procKiller{}, func(_ context.Context, started chan<- int, _ runc.IO, consoleSocket runc.ConsoleSocket, _ string) error {
		started <- os.Getpid()
		pts, err := fakeRuntimeTerminal(consoleSocket)
		if err != nil {
			return err
		}
		defer pts.Close()
		if err := waitForSize(pts, 35, 160); err != nil {
			return err
		}
		resizes <- executor.WinSize{Rows: 40, Cols: 120}
		if err := waitForSize(pts, 40, 120); err != nil {
			return err
		}
		_, err = pts.Write([]byte("resized\n"))
		return err
	})
	require.NoError(t, err)
	require.Equal(t, "resized\r\n", stdout.String())
}

// fakeRuntimeTerminal does what the runtime does for a TTY process given a
// console socket: it creates the process's terminal and sends its master over
// the socket. It returns the terminal's other end, standing in for the process.
func fakeRuntimeTerminal(consoleSocket runc.ConsoleSocket) (*os.File, error) {
	ptm, ptsName, err := console.NewPty()
	if err != nil {
		return nil, err
	}
	defer ptm.Close()
	pts, err := os.OpenFile(ptsName, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	conn, err := net.Dial("unix", consoleSocket.Path())
	if err != nil {
		pts.Close()
		return nil, err
	}
	defer conn.Close()
	_, _, err = conn.(*net.UnixConn).WriteMsgUnix([]byte(ptsName), unix.UnixRights(int(ptm.Fd())), nil)
	if err != nil {
		pts.Close()
		return nil, err
	}
	return pts, nil
}

func waitForSize(pts *os.File, rows, cols uint16) error {
	var ws *unix.Winsize
	for range 100 {
		var err error
		ws, err = unix.IoctlGetWinsize(int(pts.Fd()), unix.TIOCGWINSZ)
		if err != nil {
			return err
		}
		if ws.Row == rows && ws.Col == cols {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("terminal size is %dx%d, want %dx%d", ws.Row, ws.Col, rows, cols)
}

type gatedWriter struct {
	firstWrite chan struct{}
	release    func()
	once       sync.Once

	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *gatedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buf.Write(p)
	w.mu.Unlock()
	w.once.Do(func() {
		close(w.firstWrite)
		w.release()
	})
	return n, err
}

func (w *gatedWriter) Close() error {
	return nil
}

func (w *gatedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}
