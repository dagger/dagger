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

// Pushing a workspace's commits to a remote is done by the *client's own
// git*, for the same reason saving staged commits is (see ApplyBundle): the
// checkout is where the user's remotes, credential helpers, ssh agent and
// hooks live, so a push from here behaves exactly like `git push` run in the
// checkout. Commits that only exist engine-side arrive as a git bundle and
// are fetched into the checkout's object database without creating or moving
// any local ref, then pushed by hash — the checkout's own branches and work
// tree are never modified.

// gitPushTimeout bounds fetching the bundle and pushing to the remote. The
// push talks to a network remote, which on a large payload or slow link is
// not instant.
const gitPushTimeout = 10 * time.Minute

// Push receives a push order (and, when the commit being pushed only exists
// engine-side, a git bundle carrying it) and updates a remote ref using the
// client's own git.
//
// The stream is one metadata message optionally followed by bundle bytes in
// chunks. Nothing about the local checkout changes: the bundle's objects
// enter the object database, and the push addresses the remote directly with
// an explicit <sha>:<ref> refspec.
func (s GitAttachable) Push(srv Git_PushServer) error {
	ctx, cancel := context.WithTimeout(srv.Context(), gitPushTimeout)
	defer cancel()

	var (
		meta   *PushMetadata
		bundle bytes.Buffer
	)
	for {
		req, err := srv.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("receive push request: %w", err)
		}
		switch msg := req.GetMsg().(type) {
		case *PushRequest_Metadata:
			if meta != nil {
				return srv.SendAndClose(newPushErrorResponse(INVALID_REQUEST,
					"received more than one metadata message"))
			}
			meta = msg.Metadata
		case *PushRequest_Chunk:
			if meta == nil {
				return srv.SendAndClose(newPushErrorResponse(INVALID_REQUEST,
					"received bundle data before metadata"))
			}
			bundle.Write(msg.Chunk)
		}
	}
	if meta == nil {
		return srv.SendAndClose(newPushErrorResponse(INVALID_REQUEST, "missing metadata message"))
	}
	if meta.GetCheckoutPath() == "" || meta.GetRemote() == "" || meta.GetTargetSha() == "" {
		return srv.SendAndClose(newPushErrorResponse(INVALID_REQUEST,
			"checkout path, remote and target commit are required"))
	}
	if bundle.Len() > 0 && meta.GetBundleRef() == "" {
		return srv.SendAndClose(newPushErrorResponse(INVALID_REQUEST,
			"bundle ref is required when a bundle is sent"))
	}

	resp, err := pushCommits(ctx, meta, bundle.Bytes())
	if err != nil {
		return err
	}
	return srv.SendAndClose(resp)
}

func pushCommits(ctx context.Context, meta *PushMetadata, bundle []byte) (*PushResponse, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return newPushErrorResponse(NOT_FOUND, gitMissingMessage), nil
	}

	gitMutex.Lock()
	defer gitMutex.Unlock()

	checkout := meta.GetCheckoutPath()

	// An empty destination means "the branch the checkout has checked out",
	// resolved here so it reflects the checkout at push time. A detached
	// HEAD names no branch, so the caller must name one.
	destRef := meta.GetDestRef()
	if destRef == "" {
		out, err := runHostGit(ctx, checkout, "symbolic-ref", "-q", "HEAD")
		if err != nil {
			return newPushErrorResponse(DETACHED_HEAD,
				"checkout HEAD is detached; name the branch to push to"), nil
		}
		destRef = strings.TrimSpace(out)
	}

	if len(bundle) > 0 {
		tmpDir, err := os.MkdirTemp("", "dagger-push-bundle")
		if err != nil {
			return nil, fmt.Errorf("create bundle temp dir: %w", err)
		}
		defer os.RemoveAll(tmpDir)
		bundlePath := filepath.Join(tmpDir, "staged.bundle")
		if err := os.WriteFile(bundlePath, bundle, 0o600); err != nil {
			return nil, fmt.Errorf("write bundle: %w", err)
		}
		// A fetch with no destination refspec writes the bundle's objects
		// into the object database (verifying the bundle's prerequisites are
		// present) without creating or moving any local ref — unlike a save,
		// a push must leave the checkout exactly as it is.
		if _, err := runHostGit(ctx, checkout, "fetch", "--no-tags", bundlePath, meta.GetBundleRef()); err != nil {
			return newPushErrorResponse(PUSH_FAILED, err.Error()), nil
		}
	}

	// The commit being pushed must exist locally by now, whether it came in
	// via the bundle or was already part of the checkout's history. Checking
	// here turns a confusing push error into a clear one.
	if _, err := runHostGit(ctx, checkout, "rev-parse", "-q", "--verify", meta.GetTargetSha()+"^{commit}"); err != nil {
		return newPushErrorResponse(PUSH_FAILED, fmt.Sprintf(
			"commit %s not found in the local repository", meta.GetTargetSha())), nil
	}

	// An explicit <sha>:<ref> refspec pushes exactly the requested commit to
	// exactly the requested ref, independent of push.default and of what the
	// checkout has checked out. git itself refuses a non-fast-forward update
	// unless forced, with its own diagnostics.
	args := []string{"push"}
	if meta.GetForce() {
		args = append(args, "--force")
	}
	args = append(args, "--", meta.GetRemote(), meta.GetTargetSha()+":"+destRef)
	if _, err := runHostGit(ctx, checkout, args...); err != nil {
		return newPushErrorResponse(PUSH_FAILED, err.Error()), nil
	}

	return &PushResponse{
		Result: &PushResponse_Pushed{
			Pushed: &PushResult{
				DestRef: destRef,
				Sha:     meta.GetTargetSha(),
			},
		},
	}, nil
}

func newPushErrorResponse(errorType ErrorInfo_ErrorType, message string) *PushResponse {
	return &PushResponse{
		Result: &PushResponse_Error{
			Error: &ErrorInfo{
				Type:    errorType,
				Message: message,
			},
		},
	}
}
