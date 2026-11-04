package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/dagger/dagger/util/layercopy"
)

// A sparse tree's snapshot holds its files under gitSparseTreeDir, beside the
// list of everything in the commit's tree, so a read of the tree can't reach
// the list.
const (
	gitSparseTreeDir      = "tree"
	gitSparseManifestFile = "manifest"
)

// How the manifest marks each path of the commit's tree that isn't a directory.
const (
	gitSparseEntryFile    = 'f' // a file in the sparse tree
	gitSparseEntrySymlink = 'l' // a symlink in the sparse tree
	gitSparseEntryAbsent  = 'a' // a file or symlink only the full tree has
	gitSparseEntryGitlink = 'g' // a submodule: the full tree serves every read
)

const (
	gitModeSymlink = "120000"
	gitModeGitlink = "160000"
)

// GitTreeFiles is a commit's tree in a blobless mirror: every path and object
// ID is known, and file contents are fetched by ID when asked for.
type GitTreeFiles struct {
	// Tree is the commit's paths.
	Tree *layercopy.PathTree

	repo    *RemoteGitRepository
	git     *gitutil.GitCLI
	entries map[string]gitTreeEntry
	missing map[string]struct{}
}

type gitTreeEntry struct {
	mode string
	oid  string
}

func loadGitTreeFiles(ctx context.Context, repo *RemoteGitRepository, git *gitutil.GitCLI, sha string) (*GitTreeFiles, error) {
	listing, err := git.Run(ctx, "ls-tree", "-r", "-z", "--full-tree", sha)
	if err != nil {
		return nil, fmt.Errorf("list tree: %w", err)
	}
	files := &GitTreeFiles{
		repo:    repo,
		git:     git,
		entries: map[string]gitTreeEntry{},
		missing: map[string]struct{}{},
	}
	var paths []string
	for line := range bytes.SplitSeq(listing, []byte{0}) {
		if len(line) == 0 {
			continue
		}
		meta, name, ok := bytes.Cut(line, []byte{'\t'})
		fields := bytes.Fields(meta)
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("list tree: unexpected entry %q", line)
		}
		p := string(name)
		files.entries[p] = gitTreeEntry{mode: string(fields[0]), oid: string(fields[2])}
		paths = append(paths, p)
	}
	files.Tree = layercopy.NewPathTree(paths)

	// --missing=print reports what the mirror lacks without fetching it.
	objects, err := git.Run(ctx, "rev-list", "--objects", "--missing=print", sha)
	if err != nil {
		return nil, fmt.Errorf("list missing objects: %w", err)
	}
	for line := range bytes.SplitSeq(objects, []byte{'\n'}) {
		if oid, ok := bytes.CutPrefix(line, []byte{'?'}); ok {
			files.missing[string(oid)] = struct{}{}
		}
	}
	return files, nil
}

// writeSubmodulesManifest leaves a sparse tree holding nothing, whose manifest
// says the commit has submodules. A filter can select files inside a submodule,
// which the tree's paths don't list, so no read can tell what it selects, and
// the full checkout, which fills them, serves them all.
func (files *GitTreeFiles) writeSubmodulesManifest(snapshotRoot string) error {
	if err := os.Mkdir(filepath.Join(snapshotRoot, gitSparseTreeDir), 0o755); err != nil {
		return err
	}
	var manifest bytes.Buffer
	for p, entry := range files.entries {
		if entry.mode == gitModeGitlink {
			manifest.WriteByte(gitSparseEntryGitlink)
			manifest.WriteString(p)
			manifest.WriteByte(0)
		}
	}
	return os.WriteFile(filepath.Join(snapshotRoot, gitSparseManifestFile), manifest.Bytes(), 0o600)
}

func (files *GitTreeFiles) hasSubmodules() bool {
	for _, entry := range files.entries {
		if entry.mode == gitModeGitlink {
			return true
		}
	}
	return false
}

// Has reports whether p is in the tree and isn't a directory.
func (files *GitTreeFiles) Has(p string) bool {
	_, ok := files.entries[p]
	return ok
}

// Named returns every path of the tree with one of these base names.
func (files *GitTreeFiles) Named(names ...string) []string {
	var paths []string
	for p := range files.entries {
		if slices.Contains(names, path.Base(p)) {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	return paths
}

// Fetch gets the contents of paths in one request, as Git itself asks a
// promisor remote for a batch of objects. A path the tree doesn't have, or
// whose contents the mirror holds, needs nothing.
func (files *GitTreeFiles) Fetch(ctx context.Context, paths []string) error {
	var oids bytes.Buffer
	seen := map[string]struct{}{}
	for _, p := range paths {
		entry, ok := files.entries[p]
		if !ok || entry.mode == gitModeGitlink {
			continue
		}
		if _, missing := files.missing[entry.oid]; !missing {
			continue
		}
		if _, dup := seen[entry.oid]; dup {
			continue
		}
		seen[entry.oid] = struct{}{}
		oids.WriteString(entry.oid + "\n")
	}
	if len(seen) == 0 {
		return nil
	}
	if err := files.repo.fetchBlobs(ctx, files.git, &oids); err != nil {
		return fmt.Errorf("fetch file contents: %w", err)
	}
	for oid := range seen {
		delete(files.missing, oid)
	}
	return nil
}

// Read returns a file's contents as Git stores them, after Fetch.
func (files *GitTreeFiles) Read(ctx context.Context, p string) ([]byte, error) {
	entry, ok := files.entries[p]
	if !ok {
		return nil, fmt.Errorf("read %q: %w", p, os.ErrNotExist)
	}
	return files.git.Run(ctx, "cat-file", "blob", entry.oid)
}

// writeSparseTree writes paths under dest as a checkout of the commit writes
// them, with Git applying the tree's attributes, along with every directory of
// the tree and its symlinks. So a path's parents and any symlink resolve as they
// do in a full checkout, and the only difference is which files are there.
//
// It returns the manifest describing every path of the commit.
func (files *GitTreeFiles) writeSparseTree(ctx context.Context, snapshotRoot string, paths []string) ([]byte, error) {
	wanted := map[string]struct{}{}
	for _, p := range paths {
		wanted[p] = struct{}{}
	}
	for p, entry := range files.entries {
		// Git reads attributes from the index, so they apply as in a checkout.
		if entry.mode == gitModeSymlink || path.Base(p) == ".gitattributes" {
			wanted[p] = struct{}{}
		}
	}
	all := make([]string, 0, len(wanted))
	for p := range wanted {
		all = append(all, p)
	}
	slices.Sort(all)
	if err := files.Fetch(ctx, all); err != nil {
		return nil, err
	}

	dest := filepath.Join(snapshotRoot, gitSparseTreeDir)
	rootInfo, err := os.Stat(snapshotRoot)
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(dest, rootInfo.Mode().Perm()); err != nil {
		return nil, err
	}
	if err := os.Chmod(dest, rootInfo.Mode().Perm()); err != nil {
		return nil, err
	}
	for p := range files.entries {
		if err := os.MkdirAll(filepath.Join(dest, filepath.Dir(p)), 0o755); err != nil {
			return nil, err
		}
	}

	var index bytes.Buffer
	var manifest bytes.Buffer
	sorted := make([]string, 0, len(files.entries))
	for p := range files.entries {
		sorted = append(sorted, p)
	}
	slices.Sort(sorted)
	for _, p := range sorted {
		entry := files.entries[p]
		_, isWanted := wanted[p]
		var mark byte
		switch {
		case !isWanted:
			mark = gitSparseEntryAbsent
		case entry.mode == gitModeSymlink:
			mark = gitSparseEntrySymlink
		default:
			mark = gitSparseEntryFile
		}
		manifest.WriteByte(mark)
		manifest.WriteString(p)
		manifest.WriteByte(0)
		if mark == gitSparseEntryFile || mark == gitSparseEntrySymlink {
			fmt.Fprintf(&index, "%s %s\t%s\x00", entry.mode, entry.oid, p)
		}
	}

	indexFile := filepath.Join(snapshotRoot, "index")
	git := files.git.New(gitutil.WithWorkTree(dest), gitutil.WithIndexFile(indexFile))
	if _, err := git.RunWithStdin(ctx, &index, "update-index", "-z", "--add", "--index-info"); err != nil {
		return nil, fmt.Errorf("build index: %w", err)
	}
	if _, err := git.Run(ctx, "checkout-index", "--all", "--force"); err != nil {
		return nil, fmt.Errorf("write files: %w", err)
	}
	if err := os.Remove(indexFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return manifest.Bytes(), nil
}

// SparseTree writes the files plan picks from this commit's tree into a new
// snapshot, fetching only them. It holds the blobless mirror for all of it,
// since reading the mirror is all it does, and for few files.
//
// The result has no content digest. A subdirectory of a tree with one is hashed
// by its own files, so a view into this tree would count as the same as a view
// into the full tree, while a symlink in it can't reach what the full tree's
// can. Only what's copied out of it, which holds its whole selection, is
// identified by content.
func (ref *RemoteGitRef) SparseTree(
	ctx context.Context,
	dag *dagql.Server,
	plan func(context.Context, *GitTreeFiles) ([]string, error),
) (_ *Directory, rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := ref.repo.withBloblessMirror(ctx, dag)
	if err != nil {
		return nil, err
	}
	checkoutRef, err := query.SnapshotManager().New(ctx, nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeGitCheckout),
		bkcache.WithDescription(fmt.Sprintf("git sparse checkout for %s (%s %s)", repo.URL.Remote(), ref.Name, ref.SHA)))
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil && checkoutRef != nil {
			checkoutRef.Release(context.WithoutCancel(ctx))
		}
	}()
	err = MountRef(ctx, checkoutRef, func(root string, _ *mount.Mount) error {
		err := repo.mount(ctx, 1, false, []GitRefBackend{ref}, func(ctx context.Context, git *gitutil.GitCLI) error {
			files, err := loadGitTreeFiles(ctx, repo, git, ref.SHA)
			if err != nil {
				return err
			}
			if files.hasSubmodules() {
				return files.writeSubmodulesManifest(root)
			}
			paths, err := plan(ctx, files)
			if err != nil {
				return err
			}
			manifest, err := files.writeSparseTree(ctx, root, paths)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(root, gitSparseManifestFile), manifest, 0o600)
		})
		if err != nil {
			return err
		}
		return normalizeGitCheckoutTimes(ctx, root, "")
	})
	if err != nil {
		return nil, fmt.Errorf("sparse checkout of %s in %s: %w", ref.Name, repo.URL.Remote(), err)
	}
	snap, err := checkoutRef.Commit(ctx)
	if err != nil {
		return nil, err
	}
	checkoutRef = nil
	dir := &Directory{
		Platform: query.Platform(),
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	dir.SetPath("/" + gitSparseTreeDir)
	dir.SetSnapshot(snap)
	return dir, nil
}

// GitSparseTree is the part of a Git workspace's tree that module loading
// reads, written so that any read it can serve gets exactly what the full
// checkout would give. Whether a read can come from it is decided from the
// commit's whole list of paths, so a file it doesn't hold can never go missing:
// a read selecting one, or a path it can't answer for, goes to the full tree.
type GitSparseTree struct {
	Root dagql.ObjectResult[*Directory]

	manifestOnce sync.Once
	manifest     *gitSparseManifest
	manifestErr  error
}

type gitSparseManifest struct {
	tree       *layercopy.PathTree
	entries    map[string]byte
	submodules bool
}

func (t *GitSparseTree) loadManifest(ctx context.Context) (*gitSparseManifest, error) {
	t.manifestOnce.Do(func() {
		t.manifest, t.manifestErr = readGitSparseManifest(ctx, t.Root)
	})
	return t.manifest, t.manifestErr
}

func readGitSparseManifest(ctx context.Context, root dagql.ObjectResult[*Directory]) (*gitSparseManifest, error) {
	// A tree held only as another engine's record decodes its snapshot from the
	// server in the context, and loading a workspace has none attached.
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	ctx = dagql.ContextWithServer(ctx, dag)
	snapshot, err := root.Self().Snapshot.GetOrEval(ctx, root.Result)
	if err != nil {
		return nil, fmt.Errorf("sparse tree snapshot: %w", err)
	}
	var data []byte
	if err := MountRef(ctx, snapshot, func(dir string, _ *mount.Mount) (err error) {
		data, err = os.ReadFile(filepath.Join(dir, gitSparseManifestFile))
		return err
	}, mountRefAsReadOnly); err != nil {
		return nil, fmt.Errorf("read sparse tree manifest: %w", err)
	}
	m := &gitSparseManifest{entries: map[string]byte{}}
	var paths []string
	for entry := range bytes.SplitSeq(data, []byte{0}) {
		if len(entry) < 2 {
			continue
		}
		p := string(entry[1:])
		m.entries[p] = entry[0]
		m.submodules = m.submodules || entry[0] == gitSparseEntryGitlink
		paths = append(paths, p)
	}
	m.tree = layercopy.NewPathTree(paths)
	return m, nil
}

// CoversRead reports whether a copy of the directory dir with this filter gets
// from the sparse tree exactly what it gets from the full tree: dir is a real
// directory of the tree, reached through no symlink, and the sparse tree holds
// everything the filter selects there, which is less than all that dir holds. A
// dir the commit doesn't have, which fails the same way in both, is covered as
// well.
func (t *GitSparseTree) CoversRead(ctx context.Context, dir string, filter CopyFilter) (bool, error) {
	m, err := t.loadManifest(ctx)
	if err != nil {
		return false, err
	}
	if m.submodules {
		return false, nil
	}
	if !m.tree.IsDir(dir) {
		return t.coversPath(ctx, dir)
	}
	selected, err := m.tree.SelectedFiles(dir, layercopy.Filter{Include: filter.Include, Exclude: filter.Exclude})
	if err != nil {
		return false, err
	}
	for _, p := range selected {
		if mark := m.entries[p]; mark != gitSparseEntryFile && mark != gitSparseEntrySymlink {
			return false, nil
		}
	}
	// A copy of everything in dir has the content digest of dir itself, so one
	// can stand in for the other. A view of dir in the full tree can follow a
	// symlink out of it, which a copy can't, so the view must exist first.
	return len(selected) < m.tree.FileCount(dir), nil
}

// coversPath reports whether looking p up in the sparse tree answers as the
// full tree does. A symlink on the way, or at p, could lead to a file the
// sparse tree doesn't have.
func (t *GitSparseTree) coversPath(ctx context.Context, p string) (bool, error) {
	m, err := t.loadManifest(ctx)
	if err != nil {
		return false, err
	}
	if m.submodules {
		return false, nil
	}
	p = strings.TrimPrefix(path.Clean("/"+filepath.ToSlash(p)), "/")
	if p == "" {
		return true, nil
	}
	mark, inTree := m.entries[p]
	if inTree && mark != gitSparseEntryFile {
		return false, nil
	}
	for parent := path.Dir(p); parent != "."; parent = path.Dir(parent) {
		if _, notDir := m.entries[parent]; notDir {
			return false, nil
		}
	}
	return true, nil
}

// GitSparseTreeFS answers stats and file reads on a Git workspace's tree from
// its sparse tree where that gives the full tree's answer, and otherwise from
// the full tree, without making that checkout until a read needs it.
type GitSparseTreeFS struct {
	Sparse *GitSparseTree
	Full   dagql.ObjectResult[*Directory]
}

var (
	_ StatFS   = (*GitSparseTreeFS)(nil)
	_ ExistsFS = (*GitSparseTreeFS)(nil)
)

// dirFor picks the directory to look p up in.
func (fs *GitSparseTreeFS) dirFor(ctx context.Context, p string) dagql.ObjectResult[*Directory] {
	covered, err := fs.Sparse.coversPath(ctx, p)
	if err != nil {
		slog.Warn("reading the sparse tree's paths failed; reading the full tree", "error", err)
		return fs.Full
	}
	if covered {
		return fs.Sparse.Root
	}
	return fs.Full
}

func (fs *GitSparseTreeFS) Stat(ctx context.Context, p string) (string, *Stat, error) {
	return CallDirStat(ctx, fs.dirFor(ctx, p), p)
}

func (fs *GitSparseTreeFS) Exists(ctx context.Context, p string) (string, bool, error) {
	return CallDirExists(ctx, fs.dirFor(ctx, p), p)
}

func (fs *GitSparseTreeFS) ReadFile(ctx context.Context, p string) ([]byte, error) {
	return DirectoryReadFile(ctx, fs.dirFor(ctx, p), p)
}
