package core_test

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
)

func TestServicesStartHappy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID: "fake-client",
	})

	services := core.NewServices()

	svc1 := newStartable("fake-1")
	svc2 := newStartable("fake-2")

	startOne := func(t *testing.T, stub *fakeStartable) {
		_, err := services.Get(ctx, stub.Digest(), false)
		require.Error(t, err)

		expectedHost := stub.Succeed()

		running, err := services.Start(ctx, stub.Digest(), stub, false)
		require.NoError(t, err)
		require.Equal(t, expectedHost, running.Host)

		running2, err := services.Get(ctx, stub.Digest(), false)
		require.NoError(t, err)
		require.Equal(t, running, running2)
		require.Equal(t, expectedHost, running2.Host)
	}

	t.Run("start one", func(t *testing.T) {
		startOne(t, svc1)
	})

	t.Run("start another", func(t *testing.T) {
		startOne(t, svc2)
	})
}

func TestServicesStartHappyDifferentServers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	services := core.NewServices()

	svc := newStartable("fake")

	startOne := func(t *testing.T, stub *fakeStartable, sessionID string) {
		ctx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
			SessionID: sessionID,
		})

		expectedHost := stub.Succeed()

		_, err := services.Get(ctx, stub.Digest(), false)
		require.Error(t, err)

		running, err := services.Start(ctx, stub.Digest(), stub, false)
		require.NoError(t, err)
		require.Equal(t, expectedHost, running.Host)

		running2, err := services.Get(ctx, stub.Digest(), false)
		require.NoError(t, err)
		require.Equal(t, running, running2)
		require.Equal(t, expectedHost, running2.Host)
	}

	t.Run("start one", func(t *testing.T) {
		startOne(t, svc, "server-1")
	})

	t.Run("start another", func(t *testing.T) {
		startOne(t, svc, "server-2")
	})
}

func TestServicesStartSad(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID: "fake-client",
	})

	services := core.NewServices()

	stub := newStartable("fake")

	expected := stub.Fail()

	_, err := services.Start(ctx, stub.Digest(), stub, false)
	require.Equal(t, expected, err)

	_, err = services.Get(ctx, stub.Digest(), false)
	require.Error(t, err)
}

func TestServicesStartConcurrentHappy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID: "fake-client",
	})

	services := core.NewServices()

	stub := newStartable("fake")

	eg := new(errgroup.Group)
	eg.Go(func() error {
		_, err := services.Start(ctx, stub.Digest(), stub, false)
		return err
	})

	// wait for start attempt [hopefully not flaky]
	require.Eventually(t, func() bool {
		return stub.Starts() > 0
	}, 10*time.Second, 10*time.Millisecond)

	// start another attempt
	eg.Go(func() error {
		_, err := services.Start(ctx, stub.Digest(), stub, false)
		return err
	})

	// [try to] wait for second start attempt to start waiting
	time.Sleep(100 * time.Millisecond)
	runtime.Gosched()

	// make sure we didn't try to start twice
	require.Equal(t, 1, stub.Starts())

	// allow the first attempt to succeed
	stub.Succeed()

	// make sure all start attempts succeeded
	require.NoError(t, eg.Wait())

	// make sure we didn't try to start twice
	require.Equal(t, 1, stub.Starts())
}

func TestServicesStartConcurrentSad(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID: "fake-client",
	})

	services := core.NewServices()

	stub := newStartable("fake")

	errs := make(chan error, 100)
	go func() {
		_, err := services.Start(ctx, stub.Digest(), stub, false)
		errs <- err
	}()

	// wait for start attempt [hopefully not flaky]
	require.Eventually(t, func() bool {
		return stub.Starts() == 1
	}, 10*time.Second, 10*time.Millisecond)

	// start another attempt
	go func() {
		_, err := services.Start(ctx, stub.Digest(), stub, false)
		errs <- err
	}()

	// [try to] wait for second start attempt to start waiting
	time.Sleep(100 * time.Millisecond)
	runtime.Gosched()

	// make sure we didn't try to start twice
	require.Equal(t, 1, stub.Starts())

	// make the first attempt fail
	require.Equal(t, stub.Fail(), <-errs)

	// wait for second start attempt [hopefully not flaky]
	require.Eventually(t, func() bool {
		return stub.Starts() == 2
	}, 10*time.Second, 10*time.Millisecond)

	// make the second attempt fail too
	require.Equal(t, stub.Fail(), <-errs)

	// make sure we didn't try to start more than twice
	require.Equal(t, 2, stub.Starts())

	// make sure Get doesn't wait for any attempts, as they've all failed
	_, err := services.Get(ctx, stub.Digest(), false)
	require.Error(t, err)
}

func TestServicesStartConcurrentSadThenHappy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID: "fake-client",
	})

	services := core.NewServices()

	stub := newStartable("fake")

	errs := make(chan error, 100)
	go func() {
		_, err := services.Start(ctx, stub.Digest(), stub, false)
		errs <- err
	}()

	// wait for start attempt [hopefully not flaky]
	require.Eventually(t, func() bool {
		return stub.Starts() == 1
	}, 10*time.Second, 10*time.Millisecond)

	// start a few more attempts
	for range 3 {
		go func() {
			_, err := services.Start(ctx, stub.Digest(), stub, false)
			errs <- err
		}()
	}

	// [try to] wait for second start attempt to start waiting
	time.Sleep(100 * time.Millisecond)
	runtime.Gosched()

	// make sure we didn't try to start twice
	require.Equal(t, 1, stub.Starts())

	// make the first attempt fail
	require.Equal(t, stub.Fail(), <-errs)

	// wait for second start attempt [hopefully not flaky]
	require.Eventually(t, func() bool {
		return stub.Starts() == 2
	}, 10*time.Second, 10*time.Millisecond)

	// make the second attempt succeed
	stub.Succeed()

	// wait for all attempts to succeed
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)

	// make sure we didn't try to start more than twice
	require.Equal(t, 2, stub.Starts())
}

type fakeStartable struct {
	name   string
	digest digest.Digest

	starts       int32 // total start attempts
	startResults chan startResult

	exitErr    error
	waitResult chan struct{}
}

type startResult struct {
	configure func(*core.RunningService)
	failed    error
}

func newStartable(id string) *fakeStartable {
	return &fakeStartable{
		name:   id,
		digest: digest.FromString(id),

		// just buffer 100 to keep things simple
		startResults: make(chan startResult, 100),
	}
}

func (f *fakeStartable) Digest() digest.Digest {
	return f.digest
}

func (f *fakeStartable) Start(_ context.Context, running *core.RunningService, _ digest.Digest, _ core.ServiceStartOpts) error {
	atomic.AddInt32(&f.starts, 1)
	res := <-f.startResults
	if res.failed != nil {
		return res.failed
	}
	if res.configure == nil {
		return nil
	}
	res.configure(running)
	return nil
}

func (f *fakeStartable) Starts() int {
	return int(atomic.LoadInt32(&f.starts))
}

func (f *fakeStartable) Succeed() string {
	waitRes := make(chan struct{})
	host := f.name + "-host"

	f.waitResult = waitRes
	f.startResults <- startResult{
		configure: func(running *core.RunningService) {
			running.Host = host
			running.Wait = func(ctx context.Context) error {
				<-waitRes
				return f.exitErr
			}
		},
	}

	return host
}

func (f *fakeStartable) Fail() error {
	err := errors.New("oh no")
	f.startResults <- startResult{
		failed: err,
	}
	return err
}

func (f *fakeStartable) Exit(err error) {
	f.exitErr = err
	close(f.waitResult)
}

// TestServicesDetachRace tests the race condition where:
//   - Client A starts service (bindings=1)
//   - Client A detaches (bindings=0, spawns stop goroutine)
//   - Client B tries to start BEFORE the stop goroutine removes the service
//   - Without the fix, Client B would increment bindings to 1, but the stop
//     goroutine would still delete the service and bindings map, causing the
//     service to stop even though Client B still has a reference to it
func TestServicesDetachRace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		SessionID: "test-session",
		ClientID:  "test-client-a",
	})

	services := core.NewServices()
	stub := newStartable("race-test")

	// Client A starts the service
	expectedHost := stub.Succeed()
	running, err := services.Start(ctx, stub.Digest(), stub, false)
	require.NoError(t, err)
	require.Equal(t, expectedHost, running.Host)
	require.Equal(t, 1, stub.Starts())

	// Client A detaches - this will spawn a goroutine that waits DetachGracePeriod
	// then calls Detach, which should immediately remove the service from the running map
	services.Detach(ctx, running)
	stub.Exit(nil)

	// Client B tries to start the same service during the race window
	// This should happen after Detach has removed the service from the running map
	ctxB := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		SessionID: "test-session",
		ClientID:  "test-client-b",
	})

	// Client B should see the service is not running and start a new one
	stub.Succeed() // prepare for Client B's start
	runningB, err := services.Start(ctxB, stub.Digest(), stub, false)
	require.NoError(t, err)
	require.NotNil(t, runningB)

	// We should have started twice - once for Client A, once for Client B
	require.Equal(t, 2, stub.Starts())

	// Client B's service should still be running
	retrieved, err := services.Get(ctxB, stub.Digest(), false)
	require.NoError(t, err)
	require.Equal(t, runningB, retrieved)
}
