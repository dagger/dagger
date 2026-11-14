package git

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dagger/dagger/util/gitutil"
)

func (s GitAttachable) SnapshotState(ctx context.Context, req *SnapshotRequest) (*CheckoutStateResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, gitPackTimeout)
	defer cancel()
	checkout := req.GetCheckoutPath()
	if checkout == "" {
		return newCheckoutStateErrorResponse(INVALID_REQUEST, "checkout path is required"), nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		return newCheckoutStateErrorResponse(NOT_FOUND, gitMissingMessage), nil
	}
	if !checkoutHasGitEntry(checkout) {
		return newCheckoutStateErrorResponse(NOT_A_REPO, "no .git entry at checkout root"), nil
	}
	unlock, err := gitCheckoutLocks.lock(ctx, checkout)
	if err != nil {
		return newCheckoutStateErrorResponse(TIMEOUT, err.Error()), nil
	}
	defer unlock()
	state, err := workspaceSnapshotState(ctx, checkout)
	if err != nil {
		return newCheckoutStateErrorResponse(PACK_FAILED, err.Error()), nil
	}
	return &CheckoutStateResponse{Result: &CheckoutStateResponse_StateDigest{StateDigest: state}}, nil
}

// Snapshot streams a git bundle containing one synthetic commit whose tree
// represents the checkout's current working directory. When the checkout has
// an upstream, that exact remote commit is the bundle prerequisite, so only
// local commits, changes, and untracked content are transferred.
func (s GitAttachable) Snapshot(req *SnapshotRequest, srv Git_SnapshotServer) error {
	ctx, cancel := context.WithTimeout(srv.Context(), gitPackTimeout)
	defer cancel()

	sendErr := func(errorType ErrorInfo_ErrorType, message string) error {
		return srv.Send(&SnapshotResponse{Msg: &SnapshotResponse_Metadata{Metadata: &SnapshotMetadata{
			Error: &ErrorInfo{Type: errorType, Message: message},
		}}})
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

	unlock, err := gitCheckoutLocks.lock(ctx, checkout)
	if err != nil {
		return sendErr(TIMEOUT, err.Error())
	}
	defer unlock()

	meta, bundlePath, cleanup, err := createWorkspaceSnapshotBundle(ctx, checkout)
	if err != nil {
		return sendErr(PACK_FAILED, err.Error())
	}
	defer cleanup()
	if err := srv.Send(&SnapshotResponse{Msg: &SnapshotResponse_Metadata{Metadata: meta}}); err != nil {
		return fmt.Errorf("send snapshot metadata: %w", err)
	}

	bundle, err := os.Open(bundlePath)
	if err != nil {
		return fmt.Errorf("open snapshot bundle: %w", err)
	}
	defer bundle.Close()
	buf := make([]byte, packCheckoutChunkSize)
	for {
		n, readErr := bundle.Read(buf)
		if n > 0 {
			if err := srv.Send(&SnapshotResponse{Msg: &SnapshotResponse_Chunk{Chunk: buf[:n]}}); err != nil {
				return fmt.Errorf("send snapshot bundle chunk: %w", err)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return fmt.Errorf("read snapshot bundle: %w", readErr)
		}
	}
}

// SnapshotUploadPack serves the same synthetic workspace commit as Snapshot,
// but lets the engine negotiate missing objects with git-upload-pack instead
// of receiving a precomputed bundle.
func (s GitAttachable) SnapshotUploadPack(srv Git_SnapshotUploadPackServer) error {
	ctx, cancel := context.WithTimeout(srv.Context(), gitPackTimeout)
	defer cancel()

	first, err := srv.Recv()
	if err != nil {
		return fmt.Errorf("receive workspace upload-pack request: %w", err)
	}
	open := first.GetOpen()
	sendErr := func(errorType ErrorInfo_ErrorType, message string) error {
		return srv.Send(&SnapshotUploadPackResponse{Msg: &SnapshotUploadPackResponse_Metadata{Metadata: &SnapshotMetadata{
			Error: &ErrorInfo{Type: errorType, Message: message},
		}}})
	}
	if open == nil || open.GetCheckoutPath() == "" {
		return sendErr(INVALID_REQUEST, "checkout path is required")
	}
	checkout := open.GetCheckoutPath()
	if _, err := exec.LookPath("git"); err != nil {
		return sendErr(NOT_FOUND, gitMissingMessage)
	}
	if !checkoutHasGitEntry(checkout) {
		return sendErr(NOT_A_REPO, "no .git entry at checkout root")
	}

	unlock, err := gitCheckoutLocks.lock(ctx, checkout)
	if err != nil {
		return sendErr(TIMEOUT, err.Error())
	}
	defer unlock()

	meta, gitDir, cleanup, err := createWorkspaceSnapshotRepository(ctx, checkout)
	if err != nil {
		return sendErr(PACK_FAILED, err.Error())
	}
	defer cleanup()
	if err := srv.Send(&SnapshotUploadPackResponse{Msg: &SnapshotUploadPackResponse_Metadata{Metadata: meta}}); err != nil {
		return fmt.Errorf("send workspace upload-pack metadata: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", "upload-pack", "--strict", gitDir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open upload-pack stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open upload-pack stdout: %w", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start upload-pack: %w", err)
	}

	var once sync.Once
	closeInput := func() { once.Do(func() { _ = stdin.Close() }) }
	recvDone := make(chan error, 1)
	go func() {
		defer closeInput()
		for {
			req, recvErr := srv.Recv()
			if errors.Is(recvErr, io.EOF) {
				recvDone <- nil
				return
			}
			if recvErr != nil {
				recvDone <- recvErr
				return
			}
			chunk := req.GetChunk()
			if len(chunk) == 0 {
				continue
			}
			if _, writeErr := stdin.Write(chunk); writeErr != nil {
				recvDone <- writeErr
				return
			}
		}
	}()

	buf := make([]byte, packCheckoutChunkSize)
	for {
		n, readErr := stdout.Read(buf)
		if n > 0 {
			if err := srv.Send(&SnapshotUploadPackResponse{Msg: &SnapshotUploadPackResponse_Chunk{Chunk: buf[:n]}}); err != nil {
				closeInput()
				_ = cmd.Wait()
				return fmt.Errorf("send upload-pack chunk: %w", err)
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				closeInput()
				_ = cmd.Wait()
				return fmt.Errorf("read upload-pack output: %w", readErr)
			}
			break
		}
	}
	closeInput()
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("upload-pack failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	select {
	case err := <-recvDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("receive upload-pack input: %w", err)
		}
	default:
	}
	return nil
}

func createWorkspaceSnapshotBundle(ctx context.Context, checkout string) (*SnapshotMetadata, string, func(), error) {
	meta, gitDir, cleanup, err := createWorkspaceSnapshotRepository(ctx, checkout)
	if err != nil {
		return nil, "", func() {}, err
	}
	bundlePath := filepath.Join(filepath.Dir(gitDir), "snapshot.bundle")
	env := []string{"GIT_DIR=" + gitDir}
	bundleArgs := []string{"bundle", "create", bundlePath, "refs/dagger/workspace"}
	if meta.BaseSha != "" {
		bundleArgs = append(bundleArgs, "^"+meta.BaseSha)
	}
	if _, err := runSnapshotGit(ctx, checkout, env, bundleArgs...); err != nil {
		cleanup()
		return nil, "", func() {}, fmt.Errorf("bundle workspace snapshot: %w", err)
	}
	info, err := os.Stat(bundlePath)
	if err != nil {
		cleanup()
		return nil, "", func() {}, fmt.Errorf("stat workspace snapshot bundle: %w", err)
	}
	if info.Size() > MaxGitPackBytes {
		cleanup()
		return nil, "", func() {}, fmt.Errorf("workspace snapshot bundle size %d exceeds limit %d", info.Size(), MaxGitPackBytes)
	}
	return meta, bundlePath, cleanup, nil
}

func createWorkspaceSnapshotRepository(ctx context.Context, checkout string) (*SnapshotMetadata, string, func(), error) {
	objectFormat := "sha1"
	if out, err := runHostGit(ctx, checkout, "rev-parse", "--show-object-format"); err == nil {
		objectFormat = strings.TrimSpace(out)
	}
	commonDir, err := runHostGit(ctx, checkout, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, "", func() {}, err
	}
	realObjects := filepath.Join(strings.TrimSpace(commonDir), "objects")
	base := detectWorkspaceSnapshotBase(ctx, checkout)

	tmpDir, err := os.MkdirTemp("", "dagger-workspace-snapshot-*")
	if err != nil {
		return nil, "", func() {}, fmt.Errorf("create snapshot temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	gitDir := filepath.Join(tmpDir, "repo.git")

	initArgs := []string{"init", "-q", "--bare"}
	if objectFormat != "sha1" {
		initArgs = append(initArgs, "--object-format="+objectFormat)
	}
	initArgs = append(initArgs, gitDir)
	if _, err := runSnapshotGit(ctx, checkout, nil, initArgs...); err != nil {
		cleanup()
		return nil, "", func() {}, fmt.Errorf("initialize snapshot repository: %w", err)
	}

	env := []string{
		"GIT_DIR=" + gitDir,
		"GIT_WORK_TREE=" + checkout,
		"GIT_INDEX_FILE=" + filepath.Join(tmpDir, "index"),
		"GIT_OBJECT_DIRECTORY=" + filepath.Join(gitDir, "objects"),
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + realObjects,
		"GIT_AUTHOR_NAME=Dagger Workspace Snapshot",
		"GIT_AUTHOR_EMAIL=snapshot@dagger.io",
		"GIT_COMMITTER_NAME=Dagger Workspace Snapshot",
		"GIT_COMMITTER_EMAIL=snapshot@dagger.io",
		"GIT_AUTHOR_DATE=@0 +0000",
		"GIT_COMMITTER_DATE=@0 +0000",
	}
	if _, err := runSnapshotGit(ctx, checkout, env, "add", "-A", "-f", "--", ".", ":(exclude).git"); err != nil {
		cleanup()
		return nil, "", func() {}, fmt.Errorf("index workspace snapshot: %w", err)
	}
	tree, err := runSnapshotGit(ctx, checkout, env, "write-tree")
	if err != nil {
		cleanup()
		return nil, "", func() {}, fmt.Errorf("write workspace snapshot tree: %w", err)
	}
	commitArgs := []string{"commit-tree", strings.TrimSpace(tree)}
	if base.sha != "" {
		commitArgs = append(commitArgs, "-p", base.sha)
	}
	commit, err := runSnapshotGitInput(ctx, checkout, env, strings.NewReader("Dagger workspace snapshot\n"), commitArgs...)
	if err != nil {
		cleanup()
		return nil, "", func() {}, fmt.Errorf("commit workspace snapshot: %w", err)
	}
	commit = strings.TrimSpace(commit)
	if _, err := runSnapshotGit(ctx, checkout, env, "update-ref", "refs/dagger/workspace", commit); err != nil {
		cleanup()
		return nil, "", func() {}, fmt.Errorf("reference workspace snapshot: %w", err)
	}
	infoDir := filepath.Join(gitDir, "objects", "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		cleanup()
		return nil, "", func() {}, fmt.Errorf("create snapshot alternates directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(infoDir, "alternates"), []byte(realObjects+"\n"), 0o644); err != nil {
		cleanup()
		return nil, "", func() {}, fmt.Errorf("write snapshot alternates: %w", err)
	}
	return &SnapshotMetadata{
		CommitSha: commit, ObjectFormat: objectFormat,
		RemoteUrl: base.remoteURL, BaseSha: base.sha, BaseRef: base.ref,
	}, gitDir, cleanup, nil
}

type workspaceSnapshotBase struct {
	remoteURL string
	sha       string
	ref       string
}

func workspaceSnapshotState(ctx context.Context, checkout string) (string, error) {
	commonDir, err := runHostGit(ctx, checkout, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	tmpDir, err := os.MkdirTemp("", "dagger-workspace-state-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)
	gitDir := filepath.Join(tmpDir, "repo.git")
	if _, err := runSnapshotGit(ctx, checkout, nil, "init", "-q", "--bare", gitDir); err != nil {
		return "", err
	}
	env := []string{
		"GIT_DIR=" + gitDir,
		"GIT_WORK_TREE=" + checkout,
		"GIT_INDEX_FILE=" + filepath.Join(tmpDir, "index"),
		"GIT_OBJECT_DIRECTORY=" + filepath.Join(gitDir, "objects"),
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + filepath.Join(strings.TrimSpace(commonDir), "objects"),
	}
	if _, err := runSnapshotGit(ctx, checkout, env, "add", "-A", "-f", "--", ".", ":(exclude).git"); err != nil {
		return "", err
	}
	tree, err := runSnapshotGit(ctx, checkout, env, "write-tree")
	if err != nil {
		return "", err
	}
	base := detectWorkspaceSnapshotBase(ctx, checkout)
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(tree), base.remoteURL, base.sha, base.ref,
	}, "\x00")))), nil
}

func detectWorkspaceSnapshotBase(ctx context.Context, checkout string) workspaceSnapshotBase {
	upstream, err := runHostGit(ctx, checkout, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err == nil {
		name := strings.TrimSpace(upstream)
		if remote, branch, ok := strings.Cut(name, "/"); ok && remote != "." && branch != "" {
			sha, shaErr := runHostGit(ctx, checkout, "rev-parse", "--verify", name+"^{commit}")
			url, urlErr := runHostGit(ctx, checkout, "config", "--get", "remote."+remote+".url")
			remoteURL := strings.TrimSpace(url)
			if shaErr == nil && urlErr == nil && gitutil.IsGitTransport(remoteURL) {
				return workspaceSnapshotBase{remoteURL, strings.TrimSpace(sha), "refs/heads/" + branch}
			}
		}
	}

	refs, err := runHostGit(ctx, checkout, "for-each-ref", "--format=%(refname)", "--points-at", "HEAD", "refs/remotes")
	if err != nil {
		return workspaceSnapshotBase{}
	}
	for _, ref := range strings.Split(refs, "\n") {
		parts := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(ref), "refs/remotes/"), "/", 2)
		if len(parts) != 2 || parts[1] == "HEAD" {
			continue
		}
		url, urlErr := runHostGit(ctx, checkout, "config", "--get", "remote."+parts[0]+".url")
		sha, shaErr := runHostGit(ctx, checkout, "rev-parse", "--verify", ref+"^{commit}")
		remoteURL := strings.TrimSpace(url)
		if urlErr == nil && shaErr == nil && gitutil.IsGitTransport(remoteURL) {
			return workspaceSnapshotBase{remoteURL, strings.TrimSpace(sha), "refs/heads/" + parts[1]}
		}
	}
	return workspaceSnapshotBase{}
}

func runSnapshotGit(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	return runSnapshotGitInput(ctx, dir, env, nil, args...)
}

func runSnapshotGitInput(ctx context.Context, dir string, env []string, stdin io.Reader, args ...string) (string, error) {
	out, err := runHostGitBytes(ctx, dir, env, stdin, args...)
	return string(out), err
}
