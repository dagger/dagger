package server

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// registerAttachables registers attachables for clientID on m, ending them
// when the test does.
func registerAttachables(t *testing.T, m *sessionAttachableManager, clientID string) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		clientConn.Close()
	})
	go m.Register(ctx, clientID, serverConn, nil)
}

func waitResult(t *testing.T, m *sessionAttachableManager, ctx context.Context, clientID string) <-chan error {
	t.Helper()
	got := make(chan error, 1)
	go func() {
		_, err := m.Wait(ctx, clientID)
		got <- err
	}()
	return got
}

func requireResult(t *testing.T, got <-chan error) error {
	t.Helper()
	select {
	case err := <-got:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("wait for attachables did not return")
		return nil
	}
}

// A nested exec's session helper starts alongside its command, so a query can
// wait for its attachables past the default bound, until they register.
func TestExpectedAttachablesOutlastTheDefaultWait(t *testing.T) {
	m := newSessionAttachableManager()
	_, done := m.Expect("nested")
	defer done()

	ctx, cancel := m.waitContext(t.Context(), "nested", 50*time.Millisecond)
	defer cancel()
	got := waitResult(t, m, ctx, "nested")
	time.Sleep(200 * time.Millisecond)
	select {
	case err := <-got:
		t.Fatalf("wait ended before the attachables registered: %v", err)
	default:
	}
	registerAttachables(t, m, "nested")
	require.NoError(t, requireResult(t, got))
}

func TestUnexpectedAttachablesKeepTheDefaultWait(t *testing.T) {
	m := newSessionAttachableManager()
	ctx, cancel := m.waitContext(t.Context(), "other", 50*time.Millisecond)
	defer cancel()
	_, err := m.Wait(ctx, "other")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// When /.init reports that the helper failed, waiting queries fail at once
// with its status, and so do later ones.
func TestExpectedAttachablesFailWithTheHelper(t *testing.T) {
	m := newSessionAttachableManager()
	fail, done := m.Expect("nested")
	defer done()

	got := waitResult(t, m, t.Context(), "nested")
	time.Sleep(50 * time.Millisecond)
	helperErr := errors.New("the Dagger session helper in this container exited with status 3")
	fail(helperErr)
	require.ErrorIs(t, requireResult(t, got), helperErr)

	_, err := m.Wait(t.Context(), "nested")
	require.ErrorIs(t, err, helperErr)
}

func TestExpectedAttachablesEndWithTheExec(t *testing.T) {
	m := newSessionAttachableManager()
	_, done := m.Expect("nested")
	got := waitResult(t, m, t.Context(), "nested")
	time.Sleep(50 * time.Millisecond)
	done()
	require.ErrorIs(t, requireResult(t, got), errExecEnded)
}

// Attachables that registered stay usable after the helper is reported gone:
// a report only fails waits that have nothing to use.
func TestRegisteredAttachablesOutliveAFailureReport(t *testing.T) {
	m := newSessionAttachableManager()
	fail, done := m.Expect("nested")
	defer done()
	registerAttachables(t, m, "nested")
	require.Eventually(t, func() bool {
		_, ok := m.Lookup("nested")
		return ok
	}, 5*time.Second, time.Millisecond)
	fail(errors.New("exited"))
	_, err := m.Wait(t.Context(), "nested")
	require.NoError(t, err)
}
