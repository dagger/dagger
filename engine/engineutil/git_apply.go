package engineutil

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/git"
)

// ApplyGitBundle routes writes to the calling client, never the workspace's
// originating client. The client checks the lease even for an empty bundle.
func (c *Client) ApplyGitBundle(ctx context.Context, metadata *git.ApplyBundleMetadata, bundle io.Reader) error {
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
		buf := make([]byte, 1<<20)
		var total int64
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
				break
			}
			if err != nil {
				return fmt.Errorf("read export bundle: %w", err)
			}
		}
	}
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
