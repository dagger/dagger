package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A client that dials while the engine starts is connected once the engine
// listens, without failing first.
func TestDialerWaitsForListener(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "engine.sock")
	listening := make(chan net.Listener, 1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		l, err := net.Listen("unix", sock)
		if err != nil {
			t.Error(err)
		}
		listening <- l
	}()

	conn, err := dialer("unix://"+sock, 5*time.Second)
	require.NoError(t, err)
	conn.Close()
	l := <-listening
	require.NotNil(t, l)
	l.Close()
}

// A socket left from an earlier run, with nobody listening, is waited on like
// a missing one.
func TestDialerWaitsOnStaleSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "engine.sock")
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	require.NoError(t, err)
	stale.SetUnlinkOnClose(false)
	require.NoError(t, stale.Close())
	listening := make(chan net.Listener, 1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		if err := os.Remove(sock); err != nil {
			t.Error(err)
		}
		l, err := net.Listen("unix", sock)
		if err != nil {
			t.Error(err)
		}
		listening <- l
	}()

	conn, err := dialer("unix://"+sock, 5*time.Second)
	require.NoError(t, err)
	conn.Close()
	l := <-listening
	require.NotNil(t, l)
	l.Close()
}

// When the engine doesn't listen in time, the dial fails once the timeout
// has passed.
func TestDialerGivesUp(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "engine.sock")
	start := time.Now()
	_, err := dialer("unix://"+sock, 300*time.Millisecond)
	require.Error(t, err)
	require.GreaterOrEqual(t, time.Since(start), 250*time.Millisecond)
	require.Less(t, time.Since(start), 2*time.Second)
}
