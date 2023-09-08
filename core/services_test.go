package core_test

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestServicesStartHappy(t *testing.T) {
	ctx := context.Background()
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID: "fake-client",
	})

	stubClient := new(buildkit.Client)
	services := core.NewServices(stubClient)

	stubService := newStartable("fake")
	okRes := startResult{
		ok: &core.RunningService{
			Key: core.ServiceKey{
				Digest:   stubService.digest,
				ClientID: "fake-client",
			},
			Host: "fake-host",
		},
	}
	stubService.startResults <- okRes

	_, err := services.Get(ctx, stubService)
	require.Error(t, err)

	running, err := services.Start(ctx, stubService)
	require.NoError(t, err)
	require.Equal(t, okRes.ok, running)

	running, err = services.Get(ctx, stubService)
	require.NoError(t, err)
	require.Equal(t, okRes.ok, running)
}

func TestServicesStartSad(t *testing.T) {
	ctx := context.Background()
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID: "fake-client",
	})

	stubClient := new(buildkit.Client)
	services := core.NewServices(stubClient)

	stubService := newStartable("fake")
	notOkRes := startResult{
		err: errors.New("oh no"),
	}
	stubService.startResults <- notOkRes

	_, err := services.Start(ctx, stubService)
	require.Equal(t, notOkRes.err, err)

	_, err = services.Get(ctx, stubService)
	require.Error(t, err)
}

func TestServicesStartConcurrentHappy(t *testing.T) {
	ctx := context.Background()
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID: "fake-client",
	})

	stubClient := new(buildkit.Client)
	services := core.NewServices(stubClient)

	stubService := newStartable("fake")

	okRes := startResult{
		ok: &core.RunningService{
			Key: core.ServiceKey{
				Digest:   stubService.digest,
				ClientID: "fake-client",
			},
			Host: "fake-host",
		},
	}

	eg := new(errgroup.Group)
	eg.Go(func() error {
		_, err := services.Start(ctx, stubService)
		return err
	})

	// wait for start attempt [hopefully not flaky]
	require.Eventually(t, func() bool {
		return stubService.Starts() > 0
	}, 10*time.Second, 10*time.Millisecond)

	// start another attempt
	eg.Go(func() error {
		_, err := services.Start(ctx, stubService)
		return err
	})

	// [try to] wait for second start attempt to start waiting
	time.Sleep(100 * time.Millisecond)
	runtime.Gosched()

	// make sure we didn't try to start twice
	require.Equal(t, int32(1), stubService.Starts())

	// allow the first attempt to succeed
	stubService.startResults <- okRes

	// make sure all start attempts succeeded
	require.NoError(t, eg.Wait())

	// make sure we didn't try to start twice
	require.Equal(t, int32(1), stubService.Starts())
}

func TestServicesStartConcurrentSad(t *testing.T) {
	ctx := context.Background()
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID: "fake-client",
	})

	stubClient := new(buildkit.Client)
	services := core.NewServices(stubClient)

	stubService := newStartable("fake")

	notOkRes := startResult{
		err: errors.New("oh no"),
	}

	errs := make(chan error, 100)
	go func() {
		_, err := services.Start(ctx, stubService)
		errs <- err
	}()

	// wait for start attempt [hopefully not flaky]
	require.Eventually(t, func() bool {
		return stubService.Starts() == 1
	}, 10*time.Second, 10*time.Millisecond)

	// start another attempt
	go func() {
		_, err := services.Start(ctx, stubService)
		errs <- err
	}()

	// [try to] wait for second start attempt to start waiting
	time.Sleep(100 * time.Millisecond)
	runtime.Gosched()

	// make sure we didn't try to start twice
	require.Equal(t, int32(1), stubService.Starts())

	// make the first attempt fail
	stubService.startResults <- notOkRes
	require.Equal(t, notOkRes.err, <-errs)

	// wait for second start attempt [hopefully not flaky]
	require.Eventually(t, func() bool {
		return stubService.Starts() == 2
	}, 10*time.Second, 10*time.Millisecond)

	// make the second attempt fail too
	stubService.startResults <- notOkRes
	require.Equal(t, notOkRes.err, <-errs)

	// make sure we didn't try to start more than twice
	require.Equal(t, int32(2), stubService.Starts())

	// make sure Get doesn't wait for any attempts, as they've all failed
	_, err := services.Get(ctx, stubService)
	require.Error(t, err)
}

func TestServicesStartConcurrentSadThenHappy(t *testing.T) {
	ctx := context.Background()
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID: "fake-client",
	})

	stubClient := new(buildkit.Client)
	services := core.NewServices(stubClient)

	stubService := newStartable("fake")

	notOkRes := startResult{
		err: errors.New("oh no"),
	}

	okRes := startResult{
		ok: &core.RunningService{
			Key: core.ServiceKey{
				Digest:   stubService.digest,
				ClientID: "fake-client",
			},
			Host: "fake-host",
		},
	}

	errs := make(chan error, 100)
	go func() {
		_, err := services.Start(ctx, stubService)
		errs <- err
	}()

	// wait for start attempt [hopefully not flaky]
	require.Eventually(t, func() bool {
		return stubService.Starts() == 1
	}, 10*time.Second, 10*time.Millisecond)

	// start a few more attempts
	for i := 0; i < 3; i++ {
		go func() {
			_, err := services.Start(ctx, stubService)
			errs <- err
		}()
	}

	// [try to] wait for second start attempt to start waiting
	time.Sleep(100 * time.Millisecond)
	runtime.Gosched()

	// make sure we didn't try to start twice
	require.Equal(t, int32(1), stubService.Starts())

	// make the first attempt fail
	stubService.startResults <- notOkRes
	require.Equal(t, notOkRes.err, <-errs)

	// wait for second start attempt [hopefully not flaky]
	require.Eventually(t, func() bool {
		return stubService.Starts() == 2
	}, 10*time.Second, 10*time.Millisecond)

	// make the second attempt succeed
	stubService.startResults <- okRes

	// wait for all attempts to succeed
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)

	// make sure we didn't try to start more than twice
	require.Equal(t, int32(2), stubService.Starts())
}

type fakeStartable struct {
	digest       digest.Digest
	starts       int32
	startResults chan startResult
}

type startResult struct {
	ok  *core.RunningService
	err error
}

func newStartable(id string) *fakeStartable {
	return &fakeStartable{
		digest:       digest.FromString(id),
		startResults: make(chan startResult, 100), // allow pre-loading results
	}
}

func (f *fakeStartable) Digest() (digest.Digest, error) {
	return f.digest, nil
}

func (f *fakeStartable) Start(context.Context, *buildkit.Client, *core.Services) (*core.RunningService, error) {
	atomic.AddInt32(&f.starts, 1)
	res := <-f.startResults
	return res.ok, res.err
}

func (f *fakeStartable) Starts() int32 {
	return atomic.LoadInt32(&f.starts)
}
