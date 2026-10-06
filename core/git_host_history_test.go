package core

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/stretchr/testify/require"
)

func TestCapturedHostHistoryScope(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "host-history-scope")
	ctx, cache, srv := env.open(t)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRef]{}))
	md, err := engine.ClientMetadataFromContext(ctx)
	require.NoError(t, err)
	url, err := gitutil.ParseURL("https://example.test/repo.git")
	require.NoError(t, err)
	anchor := strings.Repeat("a", 40)
	makeRef := func(key, sha string) dagql.ObjectResult[*GitRef] {
		remote := &RemoteGitRepository{URL: url, AuthUsername: key}
		repo := env.attach(t, ctx, cache, srv, key+"-repo", &GitRepository{Backend: remote, Remote: &gitutil.Remote{}}).(dagql.ObjectResult[*GitRepository])
		repo, err = repo.WithContentDigest(ctx, hashutil.HashStrings("equal-repository-content"))
		require.NoError(t, err)
		ref := &gitutil.Ref{SHA: sha, Name: sha}
		return env.attach(t, ctx, cache, srv, key+"-ref", &GitRef{Repo: repo, Ref: ref, Backend: &RemoteGitRef{repo: remote, Ref: ref}}).(dagql.ObjectResult[*GitRef])
	}
	parent, other := makeRef("alice", anchor), makeRef("bob", anchor)
	q := &Query{} // No server/host available: registration cannot perform IO.
	require.True(t, q.RegisterCapturedHostHistory(ctx, parent.Self().Repo, md.ClientID, "/approved", anchor, url.Remote()))
	donor, ok, err := q.capturedHostHistory(ctx, parent)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "/approved", donor.path)
	_, ok, err = q.capturedHostHistory(ctx, other)
	require.NoError(t, err)
	require.False(t, ok, "equal content is not the same remote recipe")
	foreign := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "foreign", SessionID: md.SessionID})
	_, ok, err = q.capturedHostHistory(foreign, parent)
	require.NoError(t, err)
	require.False(t, ok, "donor routes never cross owners")
	require.False(t, q.RegisterCapturedHostHistory(foreign, parent.Self().Repo, md.ClientID, "/approved", anchor, url.Remote()),
		"only the capturing owner registers, and a mismatch never fails the capture")
	unrelated := &Query{}
	require.False(t, unrelated.RegisterCapturedHostHistory(ctx, parent.Self().Repo, md.ClientID, "/approved", anchor, "https://other.test/repo.git"),
		"an unrecognized route is not a donor, and never fails the capture")
	_, ok, err = unrelated.capturedHostHistory(ctx, parent)
	require.NoError(t, err)
	require.False(t, ok, "a different route registers no donor")
	_, ok, err = (&Query{}).capturedHostHistory(ctx, parent)
	require.NoError(t, err)
	require.False(t, ok, "new client lifecycle has no donor")
	pack, err := q.approvedHostCommitPack(ctx, parent, 1)
	require.NoError(t, err)
	require.Nil(t, pack, "ordinary promotion does not request host packs")
	original := parent.Self().Ref.SHA
	parent.Self().Ref.SHA = strings.Repeat("b", 40)
	parent.Self().Ref.Name = parent.Self().Ref.SHA
	_, ok, err = q.capturedHostHistory(ctx, parent)
	require.NoError(t, err)
	require.False(t, ok, "capability is for captured anchor only")
	parent.Self().Ref.SHA = original
	parent.Self().Ref.Name = "refs/heads/main"
	_, ok, err = q.capturedHostHistory(ctx, parent)
	require.NoError(t, err)
	require.False(t, ok, "only pinned remote sources may donate")
}

// Query.git records an ssh:// remote without a user as user "git". The
// client reports the host's own spelling; registration must neither fail the
// capture nor miss the donor because of that normalization.
func TestCapturedHostHistorySSHDefaultUser(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "host-history-ssh-user")
	ctx, cache, srv := env.open(t)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRef]{}))
	md, err := engine.ClientMetadataFromContext(ctx)
	require.NoError(t, err)
	recorded, err := gitutil.ParseURL("ssh://git@example.test/repo.git")
	require.NoError(t, err)
	anchor := strings.Repeat("a", 40)
	remote := &RemoteGitRepository{URL: recorded}
	repo := env.attach(t, ctx, cache, srv, "ssh-repo", &GitRepository{Backend: remote, Remote: &gitutil.Remote{}}).(dagql.ObjectResult[*GitRepository])
	ref := &gitutil.Ref{SHA: anchor, Name: anchor}
	parent := env.attach(t, ctx, cache, srv, "ssh-ref", &GitRef{Repo: repo, Ref: ref, Backend: &RemoteGitRef{repo: remote, Ref: ref}}).(dagql.ObjectResult[*GitRef])
	q := &Query{}
	require.True(t, q.RegisterCapturedHostHistory(ctx, repo, md.ClientID, "/approved", anchor, "ssh://example.test/repo.git"))
	_, ok, err := q.capturedHostHistory(ctx, parent)
	require.NoError(t, err)
	require.True(t, ok)
}

// A Query and its clones share one donor registry, even when none has been
// registered at clone time: a donor registered through either is visible to
// both.
func TestCapturedHostHistorySharedWithClones(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "host-history-clone")
	ctx, cache, srv := env.open(t)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRef]{}))
	md, err := engine.ClientMetadataFromContext(ctx)
	require.NoError(t, err)
	url, err := gitutil.ParseURL("https://example.test/repo.git")
	require.NoError(t, err)
	makeRef := func(key, sha string) dagql.ObjectResult[*GitRef] {
		remote := &RemoteGitRepository{URL: url, AuthUsername: key}
		repo := env.attach(t, ctx, cache, srv, key+"-repo", &GitRepository{Backend: remote, Remote: &gitutil.Remote{}}).(dagql.ObjectResult[*GitRepository])
		ref := &gitutil.Ref{SHA: sha, Name: sha}
		return env.attach(t, ctx, cache, srv, key+"-ref", &GitRef{Repo: repo, Ref: ref, Backend: &RemoteGitRef{repo: remote, Ref: ref}}).(dagql.ObjectResult[*GitRef])
	}
	viaClone, viaOriginal := makeRef("clone", strings.Repeat("a", 40)), makeRef("original", strings.Repeat("b", 40))
	q := &Query{}
	clone := q.Clone()
	require.True(t, clone.RegisterCapturedHostHistory(ctx, viaClone.Self().Repo, md.ClientID, "/approved", viaClone.Self().Ref.SHA, url.Remote()))
	require.True(t, q.RegisterCapturedHostHistory(ctx, viaOriginal.Self().Repo, md.ClientID, "/approved", viaOriginal.Self().Ref.SHA, url.Remote()))
	for _, query := range []*Query{q, clone, clone.Clone()} {
		for _, parent := range []dagql.ObjectResult[*GitRef]{viaClone, viaOriginal} {
			_, ok, err := query.capturedHostHistory(ctx, parent)
			require.NoError(t, err)
			require.True(t, ok)
		}
	}
}

// hostHistoryTestPack returns a donated pack of the anchor's closure, as
// PackCommit produces it, or a bad one: "unrelated" adds objects outside the
// authorized closure, "incomplete" lacks one of its blobs, "duplicate" holds
// one of its objects twice, "corrupt" has a damaged byte, and "truncated" is
// not a valid pack at all. "empty-tree" is a valid closure holding the empty
// tree; "empty-tree-stray" swaps that tree for an object outside the closure.
func hostHistoryTestPack(t *testing.T, kind string) (source, pack, anchor string) {
	t.Helper()
	if strings.HasPrefix(kind, "empty-tree") {
		source = t.TempDir()
		gitMirrorTestRun(t, source, "init", "--quiet", "--bare")
		commit, err := runWorkspaceCommitGit(t.Context(), source, nil, "commit-tree", gitEmptyTreeSHA1, "-m", "empty")
		require.NoError(t, err)
		anchor = strings.TrimSpace(commit)
		ids := []string{anchor, gitEmptyTreeSHA1}
		if kind == "empty-tree-stray" {
			stray, err := runWorkspaceCommitGitInput(t.Context(), source, nil, strings.NewReader("private\n"), "hash-object", "-w", "--stdin")
			require.NoError(t, err)
			ids[1] = strings.TrimSpace(stray)
		}
		return source, hostHistoryRawPack(t, source, ids), anchor
	}
	source, _, anchor = gitMirrorTestSource(t)
	switch kind {
	case "incomplete", "duplicate":
		ids := strings.Fields(gitMirrorTestRun(t, source, "rev-list", "--objects", "--no-object-names", anchor))
		blob := gitMirrorTestRun(t, source, "rev-parse", anchor+":small")
		require.Contains(t, ids, blob)
		if kind == "incomplete" {
			ids = slices.DeleteFunc(ids, func(id string) bool { return id == blob })
		} else {
			ids = append(ids, blob)
		}
		return source, hostHistoryRawPack(t, source, ids), anchor
	}
	prefix := filepath.Join(t.TempDir(), "pack")
	input := anchor + "\n"
	if kind == "unrelated" {
		gitMirrorTestRun(t, source, "checkout", "--orphan", "secret")
		gitMirrorTestRun(t, source, "-c", "user.name=Dagger", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "private")
		input += gitMirrorTestRun(t, source, "rev-parse", "HEAD") + "\n"
	}
	hash, err := runWorkspaceCommitGitInput(t.Context(), source, nil, strings.NewReader(input), "pack-objects", "--revs", prefix)
	require.NoError(t, err)
	pack = prefix + "-" + strings.TrimSpace(hash) + ".pack"
	switch kind {
	case "truncated":
		require.NoError(t, os.WriteFile(pack, []byte("PACK"), 0600))
	case "corrupt":
		data, err := os.ReadFile(pack)
		require.NoError(t, err)
		data[len(data)/2] ^= 0xff
		require.NoError(t, os.WriteFile(pack, data, 0600))
	}
	return source, pack, anchor
}

// hostHistoryRawPack writes an undeltified pack of exactly these objects, in
// order and duplicates included, as a buggy or hostile donor could send it.
func hostHistoryRawPack(t *testing.T, source string, ids []string) string {
	t.Helper()
	types := map[string]byte{"commit": 1, "tree": 2, "blob": 3, "tag": 4}
	var pack bytes.Buffer
	pack.WriteString("PACK")
	require.NoError(t, binary.Write(&pack, binary.BigEndian, [2]uint32{2, uint32(len(ids))}))
	for _, id := range ids {
		typ := gitMirrorTestRun(t, source, "cat-file", "-t", id)
		data, err := runWorkspaceCommitGit(t.Context(), source, nil, "cat-file", typ, id)
		require.NoError(t, err)
		size := len(data)
		header := types[typ]<<4 | byte(size&0x0f)
		for size >>= 4; size > 0; size >>= 7 {
			pack.WriteByte(header | 0x80)
			header = byte(size & 0x7f)
		}
		pack.WriteByte(header)
		z := zlib.NewWriter(&pack)
		_, err = z.Write([]byte(data))
		require.NoError(t, err)
		require.NoError(t, z.Close())
	}
	sum := sha1.Sum(pack.Bytes())
	pack.Write(sum[:])
	path := filepath.Join(t.TempDir(), "raw.pack")
	require.NoError(t, os.WriteFile(path, pack.Bytes(), 0600))
	return path
}

// The importer rejects every invalid pack; the caller turns a rejection into
// a remote fallback (see TestApprovedHostCommitBaseFallback).
func TestImportHostCommitPack(t *testing.T) {
	for kind, rejected := range map[string]string{
		"complete":   "",
		"empty-tree": "",
		// More entries than the closure: stray objects or duplicates.
		"unrelated": "the authorized closure",
		"duplicate": "the authorized closure",
		// The empty tree is present to Git without being stored; equal counts
		// must not hide a stray object in its place.
		"empty-tree-stray": "incomplete",
		// The closure walk fails on any missing object.
		"incomplete": "rev-list",
		"missing":    "rev-list",
		// index-pack verifies the pack and object hashes.
		"corrupt":   "index-pack",
		"truncated": "index-pack",
		"cancelled": "",
	} {
		t.Run(kind, func(t *testing.T) {
			source, pack, anchor := hostHistoryTestPack(t, kind)
			if kind == "missing" {
				anchor = strings.Repeat("f", 40)
			}
			ctx := t.Context()
			if kind == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			dest := t.TempDir()
			err := importHostCommitPack(ctx, dest, pack, anchor, []GitRemote{{Name: "origin", URL: "https://example.test/repo.git"}})
			switch {
			case kind == "cancelled":
				require.Error(t, err)
				return
			case rejected != "":
				require.ErrorContains(t, err, rejected)
				return
			}
			require.NoError(t, err)
			closure := gitMirrorTestRun(t, source, "rev-list", "--objects", "--no-object-names", anchor)
			require.NoError(t, os.RemoveAll(source))
			require.NoError(t, os.Remove(pack))
			gitMirrorTestRun(t, dest, "fsck", "--full", "--strict")
			require.Equal(t, anchor, gitMirrorTestRun(t, dest, "rev-parse", "HEAD"))
			require.Empty(t, gitMirrorTestRun(t, dest, "for-each-ref"))
			require.NoFileExists(t, filepath.Join(dest, "objects", "info", "alternates"))
			stored := strings.Fields(gitMirrorTestRun(t, dest, "cat-file", "--batch-all-objects", "--batch-check=%(objectname)"))
			require.ElementsMatch(t, strings.Fields(closure), stored, "the store holds exactly the closure")
		})
	}
}

// Objects with valid hashes but malformed content are not fsck-checked, but
// the closure walk still parses every commit and tree, so a tree Git cannot
// read is rejected.
func TestImportHostCommitPackRejectsMalformedTree(t *testing.T) {
	source := t.TempDir()
	gitMirrorTestRun(t, source, "init", "--quiet", "--bare")
	tree, err := runWorkspaceCommitGitInput(t.Context(), source, nil, strings.NewReader("not a tree"), "hash-object", "-t", "tree", "--literally", "-w", "--stdin")
	require.NoError(t, err)
	commit, err := runWorkspaceCommitGit(t.Context(), source, nil, "commit-tree", strings.TrimSpace(tree), "-m", "malformed")
	if err != nil {
		t.Skipf("git refuses to commit a malformed tree: %v", err)
	}
	sha := strings.TrimSpace(commit)
	pack := hostHistoryRawPack(t, source, []string{sha, strings.TrimSpace(tree)})
	require.ErrorContains(t, importHostCommitPack(t.Context(), t.TempDir(), pack, sha, nil), "rev-list")
}

// Legacy objects that only --strict fsck rejects (e.g. zero-padded tree modes,
// common in old histories) are accepted by the remote path, which does not fsck
// fetched packs. The donated closure is pinned by SHA and so byte-identical;
// the importer must accept it too.
func TestImportHostCommitPackAcceptsLegacyHistory(t *testing.T) {
	source := t.TempDir()
	gitMirrorTestRun(t, source, "init", "--quiet", "--bare")
	blob, err := runWorkspaceCommitGitInput(t.Context(), source, nil, strings.NewReader("hello\n"), "hash-object", "-w", "--stdin")
	require.NoError(t, err)
	raw, err := hex.DecodeString(strings.TrimSpace(blob))
	require.NoError(t, err)
	var tree bytes.Buffer
	tree.WriteString("0100644 file\x00") // zero-padded mode, as written by old tools
	tree.Write(raw)
	treeID, err := runWorkspaceCommitGitInput(t.Context(), source, nil, &tree, "hash-object", "-t", "tree", "--literally", "-w", "--stdin")
	require.NoError(t, err)
	commit, err := runWorkspaceCommitGitInput(t.Context(), source, []string{
		"GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@example.com", "GIT_COMMITTER_NAME=a", "GIT_COMMITTER_EMAIL=a@example.com",
	}, nil, "commit-tree", strings.TrimSpace(treeID), "-m", "legacy")
	require.NoError(t, err)
	sha := strings.TrimSpace(commit)
	gitMirrorTestRun(t, source, "fsck")

	require.NoError(t, packRemoteCommitBaseDepth(t.Context(), source, t.TempDir(), sha, nil, 0))

	prefix := filepath.Join(t.TempDir(), "pack")
	hash, err := runWorkspaceCommitGitInput(t.Context(), source, nil, strings.NewReader(sha+"\n"), "pack-objects", "--revs", prefix)
	require.NoError(t, err)
	dest := t.TempDir()
	require.NoError(t, importHostCommitPack(t.Context(), dest, prefix+"-"+strings.TrimSpace(hash)+".pack", sha, nil))
	require.Equal(t, sha, gitMirrorTestRun(t, dest, "rev-parse", "HEAD"))
}

type hostHistoryObservedManager struct {
	bkcache.SnapshotManager
	refs     []*hostHistoryObservedRef
	afterNew func()
}

func (m *hostHistoryObservedManager) New(ctx context.Context, parent bkcache.ImmutableRef, opts ...bkcache.RefOption) (bkcache.MutableRef, error) {
	ref, err := m.SnapshotManager.New(ctx, parent, opts...)
	if err != nil {
		return nil, err
	}
	observed := &hostHistoryObservedRef{MutableRef: ref}
	m.refs = append(m.refs, observed)
	if m.afterNew != nil {
		m.afterNew()
	}
	return observed, nil
}

type hostHistoryObservedRef struct {
	bkcache.MutableRef
	releases, commits int
}

func (r *hostHistoryObservedRef) Release(ctx context.Context) error {
	r.releases++
	return r.MutableRef.Release(ctx)
}

func (r *hostHistoryObservedRef) Commit(ctx context.Context) (bkcache.ImmutableRef, error) {
	r.commits++
	return r.MutableRef.Commit(ctx)
}

// A donation the engine rejects falls back to the remote: no snapshot is
// returned, the partial import is released uncommitted, and the donated pack
// is removed. Only the caller's own cancellation is still an error.
func TestApprovedHostCommitBaseFallback(t *testing.T) {
	for _, kind := range []string{"complete", "unrelated", "truncated", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			ctx, _, _, _, server := executionFixture(t)
			observed := &hostHistoryObservedManager{SnapshotManager: server.cacheManager}
			server.cacheManager = observed
			query, err := CurrentQuery(ctx)
			require.NoError(t, err)
			_, pack, anchor := hostHistoryTestPack(t, kind)
			if kind == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				observed.afterNew = cancel
			}
			donated := &engineutil.GitCheckoutPack{HeadSHA: anchor, ObjectFormat: "sha1", BundlePath: pack}
			child, err := query.importApprovedHostCommitBase(ctx, donated, anchor, []GitRemote{{Name: "origin", URL: "https://example.test/repo.git"}})
			require.NoFileExists(t, pack, "the donated pack never outlives the attempt")
			require.Len(t, observed.refs, 1)
			ref := observed.refs[0]
			switch kind {
			case "complete":
				require.NoError(t, err)
				require.Equal(t, bkcache.MutableRef(ref), child)
				snap, err := child.Commit(ctx)
				require.NoError(t, err)
				defer snap.Release(context.Background())
				require.Equal(t, anchor, gitMirrorTestRun(t, testutil.Root(t, snap), "rev-parse", "HEAD"))
				return
			case "cancelled":
				require.ErrorIs(t, err, context.Canceled)
			default:
				require.NoError(t, err, "rejected donations fall back to the remote")
			}
			require.Nil(t, child)
			require.Equal(t, 1, ref.releases, "a rejected import is discarded")
			require.Zero(t, ref.commits, "nothing from a rejected pack is published")
		})
	}
}
