package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// PackCommit is deliberately separate from PackCheckout: an older client must
// never interpret a scoped request as permission to send all branches and tags.
func (s GitAttachable) PackCommit(req *PackCommitRequest, srv Git_PackCommitServer) error {
	ctx, cancel := context.WithTimeout(srv.Context(), gitPackTimeout)
	defer cancel()
	sendErr := func(kind ErrorInfo_ErrorType, err error) error {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		return srv.Send(&PackCheckoutResponse{Msg: &PackCheckoutResponse_Metadata{Metadata: &PackCheckoutMetadata{Error: &ErrorInfo{Type: kind, Message: err.Error()}}}})
	}
	if req.CheckoutPath == "" || req.ExpectedStateDigest == "" || !validCommitSHA(req.CommitSha) || (req.Depth != 0 && req.Depth != 1) {
		return sendErr(INVALID_REQUEST, errors.New("captured checkout, state and SHA-1 commit with depth zero or one required"))
	}
	if !checkoutHasGitEntry(req.CheckoutPath) {
		return sendErr(HISTORY_UNAVAILABLE, errors.New("captured checkout unavailable"))
	}
	unlock, err := gitCheckoutLocks.lock(ctx, filepath.Clean(req.CheckoutPath))
	if err != nil {
		return err
	}
	defer unlock()
	state, err := collectCheckoutState(ctx, req.CheckoutPath)
	if err != nil {
		return sendErr(HISTORY_UNAVAILABLE, err)
	}
	if state.digest() != req.ExpectedStateDigest {
		return sendErr(CHECKOUT_STATE_MISMATCH, errors.New("captured checkout changed"))
	}
	if state.objectFormat != "sha1" {
		return sendErr(HISTORY_UNAVAILABLE, errors.New("unsupported object format"))
	}
	tmp, err := os.MkdirTemp("", "dagger-pack-commit-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	pack, err := packCapturedCommit(ctx, req.CheckoutPath, tmp, req.CommitSha, int(req.Depth))
	if errors.Is(err, errHostHistoryUnavailable) {
		return sendErr(HISTORY_UNAVAILABLE, err)
	}
	if err != nil {
		return sendErr(PACK_FAILED, err)
	}
	latest, err := collectCheckoutState(ctx, req.CheckoutPath)
	if err != nil || latest.digest() != req.ExpectedStateDigest {
		return sendErr(CHECKOUT_STATE_MISMATCH, errors.New("captured checkout changed while packing"))
	}
	if err := srv.Send(&PackCheckoutResponse{Msg: &PackCheckoutResponse_Metadata{Metadata: &PackCheckoutMetadata{HeadSha: req.CommitSha, ObjectFormat: "sha1", StateDigest: req.ExpectedStateDigest}}}); err != nil {
		return err
	}
	f, err := os.Open(pack)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, packCheckoutChunkSize)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if err := srv.Send(&PackCheckoutResponse{Msg: &PackCheckoutResponse_Chunk{Chunk: buf[:n]}}); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

var errHostHistoryUnavailable = errors.New("local commit history unavailable")

func validCommitSHA(sha string) bool {
	if len(sha) != 40 {
		return false
	}
	for _, c := range sha {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Borrow only the object database, never refs, replacement refs, config or the
// donor's shallow boundaries. All writable Git metadata lives in scratch.
func packCapturedCommit(ctx context.Context, checkout, scratch, sha string, depth int) (string, error) {
	objects, err := runHostGit(ctx, checkout, "rev-parse", "--path-format=absolute", "--git-path", "objects")
	if err != nil {
		return "", fmt.Errorf("%w: object database", errHostHistoryUnavailable)
	}
	if depth == 0 {
		shallow, err := runHostGit(ctx, checkout, "rev-parse", "--is-shallow-repository")
		if err != nil || strings.TrimSpace(shallow) != "false" {
			return "", fmt.Errorf("%w: shallow donor", errHostHistoryUnavailable)
		}
	}
	if _, err := runIsolatedHostGit(ctx, scratch, nil, nil, "init", "--bare", "--template=", "--object-format=sha1"); err != nil {
		return "", err
	}
	if depth == 1 {
		if err := os.WriteFile(filepath.Join(scratch, "shallow"), []byte(sha+"\n"), 0600); err != nil {
			return "", err
		}
	}
	env := []string{"GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_ALTERNATE_OBJECT_DIRECTORIES=" + strconv.Quote(strings.TrimSpace(objects))}
	// Probe in the isolated repository with lazy fetching disabled. Missing
	// commits, trees or blobs are a normal optional-donor miss, not a partial pack.
	typ, err := runIsolatedHostGit(ctx, scratch, env, strings.NewReader(sha+"\n"), "cat-file", "--batch-check=%(objecttype)")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(typ)) == sha+" missing" {
		return "", fmt.Errorf("%w: commit", errHostHistoryUnavailable)
	}
	if strings.TrimSpace(string(typ)) != "commit" {
		return "", fmt.Errorf("captured history anchor is not a commit")
	}
	inventory, err := runIsolatedHostGit(ctx, scratch, env, nil, "rev-list", "--objects", "--missing=print", sha)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(string(inventory), "?") || strings.Contains(string(inventory), "\n?") {
		return "", fmt.Errorf("%w: incomplete closure", errHostHistoryUnavailable)
	}
	prefix := filepath.Join(scratch, "objects", "pack", "pack")
	hash, err := runIsolatedHostGit(ctx, scratch, env, strings.NewReader(sha+"\n"), "pack-objects", "--revs", "--delta-base-offset", prefix)
	if err != nil {
		return "", err
	}
	pack := prefix + "-" + strings.TrimSpace(string(hash)) + ".pack"
	info, err := os.Stat(pack)
	if err != nil {
		return "", err
	}
	if info.Size() > MaxGitPackBytes {
		return "", fmt.Errorf("commit pack exceeds size limit")
	}
	return pack, nil
}

// Environment Git routing overrides must not redirect scratch writes into the
// owning checkout. Nor may global config add a promisor remote or hooks there.
func runIsolatedHostGit(ctx context.Context, dir string, extraEnv []string, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
