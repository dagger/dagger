package git

import (
	"bufio"
	"bytes"
	"context"
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

// ApplyBundle spools a bounded stream before touching the checkout. Git itself
// interprets its layout, including linked worktrees and separate Git dirs.
func (s GitAttachable) ApplyBundle(srv Git_ApplyBundleServer) error {
	ctx, cancel := context.WithTimeout(srv.Context(), 5*time.Minute)
	defer cancel()
	tmp, err := os.CreateTemp("", "dagger-export-*.bundle")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	var metadata *ApplyBundleMetadata
	var size int64
	for {
		req, err := srv.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		switch msg := req.GetMsg().(type) {
		case *ApplyBundleRequest_Metadata:
			if metadata != nil || msg.Metadata == nil {
				return srv.SendAndClose(applyBundleError("expected exactly one metadata message"))
			}
			metadata = msg.Metadata
		case *ApplyBundleRequest_Chunk:
			if metadata == nil {
				return srv.SendAndClose(applyBundleError("received bundle bytes before metadata"))
			}
			size += int64(len(msg.Chunk))
			if size > MaxGitPackBytes {
				return srv.SendAndClose(applyBundleError("git export bundle exceeds size limit"))
			}
			if _, err := tmp.Write(msg.Chunk); err != nil {
				return err
			}
		default:
			return srv.SendAndClose(applyBundleError("unknown bundle message"))
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return srv.SendAndClose(applyBundle(ctx, metadata, tmp.Name(), size))
}

func applyBundleError(message string) *ApplyBundleResponse {
	return &ApplyBundleResponse{Error: &ErrorInfo{Type: INVALID_REQUEST, Message: message}}
}

func validExportSHA(sha string) bool {
	if len(sha) != 40 && len(sha) != 64 {
		return false
	}
	_, err := hex.DecodeString(sha)
	return err == nil && sha == strings.ToLower(sha)
}

func applyBundle(ctx context.Context, meta *ApplyBundleMetadata, bundlePath string, size int64) *ApplyBundleResponse {
	if meta == nil || meta.CheckoutPath == "" || !validExportSHA(meta.TargetSha) || meta.ExpectedStateDigest == "" {
		return applyBundleError("checkout path, full target commit SHA and expected ref state are required")
	}
	checkout := filepath.Clean(meta.CheckoutPath)
	if !checkoutHasGitEntry(checkout) {
		return applyBundleError("workspace export requires a local Git checkout")
	}
	unlock, err := gitCheckoutLocks.lock(ctx, checkout)
	if err != nil {
		return applyBundleError(err.Error())
	}
	defer unlock()
	state, err := collectCheckoutState(ctx, checkout)
	if err != nil {
		return applyBundleError(err.Error())
	}
	if size > 0 {
		if meta.BundleRef != "HEAD" {
			if !strings.HasPrefix(meta.BundleRef, "refs/") {
				return applyBundleError("invalid export bundle ref")
			}
			if _, err := runExportGit(ctx, checkout, "check-ref-format", meta.BundleRef); err != nil {
				return applyBundleError(err.Error())
			}
		}
		// Verify the advertised ref before importing anything. Fetch only that
		// ref, without updating FETCH_HEAD, tags, remotes or submodules.
		out, err := runExportGit(ctx, checkout, "bundle", "list-heads", bundlePath, meta.BundleRef)
		if err != nil {
			return applyBundleError(err.Error())
		}
		if strings.TrimSpace(out) != meta.TargetSha+" "+meta.BundleRef {
			return applyBundleError("export bundle ref does not match the requested commit")
		}
		if _, err := runExportGit(ctx, checkout, "fetch", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", "--no-auto-maintenance", bundlePath, meta.BundleRef); err != nil {
			return applyBundleError(err.Error())
		}
	}
	if _, err := runExportGit(ctx, checkout, "cat-file", "-e", meta.TargetSha+"^{commit}"); err != nil {
		return applyBundleError(err.Error())
	}
	// Keep a recovery ref before any worktree mutation, including on a failed
	// lease. Never overwrite an existing hand-off, even a short-SHA collision.
	parked, created, err := parkExportCommit(ctx, checkout, meta.TargetSha)
	if err != nil {
		return applyBundleError(err.Error())
	}
	park := func(err error) *ApplyBundleResponse {
		return &ApplyBundleResponse{HeadSha: state.headSHA, ParkedRef: parked,
			Error: &ErrorInfo{Type: UNKNOWN, Message: fmt.Sprintf("cannot export: %v; commits left on %s — integrate with ordinary git, then retry", err, parked)}}
	}
	if state.digest() != meta.ExpectedStateDigest {
		return park(errors.New("checkout refs changed while preparing export"))
	}
	if state.headSHA == "" {
		return park(errors.New("destination HEAD is unborn"))
	}
	if state.headSHA != meta.TargetSha {
		if _, err := runExportGit(ctx, checkout, "merge-base", "--is-ancestor", state.headSHA, meta.TargetSha); err != nil {
			return park(fmt.Errorf("checkout HEAD %s is not an ancestor of %s", state.headSHA, meta.TargetSha))
		}
	}
	if err := fastForwardExport(ctx, checkout, state, meta.TargetSha); err != nil {
		return park(err)
	}
	if created {
		// Removing our temporary recovery ref is best-effort. Its presence is
		// harmless, and cleanup must not turn a completed export into a failure.
		_, _ = runExportGit(ctx, checkout, "update-ref", "--no-deref", "-d", parked, meta.TargetSha)
	}
	return &ApplyBundleResponse{HeadSha: meta.TargetSha}
}

func parkExportCommit(ctx context.Context, checkout, sha string) (string, bool, error) {
	for _, suffix := range []string{sha[:12], sha} {
		ref := "refs/dagger/checkpoints/" + suffix
		if _, err := runExportGit(ctx, checkout, "symbolic-ref", "-q", ref); err == nil {
			continue // Never follow or replace a user-created symbolic hand-off.
		}
		out, err := runExportGit(ctx, checkout, "rev-parse", "--verify", ref)
		if err == nil {
			if strings.TrimSpace(out) == sha {
				return ref, false, nil
			}
			continue
		}
		if _, err := runExportGit(ctx, checkout, "update-ref", "--no-deref", ref, sha, strings.Repeat("0", len(sha))); err != nil {
			return "", false, fmt.Errorf("retain export commits: %w", err)
		}
		return ref, true, nil
	}
	return "", false, errors.New("export hand-off refs already name different commits")
}

// Use Git's two-tree fast-forward machinery under a prepared ref transaction.
// Unlike checking HEAD and then invoking merge, prepare holds the HEAD and
// branch locks *before* updating the worktree. The old SHA is a real ref lease.
// read-tree -m -u preserves unrelated staged/unstaged changes and refuses
// overlapping edits and untracked obstructions. No staging, reset or rebase is
// used to make a dirty checkout acceptable. Hooks and recursive submodule
// updates are deliberately disabled.
func fastForwardExport(ctx context.Context, checkout string, state checkoutState, target string) error {
	gitDir, err := runExportGit(ctx, checkout, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return err
	}
	for _, marker := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer"} {
		if _, err := os.Lstat(filepath.Join(strings.TrimSpace(gitDir), marker)); err == nil {
			return fmt.Errorf("checkout has an in-progress Git operation (%s)", marker)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	cmd := exportGitCommand(ctx, checkout, "update-ref", "-m", "dagger export: fast-forward", "--stdin")
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		in.Close() // EOF aborts a prepared, uncommitted transaction.
		_ = cmd.Wait()
	}()
	reader := bufio.NewReader(out)
	command := func(input, expected string) error {
		if _, err := io.WriteString(in, input+"\n"); err != nil {
			return err
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			// Wait before reading stderr: os/exec copies it concurrently.
			_ = cmd.Wait()
			return fmt.Errorf("git ref lease: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		if strings.TrimSpace(line) != expected {
			return fmt.Errorf("unexpected git ref transaction response: %q", line)
		}
		return nil
	}
	if err := command("start", "start: ok"); err != nil {
		return err
	}
	if err := command(fmt.Sprintf("update HEAD %s %s\nprepare", target, state.headSHA), "prepare: ok"); err != nil {
		return err
	}
	// HEAD could have switched branches before prepare acquired its locks.
	actualRef, _ := runExportGit(ctx, checkout, "symbolic-ref", "-q", "HEAD")
	if strings.TrimSpace(actualRef) != state.headRef {
		return errors.New("checkout branch changed while preparing export")
	}
	if state.headSHA != target {
		if err := checkExportUntrackedPaths(ctx, checkout, state.headSHA, target); err != nil {
			return err
		}
		if _, err := runExportGit(ctx, checkout, "read-tree", "--no-recurse-submodules", "-m", "-u", state.headSHA, target); err != nil {
			return fmt.Errorf("fast-forward refused: %w", err)
		}
	}
	return command("commit", "commit: ok")
}

// Git's default fast-forward may overwrite ignored files. Treat those as user
// data too, including files beneath an untracked directory being replaced.
func checkExportUntrackedPaths(ctx context.Context, checkout, base, target string) error {
	changed, err := runExportGit(ctx, checkout, "diff", "--no-ext-diff", "--no-renames", "--name-only", "-z", base, target, "--")
	if err != nil {
		return err
	}
	untracked, err := runExportGit(ctx, checkout, "ls-files", "--others", "-z")
	if err != nil {
		return err
	}
	incoming := map[string]bool{}
	parents := map[string]bool{}
	for _, p := range strings.Split(changed, "\x00") {
		if p == "" {
			continue
		}
		incoming[p] = true
		for i := strings.LastIndexByte(p, '/'); i >= 0; i = strings.LastIndexByte(p, '/') {
			p = p[:i]
			parents[p] = true
		}
	}
	for _, p := range strings.Split(untracked, "\x00") {
		if p == "" {
			continue
		}
		if parents[p] {
			return fmt.Errorf("untracked path %q would be overwritten by fast-forward", p)
		}
		for ancestor := p; ancestor != ""; {
			if incoming[ancestor] {
				return fmt.Errorf("untracked path %q would be overwritten by fast-forward", p)
			}
			i := strings.LastIndexByte(ancestor, '/')
			if i < 0 {
				break
			}
			ancestor = ancestor[:i]
		}
	}
	return nil
}

func exportGitCommand(ctx context.Context, checkout string, args ...string) *exec.Cmd {
	base := []string{"-C", checkout, "--no-replace-objects", "-c", "core.hooksPath=" + os.DevNull, "-c", "submodule.recurse=false"}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_REFLOG_ACTION=dagger export")
	return cmd
}

func runExportGit(ctx context.Context, checkout string, args ...string) (string, error) {
	cmd := exportGitCommand(ctx, checkout, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
