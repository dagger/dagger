package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// applyIntegrationWorktree runs under both the checkout mutex and prepared
// HEAD/branch ref transaction. Prepare and check everything before changing
// files; hold index.lock through installing the prepared index. A second Dagger
// save therefore sees either the old or new checkout, never the intermediate.
func applyIntegrationWorktree(ctx context.Context, checkout, base, target, after, indexPath string) error {
	before, err := validateIntegrationParents(ctx, checkout, base, target, after)
	if err != nil {
		return err
	}
	// Keep the prepared index on the same filesystem for atomic rename.
	tmp, err := os.MkdirTemp(filepath.Dir(indexPath), "dagger-integration-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	index := filepath.Join(tmp, "index")
	run := func(input string, args ...string) (string, error) {
		cmd := exportGitCommand(ctx, checkout, args...)
		cmd.Env = append(cmd.Env, "GIT_INDEX_FILE="+index, "GIT_LITERAL_PATHSPECS=1")
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("prepare integration: %w: %s", err, out)
		}
		return string(out), nil
	}
	committed, err := runExportGit(ctx, checkout, "diff", "--no-ext-diff", "--no-renames", "--name-only", "-z", base, target, "--")
	if err != nil {
		return err
	}
	changed, err := runExportGit(ctx, checkout, "diff", "--no-ext-diff", "--no-renames", "--name-only", "-z", before, after, "--")
	if err != nil {
		return err
	}
	touched := integrationPaths(committed + changed)
	if _, err := run("", "read-tree", before); err != nil {
		return err
	}
	modified, err := run("", "diff", "--no-ext-diff", "--name-only", "-z", "--")
	if err != nil {
		return err
	}
	for _, path := range integrationPaths(modified) {
		if integrationOverlaps(path, touched) {
			return fmt.Errorf("checkout path %q changed since integration was prepared", path)
		}
	}
	// Include ignored obstructions too. Paths present in the approved before
	// snapshot are tracked in this temporary index, even if untracked on disk.
	untracked, err := run("", "ls-files", "--others", "-z")
	if err != nil {
		return err
	}
	for _, path := range integrationPaths(untracked) {
		if integrationOverlaps(path, touched) {
			return fmt.Errorf("untracked path %q would be overwritten by integration", path)
		}
	}
	patch, err := runExportGit(ctx, checkout, "diff", "--no-ext-diff", "--no-textconv", "--binary", "--full-index", "--no-renames", before, after, "--")
	if err != nil {
		return err
	}
	// Use the real index as the starting point for the resulting staging state.
	// Only paths changed by committed history are advanced to the new HEAD.
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(index, data, 0o600); err != nil {
		return err
	}
	staged, err := run("", "diff", "--cached", "--name-only", "-z", base, "--")
	if err != nil {
		return err
	}
	notCaptured, err := run("", "diff", "--cached", "--name-only", "-z", before, "--")
	if err != nil {
		return err
	}
	for _, path := range integrationPaths(staged) {
		if integrationOverlaps(path, integrationPaths(committed)) && integrationOverlaps(path, integrationPaths(notCaptured)) {
			return fmt.Errorf("staged path %q differs from the captured worktree", path)
		}
	}
	// NUL-delimited pathspecs avoid argv limits and pathspec interpretation.
	if committed != "" {
		if _, err := run(committed, "reset", "-q", target, "--pathspec-from-file=-", "--pathspec-file-nul"); err != nil {
			return err
		}
	}
	if _, err := run("", "update-index", "--no-split-index"); err != nil {
		return err
	}
	if patch != "" {
		if _, err := run(patch, "apply", "--check", "--whitespace=nowarn", "-"); err != nil {
			return err
		}
		if _, err := run(patch, "apply", "--whitespace=nowarn", "-"); err != nil {
			return err
		}
	}
	return os.Rename(index, indexPath)
}

func validateIntegrationParents(ctx context.Context, checkout, base, target, after string) (string, error) {
	parents, err := runExportGit(ctx, checkout, "show", "-s", "--format=%P", after)
	if err != nil {
		return "", err
	}
	p := strings.Fields(parents)
	if len(p) != 2 || p[0] != target {
		return "", fmt.Errorf("invalid integration transport parents")
	}
	before := p[1]
	parents, err = runExportGit(ctx, checkout, "show", "-s", "--format=%P", before)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(parents) != base {
		return "", fmt.Errorf("checkout HEAD changed since integration was prepared")
	}
	return before, nil
}

// Keep index.lock until the ref transaction commits too, not just until the
// prepared index is installed. Never delete a lock created by another writer.
func lockIntegrationIndex(ctx context.Context, checkout string) (string, func(), error) {
	indexPath, err := runExportGit(ctx, checkout, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return "", nil, err
	}
	indexPath = strings.TrimSpace(indexPath)
	lock, err := os.OpenFile(indexPath+".lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return "", nil, fmt.Errorf("lock checkout index: %w", err)
	}
	return indexPath, func() { lock.Close(); os.Remove(indexPath + ".lock") }, nil
}

func integrationPaths(out string) []string {
	return strings.FieldsFunc(out, func(r rune) bool { return r == 0 })
}

func integrationOverlaps(path string, paths []string) bool {
	for _, other := range paths {
		if path == other || strings.HasPrefix(path, other+"/") || strings.HasPrefix(other, path+"/") {
			return true
		}
	}
	return false
}
