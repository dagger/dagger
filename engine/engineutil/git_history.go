package engineutil

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/dagger/dagger/engine/session/git"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrGitHistoryUnavailable is an optional donor miss. Protocol errors, invalid
// packs and cancellation are not misses and must never be hidden by fallback.
var ErrGitHistoryUnavailable = errors.New("host Git history unavailable")

// ReceiveGitCommitPack requires the caller to have authorized this exact host
// route and captured commit. The returned file is owned, and must be closed.
func ReceiveGitCommitPack(ctx context.Context, client git.GitClient, req *git.PackCommitRequest) (_ *GitCheckoutPack, rerr error) {
	stream, err := client.PackCommit(ctx, req)
	if err != nil {
		return nil, hostHistoryStreamError(ctx, err)
	}
	var pack *GitCheckoutPack
	var spool *gitPackSpool
	defer func() {
		if rerr != nil && spool != nil {
			if cleanupErr := spool.remove(); cleanupErr != nil {
				// A failed spool cleanup is not an optional donor miss, even if
				// the transport disappeared while it was being received.
				rerr = fmt.Errorf("host history cleanup after %s: %w", rerr.Error(), cleanupErr)
			}
		}
	}()
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, hostHistoryStreamError(ctx, err)
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
				switch md.Error.Type {
				case git.HISTORY_UNAVAILABLE, git.CHECKOUT_STATE_MISMATCH:
					return nil, fmt.Errorf("%w: %s", ErrGitHistoryUnavailable, md.Error.Message)
				default:
					return nil, fmt.Errorf("host history pack: %s", md.Error.Message)
				}
			}
			if md.HeadSha != req.CommitSha || md.StateDigest != req.ExpectedStateDigest || md.ObjectFormat != "sha1" || md.HeadRef != "" {
				return nil, fmt.Errorf("host history metadata does not match captured request")
			}
			pack = &GitCheckoutPack{HeadSHA: md.HeadSha, StateDigest: md.StateDigest, ObjectFormat: md.ObjectFormat}
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

func hostHistoryStreamError(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	switch status.Code(err) {
	case codes.Unimplemented, codes.Unavailable:
		return fmt.Errorf("%w: %w", ErrGitHistoryUnavailable, err)
	default:
		return err
	}
}
