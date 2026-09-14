package engineutil

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/internal/buildkit/util/tracing"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
)

// ApplyGitBundle routes writes to the calling client, never the workspace's
// originating client. The client checks the lease even for an empty bundle.
func (c *Client) ApplyGitBundle(ctx context.Context, metadata *git.ApplyBundleMetadata, bundle io.Reader) (rerr error) {
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}
	caller, err := c.GetHostServiceCaller(ctx, md.ClientID)
	if err != nil {
		return err
	}
	if !caller.Supports("/dagger.git.Git/ApplyBundle") {
		return errors.New("client does not support applying git bundles; upgrade the dagger CLI")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := git.NewGitClient(caller.Conn()).ApplyBundle(ctx)
	if err != nil {
		return err
	}
	if err := stream.Send(&git.ApplyBundleRequest{Msg: &git.ApplyBundleRequest_Metadata{Metadata: metadata}}); err != nil {
		return err
	}
	if bundle != nil {
		if err := func() (rerr error) {
			span, _ := tracing.StartSpan(ctx, "stream workspace export bundle", telemetry.Internal())
			defer telemetry.EndWithCause(span, &rerr)
			var total int64
			defer func() { span.SetAttributes(attribute.Int64("dagger.workspace.export.bundle_bytes", total)) }()
			buf := make([]byte, 1<<20)
			for {
				n, err := bundle.Read(buf)
				if n > 0 {
					total += int64(n)
					if total > git.MaxGitPackBytes {
						return errors.New("git export bundle exceeds size limit")
					}
					if err := stream.Send(&git.ApplyBundleRequest{Msg: &git.ApplyBundleRequest_Chunk{Chunk: buf[:n]}}); err != nil {
						return err
					}
				}
				if errors.Is(err, io.EOF) {
					return nil
				}
				if err != nil {
					return fmt.Errorf("read export bundle: %w", err)
				}
			}
		}(); err != nil {
			return err
		}
	}
	// This includes the final stream drain and response round trip, not just
	// the host writer's execution time. No checkout changes begin before EOF.
	applySpan, _ := tracing.StartSpan(ctx, "await workspace bundle host application", telemetry.Internal())
	defer telemetry.EndWithCause(applySpan, &rerr)
	response, err := stream.CloseAndRecv()
	if err != nil {
		return err
	}
	if response.Error != nil {
		return errors.New(response.Error.Message)
	}
	if response.HeadSha != metadata.TargetSha || response.ParkedRef != "" {
		return errors.New("unexpected git export result")
	}
	return nil
}
