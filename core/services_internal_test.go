package core

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	telemetry "github.com/dagger/otel-go"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

type interactiveOriginStartable struct {
	opts    chan ServiceStartOpts
	stopped chan struct{}
	once    sync.Once
}

func (s *interactiveOriginStartable) Start(_ context.Context, running *RunningService, _ digest.Digest, opts ServiceStartOpts) error {
	s.opts <- opts
	running.Stop = func(context.Context, bool) error {
		s.once.Do(func() { close(s.stopped) })
		return nil
	}
	running.Wait = func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-s.stopped:
			return nil
		}
	}
	return nil
}

func TestServiceProcessStdinDoesNotHoldProcessWaitOpen(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	processStdin, cleanup, err := serviceProcessStdin(stdinR)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup()
		_ = stdinW.Close()
	})

	cmd := exec.Command("sh", "-c", "exit 42")
	cmd.Stdin = processStdin
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()

	select {
	case err := <-done:
		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr)
		require.Equal(t, 42, exitErr.ExitCode())
	case <-time.After(time.Second):
		t.Fatal("process wait was held open by persistent service stdin")
	}
}

func TestStartInteractivePreservesServiceOrigins(t *testing.T) {
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		SessionID: "session",
		ClientID:  "client",
	})
	origin := testSpanContext("00000000000000000000000000000001", "0000000000000001")
	startable := &interactiveOriginStartable{
		opts:    make(chan ServiceStartOpts, 1),
		stopped: make(chan struct{}),
	}
	services := NewServices()
	running, release, err := services.startInteractive(
		ctx,
		digest.FromString("interactive"),
		startable,
		&ServiceIO{},
		[]trace.SpanContext{origin},
	)
	require.NoError(t, err)
	defer release()
	require.Equal(t, ServiceRuntimeInteractive, running.Key.Kind)
	require.NotEmpty(t, running.Key.InstanceID)
	require.Equal(t, []trace.SpanContext{origin}, (<-startable.opts).OriginSpanContexts)
	require.NoError(t, services.StopRunning(ctx, running, true))
}

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
