package engineutil

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/dagger/dagger/engine/session/git"
)

// ErrGitHistoryUnavailable is an optional donor miss: any donor failure other
// than the caller's own cancellation. Donors are an optimization, so host pack
// errors and timeouts, size limits, lost transports and protocol violations all
// fall back to the authorized remote. Nothing from a failed donation is kept.
var ErrGitHistoryUnavailable = errors.New("host Git history unavailable")

// ReceiveGitCommitPack requires the caller to have authorized this exact host
// route and captured commit. The returned file is owned, and must be closed.
// Every failure is an ErrGitHistoryUnavailable miss unless ctx itself is done;
// the donor's own pack timeout also surfaces as DeadlineExceeded, so only ctx
// (never the error code) decides that the caller was cancelled.
func ReceiveGitCommitPack(ctx context.Context, client git.GitClient, req *git.PackCommitRequest) (_ *GitCheckoutPack, rerr error) {
	var spool *gitPackSpool
	defer func() {
		if rerr == nil {
			return
		}
		if spool != nil {
			if cleanupErr := spool.remove(); cleanupErr != nil {
				// A failed spool cleanup is not an optional donor miss, even if
				// the transport disappeared while it was being received.
				rerr = fmt.Errorf("host history cleanup after %s: %w", rerr.Error(), cleanupErr)
				return
			}
		}
		if ctx.Err() != nil {
			rerr = context.Cause(ctx)
			return
		}
		rerr = fmt.Errorf("%w: %w", ErrGitHistoryUnavailable, rerr)
	}()
	stream, err := client.PackCommit(ctx, req)
	if err != nil {
		return nil, err
	}
	var pack *GitCheckoutPack
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		switch msg := resp.Msg.(type) {
		case *git.PackCheckoutResponse_Metadata:
			if pack != nil || msg.Metadata == nil {
				return nil, fmt.Errorf("invalid host history metadata")
			}
			md := msg.Metadata
			if md.Error != nil {
				return nil, fmt.Errorf("host history pack: %s: %s", md.Error.Type, md.Error.Message)
			}
			// PackCommit does not pin the checkout's refs, so there is no
			// state digest to check: the importer verifies the closure itself.
			if md.HeadSha != req.CommitSha || md.ObjectFormat != "sha1" || md.HeadRef != "" {
				return nil, fmt.Errorf("host history metadata does not match captured request")
			}
			pack = &GitCheckoutPack{HeadSHA: md.HeadSha, ObjectFormat: md.ObjectFormat}
		case *git.PackCheckoutResponse_Chunk:
			if pack == nil {
				return nil, fmt.Errorf("host history bytes before metadata")
			}
			if spool == nil {
				spool, err = newGitPackSpool("dagger-commit-pack-*", git.MaxGitPackBytes)
				if err != nil {
					return nil, err
				}
			}
			if err := spool.write(msg.Chunk); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unexpected host history response")
		}
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	if pack == nil || spool == nil || spool.size == 0 {
		return nil, fmt.Errorf("missing host history pack")
	}
	pack.BundlePath, err = spool.finish()
	if err != nil {
		return nil, err
	}
	return pack, nil
}
