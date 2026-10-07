package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/internal/tracesource"
)

// openEngineTrace opens read-only client plumbing, never workspace modules or
// archived recipes. Its cleanup remains alive for lazy interactive expansion.
func openEngineTrace(ctx context.Context, traceID string) (tracesource.Source, func() error, error) {
	params, err := finalizeEngineParams(ctx, client.Params{LoadWorkspaceModules: false})
	if err != nil {
		return nil, nil, err
	}
	session, err := client.Connect(ctx, params)
	if err != nil {
		var transport *net.OpError
		if errors.As(err, &transport) && !transport.Timeout() && ctx.Err() == nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("%w: %w", archive.ErrTransient, err)
		}
		return nil, nil, err
	}
	source, err := tracesource.OpenArchive(ctx, archive.NewClient(client.EngineConn(session)), traceID)
	return source, session.Close, err
}
