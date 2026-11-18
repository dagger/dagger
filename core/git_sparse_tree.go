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
	// gitSparseObjectsFile lists each path's mode and object ID, so a read can
	// be identified by the objects it selects without asking Git.
	gitSparseObjectsFile = "objects"
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

const gitignoreFile = ".gitignore"

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

// objectList returns "mode oid path\0" for every path of the tree that isn't
// a directory.
func (files *GitTreeFiles) objectList() []byte {
	var list bytes.Buffer
	for p, entry := range files.entries {
		list.WriteString(entry.mode + " " + entry.oid + " " + p)
		list.WriteByte(0)
	}
	return list.Bytes()
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
		// Git reads attributes from the index, so they apply as in a checkout,
		// and a copy with a gitignore filter reads ignore files from the tree.
		if entry.mode == gitModeSymlink || path.Base(p) == ".gitattributes" || path.Base(p) == gitignoreFile {
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
			if err := os.WriteFile(filepath.Join(root, gitSparseObjectsFile), files.objectList(), 0o600); err != nil {
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
	// objects holds each path's mode and object ID; nil for a tree written
	// before sparse trees listed them.
	objects map[string]gitTreeEntry
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
	m := parseGitSparseManifest(data)
	if err := MountRef(ctx, snapshot, func(dir string, _ *mount.Mount) error {
		objects, err := os.ReadFile(filepath.Join(dir, gitSparseObjectsFile))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		m.objects = parseGitSparseObjects(objects)
		return nil
	}, mountRefAsReadOnly); err != nil {
		return nil, fmt.Errorf("read sparse tree objects: %w", err)
	}
	return m, nil
}

func parseGitSparseObjects(data []byte) map[string]gitTreeEntry {
	objects := map[string]gitTreeEntry{}
	for rec := range bytes.SplitSeq(data, []byte{0}) {
		mode, rest, ok := bytes.Cut(rec, []byte{' '})
		if !ok {
			continue
		}
		oid, p, ok := bytes.Cut(rest, []byte{' '})
		if !ok {
			continue
		}
		objects[string(p)] = gitTreeEntry{mode: string(mode), oid: string(oid)}
	}
	return objects
}

func parseGitSparseManifest(data []byte) *gitSparseManifest {
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
	return m
}

// GitSparseReadSource is where a filtered read of a Git workspace's tree comes
// from.
type GitSparseReadSource int

const (
	// GitSparseReadFull reads the full checkout.
	GitSparseReadFull GitSparseReadSource = iota
	// GitSparseReadPlanned reads the sparse tree, which holds what it selects.
	GitSparseReadPlanned
	// GitSparseReadOwn reads a sparse tree of its own, written with only the
	// files it selects, identified by their object IDs (see ReadFiles).
	GitSparseReadOwn
)

// ReadSource picks where a copy of the directory dir with this filter gets
// exactly what it gets from the full tree. A sparse tree serves it when dir is
// a real directory of the tree, reached through no symlink, and the filter
// selects less than all that dir holds: this one if it holds every selected
// file and every .gitignore a gitignore filter reads, and otherwise one of the
// read's own. A dir the commit doesn't have, which fails the same way in both,
// is read from this one as well.
func (t *GitSparseTree) ReadSource(ctx context.Context, dir string, filter CopyFilter) (GitSparseReadSource, error) {
	m, err := t.loadManifest(ctx)
	if err != nil {
		return GitSparseReadFull, err
	}
	return m.readSource(dir, filter)
}

func (m *gitSparseManifest) readSource(dir string, filter CopyFilter) (GitSparseReadSource, error) {
	if m.submodules {
		return GitSparseReadFull, nil
	}
	if !m.tree.IsDir(dir) {
		if !m.coversPath(dir) {
			return GitSparseReadFull, nil
		}
		return GitSparseReadPlanned, nil
	}
	// A gitignore filter only drops what the others select, reading the
	// .gitignore files at and below dir and above it, all of which every
	// sparse tree writes. So both trees select the same files with it.
	selected, err := m.tree.SelectedFiles(dir, layercopy.Filter{Include: filter.Include, Exclude: filter.Exclude})
	if err != nil {
		return GitSparseReadFull, err
	}
	// A copy of everything in dir has the content digest of dir itself, so one
	// can stand in for the other. A view of dir in the full tree can follow a
	// symlink out of it, which a copy can't, so the view must exist first.
	if len(selected) == m.tree.FileCount(dir) {
		return GitSparseReadFull, nil
	}
	if filter.Gitignore {
		ignoresHeld, ok := m.gitignoresHeld(dir)
		if !ok {
			return GitSparseReadFull, nil
		}
		if !ignoresHeld {
			return GitSparseReadOwn, nil
		}
	}
	for _, p := range selected {
		if mark := m.entries[p]; mark != gitSparseEntryFile && mark != gitSparseEntrySymlink {
			return GitSparseReadOwn, nil
		}
	}
	return GitSparseReadPlanned, nil
}

// GitSparseReadFiles identifies a read by what it selects: each file's mode,
// object ID and path ("mode oid path"), with the attribute and ignore files
// that apply to them, and the directories it holds. Two commits that give a
// read the same list give it the same files.
type GitSparseReadFiles struct {
	Files []string
	Dirs  []string
}

// ReadFiles returns the list identifying a read that ReadSource sends to a
// tree of its own. It's nil for a tree written before sparse trees listed
// their objects.
func (t *GitSparseTree) ReadFiles(ctx context.Context, dir string, filter CopyFilter) (*GitSparseReadFiles, error) {
	m, err := t.loadManifest(ctx)
	if err != nil || m.objects == nil {
		return nil, err
	}
	return m.readFiles(dir, filter)
}

func (m *gitSparseManifest) readFiles(dir string, filter CopyFilter) (*GitSparseReadFiles, error) {
	dir = strings.TrimPrefix(path.Clean("/"+dir), "/")
	selected, selectedDirs, err := m.tree.SelectedPaths(dir, layercopy.Filter{Include: filter.Include, Exclude: filter.Exclude})
	if err != nil {
		return nil, err
	}
	paths := map[string]struct{}{}
	dirs := map[string]struct{}{}
	addParents := func(p string) {
		for parent := path.Dir(p); parent != "." && parent != "/"; parent = path.Dir(parent) {
			dirs[parent] = struct{}{}
		}
	}
	for _, p := range selected {
		paths[p] = struct{}{}
		addParents(p)
	}
	for _, d := range selectedDirs {
		dirs[d] = struct{}{}
		addParents(d)
	}
	if dir != "" {
		dirs[dir] = struct{}{}
		addParents(dir)
	}
	// Attributes apply to a file from its own directory and every directory
	// above it; a gitignore filter reads ignore files at, below and above dir.
	under := func(p, d string) bool { return d == "" || p == d || strings.HasPrefix(p, d+"/") }
	for p := range m.objects {
		base := path.Base(p)
		parent := path.Dir(p)
		if parent == "." {
			parent = ""
		}
		switch {
		case base == ".gitattributes":
			if under(dir, parent) {
				paths[p] = struct{}{}
				continue
			}
			for d := range dirs {
				if under(d, parent) {
					paths[p] = struct{}{}
					break
				}
			}
		case base == gitignoreFile && filter.Gitignore:
			if under(dir, parent) || under(p, dir) {
				paths[p] = struct{}{}
			}
		}
	}
	files := make([]string, 0, len(paths))
	for p := range paths {
		obj, ok := m.objects[p]
		if !ok {
			return nil, fmt.Errorf("sparse read files: no object for %q", p)
		}
		files = append(files, obj.mode+" "+obj.oid+" "+p)
	}
	slices.Sort(files)
	dirList := make([]string, 0, len(dirs))
	for d := range dirs {
		dirList = append(dirList, d)
	}
	slices.Sort(dirList)
	return &GitSparseReadFiles{Files: files, Dirs: dirList}, nil
}

// gitignoresHeld reports whether the sparse tree holds every .gitignore a
// gitignore filter on dir can read: those in dir and below it, and those in the
// directories above it. A tree written before sparse trees held them doesn't.
// It's not ok when one is a symlink, whose target no sparse tree needs to hold.
func (m *gitSparseManifest) gitignoresHeld(dir string) (held, ok bool) {
	dir = strings.TrimPrefix(path.Clean("/"+dir), "/")
	held = true
	for p, mark := range m.entries {
		if path.Base(p) != gitignoreFile {
			continue
		}
		parent := path.Dir(p)
		if parent == "." {
			parent = ""
		}
		reads := parent == "" || dir == parent || strings.HasPrefix(dir, parent+"/") ||
			dir == "" || strings.HasPrefix(p, dir+"/")
		if !reads {
			continue
		}
		switch mark {
		case gitSparseEntryFile:
		case gitSparseEntrySymlink:
			return false, false
		default:
			held = false
		}
	}
	return held, true
}

// coversPath reports whether looking p up in the sparse tree answers as the
// full tree does. A symlink on the way, or at p, could lead to a file the
// sparse tree doesn't have.
func (t *GitSparseTree) coversPath(ctx context.Context, p string) (bool, error) {
	m, err := t.loadManifest(ctx)
	if err != nil {
		return false, err
	}
	return m.coversPath(p), nil
}

func (m *gitSparseManifest) coversPath(p string) bool {
	if m.submodules {
		return false
	}
	p = strings.TrimPrefix(path.Clean("/"+filepath.ToSlash(p)), "/")
	if p == "" {
		return true
	}
	mark, inTree := m.entries[p]
	if inTree && mark != gitSparseEntryFile {
		return false
	}
	for parent := path.Dir(p); parent != "."; parent = path.Dir(parent) {
		if _, notDir := m.entries[parent]; notDir {
			return false
		}
	}
	return true
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

// gitBlobPools holds, per repository result, the objects readers are waiting
// to have fetched. Whoever of them holds the mirror next fetches all of them
// in one request, so reads that arrive together make one request between
// them. A repository result carries its credentials, so readers only pool
// with readers that could fetch the same objects.
var gitBlobPools sync.Map // pool key -> *gitBlobPool

type gitBlobPool struct {
	mu      sync.Mutex
	pending map[string]struct{}
}

func (pool *gitBlobPool) add(oids []string) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	for _, oid := range oids {
		pool.pending[oid] = struct{}{}
	}
}

func (pool *gitBlobPool) drain() []string {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	oids := make([]string, 0, len(pool.pending))
	for oid := range pool.pending {
		oids = append(oids, oid)
	}
	pool.pending = map[string]struct{}{}
	return oids
}

// SparseFiles writes files, each "mode oid path", and the directories dirs
// into a new snapshot under gitSparseTreeDir, fetching into the blobless
// mirror whichever of the objects it doesn't hold, along with those other
// readers of the same repository result, poolKey, are waiting for.
func (repo *RemoteGitRepository) SparseFiles(ctx context.Context, dag *dagql.Server, poolKey string, files, dirs []string) (_ *Directory, rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	// Every caller of this URL shares the mirror, so objects another caller
	// fetched are there without asking the remote. Listing the remote with
	// this caller's credentials, as resolving a ref does, establishes that it
	// may read the repository before anything is served from the mirror.
	if _, err := repo.Remote(ctx); err != nil {
		return nil, fmt.Errorf("sparse files: %w", err)
	}
	blobless, err := repo.withBloblessMirror(ctx, dag)
	if err != nil {
		return nil, err
	}
	var index bytes.Buffer
	oids := make([]string, 0, len(files))
	for _, f := range files {
		mode, rest, ok1 := strings.Cut(f, " ")
		oid, p, ok2 := strings.Cut(rest, " ")
		if !ok1 || !ok2 || !filepath.IsLocal(p) {
			return nil, fmt.Errorf("sparse files: bad entry %q", f)
		}
		if mode != gitModeGitlink {
			oids = append(oids, oid)
		}
		fmt.Fprintf(&index, "%s %s\t%s\x00", mode, oid, p)
	}
	poolAny, _ := gitBlobPools.LoadOrStore(poolKey, &gitBlobPool{pending: map[string]struct{}{}})
	pool := poolAny.(*gitBlobPool)
	pool.add(oids)

	checkoutRef, err := query.SnapshotManager().New(ctx, nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeGitCheckout),
		bkcache.WithDescription(fmt.Sprintf("git sparse files from %s", repo.URL.Remote())))
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil && checkoutRef != nil {
			checkoutRef.Release(context.WithoutCancel(ctx))
		}
	}()
	err = MountRef(ctx, checkoutRef, func(root string, _ *mount.Mount) error {
		err := blobless.mount(ctx, 1, false, nil, func(ctx context.Context, git *gitutil.GitCLI) error {
			err := fetchPooled(oids, pool.drain(),
				func(oids []string) ([]string, error) { return gitMissingObjects(ctx, git, oids) },
				func(oids []string) error {
					return blobless.fetchBlobs(ctx, git, strings.NewReader(strings.Join(oids, "\n")+"\n"))
				})
			if err != nil {
				return fmt.Errorf("fetch file contents: %w", err)
			}
			dest := filepath.Join(root, gitSparseTreeDir)
			if err := os.Mkdir(dest, 0o755); err != nil {
				return err
			}
			for _, d := range dirs {
				if !filepath.IsLocal(d) {
					return fmt.Errorf("sparse files: bad directory %q", d)
				}
				if err := os.MkdirAll(filepath.Join(dest, d), 0o755); err != nil {
					return err
				}
			}
			for _, f := range files {
				_, rest, _ := strings.Cut(f, " ")
				_, p, _ := strings.Cut(rest, " ")
				if err := os.MkdirAll(filepath.Join(dest, filepath.Dir(p)), 0o755); err != nil {
					return err
				}
			}
			indexFile := filepath.Join(root, "index")
			git = git.New(gitutil.WithWorkTree(dest), gitutil.WithIndexFile(indexFile))
			if _, err := git.RunWithStdin(ctx, &index, "update-index", "-z", "--add", "--index-info"); err != nil {
				return fmt.Errorf("build index: %w", err)
			}
			if _, err := git.Run(ctx, "checkout-index", "--all", "--force"); err != nil {
				return fmt.Errorf("write files: %w", err)
			}
			return os.Remove(indexFile)
		})
		if err != nil {
			return err
		}
		return normalizeGitCheckoutTimes(ctx, root, "")
	})
	if err != nil {
		return nil, fmt.Errorf("sparse files from %s: %w", repo.URL.Remote(), err)
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

// fetchPooled fetches whatever own and pooled, the objects other readers are
// waiting for, lack, in one request. If that fails, it fetches own's alone,
// so another reader's objects, which may be gone from the remote, can't fail
// this read; that reader asks for its own again when it holds the mirror.
func fetchPooled(own, pooled []string, missing func([]string) ([]string, error), fetch func([]string) error) error {
	lacking, err := missing(append(slices.Clone(pooled), own...))
	if err != nil || len(lacking) == 0 {
		return err
	}
	if fetch(lacking) == nil {
		return nil
	}
	lacking, err = missing(own)
	if err != nil || len(lacking) == 0 {
		return err
	}
	return fetch(lacking)
}

// gitMissingObjects returns which of oids the mirror doesn't hold, without
// fetching any of them.
func gitMissingObjects(ctx context.Context, git *gitutil.GitCLI, oids []string) ([]string, error) {
	if len(oids) == 0 {
		return nil, nil
	}
	oids = slices.Clone(oids)
	slices.Sort(oids)
	oids = slices.Compact(oids)
	out, err := git.New(gitutil.WithEnv("GIT_NO_LAZY_FETCH=1")).RunWithStdin(ctx, strings.NewReader(strings.Join(oids, "\n")+"\n"), "cat-file", "--batch-check=%(objectname)")
	if err != nil {
		return nil, fmt.Errorf("check objects: %w", err)
	}
	var missing []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if oid, ok := strings.CutSuffix(line, " missing"); ok {
			missing = append(missing, oid)
		}
	}
	return missing, nil
}
