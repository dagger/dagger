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
//
// It neither checks the checkout's refs against their captured state nor takes
// the checkout lock. The closure of a fixed SHA does not depend on where HEAD,
// branches or tags point, so committing, switching branches or fetching after
// capture must not disable the donor; the engine verifies object hashes and the
// exact closure inventory on import. Packing only reads the object database
// into private scratch, so it need not serialize with CheckoutState, CaptureGit
// or PackCheckout on the same checkout either.
func (s GitAttachable) PackCommit(req *PackCommitRequest, srv Git_PackCommitServer) error {
	ctx, cancel := context.WithTimeout(srv.Context(), gitPackTimeout)
	defer cancel()
	sendErr := func(kind ErrorInfo_ErrorType, err error) error {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		return srv.Send(&PackCheckoutResponse{Msg: &PackCheckoutResponse_Metadata{Metadata: &PackCheckoutMetadata{Error: &ErrorInfo{Type: kind, Message: err.Error()}}}})
	}
	if req.CheckoutPath == "" || !validCommitSHA(req.CommitSha) {
		return sendErr(INVALID_REQUEST, errors.New("captured checkout and SHA-1 commit required"))
	}
	if !checkoutHasGitEntry(req.CheckoutPath) {
		return sendErr(HISTORY_UNAVAILABLE, errors.New("captured checkout unavailable"))
	}
	tmp, err := os.MkdirTemp("", "dagger-pack-commit-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	pack, err := packCapturedCommit(ctx, req.CheckoutPath, tmp, req.CommitSha)
	if errors.Is(err, errHostHistoryUnavailable) {
		return sendErr(HISTORY_UNAVAILABLE, err)
	}
	if err != nil {
		return sendErr(PACK_FAILED, err)
	}
	if err := srv.Send(&PackCheckoutResponse{Msg: &PackCheckoutResponse_Metadata{Metadata: &PackCheckoutMetadata{HeadSha: req.CommitSha, ObjectFormat: "sha1"}}}); err != nil {
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
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Borrow only the object database, never refs, replacement refs, config or the
// donor's shallow boundaries. All writable Git metadata lives in scratch. The
// pack is always the commit's complete closure, so a shallow donor never
// qualifies.
func packCapturedCommit(ctx context.Context, checkout, scratch, sha string) (string, error) {
	objects, err := runHostGit(ctx, checkout, "rev-parse", "--path-format=absolute", "--git-path", "objects")
	if err != nil {
		return "", fmt.Errorf("%w: object database", errHostHistoryUnavailable)
	}
	shallow, err := runHostGit(ctx, checkout, "rev-parse", "--is-shallow-repository")
	if err != nil || strings.TrimSpace(shallow) != "false" {
		return "", fmt.Errorf("%w: shallow donor", errHostHistoryUnavailable)
	}
	format, err := runHostGit(ctx, checkout, "rev-parse", "--show-object-format")
	if err != nil || strings.TrimSpace(format) != "sha1" {
		return "", fmt.Errorf("%w: unsupported object format", errHostHistoryUnavailable)
	}
	if _, err := runIsolatedHostGit(ctx, scratch, nil, nil, "init", "--bare", "--template=", "--object-format=sha1"); err != nil {
		return "", err
	}
	env := []string{"GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_ALTERNATE_OBJECT_DIRECTORIES=" + strconv.Quote(strings.TrimSpace(objects))}
	// Probe the anchor cheaply in the isolated repository with lazy fetching
	// disabled: a missing commit is a normal optional-donor miss.
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
	// The scratch repository is no partial clone and lazy fetching is off, so
	// pack-objects fails outright on any missing ancestor, tree or blob rather
	// than writing a partial pack. Detecting that up front would walk the whole
	// history a second time for nothing: the engine treats every failure as a
	// miss.
	prefix := filepath.Join(scratch, "objects", "pack", "pack")
	hash, err := runIsolatedHostGit(ctx, scratch, env, strings.NewReader(sha+"\n"), "pack-objects", "--revs", "--missing=error", "--delta-base-offset", prefix)
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
