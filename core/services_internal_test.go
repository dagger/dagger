package core

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	telemetry "github.com/dagger/otel-go"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

type dependencyExitPropagationStartable struct {
	depExited chan struct{}
	stopped   chan struct{}
	stopOnce  sync.Once
}

func newDependencyExitPropagationStartable() *dependencyExitPropagationStartable {
	return &dependencyExitPropagationStartable{
		depExited: make(chan struct{}),
		stopped:   make(chan struct{}),
	}
}

func (s *dependencyExitPropagationStartable) Start(_ context.Context, running *RunningService, _ digest.Digest, _ ServiceStartOpts) error {
	depErr := errors.New("dependency exited")

	select {
	case <-s.depExited:
		if !running.isDependencyExitPropagationSuppressed() {
			return depErr
		}
	default:
	}

	running.Stop = func(context.Context, bool) error {
		s.stopOnce.Do(func() {
			close(s.stopped)
		})
		return nil
	}
	running.Wait = func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-s.stopped:
			return depErr
		}
	}

	go func() {
		<-s.depExited
		if err := running.waitDependencyExitPropagationUnsuppressed(context.Background()); err != nil {
			return
		}
		_ = running.Stop(context.Background(), true)
	}()

	return nil
}

func testSpanContext(traceHex, spanHex string) trace.SpanContext {
	traceID, err := trace.TraceIDFromHex(traceHex)
	if err != nil {
		panic(err)
	}
	spanID, err := trace.SpanIDFromHex(spanHex)
	if err != nil {
		panic(err)
	}
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	})
}

func TestRunningServiceOriginSpanContextsAreDeterministic(t *testing.T) {
	running := &RunningService{}
	later := testSpanContext("00000000000000000000000000000002", "0000000000000002")
	earlier := testSpanContext("00000000000000000000000000000001", "0000000000000001")

	running.addOriginSpanContexts([]trace.SpanContext{later, earlier, later})

	require.Equal(t, []trace.SpanContext{earlier, later}, running.originSpanContextsSnapshot())
	require.Equal(t, earlier, running.errorOriginSpanContext())
}

func TestServiceBindingExitErrorAddsBindingOriginAlongsideInnerOrigin(t *testing.T) {
	starter := testSpanContext("00000000000000000000000000000001", "0000000000000001")
	binding := testSpanContext("00000000000000000000000000000002", "0000000000000002")

	err := &serviceBindingExitError{
		err:     telemetry.TrackOrigin(errors.New("service exited"), starter),
		origins: []trace.SpanContext{starter, binding},
	}

	origins := telemetry.ParseErrorOrigins(err.Error())
	require.Len(t, origins, 2)
	require.Contains(t, origins, starter)
	require.Contains(t, origins, binding)
}

func TestStartWithKeySuppressesDependencyExitPropagationUntilRelease(t *testing.T) {
	services := NewServices()
	svc := newDependencyExitPropagationStartable()
	close(svc.depExited)

	key := ServiceKey{
		Digest:    digest.FromString("suppressed-dependency"),
		SessionID: "test-session",
		Kind:      ServiceRuntimeShared,
	}
	running, release, err := services.startWithKey(context.Background(), key, svc, ServiceStartOpts{}, true)
	require.NoError(t, err)
	require.NotNil(t, running)

	otherRunning, otherRelease, err := services.startWithKey(context.Background(), key, svc, ServiceStartOpts{}, false)
	require.NoError(t, err)
	defer otherRelease()
	require.Same(t, running, otherRunning)

	select {
	case <-svc.stopped:
		t.Fatal("dependency-exit propagation was not suppressed")
	case <-time.After(50 * time.Millisecond):
	}

	// Releasing the suppressed start should resume dependency-exit propagation. The
	// other binding is still attached, so a plain detach would not stop the service.
	release()
	require.Eventually(t, func() bool {
		select {
		case <-svc.stopped:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}

type failedStartRef struct {
	releaseErr error
	releases   atomic.Int32
}

func (*failedStartRef) ID() string         { return "failed-start-ref" }
func (*failedStartRef) SnapshotID() string { return "failed-start-snapshot" }
func (*failedStartRef) Size(context.Context) (int64, error) {
	return 0, nil
}
func (*failedStartRef) Mount(context.Context, bool) (bkcache.MountableRef, error) {
	return nil, nil
}
func (ref *failedStartRef) Release(context.Context) error {
	ref.releases.Add(1)
	return ref.releaseErr
}

type publicationRejectingStartable struct {
	publishErr error
	stopErr    error
	ref        *failedStartRef
	stops      atomic.Int32
}

func (startable *publicationRejectingStartable) Start(
	_ context.Context,
	running *RunningService,
	_ digest.Digest,
	_ ServiceStartOpts,
) error {
	running.refs = []bkcache.Ref{startable.ref}
	running.Stop = func(context.Context, bool) error {
		startable.stops.Add(1)
		return startable.stopErr
	}
	running.Wait = func(ctx context.Context) error {
		<-ctx.Done()
		return context.Cause(ctx)
	}
	running.publishIfReady = func(func()) error { return startable.publishErr }
	return nil
}

func TestStartWithKeyPublicationRejectionAlwaysReleasesTrackedResources(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"ordinary", "detachable"} {
		t.Run(mode, func(t *testing.T) {
			services := NewServices()
			ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
				SessionID:         mode,
				DetachableSession: mode == "detachable",
			})
			publishErr := errors.New("listener failed before publication")
			stopErr := errors.New("listener cleanup failed")
			resourceErr := errors.New("tracked resource release failed")
			ref := &failedStartRef{releaseErr: resourceErr}
			startable := &publicationRejectingStartable{
				publishErr: publishErr, stopErr: stopErr, ref: ref,
			}
			key := ServiceKey{Digest: digest.FromString("publication-rejection-" + mode), SessionID: mode}

			running, release, err := services.startWithKey(ctx, key, startable, ServiceStartOpts{}, false)
			require.Nil(t, running)
			require.Nil(t, release)
			require.ErrorIs(t, err, publishErr)
			require.ErrorIs(t, err, stopErr)
			require.ErrorIs(t, err, resourceErr)
			require.Equal(t, int32(1), startable.stops.Load())
			require.Equal(t, int32(1), ref.releases.Load())
			services.l.Lock()
			defer services.l.Unlock()
			require.NotContains(t, services.running, key)
			require.NotContains(t, services.bindings, key)
			require.NotContains(t, services.starting, key)
		})
	}
}

type startErrorWithTrackedRef struct {
	startErr error
	ref      *failedStartRef
	stops    atomic.Int32
}

func (startable *startErrorWithTrackedRef) Start(
	_ context.Context,
	running *RunningService,
	_ digest.Digest,
	_ ServiceStartOpts,
) error {
	running.refs = []bkcache.Ref{startable.ref}
	running.Stop = func(context.Context, bool) error {
		startable.stops.Add(1)
		return nil
	}
	return startable.startErr
}

func TestStartWithKeyStartErrorReleasesResourcesWithoutStop(t *testing.T) {
	t.Parallel()
	services := NewServices()
	startErr := errors.New("start failed")
	resourceErr := errors.New("tracked resource release failed")
	ref := &failedStartRef{releaseErr: resourceErr}
	startable := &startErrorWithTrackedRef{startErr: startErr, ref: ref}
	key := ServiceKey{Digest: digest.FromString("start-error"), SessionID: "session"}

	_, _, err := services.startWithKey(t.Context(), key, startable, ServiceStartOpts{}, false)
	require.ErrorIs(t, err, startErr)
	require.ErrorIs(t, err, resourceErr)
	require.Zero(t, startable.stops.Load())
	require.Equal(t, int32(1), ref.releases.Load())
}

type missingWaitStartable struct {
	stopErr error
	ref     *failedStartRef
	stops   atomic.Int32
}

func (startable *missingWaitStartable) Start(
	_ context.Context,
	running *RunningService,
	_ digest.Digest,
	_ ServiceStartOpts,
) error {
	running.refs = []bkcache.Ref{startable.ref}
	running.Stop = func(context.Context, bool) error {
		startable.stops.Add(1)
		return startable.stopErr
	}
	return nil
}

func TestStartWithKeyMissingWaitReleasesResourcesAfterStopError(t *testing.T) {
	t.Parallel()
	services := NewServices()
	stopErr := errors.New("stop failed")
	resourceErr := errors.New("tracked resource release failed")
	ref := &failedStartRef{releaseErr: resourceErr}
	startable := &missingWaitStartable{stopErr: stopErr, ref: ref}
	key := ServiceKey{Digest: digest.FromString("missing-wait"), SessionID: "session"}

	_, _, err := services.startWithKey(t.Context(), key, startable, ServiceStartOpts{}, false)
	require.ErrorContains(t, err, "started without Wait callback")
	require.ErrorIs(t, err, stopErr)
	require.ErrorIs(t, err, resourceErr)
	require.Equal(t, int32(1), startable.stops.Load())
	require.Equal(t, int32(1), ref.releases.Load())
}

type canceledStartable struct {
	entered  chan struct{}
	canceled chan struct{}
	release  chan struct{}
	stopErr  error
	ref      *failedStartRef
	stops    atomic.Int32
}

func (startable *canceledStartable) Start(
	ctx context.Context,
	running *RunningService,
	_ digest.Digest,
	_ ServiceStartOpts,
) error {
	running.refs = []bkcache.Ref{startable.ref}
	running.Stop = func(context.Context, bool) error {
		startable.stops.Add(1)
		return startable.stopErr
	}
	running.Wait = func(ctx context.Context) error {
		<-ctx.Done()
		return context.Cause(ctx)
	}
	close(startable.entered)
	go func() {
		<-ctx.Done()
		close(startable.canceled)
	}()
	<-startable.release
	return nil
}

func TestStartWithKeyCancellationReleasesResourcesAfterStopError(t *testing.T) {
	t.Parallel()
	services := NewServices()
	stopErr := errors.New("stop failed")
	resourceErr := errors.New("tracked resource release failed")
	ref := &failedStartRef{releaseErr: resourceErr}
	startable := &canceledStartable{
		entered:  make(chan struct{}),
		canceled: make(chan struct{}),
		release:  make(chan struct{}),
		stopErr:  stopErr,
		ref:      ref,
	}
	key := ServiceKey{Digest: digest.FromString("canceled-start"), SessionID: "session"}

	startResult := make(chan error, 1)
	go func() {
		_, _, err := services.startWithKey(t.Context(), key, startable, ServiceStartOpts{}, false)
		startResult <- err
	}()
	<-startable.entered
	stopResult := make(chan error, 1)
	go func() {
		stopResult <- services.StopSessionServices(t.Context(), key.SessionID)
	}()
	<-startable.canceled
	close(startable.release)

	err := <-startResult
	require.ErrorContains(t, err, "session closed during service start")
	require.ErrorIs(t, err, stopErr)
	require.ErrorIs(t, err, resourceErr)
	require.NoError(t, <-stopResult)
	require.Equal(t, int32(1), startable.stops.Load())
	require.Equal(t, int32(1), ref.releases.Load())
}

var _ bkcache.Ref = (*failedStartRef)(nil)
