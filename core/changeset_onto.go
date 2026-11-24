package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagger/dagger/dagql"
)

// PatchOnto is a changeset rendered against a tree other than its own Before:
// removing RemovedFiles from that tree, applying Patch to it with `git apply`,
// then removing RemovedDirectories and creating NewDirectories, in that order,
// leaves every path the changeset declares as Directory.withChanges would,
// without comparing the two trees.
//
// Deletions stay out of the patch, which only creates and modifies files: a
// deletion needs no content, while a patch hunk would carry every deleted
// byte, and a binary one would not be embeddable at all. Since the patch
// deletes nothing, `git apply` prunes no directory either.
type PatchOnto struct {
	// RemovedFiles are the paths to remove before the patch: the files the
	// changeset removes outside RemovedDirectories, and the directories it
	// replaces with a file, which the patch then creates. Sorted, and none
	// beneath another.
	RemovedFiles []string
	// Patch is a `git diff --binary` from the tree's content at the files the
	// changeset writes to the changeset's After content there. Empty when
	// they already match.
	Patch []byte
	// RemovedDirectories are the directories the changeset removes, whole,
	// with whatever the tree holds in them that Before did not, e.g. ignored
	// files.
	RemovedDirectories []string
	// NewDirectories are the empty directories the changeset adds, which a
	// patch cannot express. Parents come before their children.
	NewDirectories []PatchOntoDirectory
}

// PatchOntoDirectory is a directory to create, with its mode in After.
type PatchOntoDirectory struct {
	Path        string
	Permissions int
}

// IsEmpty reports whether applying p changes nothing.
func (p *PatchOnto) IsEmpty() bool {
	return len(p.RemovedFiles) == 0 && len(p.Patch) == 0 && len(p.NewDirectories) == 0 && len(p.RemovedDirectories) == 0
}

// Merge combines p with other, a rendering of other paths of the same
// changeset onto the same workspace (so the two touch no path in common),
// into one PatchOnto applied in the usual order.
func (p *PatchOnto) Merge(other *PatchOnto) *PatchOnto {
	merged := &PatchOnto{
		RemovedFiles:       outermostPaths(slices.Concat(p.RemovedFiles, other.RemovedFiles)),
		Patch:              slices.Concat(p.Patch, other.Patch),
		RemovedDirectories: slices.Sorted(slices.Values(slices.Concat(p.RemovedDirectories, other.RemovedDirectories))),
		NewDirectories:     slices.Concat(p.NewDirectories, other.NewDirectories),
	}
	// Parents sort before their children.
	slices.SortFunc(merged.NewDirectories, func(a, b PatchOntoDirectory) int { return strings.Compare(a.Path, b.Path) })
	return merged
}

var (
	// ErrPatchTooLarge is returned by RenderPatchOnto when the patch exceeds
	// its size budget.
	ErrPatchTooLarge = errors.New("patch exceeds the size budget")
	// ErrPatchBinary is returned by RenderPatchOnto when the patch would
	// carry a binary file's content.
	ErrPatchBinary = errors.New("patch has binary content")
)

// EmbeddedPatchMaxBytes bounds a patch the engine embeds in a recipe with
// EmbedPatch.
const EmbeddedPatchMaxBytes = 16 << 20

// PatchNotEmbeddable reports whether err means a patch must not be embedded in
// a recipe (ErrPatchTooLarge or ErrPatchBinary), so the caller should keep a
// by-reference representation, such as the raw changeset, instead.
func PatchNotEmbeddable(err error) bool {
	return errors.Is(err, ErrPatchTooLarge) || errors.Is(err, ErrPatchBinary)
}

// CheckEmbeddablePatch fails with ErrPatchTooLarge for a patch over
// EmbeddedPatchMaxBytes, and with ErrPatchBinary for one that carries a binary
// file's content (or names a binary file it cannot carry).
func CheckEmbeddablePatch(patch []byte) error {
	if int64(len(patch)) > EmbeddedPatchMaxBytes {
		return ErrPatchTooLarge
	}
	if patchHasBinary(patch) {
		return ErrPatchBinary
	}
	return nil
}

// patchHasBinary reports whether a `git diff` has a binary hunk ("GIT binary
// patch", with --binary) or a binary file it left out ("Binary files ...
// differ", without). Both are header lines: text hunk lines always start with
// ' ', '+', '-' or '\', so content cannot be mistaken for them.
func patchHasBinary(patch []byte) bool {
	for line := range bytes.Lines(patch) {
		if bytes.Equal(line, gitBinaryPatchLine) ||
			(bytes.HasPrefix(line, []byte("Binary files ")) && bytes.HasSuffix(line, []byte(" differ\n"))) {
			return true
		}
	}
	return false
}

// EmbedPatch returns patch as a Query.blob file, to apply from a recipe.
//
// Every engine-built recipe that inlines a rendered patch must go through it.
// A blob's contents are stored inline in its call ID, so the patch is carried
// by every recipe built on it and recorded in every trace span that calls it,
// and a span's call attribute must fit an OTLP frame for the trace to be
// restorable at all. A build output such as `go test -c` is megabytes of
// base85 there, and is better rebuilt from its producer. So a patch that fails
// CheckEmbeddablePatch is refused with its error: the caller falls back to a
// by-reference representation (PatchNotEmbeddable).
func EmbedPatch(ctx context.Context, srv *dagql.Server, name string, patch []byte) (dagql.ObjectResult[*File], error) {
	var blob dagql.ObjectResult[*File]
	if err := CheckEmbeddablePatch(patch); err != nil {
		return blob, err
	}
	// No View: blob postdates some client views, and this is engine-internal
	// plumbing.
	err := srv.Select(ctx, srv.Root(), &blob, dagql.Selector{
		Field: "blob",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString(name)},
			{Name: "contents", Value: dagql.Bytes(patch)},
			{Name: "permissions", Value: dagql.NewInt(0o600)},
		},
	})
	return blob, err
}

// ApplyPatchOnto applies p to ws, the workspace whose root p was rendered
// against (RenderPatchOnto), as recipes that hold p and nothing of the
// changeset it came from: removed files, the patch, embedded with EmbedPatch
// as a blob called name, then removed and new directories, in the order
// PatchOnto prescribes.
//
// It is the one way the engine records a rendered changeset in a workspace's
// recipe, so the changeset's producer is never replayed. A patch EmbedPatch
// refuses fails with its error, for the caller to apply the changeset raw
// (PatchNotEmbeddable). The result is lazy: nothing evaluates the patch here.
func ApplyPatchOnto(
	ctx context.Context,
	srv *dagql.Server,
	ws dagql.ObjectResult[*Workspace],
	p *PatchOnto,
	name string,
	onConflict PatchConflict,
) (dagql.ObjectResult[*Workspace], error) {
	selectWS := func(sel dagql.Selector) error {
		sel.View = srv.View
		return srv.Select(ctx, ws, &ws, sel)
	}
	if len(p.RemovedFiles) > 0 {
		// Before the patch, which only writes: deletions are path-only.
		removed := make([]string, len(p.RemovedFiles))
		for i, f := range p.RemovedFiles {
			removed[i] = "/" + f
		}
		if err := selectWS(dagql.Selector{
			Field: "withoutFiles",
			Args:  []dagql.NamedInput{{Name: "paths", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(removed...))}},
		}); err != nil {
			return ws, err
		}
	}
	if len(p.Patch) > 0 {
		blob, err := EmbedPatch(ctx, srv, name, p.Patch)
		if err != nil {
			// Still PatchNotEmbeddable through the wrap, for the raw fallback.
			return ws, fmt.Errorf("embed %s: %w", name, err)
		}
		blobID, err := blob.ID()
		if err != nil {
			return ws, err
		}
		if err := selectWS(dagql.Selector{
			Field: "withPatchFile",
			Args: []dagql.NamedInput{
				{Name: "patch", Value: dagql.NewID[*File](blobID)},
				{Name: "onConflict", Value: onConflict},
			},
		}); err != nil {
			return ws, err
		}
	}
	// What a patch cannot express. Paths are absolute: the workspace resolves
	// relative ones from its cwd.
	for _, dir := range p.RemovedDirectories {
		if err := selectWS(dagql.Selector{
			Field: "withoutDirectory",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString("/" + dir)}},
		}); err != nil {
			return ws, err
		}
	}
	for _, dir := range p.NewDirectories {
		// Merged in rather than replaced (withNewDirectory), which would drop
		// what the workspace holds there and Before did not, e.g. ignored
		// files. A directory created by the merge takes the source's mode.
		var empty dagql.ObjectResult[*Directory]
		if err := srv.Select(ctx, srv.Root(), &empty,
			dagql.Selector{View: srv.View, Field: "directory"},
			dagql.Selector{View: srv.View, Field: "withNewDirectory", Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString("dir")},
				{Name: "permissions", Value: dagql.NewInt(dir.Permissions)},
			}},
			dagql.Selector{View: srv.View, Field: "directory", Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString("dir")},
			}},
		); err != nil {
			return ws, fmt.Errorf("directory %q: %w", dir.Path, err)
		}
		emptyID, err := empty.ID()
		if err != nil {
			return ws, err
		}
		if err := selectWS(dagql.Selector{
			Field: "withDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString("/" + dir.Path)},
				{Name: "source", Value: dagql.NewID[*Directory](emptyID)},
			},
		}); err != nil {
			return ws, err
		}
	}
	return ws, nil
}

// RenderPatchOnto renders the changeset as a patch against base, a tree the
// changeset is about to be applied to at prefix (a base-relative directory;
// "." for its root). Paths in the result are base-relative.
//
// Only the changeset's paths are staged — base's content there, and After's —
// and `git diff` runs between the two, so the cost follows the size of the
// change rather than of either tree. Because the patch starts from base's own
// content, applying it reproduces the changeset by construction, even where
// base and Before differ: a file the changeset adds that base already has
// becomes a modification, and a file it removes that base never had drops out.
//
// The patch is read into memory; past maxBytes it fails with ErrPatchTooLarge.
// It is meant to be embedded in a recipe, and a binary file's content does not
// belong there: a build output such as a compiled binary is better rebuilt
// from its producer than carried, base85-encoded, in every recipe and trace
// that includes the patch. So a patch that adds or modifies a binary file
// fails with ErrPatchBinary, as soon as git writes its first binary hunk.
// Removing one is fine: removals are path-only (see PatchOnto).
func (ch *Changeset) RenderPatchOnto(ctx context.Context, base dagql.ObjectResult[*Directory], prefix string, maxBytes int64) (*PatchOnto, error) {
	paths, err := ch.ComputePaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("compute changeset paths: %w", err)
	}
	return ch.RenderPatchOntoPaths(ctx, base, prefix, paths, maxBytes)
}

// RenderPatchOntoPaths is RenderPatchOnto for just some of the changeset's
// paths, as ComputePaths reports them (see ChangesetPaths.Filter): what the
// changeset does elsewhere is left out of the result, for rendering parts of
// it against different trees.
func (ch *Changeset) RenderPatchOntoPaths(ctx context.Context, base dagql.ObjectResult[*Directory], prefix string, paths *ChangesetPaths, maxBytes int64) (*PatchOnto, error) {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	if err := cache.Evaluate(ctx, base, ch.After); err != nil {
		return nil, fmt.Errorf("evaluate patch trees: %w", err)
	}
	var out *PatchOnto
	err = mountDirectoryOrEmpty(ctx, base, func(baseDir string) error {
		return mountDirectoryOrEmpty(ctx, ch.After, func(afterDir string) (err error) {
			out, err = renderPatchOntoDirs(ctx, baseDir, afterDir, prefix, paths, maxBytes)
			return err
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// renderPatchOntoDirs is RenderPatchOnto over mounted trees, given the
// changeset's paths.
func renderPatchOntoDirs(ctx context.Context, baseDir, afterDir, prefix string, paths *ChangesetPaths, maxBytes int64) (*PatchOnto, error) {
	prefix = strings.TrimPrefix(path.Clean("/"+filepath.ToSlash(prefix)), "/")
	if prefix == "" {
		prefix = "."
	}
	rooted := func(p string) string {
		return path.Join(prefix, strings.TrimSuffix(p, "/"))
	}

	stage, err := os.MkdirTemp("", "dagger-patch-onto-")
	if err != nil {
		return nil, fmt.Errorf("create patch staging dir: %w", err)
	}
	defer os.RemoveAll(stage)
	stagedBase := filepath.Join(stage, "a")
	stagedAfter := filepath.Join(stage, "b")
	for _, dir := range []string{stagedBase, stagedAfter} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			return nil, err
		}
	}

	plan, err := planPatchOnto(baseDir, afterDir, paths, rooted)
	if err != nil {
		return nil, err
	}
	out := &PatchOnto{
		RemovedFiles:       outermostPaths(plan.removed),
		RemovedDirectories: plan.removedDirs,
	}

	if err := materializeDeltaFiles(ctx, baseDir, stagedBase, plan.baseFiles); err != nil {
		return nil, fmt.Errorf("stage base files: %w", err)
	}
	// After is rooted at prefix within base: stage its files there, so the
	// patch's paths are base-relative.
	if err := materializeDeltaFiles(ctx, afterDir, filepath.Join(stagedAfter, prefix), plan.afterFiles); err != nil {
		return nil, fmt.Errorf("stage after files: %w", err)
	}
	if len(plan.baseFiles) > 0 || len(plan.afterFiles) > 0 {
		if out.Patch, err = renderStagedPatch(ctx, stage, maxBytes); err != nil {
			return nil, err
		}
	}

	newDirs, err := addedEmptyDirectories(afterDir, paths, plan.afterFiles, rooted)
	if err != nil {
		return nil, err
	}
	for dir, perm := range newDirs {
		out.NewDirectories = append(out.NewDirectories, PatchOntoDirectory{Path: dir, Permissions: perm})
	}
	// Parents sort before their children.
	slices.SortFunc(out.NewDirectories, func(a, b PatchOntoDirectory) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

// patchOntoPlan sorts a changeset's paths by how they reach base. baseFiles
// are the files of base the patch modifies: those at a path After writes a
// file to. Anything else base holds at the changeset's paths goes before the
// patch (removed) or after it (removedDirs), by path alone. Paths are
// base-relative, except afterFiles, which are After's own.
type patchOntoPlan struct {
	baseFiles, afterFiles, removed, removedDirs []string
	written                                     map[string]bool
}

func planPatchOnto(baseDir, afterDir string, paths *ChangesetPaths, rooted func(string) string) (*patchOntoPlan, error) {
	plan := &patchOntoPlan{written: map[string]bool{}}
	for _, p := range slices.Concat(paths.Added, paths.Modified) {
		if err := plan.addWrite(baseDir, p, rooted); err != nil {
			return nil, err
		}
	}
	for _, p := range paths.Removed {
		if err := plan.addRemoval(baseDir, afterDir, p, rooted); err != nil {
			return nil, err
		}
	}
	slices.Sort(plan.baseFiles)
	plan.baseFiles = slices.Compact(plan.baseFiles)
	slices.Sort(plan.removedDirs)
	return plan, nil
}

// addWrite plans a path the changeset adds or modifies.
func (plan *patchOntoPlan) addWrite(baseDir, p string, rooted func(string) string) error {
	if strings.HasSuffix(p, "/") {
		return nil
	}
	plan.afterFiles = append(plan.afterFiles, p)
	plan.written[rooted(p)] = true
	fi, err := lstatInRoot(baseDir, rooted(p))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if fi.IsDir() {
		// A directory After replaces with a file goes whole, before the
		// patch creates the file: `git apply` can replace only an empty
		// directory.
		plan.removed = append(plan.removed, rooted(p))
	} else {
		plan.baseFiles = append(plan.baseFiles, rooted(p))
	}
	return nil
}

// addRemoval plans a path the changeset removes. Call it after every write is
// planned.
func (plan *patchOntoPlan) addRemoval(baseDir, afterDir, p string, rooted func(string) string) error {
	dir := rooted(p)
	baseFi, err := lstatInRoot(baseDir, dir)
	if errors.Is(err, fs.ErrNotExist) {
		// Nothing to remove.
		return nil
	} else if err != nil {
		return err
	}
	afterFi, err := lstatInRoot(afterDir, strings.TrimSuffix(p, "/"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	switch {
	case plan.written[dir] && !baseFi.IsDir():
		// The patch modifies it.
	case afterFi == nil && baseFi.IsDir():
		// Removed whole, including what Before never had there.
		plan.removedDirs = append(plan.removedDirs, dir)
	case afterFi == nil || !baseFi.IsDir() || !afterFi.IsDir():
		// A file, or one side's directory replaced by the other's file:
		// removed before the patch writes there or beneath.
		plan.removed = append(plan.removed, dir)
	default:
		// Both sides have a directory: what base holds there that After
		// does not write goes, file by file.
		found, err := treeFiles(baseDir, dir)
		if err != nil {
			return err
		}
		for _, f := range found {
			if !plan.written[f] {
				plan.removed = append(plan.removed, f)
			}
		}
	}
	return nil
}

// renderStagedPatch runs `git diff --binary` between the a/ and b/ trees
// staged beneath stage, within the size budget and refusing binary hunks.
func renderStagedPatch(ctx context.Context, stage string, maxBytes int64) ([]byte, error) {
	var patch bytes.Buffer
	var stderr strings.Builder
	budget := &patchBudgetWriter{w: &patch, remaining: maxBytes}
	err := writeGitDiffPatch(ctx, stage, nil, budget, io.Discard, &stderr)
	// Checked first: a small patch fits in the pipe, so git can exit as
	// usual before it notices we stopped reading.
	if budget.err != nil {
		return nil, budget.err
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return patch.Bytes(), nil
}

// outermostPaths returns paths sorted, without duplicates or any path beneath
// another of them: removing that one removes it too.
func outermostPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[p] = true
	}
	out := make([]string, 0, len(set))
	for p := range set {
		beneath := false
		for dir := path.Dir(p); dir != "." && dir != "/"; dir = path.Dir(dir) {
			if set[dir] {
				beneath = true
				break
			}
		}
		if !beneath {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// addedEmptyDirectories returns the directories the changeset adds that no
// file the patch writes lies beneath, by rooted path, with their modes in
// After. A directory the changeset adds with files in it is left to the patch,
// which creates it along with them: its mode is lost, but listing every such
// directory would make each workspace read replay one more step per directory.
func addedEmptyDirectories(afterDir string, paths *ChangesetPaths, afterFiles []string, rooted func(string) string) (map[string]int, error) {
	// The directories the patch creates, as parents of the files it writes.
	holdsFiles := map[string]bool{}
	for _, p := range afterFiles {
		for dir := path.Dir(p); dir != "." && dir != "/" && !holdsFiles[dir]; dir = path.Dir(dir) {
			holdsFiles[dir] = true
		}
	}
	dirs := map[string]int{}
	for _, p := range paths.Added {
		if !strings.HasSuffix(p, "/") || holdsFiles[strings.TrimSuffix(p, "/")] {
			continue
		}
		fi, err := lstatInRoot(afterDir, p)
		if err != nil {
			return nil, fmt.Errorf("stat added directory %q: %w", p, err)
		}
		dirs[rooted(p)] = int(fi.Mode().Perm())
	}
	return dirs, nil
}

// treeFiles returns rel itself if it is a file or symlink under root, every
// file and symlink beneath it if it is a directory, and nothing if it does not
// exist.
func treeFiles(root, rel string) ([]string, error) {
	fi, err := lstatInRoot(root, rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return []string{rel}, nil
	}
	var files []string
	err = filepath.WalkDir(filepath.Join(root, rel), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		sub, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(sub))
		return nil
	})
	return files, err
}

// lstatInRoot is os.Lstat of rel beneath root, refusing to resolve it through
// a parent that is not a directory: a symlink there could lead out of root. A
// path whose parent is a symlink or a file does not exist in the tree, so it
// reports fs.ErrNotExist.
func lstatInRoot(root, rel string) (fs.FileInfo, error) {
	rel = path.Clean(filepath.ToSlash(rel))
	if rel == "." {
		return os.Lstat(root)
	}
	parts := strings.Split(strings.TrimPrefix(rel, "/"), "/")
	cur := root
	for i, part := range parts {
		if part == ".." {
			return nil, fmt.Errorf("path %q escapes its root", rel)
		}
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err != nil {
			return nil, err
		}
		if i == len(parts)-1 {
			return fi, nil
		}
		if !fi.IsDir() {
			return nil, fs.ErrNotExist
		}
	}
	return nil, fs.ErrNotExist
}

// mountDirectoryOrEmpty mounts dir read-only, or an empty directory when it
// has no snapshot (an empty directory result).
func mountDirectoryOrEmpty(ctx context.Context, dir dagql.ObjectResult[*Directory], fn func(string) error) error {
	err := dir.Self().Mount(ctx, dir, fn)
	if !errors.Is(err, errEmptyResultRef) {
		return err
	}
	empty, err := os.MkdirTemp("", "dagger-empty-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(empty)
	return fn(empty)
}

// gitBinaryPatchLine starts each binary hunk of a `git diff --binary`. Text
// hunk lines always start with ' ', '+', '-' or '\', so it cannot be content.
var gitBinaryPatchLine = []byte("GIT binary patch\n")

// patchBudgetWriter fails writes past its budget, or of a binary hunk,
// recording why. It expects whole lines, as writeGitDiffPatch writes them.
type patchBudgetWriter struct {
	w         io.Writer
	remaining int64
	err       error
}

func (l *patchBudgetWriter) Write(p []byte) (int, error) {
	if bytes.Equal(p, gitBinaryPatchLine) {
		l.err = ErrPatchBinary
		return 0, l.err
	}
	if int64(len(p)) > l.remaining {
		l.err = ErrPatchTooLarge
		return 0, l.err
	}
	l.remaining -= int64(len(p))
	return l.w.Write(p)
}
