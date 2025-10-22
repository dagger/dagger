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
	"github.com/vektah/gqlparser/v2/ast"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql/call"
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
		_, err := services.Get(ctx, stub.ID(), false)
		require.Error(t, err)

		expected := stub.Succeed()

		running, err := services.Start(ctx, stub.ID(), stub, false)
		require.NoError(t, err)
		require.Equal(t, expected, running)

		running, err = services.Get(ctx, stub.ID(), false)
		require.NoError(t, err)
		require.Equal(t, expected, running)
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

		expected := stub.Succeed()

		_, err := services.Get(ctx, stub.ID(), false)
		require.Error(t, err)

		running, err := services.Start(ctx, stub.ID(), stub, false)
		require.NoError(t, err)
		require.Equal(t, expected, running)

		running, err = services.Get(ctx, stub.ID(), false)
		require.NoError(t, err)
		require.Equal(t, expected, running)
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

	_, err := services.Start(ctx, stub.ID(), stub, false)
	require.Equal(t, expected, err)

	_, err = services.Get(ctx, stub.ID(), false)
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
		_, err := services.Start(ctx, stub.ID(), stub, false)
		return err
	})

	// wait for start attempt [hopefully not flaky]
	require.Eventually(t, func() bool {
		return stub.Starts() > 0
	}, 10*time.Second, 10*time.Millisecond)

	// start another attempt
	eg.Go(func() error {
		_, err := services.Start(ctx, stub.ID(), stub, false)
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
		_, err := services.Start(ctx, stub.ID(), stub, false)
		errs <- err
	}()

	// wait for start attempt [hopefully not flaky]
	require.Eventually(t, func() bool {
		return stub.Starts() == 1
	}, 10*time.Second, 10*time.Millisecond)

	// start another attempt
	go func() {
		_, err := services.Start(ctx, stub.ID(), stub, false)
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
	_, err := services.Get(ctx, stub.ID(), false)
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
		_, err := services.Start(ctx, stub.ID(), stub, false)
		errs <- err
	}()

	// wait for start attempt [hopefully not flaky]
	require.Eventually(t, func() bool {
		return stub.Starts() == 1
	}, 10*time.Second, 10*time.Millisecond)

	// start a few more attempts
	for range 3 {
		go func() {
			_, err := services.Start(ctx, stub.ID(), stub, false)
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
}

type startResult struct {
	Started *core.RunningService
	Failed  error
}

func newStartable(id string) *fakeStartable {
	return &fakeStartable{
		name:   id,
		digest: digest.FromString(id),

		// just buffer 100 to keep things simple
		startResults: make(chan startResult, 100),
	}
}

func (f *fakeStartable) ID() *call.ID {
	id := call.New().Append(&ast.Type{
		NamedType: "FakeService",
		NonNull:   true,
	}, f.name)
	return id
}

func (f *fakeStartable) Start(context.Context, *call.ID, *core.ServiceIO) (*core.RunningService, error) {
	atomic.AddInt32(&f.starts, 1)
	res := <-f.startResults
	return res.Started, res.Failed
}

func (f *fakeStartable) Starts() int {
	return int(atomic.LoadInt32(&f.starts))
}

func (f *fakeStartable) Succeed() *core.RunningService {
	running := &core.RunningService{
		Key: core.ServiceKey{
			Digest:    f.digest,
			SessionID: "doesnt-matter",
		},
		Host: f.name + "-host",
	}

	f.startResults <- startResult{
		Started: running,
	}

	return running
}

func (f *fakeStartable) Fail() error {
	err := errors.New("oh no")
	f.startResults <- startResult{
		Failed: err,
	}
	return err
}
