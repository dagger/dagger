package engineutil

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/util/gitutil"
)

const captureGitMethod = "/dagger.git.Git/CaptureGit"

// ErrGitCaptureUnsupported means the client cannot safely capture a workspace.
var ErrGitCaptureUnsupported = errors.New("client cannot capture git workspaces")

// GitCaptureApprovalError contains only metadata for paths awaiting approval.
// No candidate file or Git object bytes have been sent.
type GitCaptureApprovalError struct {
	Message    string
	Candidates []*git.CaptureGitCandidate
}

func (e *GitCaptureApprovalError) Error() string { return e.Message }

// CaptureGit streams a client-preflighted portable Git capture. The consumer
// should stage chunks until this method returns successfully; final size and
// digest checks happen after the stream ends. A GitCaptureApprovalError means
// no candidate bytes were sent and can be used to prompt before an exact-path
// approval retry.
func (c *Client) CaptureGit(
	ctx context.Context,
	checkoutPath string,
	policy *git.CaptureGitPolicy,
	consume func(git.CaptureGitChunk_Kind, []byte) error,
) (*git.CaptureGitMetadata, error) {
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := c.GetHostServiceCaller(ctx, md.ClientID)
	if err != nil {
		return nil, fmt.Errorf("failed to get client caller for %q: %w", md.ClientID, err)
	}
	if !caller.Supports(captureGitMethod) {
		return nil, ErrGitCaptureUnsupported
	}
	stream, err := git.NewGitClient(caller.Conn()).CaptureGit(ctx, &git.CaptureGitRequest{CheckoutPath: checkoutPath, Policy: policy})
	if err != nil {
		return nil, fmt.Errorf("failed to open git capture stream: %w", err)
	}

	bundleHash := sha256.New()
	var bundleBytes int64
	var metadata *git.CaptureGitMetadata
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to receive git capture: %w", err)
		}
		switch msg := response.Msg.(type) {
		case *git.CaptureGitResponse_Metadata:
			if metadata != nil {
				return nil, errors.New("received more than one git capture metadata message")
			}
			metadata = msg.Metadata
			if errInfo := metadata.GetError(); errInfo != nil {
				switch errInfo.Type {
				case git.CAPTURE_REJECTED:
					return nil, &GitCaptureApprovalError{Message: errInfo.Message, Candidates: append([]*git.CaptureGitCandidate(nil), metadata.ApprovalCandidates...)}
				case git.NOT_A_REPO:
					return nil, fmt.Errorf("%s: %w", errInfo.Message, gitutil.ErrGitNoRepo)
				case git.NOT_FOUND:
					return nil, fmt.Errorf("%s: %w", errInfo.Message, ErrGitCaptureUnsupported)
				default:
					return nil, errors.New(errInfo.Message)
				}
			}
		case *git.CaptureGitResponse_Chunk:
			if metadata == nil {
				return nil, errors.New("received git capture bytes before metadata")
			}
			chunk := msg.Chunk
			switch chunk.GetKind() {
			case git.CAPTURE_CHUNK_BUNDLE:
				bundleBytes += int64(len(chunk.Data))
				if bundleBytes > MaxFileContentsSize || bundleBytes > metadata.BundleBytes {
					return nil, errors.New("git capture bundle exceeds declared size")
				}
				_, _ = bundleHash.Write(chunk.Data)
			default:
				return nil, errors.New("received unknown git capture chunk kind")
			}
			if consume != nil {
				if err := consume(chunk.Kind, chunk.Data); err != nil {
					return nil, fmt.Errorf("consume git capture chunk: %w", err)
				}
			}
		}
	}
	if err := validateCaptureResult(metadata, bundleBytes, hex.EncodeToString(bundleHash.Sum(nil))); err != nil {
		return nil, err
	}
	return metadata, nil
}

func validateCaptureResult(metadata *git.CaptureGitMetadata, bundleBytes int64, bundleDigest string) error {
	if metadata == nil {
		return errors.New("missing git capture metadata")
	}
	if metadata.FormatVersion != 2 || metadata.ObjectFormat == "" || metadata.BaseSha == "" || metadata.HeadSha == "" || (metadata.RemoteUrl != "" && metadata.RemoteRef == "") {
		return errors.New("invalid git capture manifest metadata")
	}
	if metadata.BundleBytes == 0 && (metadata.HeadSha != metadata.BaseSha || metadata.WorktreeSha != "") {
		return errors.New("git capture local state is missing its bundle")
	}
	if bundleBytes != metadata.BundleBytes || bundleDigest != metadata.BundleSha256 {
		return errors.New("git capture bundle digest mismatch")
	}
	return nil
}
