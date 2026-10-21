package daggercmd

import (
	"context"
	"fmt"
	"os"

	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/internal/tracesource"
)

// openEngineTrace opens read-only client plumbing, never workspace modules or
// archived recipes. Its cleanup remains alive for lazy interactive expansion.
func openEngineTrace(ctx context.Context, traceID string) (tracesource.Source, func() error, error) {
	if err := nestedEngineSession(); err != nil {
		return nil, nil, err
	}
	params, err := finalizeEngineParams(ctx, client.Params{LoadWorkspaceModules: false})
	if err != nil {
		return nil, nil, err
	}
	return openEngineTraceWith(ctx, traceID, func(ctx context.Context) (*client.Client, error) {
		return client.Connect(ctx, params)
	})
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
	source, err := tracesource.OpenArchive(ctx, archive.NewClient(client.EngineConn(session)), traceID)
	return source, session.Close, err
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
