package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	continuityfs "github.com/containerd/continuity/fs"

	"github.com/dagger/dagger/engine/snapshots/fsdiff"
)

// changesetDelta is the file-level delta between two mounted directory trees,
// computed from filesystem metadata (like container layer diffing) instead of
// content-diffing both full trees. Paths are relative; directories carry a
// trailing "/" like listSubdirectories.
type changesetDelta struct {
	addedFiles []string
	// modifiedCandidates are files whose metadata differs; content may still
	// be identical (e.g. an mtime-only change), so they must be verified
	// before being reported as modified.
	modifiedCandidates []string
	removedFiles       []string
	addedDirs          []string
	removedDirs        []string
}

// computeChangesetPathsDelta computes ChangesetPaths by walking filesystem
// metadata and reading content only for files the metadata can't rule out,
// instead of content-diffing both full trees like computeChangesetPaths.
// Rename detection and line counts still come from git, but scoped to the
// changed files only. When withStats is true it also returns per-path
// line-change counts matching `git diff --numstat` semantics.
func computeChangesetPathsDelta(ctx context.Context, beforeDir, afterDir string, withStats bool) (*ChangesetPaths, map[string]lineChanges, error) {
	delta, err := collectChangesetDelta(ctx, beforeDir, afterDir)
	if err != nil {
		return nil, nil, fmt.Errorf("collect delta: %w", err)
	}

	modified, err := verifyModifiedFiles(ctx, beforeDir, afterDir, delta.modifiedCandidates)
	if err != nil {
		return nil, nil, fmt.Errorf("verify modified files: %w", err)
	}

	fc := fileChanges{
		Added:    delta.addedFiles,
		Modified: modified,
		Removed:  delta.removedFiles,
	}

	// Renames are always an added/removed pair, so the changed files are the
	// complete candidate set; running git over just them yields the same
	// pairings as a full-tree diff.
	detectRenames := len(delta.addedFiles) > 0 && len(delta.removedFiles) > 0
	materializeModified := withStats && len(modified) > 0

	var stats map[string]lineChanges
	if detectRenames || materializeModified {
		tmpRoot, err := os.MkdirTemp("", "dagger-changeset-delta-")
		if err != nil {
			return nil, nil, fmt.Errorf("create delta staging dir: %w", err)
		}
		defer os.RemoveAll(tmpRoot)
		tmpBefore := filepath.Join(tmpRoot, "before")
		tmpAfter := filepath.Join(tmpRoot, "after")

		// Modified files exist at the same path on both sides, so they can
		// never participate in rename pairing; stage them only when numstat
		// needs their content.
		var beforePaths, afterPaths []string
		if materializeModified {
			beforePaths = slices.Clone(modified)
			afterPaths = slices.Clone(modified)
		}
		if detectRenames {
			beforePaths = append(beforePaths, delta.removedFiles...)
			afterPaths = append(afterPaths, delta.addedFiles...)
		}
		if err := materializeDeltaFiles(ctx, beforeDir, tmpBefore, beforePaths); err != nil {
			return nil, nil, fmt.Errorf("stage before delta: %w", err)
		}
		if err := materializeDeltaFiles(ctx, afterDir, tmpAfter, afterPaths); err != nil {
			return nil, nil, fmt.Errorf("stage after delta: %w", err)
		}

		if detectRenames {
			gitFC, err := compareDirectories(ctx, tmpBefore, tmpAfter)
			if err != nil {
				return nil, nil, fmt.Errorf("compare delta files: %w", err)
			}
			fc.Added = gitFC.Added
			fc.Removed = gitFC.Removed
			fc.Renamed = gitFC.Renamed
		}
		if withStats {
			stats, err = compareDirectoriesNumStat(ctx, tmpBefore, tmpAfter)
			if err != nil {
				return nil, nil, fmt.Errorf("numstat delta files: %w", err)
			}
		}
	}

	if withStats {
		if stats == nil {
			stats = make(map[string]lineChanges)
		}
		if !detectRenames {
			// Added/removed files weren't staged for git; their counts are
			// just the file's own line count.
			for _, rel := range fc.Added {
				lines, ok, err := countGitLines(filepath.Join(afterDir, rel))
				if err != nil {
					return nil, nil, fmt.Errorf("count lines of added %s: %w", rel, err)
				}
				if ok {
					stats[rel] = lineChanges{Added: lines}
				}
			}
			for _, rel := range fc.Removed {
				lines, ok, err := countGitLines(filepath.Join(beforeDir, rel))
				if err != nil {
					return nil, nil, fmt.Errorf("count lines of removed %s: %w", rel, err)
				}
				if ok {
					stats[rel] = lineChanges{Removed: lines}
				}
			}
		}
	}

	renamedNew := make([]string, 0, len(fc.Renamed))
	renamedOld := make([]string, 0, len(fc.Renamed))
	for newPath, oldPath := range fc.Renamed {
		renamedNew = append(renamedNew, newPath)
		renamedOld = append(renamedOld, oldPath)
	}

	allRemoved := slices.Concat(fc.Removed, renamedOld, delta.removedDirs)
	slices.Sort(allRemoved)
	added := slices.Concat(fc.Added, renamedNew, delta.addedDirs)
	slices.Sort(added)
	slices.Sort(fc.Modified)

	return &ChangesetPaths{
		Added:      added,
		Modified:   fc.Modified,
		Removed:    collapseChildPaths(allRemoved),
		AllRemoved: allRemoved,
		Renamed:    fc.Renamed,
	}, stats, nil
}

// collectChangesetDelta double-walks both trees comparing stat metadata,
// reading content only when metadata alone is inconclusive: files backed by
// the same inode are unchanged (snapshots sharing a lineage resolve unchanged
// files to the same backing file), and distinct files whose stat happens to
// match are content-compared rather than trusted.
func collectChangesetDelta(ctx context.Context, beforeDir, afterDir string) (*changesetDelta, error) {
	delta := &changesetDelta{}
	err := fsdiff.WalkChanges(ctx, beforeDir, afterDir, fsdiff.CompareInodeThenContent, func(kind continuityfs.ChangeKind, path string, f os.FileInfo, prevErr error) error {
		if prevErr != nil {
			return prevErr
		}
		rel := strings.TrimPrefix(path, string(os.PathSeparator))
		if rel == "" || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		switch kind {
		case continuityfs.ChangeKindAdd:
			if f != nil && f.IsDir() {
				delta.addedDirs = append(delta.addedDirs, rel+"/")
			} else {
				delta.addedFiles = append(delta.addedFiles, rel)
			}
		case continuityfs.ChangeKindDelete:
			// The walker collapses deletions: a removed directory is emitted
			// once, without its children. Changeset semantics (AllRemoved)
			// need every removed path, so re-expand from the before tree.
			fi, err := os.Lstat(filepath.Join(beforeDir, rel))
			if err != nil {
				return fmt.Errorf("stat removed path %s: %w", rel, err)
			}
			if fi.IsDir() {
				return delta.appendRemovedTree(beforeDir, rel)
			}
			delta.removedFiles = append(delta.removedFiles, rel)
		case continuityfs.ChangeKindModify:
			if f == nil {
				return nil
			}
			beforeFi, err := os.Lstat(filepath.Join(beforeDir, rel))
			if err != nil {
				return fmt.Errorf("stat modified path %s: %w", rel, err)
			}
			switch {
			case beforeFi.IsDir() && f.IsDir():
				// Attribute-only directory change; path-level diffs ignore
				// directories that exist on both sides.
			case beforeFi.IsDir():
				// Directory replaced by a file.
				if err := delta.appendRemovedTree(beforeDir, rel); err != nil {
					return err
				}
				delta.addedFiles = append(delta.addedFiles, rel)
			case f.IsDir():
				// File replaced by a directory; its children arrive as adds.
				delta.removedFiles = append(delta.removedFiles, rel)
				delta.addedDirs = append(delta.addedDirs, rel+"/")
			default:
				delta.modifiedCandidates = append(delta.modifiedCandidates, rel)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return delta, nil
}

func (d *changesetDelta) appendRemovedTree(root, rel string) error {
	d.removedDirs = append(d.removedDirs, rel+"/")
	return filepath.WalkDir(filepath.Join(root, rel), func(p string, ent fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		sub, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		sub = filepath.ToSlash(sub)
		if sub == rel {
			return nil
		}
		if ent.IsDir() {
			d.removedDirs = append(d.removedDirs, sub+"/")
		} else {
			d.removedFiles = append(d.removedFiles, sub)
		}
		return nil
	})
}

// verifyModifiedFiles filters metadata-suspect candidates down to files whose
// git-visible mode or content actually differ, matching the classification a
// full-tree `git diff --no-index` would produce (which ignores e.g. mtime- or
// ownership-only changes).
func verifyModifiedFiles(ctx context.Context, beforeDir, afterDir string, candidates []string) ([]string, error) {
	var modified []string
	for _, rel := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, context.Cause(ctx)
		}
		changed, err := modifiedFileDiffers(beforeDir, afterDir, rel)
		if err != nil {
			return nil, err
		}
		if changed {
			modified = append(modified, rel)
		}
	}
	return modified, nil
}

// modifiedFileDiffers reports whether a single metadata-suspect candidate's
// git-visible mode or content actually differ between the two trees.
func modifiedFileDiffers(beforeDir, afterDir, rel string) (bool, error) {
	beforePath := filepath.Join(beforeDir, rel)
	afterPath := filepath.Join(afterDir, rel)
	beforeFi, err := os.Lstat(beforePath)
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", beforePath, err)
	}
	afterFi, err := os.Lstat(afterPath)
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", afterPath, err)
	}
	if gitFileMode(beforeFi) != gitFileMode(afterFi) {
		return true, nil
	}
	if beforeFi.Mode()&os.ModeSymlink != 0 {
		beforeTarget, err := os.Readlink(beforePath)
		if err != nil {
			return false, fmt.Errorf("readlink %s: %w", beforePath, err)
		}
		afterTarget, err := os.Readlink(afterPath)
		if err != nil {
			return false, fmt.Errorf("readlink %s: %w", afterPath, err)
		}
		return beforeTarget != afterTarget, nil
	}
	if !beforeFi.Mode().IsRegular() || !afterFi.Mode().IsRegular() {
		// Sockets, devices, etc.: metadata differs, so report the change.
		return true, nil
	}
	if beforeFi.Size() != afterFi.Size() {
		return true, nil
	}
	same, err := fileContentsEqual(beforePath, afterPath)
	if err != nil {
		return false, err
	}
	return !same, nil
}

// changesetDeltaIsEmpty reports whether the two trees differ in any
// git-visible file, without staging files or running git. Like
// `git diff --quiet`, only file-level changes count; directory-only changes
// (e.g. an added empty dir) don't. Added or removed files prove the changeset
// non-empty regardless of how git would pair them into renames, and
// metadata-suspect files are verified by content with an early exit on the
// first real difference.
func changesetDeltaIsEmpty(ctx context.Context, beforeDir, afterDir string) (bool, error) {
	delta, err := collectChangesetDelta(ctx, beforeDir, afterDir)
	if err != nil {
		return false, fmt.Errorf("collect delta: %w", err)
	}
	if len(delta.addedFiles) > 0 || len(delta.removedFiles) > 0 {
		return false, nil
	}
	for _, rel := range delta.modifiedCandidates {
		if err := ctx.Err(); err != nil {
			return false, context.Cause(ctx)
		}
		changed, err := modifiedFileDiffers(beforeDir, afterDir, rel)
		if err != nil {
			return false, fmt.Errorf("verify modified file: %w", err)
		}
		if changed {
			return false, nil
		}
	}
	return true, nil
}

// gitFileMode maps a file's mode onto the modes git tracks: symlink, and
// executable vs regular file. Other permission bits are invisible to git.
func gitFileMode(fi os.FileInfo) uint32 {
	if fi.Mode()&os.ModeSymlink != 0 {
		return 0o120000
	}
	if fi.Mode()&0o111 != 0 {
		return 0o100755
	}
	return 0o100644
}

func fileContentsEqual(p1, p2 string) (bool, error) {
	f1, err := os.Open(p1)
	if err != nil {
		return false, err
	}
	defer f1.Close()
	f2, err := os.Open(p2)
	if err != nil {
		return false, err
	}
	defer f2.Close()

	b1 := make([]byte, 32*1024)
	b2 := make([]byte, 32*1024)
	for {
		n1, err1 := io.ReadFull(f1, b1)
		n2, err2 := io.ReadFull(f2, b2)
		if n1 != n2 || !bytes.Equal(b1[:n1], b2[:n2]) {
			return false, nil
		}
		if err1 != nil || err2 != nil {
			if (err1 == io.EOF || err1 == io.ErrUnexpectedEOF) && (err2 == io.EOF || err2 == io.ErrUnexpectedEOF) {
				return true, nil
			}
			if err1 != nil && err1 != io.EOF && err1 != io.ErrUnexpectedEOF {
				return false, err1
			}
			if err2 != nil && err2 != io.EOF && err2 != io.ErrUnexpectedEOF {
				return false, err2
			}
			return false, nil
		}
	}
}

// materializeDeltaFiles copies the given files from srcRoot into dstRoot,
// preserving relative layout, symlinks, and the exec bit, so git can operate
// on just the changed files.
func materializeDeltaFiles(ctx context.Context, srcRoot, dstRoot string, rels []string) error {
	for _, rel := range rels {
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		srcPath := filepath.Join(srcRoot, rel)
		dstPath := filepath.Join(dstRoot, rel)
		fi, err := os.Lstat(srcPath)
		if err != nil {
			return fmt.Errorf("stat %s: %w", srcPath, err)
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(srcPath)
			if err != nil {
				return err
			}
			if err := os.Symlink(target, dstPath); err != nil {
				return err
			}
		case fi.Mode().IsRegular():
			if err := copyFileContents(srcPath, dstPath, fi.Mode().Perm()); err != nil {
				return err
			}
		default:
			// Sockets, devices, etc. can't be diffed by git; skip them.
		}
	}
	return nil
}

func copyFileContents(srcPath, dstPath string, perm os.FileMode) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

// countGitLines counts lines the way `git diff --numstat` does for a fully
// added or removed file: newline count, plus one for an unterminated final
// line. Binary files (NUL byte within the first 8000 bytes, git's heuristic)
// return ok=false, matching numstat's "-" columns.
func countGitLines(path string) (lines int, ok bool, _ error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, false, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		// git stores the target string as the blob: one unterminated line.
		return 1, true, nil
	}
	if !fi.Mode().IsRegular() {
		return 0, false, nil
	}
	if fi.Size() == 0 {
		return 0, true, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return 0, false, err
	}
	defer f.Close()

	buf := make([]byte, 32*1024)
	var lastByte byte
	firstChunk := true
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if firstChunk {
				checkLen := min(n, 8000)
				if bytes.IndexByte(buf[:checkLen], 0) >= 0 {
					return 0, false, nil
				}
				firstChunk = false
			}
			lines += bytes.Count(buf[:n], []byte{'\n'})
			lastByte = buf[n-1]
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, false, err
		}
	}
	if lastByte != '\n' {
		lines++
	}
	return lines, true, nil
}
