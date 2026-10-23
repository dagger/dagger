package schema

import (
	"context"

	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	enginetel "github.com/dagger/dagger/engine/telemetry"
)

// filesyncTransfer records a transfer with the session's main client. Nested
// clients run inside the engine, so their transfers are not external.
func filesyncTransfer(
	ctx context.Context,
	query *core.Query,
	clientID string,
	start func(context.Context, string) (context.Context, *enginetel.WorkloadTransfer),
) (context.Context, *enginetel.WorkloadTransfer) {
	main, err := query.MainClientCallerMetadata(ctx)
	if err == nil && main.ClientID != clientID {
		return ctx, nil
	}
	ctx, transfer := start(ctx, enginetel.WorkloadTransferFilesync)
	if err != nil {
		transfer.Unmeasured()
	}
	return ctx, transfer
}

type contentDigester interface {
	ContentPreferredDigest(context.Context) (digest.Digest, error)
}

// filesyncExport records an export to the calling client. The identity of the
// export is the content of the exported object. Call the returned function
// when the export ends.
func filesyncExport(ctx context.Context, content contentDigester) (context.Context, func(error)) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return ctx, func(error) {}
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return ctx, func(error) {}
	}
	ctx, output := filesyncTransfer(ctx, query, clientMetadata.ClientID, enginetel.StartWorkloadOutput)
	return ctx, func(err error) {
		if output == nil {
			return
		}
		var identity string
		if dgst, digestErr := content.ContentPreferredDigest(ctx); digestErr == nil {
			identity = dgst.String()
		}
		output.End(identity, err)
	}
}
