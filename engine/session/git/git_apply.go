package git

import (
	bytes "bytes"
	context "context"
	fmt "fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	strings "strings"
	"time"
)

// Landing engine-staged commits on the user's checkout is done by the
// *client's own git*: the engine builds a bundle of the staged commits and
// hands it over, the client fetches it and fast-forwards. Host git natively
// understands checkout layouts the engine cannot reassemble faithfully —
// worktrees and submodules, whose .git is a pointer file — and it produces
// the reflog entries, index update and work tree update a user expects.

const (
	// gitHeadTimeout bounds resolving a checkout's HEAD.
	gitHeadTimeout = 30 * time.Second
	// gitApplyBundleTimeout bounds fetching and fast-forwarding a bundle.
	// Both are local operations, but the fast-forward rewrites the work
	// tree, which on a large checkout is not instant.
	gitApplyBundleTimeout = 5 * time.Minute
)

// GetHead resolves the current HEAD commit of a local checkout, using the
// client's git. Unlike reading .git from the engine, this handles worktree and
// submodule checkouts, whose .git is a pointer file into another repository.
func (s GitAttachable) GetHead(ctx context.Context, req *GitHeadRequest) (*GitHeadResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, gitHeadTimeout)
	defer cancel()

	if req.GetCheckoutPath() == "" {
		return newGitHeadErrorResponse(INVALID_REQUEST, "checkout path is required"), nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		return newGitHeadErrorResponse(NOT_FOUND, gitMissingMessage), nil
	}

	gitMutex.Lock()
	defer gitMutex.Unlock()

	out, err := runHostGit(ctx, req.GetCheckoutPath(), "rev-parse", "HEAD")
	if err != nil {
		return newGitHeadErrorResponse(UNKNOWN, err.Error()), nil
	}
	return &GitHeadResponse{
		Result: &GitHeadResponse_HeadSha{HeadSha: strings.TrimSpace(out)},
	}, nil
}

// ApplyBundle receives a git bundle holding commits staged engine-side and
// lands them on a local checkout as a fast-forward.
//
// The stream is one metadata message followed by the bundle's bytes in chunks.
// Nothing is written until the checkout is re-verified to still sit at the
// commit the bundle was built on: that check is the authoritative one, taken
// here, on the host, immediately before the fetch, so a checkout that moved
// between the engine's earlier check and now is still refused.
func (s GitAttachable) ApplyBundle(srv Git_ApplyBundleServer) error {
	ctx, cancel := context.WithTimeout(srv.Context(), gitApplyBundleTimeout)
	defer cancel()

	var (
		meta   *ApplyBundleMetadata
		bundle bytes.Buffer
	)
	for {
		req, err := srv.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("receive bundle: %w", err)
		}
		switch msg := req.GetMsg().(type) {
		case *ApplyBundleRequest_Metadata:
			if meta != nil {
				return srv.SendAndClose(newApplyBundleErrorResponse(INVALID_REQUEST,
					"received more than one metadata message"))
			}
			meta = msg.Metadata
		case *ApplyBundleRequest_Chunk:
			if meta == nil {
				return srv.SendAndClose(newApplyBundleErrorResponse(INVALID_REQUEST,
					"received bundle data before metadata"))
			}
			bundle.Write(msg.Chunk)
		}
	}
	if meta == nil {
		return srv.SendAndClose(newApplyBundleErrorResponse(INVALID_REQUEST, "missing metadata message"))
	}
	if meta.GetCheckoutPath() == "" || meta.GetTargetSha() == "" || meta.GetBundleRef() == "" {
		return srv.SendAndClose(newApplyBundleErrorResponse(INVALID_REQUEST,
			"checkout path, target commit and bundle ref are required"))
	}
	if bundle.Len() == 0 {
		return srv.SendAndClose(newApplyBundleErrorResponse(INVALID_REQUEST, "empty bundle"))
	}

	resp, err := applyBundle(ctx, meta, bundle.Bytes())
	if err != nil {
		return err
	}
	return srv.SendAndClose(resp)
}

func applyBundle(ctx context.Context, meta *ApplyBundleMetadata, bundle []byte) (*ApplyBundleResponse, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return newApplyBundleErrorResponse(NOT_FOUND, gitMissingMessage), nil
	}

	gitMutex.Lock()
	defer gitMutex.Unlock()

	checkout := meta.GetCheckoutPath()

	// Last-second precondition, on the host itself: the checkout must still
	// be exactly where the staged commits were built from, or fast-forwarding
	// it would silently discard whatever moved it.
	if want := meta.GetExpectedBaseSha(); want != "" {
		out, err := runHostGit(ctx, checkout, "rev-parse", "HEAD")
		if err != nil {
			return newApplyBundleErrorResponse(BUNDLE_APPLY_FAILED, err.Error()), nil
		}
		if got := strings.TrimSpace(out); got != want {
			return newApplyBundleErrorResponse(HEAD_MISMATCH, fmt.Sprintf(
				"local branch moved from %s to %s", want, got)), nil
		}
	}

	tmpDir, err := os.MkdirTemp("", "dagger-bundle")
	if err != nil {
		return nil, fmt.Errorf("create bundle temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	bundlePath := filepath.Join(tmpDir, "staged.bundle")
	if err := os.WriteFile(bundlePath, bundle, 0o600); err != nil {
		return nil, fmt.Errorf("write bundle: %w", err)
	}

	// Fetch brings the staged objects in (and verifies the bundle's
	// prerequisites are present), then the fast-forward moves the checked-out
	// ref, the reflog, the index and the work tree in one step — refusing,
	// with git's own diagnostics, anything that is not a fast-forward or that
	// would clobber local work.
	if _, err := runHostGit(ctx, checkout, "fetch", "--no-tags", bundlePath, meta.GetBundleRef()); err != nil {
		return newApplyBundleErrorResponse(BUNDLE_APPLY_FAILED, err.Error()), nil
	}
	if _, err := runHostGit(ctx, checkout, "merge", "--ff-only", meta.GetTargetSha()); err != nil {
		return newApplyBundleErrorResponse(BUNDLE_APPLY_FAILED, err.Error()), nil
	}

	out, err := runHostGit(ctx, checkout, "rev-parse", "HEAD")
	if err != nil {
		return newApplyBundleErrorResponse(BUNDLE_APPLY_FAILED, err.Error()), nil
	}
	return &ApplyBundleResponse{
		Result: &ApplyBundleResponse_Applied{
			Applied: &ApplyBundleResult{HeadSha: strings.TrimSpace(out)},
		},
	}, nil
}

const gitMissingMessage = "git is not installed or not in PATH"

// runHostGit runs a git command against a local checkout, returning its
// standard output. Errors carry git's standard error, which is where it
// explains what it refused to do.
func runHostGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"SSH_ASKPASS=echo",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("git %s timed out", strings.Join(args, " "))
		}
		detail := strings.TrimSpace(stderr.String() + stdout.String())
		if detail == "" {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, detail)
	}
	return stdout.String(), nil
}

func newGitHeadErrorResponse(errorType ErrorInfo_ErrorType, message string) *GitHeadResponse {
	return &GitHeadResponse{
		Result: &GitHeadResponse_Error{
			Error: &ErrorInfo{
				Type:    errorType,
				Message: message,
			},
		},
	}
}

func newApplyBundleErrorResponse(errorType ErrorInfo_ErrorType, message string) *ApplyBundleResponse {
	return &ApplyBundleResponse{
		Result: &ApplyBundleResponse_Error{
			Error: &ErrorInfo{
				Type:    errorType,
				Message: message,
			},
		},
	}
}
