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
	"sort"
	"strings"
	"syscall"
)

// PackWorktree streams the checkout's git-visible worktree delta relative to
// HEAD as a binary patch. A temporary index starts at HEAD and is populated
// only with paths that git itself reports as changed or ordinary untracked;
// ignored paths and untracked nested repositories consequently stay out.
func (s GitAttachable) PackWorktree(req *PackWorktreeRequest, srv Git_PackWorktreeServer) error {
	ctx, cancel := context.WithTimeout(srv.Context(), gitPackTimeout)
	defer cancel()

	sendErr := func(errorType ErrorInfo_ErrorType, message string) error {
		return srv.Send(&PackWorktreeResponse{
			Msg: &PackWorktreeResponse_Metadata{
				Metadata: &PackWorktreeMetadata{
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
		// There is no canonical HEAD tree on which the engine can apply a
		// patch. The engine falls back to the existing directory transport.
		return sendErr(WORKTREE_UNSUPPORTED, "cannot pack an unborn worktree")
	}
	expectedHead := req.GetExpectedHeadSha()
	if expectedHead == "" {
		return sendErr(INVALID_REQUEST, "expected HEAD is required")
	}
	if state.headSHA != expectedHead {
		return sendErr(HEAD_MISMATCH, fmt.Sprintf("checkout HEAD moved from %s to %s", expectedHead, state.headSHA))
	}

	trackedOut, err := runHostGitBytes(ctx, checkout, nil, nil,
		"diff", "--name-only", "-z", "--no-renames", "--no-ext-diff", expectedHead, "--")
	if err != nil {
		return sendErr(PACK_FAILED, err.Error())
	}
	untrackedOut, err := runHostGitBytes(ctx, checkout, nil, nil,
		"ls-files", "--others", "--exclude-standard", "-z", "--")
	if err != nil {
		return sendErr(PACK_FAILED, err.Error())
	}

	trackedPaths := splitNullPaths(trackedOut)
	tracked := make(map[string]struct{}, len(trackedPaths))
	paths := map[string]struct{}{}
	for _, p := range trackedPaths {
		tracked[p] = struct{}{}
		paths[p] = struct{}{}
	}
	var nested []string
	for _, p := range splitNullPaths(untrackedOut) {
		if strings.HasSuffix(p, "/") {
			// With --others, git reports an untracked embedded repository as
			// one directory entry even without --directory. Do not add its
			// contents to the outer patch; preserve only the boundary.
			nested = append(nested, strings.TrimSuffix(p, "/"))
			continue
		}
		paths[p] = struct{}{}
	}
	sort.Strings(nested)

	tmpDir, err := os.MkdirTemp("", "dagger-pack-worktree")
	if err != nil {
		return fmt.Errorf("create worktree pack temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	indexPath := filepath.Join(tmpDir, "index")
	env := []string{"GIT_INDEX_FILE=" + indexPath}

	if _, err := runHostGitBytes(ctx, checkout, env, nil, "read-tree", expectedHead); err != nil {
		return sendErr(PACK_FAILED, err.Error())
	}
	if len(trackedPaths) > 0 {
		var input bytes.Buffer
		for _, p := range trackedPaths {
			input.WriteString(p)
			input.WriteByte(0)
		}
		// Remove every changed tracked path first, then add back only what
		// exists in the final worktree. Two phases are required for file ↔
		// directory type changes, where both forms cannot coexist in an index.
		if _, err := runHostGitBytes(ctx, checkout, env, &input,
			"update-index", "--force-remove", "-z", "--stdin"); err != nil {
			return sendErr(PACK_FAILED, err.Error())
		}
	}
	if len(paths) > 0 {
		sorted := make([]string, 0, len(paths))
		for p := range paths {
			info, err := os.Lstat(filepath.Join(checkout, filepath.FromSlash(p)))
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
				continue
			}
			if err != nil {
				return sendErr(PACK_FAILED, fmt.Sprintf("inspect changed path %q: %v", p, err))
			}
			if info.IsDir() {
				if _, isTracked := tracked[p]; isTracked && checkoutHasGitEntry(filepath.Join(checkout, filepath.FromSlash(p))) {
					// A changed checked-out submodule needs its gitlink/index
					// semantics preserved; a filesystem patch cannot encode it.
					return sendErr(WORKTREE_UNSUPPORTED, fmt.Sprintf("changed submodule at %q", p))
				}
				continue
			}
			sorted = append(sorted, p)
		}
		sort.Strings(sorted)
		var input bytes.Buffer
		for _, p := range sorted {
			input.WriteString(p)
			input.WriteByte(0)
		}
		if input.Len() > 0 {
			if _, err := runHostGitBytes(ctx, checkout, env, &input,
				"update-index", "--add", "--replace", "-z", "--stdin"); err != nil {
				return sendErr(PACK_FAILED, err.Error())
			}
		}
	}

	cmd := hostGitCommand(ctx, checkout, env,
		"diff", "--cached", "--binary", "--full-index", "--no-renames", "--no-ext-diff", expectedHead, "--")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open worktree patch stream: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return sendErr(PACK_FAILED, fmt.Sprintf("start worktree patch: %v", err))
	}

	if err := srv.Send(&PackWorktreeResponse{
		Msg: &PackWorktreeResponse_Metadata{
			Metadata: &PackWorktreeMetadata{
				HeadSha:            state.headSHA,
				NestedRepositories: nested,
			},
		},
	}); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("send worktree metadata: %w", err)
	}

	buf := make([]byte, packCheckoutChunkSize)
	for {
		n, readErr := stdout.Read(buf)
		if n > 0 {
			if err := srv.Send(&PackWorktreeResponse{
				Msg: &PackWorktreeResponse_Chunk{Chunk: buf[:n]},
			}); err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return fmt.Errorf("send worktree patch chunk: %w", err)
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return fmt.Errorf("read worktree patch: %w", readErr)
			}
			break
		}
	}
	if err := cmd.Wait(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			return fmt.Errorf("git diff worktree: %w", err)
		}
		return fmt.Errorf("git diff worktree: %w: %s", err, detail)
	}
	return nil
}

func splitNullPaths(out []byte) []string {
	parts := bytes.Split(out, []byte{0})
	paths := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) > 0 {
			paths = append(paths, string(p))
		}
	}
	return paths
}

func hostGitCommand(ctx context.Context, dir string, extraEnv []string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), append([]string{
		"GIT_TERMINAL_PROMPT=0",
		"SSH_ASKPASS=echo",
	}, extraEnv...)...)
	return cmd
}

func runHostGitBytes(ctx context.Context, dir string, extraEnv []string, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := hostGitCommand(ctx, dir, extraEnv, args...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("git %s timed out", strings.Join(args, " "))
		}
		detail := strings.TrimSpace(stderr.String() + stdout.String())
		if detail == "" {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, detail)
	}
	return stdout.Bytes(), nil
}
