package daggercmd

import (
	"context"
	"fmt"
	"os"

	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/dagger/dagger/internal/tracesource"
)

// openEngineTrace opens a trace in the engine's retained telemetry store over
// read-only client plumbing, never workspace modules or archived recipes. Its
// cleanup remains alive for lazy interactive expansion.
func openEngineTrace(ctx context.Context, traceID string) (tracesource.Source, func() error, error) {
	if err := nestedEngineSession(); err != nil {
		return nil, nil, err
	}
	params, err := engineTraceParams(ctx)
	if err != nil {
		return nil, nil, err
	}
	return openEngineTraceWith(ctx, traceID, func(ctx context.Context) (*client.Client, error) {
		return client.Connect(ctx, params)
	})
}

// engineTraceParams are the client params for reading an engine archive.
func engineTraceParams(ctx context.Context) (client.Params, error) {
	// Reading a local archive needs no Cloud credentials: don't refresh a
	// login (or fail on one) before Cloud is actually needed.
	ctx = context.WithValue(ctx, doctorCloudAuthKey{}, (*auth.Cloud)(nil))
	params, err := finalizeEngineParams(ctx, client.Params{LoadWorkspaceModules: false})
	if err != nil {
		return params, err
	}
	// The connection only reads an archive; it is not the run being viewed.
	// Its Cloud URL -- with no Cloud telemetry configured, the setup page --
	// must not become the frontend's, which would enable the web key and
	// `-w` on the setup page even when Cloud serves the trace.
	params.CloudURLCallback = nil
	// Nor does reading an archive ever prompt; the non-pager --log dump
	// runs with no frontend to answer one.
	params.PromptHandler = nil
	return params, nil
}

// nestedEngineSession reports, as unavailability, that this CLI runs inside a
// Dagger session (an agent, a nested CI step) and would connect to it as a
// nested client. The archive API serves only a session's main client, so the
// engine is never consulted rather than asked and refused: its 401 stays an
// authority error that is never silently bypassed, while this case -- which
// can never succeed, by design -- reads Cloud as it did before engine
// archives existed.
func nestedEngineSession() error {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		return fmt.Errorf("%w: engine archives are only readable by a session's main client, not from inside a Dagger session", tracesource.ErrEngineUnavailable)
	}
	return nil
}

// openEngineTraceWith is openEngineTrace with the engine connection supplied.
func openEngineTraceWith(ctx context.Context, traceID string, connect func(context.Context) (*client.Client, error)) (tracesource.Source, func() error, error) {
	session, err := connect(ctx)
	if err != nil {
		return nil, nil, engineConnectError(ctx, err)
	}
	source, err := tracesource.OpenStore(ctx, archive.NewClient(client.EngineConn(session)), traceID)
	if err != nil {
		return nil, session.Close, err
	}
	return source, session.Close, nil
}

// engineConnectError classifies a failure to reach or provision the engine
// (no container runtime, an unreachable runner, a failed engine start) as
// unavailability: the engine was never asked for the trace, so another source
// may be. Only the caller's own cancellation stays final; the client's
// internal connect timeout is unavailability too.
func engineConnectError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return err
	}
	return fmt.Errorf("%w: %w", tracesource.ErrEngineUnavailable, err)
}
