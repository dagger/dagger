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
// lands them on a local checkout. When the checkout still sits at the base the
// bundle was built from, it is applied as an exact fast-forward. If the local
// branch advanced, the staged commits are first rebased onto its current HEAD
// in an isolated worktree, then the checkout is fast-forwarded to the rewritten
// result.
//
// The stream is one metadata message followed by the bundle's bytes in chunks.
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

	tmpDir, err := os.MkdirTemp("", "dagger-bundle")
	if err != nil {
		return nil, fmt.Errorf("create bundle temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	bundlePath := filepath.Join(tmpDir, "staged.bundle")
	if err := os.WriteFile(bundlePath, bundle, 0o600); err != nil {
		return nil, fmt.Errorf("write bundle: %w", err)
	}

	// Fetch brings the staged objects in and verifies that the bundle's
	// prerequisites are present. It does not move any local ref.
	if _, err := runHostGit(ctx, checkout, "fetch", "--no-tags", bundlePath, meta.GetBundleRef()); err != nil {
		return newApplyBundleErrorResponse(BUNDLE_APPLY_FAILED, err.Error()), nil
	}

	base := meta.GetExpectedBaseSha()
	out, err := runHostGit(ctx, checkout, "rev-parse", "--verify", "HEAD")
	if err != nil && base != "" {
		return newApplyBundleErrorResponse(BUNDLE_APPLY_FAILED, err.Error()), nil
	}
	currentHead := strings.TrimSpace(out)
	targetHead := meta.GetTargetSha()
	if currentHead != "" && currentHead != base {
		targetHead, err = replayBundle(ctx, checkout, tmpDir, base, targetHead, currentHead)
		if err != nil {
			return newApplyBundleErrorResponse(BUNDLE_APPLY_FAILED, err.Error()), nil
		}
	}

	// A staged commit can fold in work the checkout was already carrying — a
	// file the user had modified (or created) before the agent ever ran. Its
	// work tree content is then byte-identical to what the commit holds, but
	// git still refuses to fast-forward over a locally modified or untracked
	// path. Staging exactly those paths first makes the index agree with the
	// incoming commit, which git fast-forwards without touching the work tree
	// at all, leaving the file clean afterwards.
	staged := stageAlreadyMatchingPaths(ctx, checkout, targetHead)
	if _, err := runHostGit(ctx, checkout, "merge", "--ff-only", targetHead); err != nil {
		// Put the index back the way it was found, so a refused save leaves
		// the checkout untouched.
		for _, p := range staged {
			_, _ = runHostGit(ctx, checkout, "restore", "--staged", "--", p)
		}
		return newApplyBundleErrorResponse(BUNDLE_APPLY_FAILED, err.Error()), nil
	}

	out, err = runHostGit(ctx, checkout, "rev-parse", "HEAD")
	if err != nil {
		return newApplyBundleErrorResponse(BUNDLE_APPLY_FAILED, err.Error()), nil
	}
	return &ApplyBundleResponse{
		Result: &ApplyBundleResponse_Applied{
			Applied: &ApplyBundleResult{HeadSha: strings.TrimSpace(out)},
		},
	}, nil
}

// replayBundle rebases the engine-staged commit stack onto currentHead in a
// detached temporary worktree. In particular, a conflict only dirties that
// disposable worktree; the user's branch, index and work tree are not involved.
func replayBundle(ctx context.Context, checkout, tmpDir, base, target, currentHead string) (string, error) {
	if base != "" {
		if _, err := runHostGit(ctx, checkout, "merge-base", "--is-ancestor", base, target); err != nil {
			return "", fmt.Errorf("staged commits are not based on %s: %w", base, err)
		}
	}

	replayDir := filepath.Join(tmpDir, "replay")
	if _, err := runHostGit(ctx, checkout,
		"-c", "core.hooksPath=/dev/null",
		"worktree", "add", "--detach", replayDir, target,
	); err != nil {
		return "", fmt.Errorf("create replay worktree: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), gitHeadTimeout)
		defer cancel()
		_, _ = runHostGit(cleanupCtx, checkout, "worktree", "remove", "--force", replayDir)
	}()

	// Do not let repository-local hooks or user rebase.updateRefs configuration
	// turn preparation in the disposable worktree into changes elsewhere.
	rebaseArgs := []string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "rebase.updateRefs=false",
		"-c", "rerere.enabled=false",
		"rebase",
	}
	if base == "" {
		rebaseArgs = append(rebaseArgs, "--root", "--onto", currentHead)
	} else {
		rebaseArgs = append(rebaseArgs, "--onto", currentHead, base)
	}
	if _, err := runHostGit(ctx, replayDir, rebaseArgs...); err != nil {
		return "", fmt.Errorf("replay staged commits onto local HEAD: %w", err)
	}
	out, err := runHostGit(ctx, replayDir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve replayed HEAD: %w", err)
	}
	return strings.TrimSpace(out), nil
}

const gitMissingMessage = "git is not installed or not in PATH"

// stageAlreadyMatchingPaths stages the work tree paths that the incoming
// commits change and that already hold exactly the content those commits
// carry — the case where a staged commit folded in work the checkout was
// already dirty with.
//
// git refuses to fast-forward over a locally modified or untracked path even
// when the incoming content is byte-identical, because it compares the *index*
// (still at the old blob) with the work tree. Staging such a path makes the
// index entry equal the incoming tree's entry, which git resolves by keeping
// the work tree as-is — no overwrite, no data at risk — and the file ends up
// clean once HEAD moves.
//
// Every step is best-effort: anything that cannot be verified is left alone,
// and the fast-forward then fails with git's own diagnostics, as before.
func stageAlreadyMatchingPaths(ctx context.Context, checkout, targetSHA string) []string {
	out, err := runHostGit(ctx, checkout, "diff", "--name-only", "HEAD", targetSHA)
	if err != nil {
		return nil
	}
	var staged []string
	for _, p := range strings.Split(strings.TrimSpace(out), "\n") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Only paths the checkout is actually dirty with are candidates:
		// clean ones fast-forward normally.
		status, err := runHostGit(ctx, checkout, "status", "--porcelain", "--untracked-files=all", "--", p)
		if err != nil || strings.TrimSpace(status) == "" {
			continue
		}
		// The work tree content must already be exactly what the commit
		// holds; otherwise this really is a conflicting local change and git
		// must be left to refuse it.
		have, err := runHostGit(ctx, checkout, "hash-object", "--", p)
		if err != nil {
			continue
		}
		want, err := runHostGit(ctx, checkout, "rev-parse", targetSHA+":"+p)
		if err != nil {
			continue
		}
		if strings.TrimSpace(have) != strings.TrimSpace(want) {
			continue
		}
		if _, err := runHostGit(ctx, checkout, "add", "--", p); err != nil {
			continue
		}
		staged = append(staged, p)
	}
	return staged
}

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
