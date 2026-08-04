package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The engine's canonical view of a local checkout's repository is built by
// the *client's own git*: the client packs the repository (a bundle of HEAD
// plus all branches and tags) and the engine reconstructs a standalone repo
// from it. Host git natively understands every checkout layout - worktrees
// and submodules (whose .git is a pointer file), separate git dirs,
// alternates, partial clones - so the engine never interprets raw .git
// state. Mirror image of ApplyBundle, which streams engine-staged commits
// the other way.

const (
	// gitStateTimeout bounds reading a checkout's ref state.
	gitStateTimeout = 30 * time.Second
	// gitPackTimeout bounds packing a checkout into a bundle. Packing walks
	// and compresses the full history, which on a large repository is not
	// instant.
	gitPackTimeout = 10 * time.Minute
	// packCheckoutChunkSize bounds each streamed bundle message, keeping it
	// well under gRPC's default 4MiB limit.
	packCheckoutChunkSize = 1 << 20
)

// CheckoutState reports a digest identifying the current git state of a local
// checkout: HEAD, the symbolic HEAD, the object format, and all branch and tag
// refs. The engine uses it as a cache key: a checkout is only re-packed when
// its refs actually move, not on every read.
//
// A checkout with no .git entry at its root reports NOT_A_REPO, which the
// engine degrades to its "no git context" state; a .git that exists but is
// unusable is a broken environment and reports a hard error.
func (s GitAttachable) CheckoutState(ctx context.Context, req *CheckoutStateRequest) (*CheckoutStateResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, gitStateTimeout)
	defer cancel()

	if req.GetCheckoutPath() == "" {
		return newCheckoutStateErrorResponse(INVALID_REQUEST, "checkout path is required"), nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		return newCheckoutStateErrorResponse(NOT_FOUND, gitMissingMessage), nil
	}
	if !checkoutHasGitEntry(req.GetCheckoutPath()) {
		return newCheckoutStateErrorResponse(NOT_A_REPO, "no .git entry at checkout root"), nil
	}

	gitMutex.Lock()
	defer gitMutex.Unlock()

	state, err := collectCheckoutState(ctx, req.GetCheckoutPath())
	if err != nil {
		return newCheckoutStateErrorResponse(UNKNOWN, err.Error()), nil
	}

	sum := sha256.Sum256([]byte(strings.Join([]string{
		state.headSHA,
		state.headRef,
		state.objectFormat,
		state.refs,
	}, "\x00")))
	return &CheckoutStateResponse{
		Result: &CheckoutStateResponse_StateDigest{StateDigest: hex.EncodeToString(sum[:])},
	}, nil
}

// PackCheckout packs a local checkout's repository into a git bundle of HEAD
// plus all branches and tags, streaming one metadata message followed by the
// bundle's bytes in chunks. A repository with no commits yet (unborn HEAD)
// returns metadata carrying only the branch name, with no bundle.
func (s GitAttachable) PackCheckout(req *PackCheckoutRequest, srv Git_PackCheckoutServer) error {
	ctx, cancel := context.WithTimeout(srv.Context(), gitPackTimeout)
	defer cancel()

	sendErr := func(errorType ErrorInfo_ErrorType, message string) error {
		return srv.Send(&PackCheckoutResponse{
			Msg: &PackCheckoutResponse_Metadata{
				Metadata: &PackCheckoutMetadata{
					Error: &ErrorInfo{Type: errorType, Message: message},
				},
			},
		})
	}

	checkout := req.GetCheckoutPath()
	if checkout == "" {
		return sendErr(INVALID_REQUEST, "checkout path is required")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return sendErr(NOT_FOUND, gitMissingMessage)
	}
	if !checkoutHasGitEntry(checkout) {
		return sendErr(NOT_A_REPO, "no .git entry at checkout root")
	}

	gitMutex.Lock()
	defer gitMutex.Unlock()

	state, err := collectCheckoutState(ctx, checkout)
	if err != nil {
		return sendErr(PACK_FAILED, err.Error())
	}

	if state.headSHA == "" {
		// Unborn HEAD: nothing to pack; the engine reconstructs an empty
		// repository on the same branch.
		return srv.Send(&PackCheckoutResponse{
			Msg: &PackCheckoutResponse_Metadata{
				Metadata: &PackCheckoutMetadata{
					HeadRef:      state.headRef,
					ObjectFormat: state.objectFormat,
				},
			},
		})
	}

	tmpDir, err := os.MkdirTemp("", "dagger-pack-checkout")
	if err != nil {
		return fmt.Errorf("create pack temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	bundlePath := filepath.Join(tmpDir, "checkout.bundle")

	if _, err := runHostGit(ctx, checkout, "bundle", "create", bundlePath, "--branches", "--tags", "HEAD"); err != nil {
		return sendErr(PACK_FAILED, err.Error())
	}

	if err := srv.Send(&PackCheckoutResponse{
		Msg: &PackCheckoutResponse_Metadata{
			Metadata: &PackCheckoutMetadata{
				HeadSha:      state.headSHA,
				HeadRef:      state.headRef,
				ObjectFormat: state.objectFormat,
			},
		},
	}); err != nil {
		return fmt.Errorf("send pack metadata: %w", err)
	}

	bundle, err := os.Open(bundlePath)
	if err != nil {
		return fmt.Errorf("open bundle: %w", err)
	}
	defer bundle.Close()
	buf := make([]byte, packCheckoutChunkSize)
	for {
		n, err := bundle.Read(buf)
		if n > 0 {
			if err := srv.Send(&PackCheckoutResponse{
				Msg: &PackCheckoutResponse_Chunk{Chunk: buf[:n]},
			}); err != nil {
				return fmt.Errorf("send bundle chunk: %w", err)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("read bundle: %w", err)
		}
	}
	return nil
}

// checkoutState is the raw material of a checkout's state digest and pack
// metadata.
type checkoutState struct {
	headSHA      string // empty on unborn HEAD
	headRef      string // symbolic HEAD, empty when detached
	objectFormat string
	refs         string // all branch and tag refs with their targets
}

// collectCheckoutState gathers a checkout's git state with host git. HEAD not
// resolving to a commit is a legitimate state (unborn HEAD) as long as the
// repository itself is readable, which listing refs verifies.
func collectCheckoutState(ctx context.Context, checkout string) (checkoutState, error) {
	var state checkoutState

	// for-each-ref doubles as the "is this a usable repository" probe: it
	// fails loudly on a broken layout, where the optional probes below
	// tolerate failure for legitimate reasons.
	refs, err := runHostGit(ctx, checkout, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/tags")
	if err != nil {
		return state, err
	}
	state.refs = strings.TrimSpace(refs)

	// Fails on unborn HEAD (no commits yet).
	if out, err := runHostGit(ctx, checkout, "rev-parse", "-q", "--verify", "HEAD"); err == nil {
		state.headSHA = strings.TrimSpace(out)
	}
	// Fails when HEAD is detached.
	if out, err := runHostGit(ctx, checkout, "symbolic-ref", "-q", "HEAD"); err == nil {
		state.headRef = strings.TrimSpace(out)
	}
	// Not supported by ancient git; the engine defaults to sha1.
	if out, err := runHostGit(ctx, checkout, "rev-parse", "--show-object-format"); err == nil {
		state.objectFormat = strings.TrimSpace(out)
	}
	return state, nil
}

// checkoutHasGitEntry reports whether the checkout has a .git entry (directory
// or pointer file) at its root. Its absence is the legitimate "not a git
// checkout" state; discovery of an enclosing repository higher up is
// deliberately not attempted, matching how module contexts and workspaces
// define their git-ness by the .git entry at their own root.
func checkoutHasGitEntry(checkout string) bool {
	_, err := os.Lstat(filepath.Join(checkout, ".git"))
	return err == nil
}

func newCheckoutStateErrorResponse(errorType ErrorInfo_ErrorType, message string) *CheckoutStateResponse {
	return &CheckoutStateResponse{
		Result: &CheckoutStateResponse_Error{
			Error: &ErrorInfo{
				Type:    errorType,
				Message: message,
			},
		},
	}
}
