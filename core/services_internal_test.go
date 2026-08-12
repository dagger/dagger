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

type retainedTestGeneration struct {
	done chan struct{}
	once sync.Once
}

func (generation *retainedTestGeneration) exit() {
	generation.once.Do(func() { close(generation.done) })
}

type retainedTestStartable struct {
	started chan *retainedTestGeneration
	stops   atomic.Int32
}

func newRetainedTestStartable() *retainedTestStartable {
	return &retainedTestStartable{started: make(chan *retainedTestGeneration, 8)}
}

func (startable *retainedTestStartable) Start(
	_ context.Context,
	running *RunningService,
	_ digest.Digest,
	_ ServiceStartOpts,
) error {
	generation := &retainedTestGeneration{done: make(chan struct{})}
	running.Host = "retained-backend"
	running.Kind = RunningServiceKindContainer
	running.Stop = func(context.Context, bool) error {
		startable.stops.Add(1)
		generation.exit()
		return nil
	}
	running.Wait = func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-generation.done:
			return nil
		}
	}
	startable.started <- generation
	return nil
}

func retainedTestKey(name string) ServiceKey {
	return ServiceKey{
		Digest:    digest.FromString(name),
		SessionID: "retained-session",
		Kind:      ServiceRuntimeShared,
	}
}

func TestRetainedServiceIdlesAndJoinsWithoutBindingLeaks(t *testing.T) {
	t.Parallel()
	services := NewServices()
	startable := newRetainedTestStartable()
	key := retainedTestKey("retained-idle")

	running, release, err := services.startWithKey(t.Context(), key, startable, ServiceStartOpts{
		RetainNames: []string{"web"},
	}, false)
	require.NoError(t, err)
	firstGeneration := <-startable.started
	release()

	services.l.Lock()
	require.Equal(t, 0, services.bindings[key])
	require.Equal(t, map[string]struct{}{"web": {}}, services.retained[key])
	services.l.Unlock()
	select {
	case <-firstGeneration.done:
		t.Fatal("retained zero-binding service stopped")
	default:
	}

	for range 4 {
		joined, detach, err := services.startWithKey(t.Context(), key, startable, ServiceStartOpts{}, false)
		require.NoError(t, err)
		require.Same(t, running, joined)
		detach()
		services.l.Lock()
		require.Equal(t, 0, services.bindings[key])
		services.l.Unlock()
		select {
		case <-firstGeneration.done:
			t.Fatal("retained service stopped after join/detach")
		default:
		}
	}
	require.Empty(t, startable.started, "join started another generation")

	require.NoError(t, services.StopSessionServices(t.Context(), key.SessionID))
	require.Equal(t, int32(1), startable.stops.Load())
	services.l.Lock()
	require.NotContains(t, services.retained, key)
	services.l.Unlock()
}

func TestRetainedServiceRestartAndDelayedOldDetachAreGenerationSafe(t *testing.T) {
	t.Parallel()
	services := NewServices()
	startable := newRetainedTestStartable()
	key := retainedTestKey("retained-restart")

	oldRunning, oldDetach, err := services.startWithKey(t.Context(), key, startable, ServiceStartOpts{
		RetainNames: []string{"web"},
	}, false)
	require.NoError(t, err)
	oldGeneration := <-startable.started
	oldGeneration.exit()
	require.Eventually(t, func() bool {
		services.l.Lock()
		defer services.l.Unlock()
		_, running := services.running[key]
		_, retained := services.retained[key]
		return !running && retained
	}, time.Second, time.Millisecond)

	newRunning, newDetach, err := services.startWithKey(t.Context(), key, startable, ServiceStartOpts{}, false)
	require.NoError(t, err)
	require.NotSame(t, oldRunning, newRunning)
	newGeneration := <-startable.started
	oldDetach()
	services.l.Lock()
	require.Same(t, newRunning, services.running[key])
	require.Equal(t, 1, services.bindings[key], "old generation release changed the new binding")
	require.Equal(t, map[string]struct{}{"web": {}}, services.retained[key])
	services.l.Unlock()

	newDetach()
	select {
	case <-newGeneration.done:
		t.Fatal("restarted retained generation stopped at zero bindings")
	default:
	}
	require.NoError(t, services.StopSessionServices(t.Context(), key.SessionID))
	require.Equal(t, int32(1), startable.stops.Load(), "only the current generation should be stopped")
}

func TestRetainedAliasesAndServiceDescriptionsAreStructured(t *testing.T) {
	t.Parallel()
	services := NewServices()
	startable := newRetainedTestStartable()
	key := retainedTestKey("retained-aliases")

	backend, releaseWeb, err := services.startWithKey(t.Context(), key, startable, ServiceStartOpts{
		RetainNames: []string{"web"},
	}, false)
	require.NoError(t, err)
	<-startable.started
	joined, releaseAPI, err := services.startWithKey(t.Context(), key, startable, ServiceStartOpts{
		RetainNames: []string{"api"},
	}, false)
	require.NoError(t, err)
	require.Same(t, backend, joined)
	releaseWeb()
	releaseAPI()

	tunnelKey := ServiceKey{
		Digest:    digest.FromString("published-tunnel"),
		SessionID: key.SessionID,
		ClientID:  "publisher",
		Kind:      ServiceRuntimeShared,
	}
	frontendDescription := "published frontend"
	services.l.Lock()
	services.running[tunnelKey] = &RunningService{
		Key: tunnelKey, Host: "127.0.0.1", Kind: RunningServiceKindTunnel,
		Ports:              []Port{{Port: 8080, Protocol: NetworkProtocolTCP, Description: &frontendDescription}},
		TunnelPortBackends: []int{80},
		TunnelUpstream:     &key,
	}
	services.bindings[tunnelKey] = 1
	services.l.Unlock()

	descriptions := services.Describe(key.SessionID)
	require.Len(t, descriptions, 2)
	var backendDescription, tunnelDescription *ServiceDescription
	for i := range descriptions {
		switch descriptions[i].Kind {
		case RunningServiceKindContainer:
			backendDescription = &descriptions[i]
		case RunningServiceKindTunnel:
			tunnelDescription = &descriptions[i]
		}
	}
	require.NotNil(t, backendDescription)
	require.Equal(t, []string{"api", "web"}, backendDescription.Names)
	require.True(t, backendDescription.Retained)
	require.NotNil(t, tunnelDescription)
	require.Equal(t, "publisher", tunnelDescription.OwnerClientID)
	require.Equal(t, key, *tunnelDescription.TunnelUpstream)
	require.Equal(t, 8080, tunnelDescription.Ports[0].Port)
	require.Equal(t, []int{80}, tunnelDescription.TunnelPortBackends)
}

func TestRetentionCannotRaceSessionStop(t *testing.T) {
	t.Parallel()
	services := NewServices()
	startable := newRetainedTestStartable()
	key := retainedTestKey("retained-stop-race")
	beforeStart := make(chan struct{})
	allowStart := make(chan struct{})
	services.testBeforeStartWithKey = func(ServiceKey) {
		close(beforeStart)
		<-allowStart
	}

	startResult := make(chan error, 1)
	go func() {
		_, _, err := services.startWithKey(t.Context(), key, startable, ServiceStartOpts{
			RetainNames: []string{"web"},
		}, false)
		startResult <- err
	}()
	<-beforeStart
	require.NoError(t, services.StopSessionServices(t.Context(), key.SessionID))
	close(allowStart)
	require.ErrorContains(t, <-startResult, "is stopping")
	services.l.Lock()
	require.NotContains(t, services.retained, key)
	require.NotContains(t, services.starting, key)
	require.NotContains(t, services.running, key)
	services.l.Unlock()
	require.Zero(t, startable.stops.Load())
}
