package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
	gitsession "github.com/dagger/dagger/engine/session/git"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
	"github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type hostHistoryKey struct {
	recipe digest.Digest
	sha    string
}
type hostHistoryDonor struct{ owner, path, state string }

// hostHistoryRegistry holds a client's approved donors. A Query and its clones
// share one registry, so a donor registered through either is visible to both.
type hostHistoryRegistry struct {
	mu     sync.Mutex
	donors map[hostHistoryKey]hostHistoryDonor
}

// hostHistories returns the Query's registry, allocating it before it can be
// shared with a clone.
func (q *Query) hostHistories() *hostHistoryRegistry {
	q.hostHistoryMu.Lock()
	defer q.hostHistoryMu.Unlock()
	if q.hostHistory == nil {
		q.hostHistory = &hostHistoryRegistry{donors: map[hostHistoryKey]hostHistoryDonor{}}
	}
	return q.hostHistory
}

func (r *hostHistoryRegistry) register(key hostHistoryKey, donor hostHistoryDonor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.donors[key] = donor
}

func (r *hostHistoryRegistry) lookup(key hostHistoryKey) (hostHistoryDonor, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	donor, ok := r.donors[key]
	return donor, ok
}

func (r *hostHistoryRegistry) empty() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.donors) == 0
}

// RegisterCapturedHostHistory records an optional donor only after successful
// owning-client capture and approval. The caller must pass the exact captured
// remote anchor, not the checkout HEAD or an arbitrary later ref. No host IO is
// performed here; replay and other clients continue to use the remote recipe.
//
// It reports whether a donor was registered, and never fails: the donor is
// only an optimization, so a capture it does not recognize, or cannot key,
// registers nothing rather than failing the capture. Either outcome is
// recorded on the current span, so a donor that never engages is debuggable.
func (q *Query) RegisterCapturedHostHistory(ctx context.Context, repo dagql.ObjectResult[*GitRepository], owner, path, state, anchor, remoteURL string) bool {
	if repo.Self() == nil {
		return SkipHostHistoryDonor(ctx, "no captured repository", nil)
	}
	remote, ok := repo.Self().Backend.(*RemoteGitRepository)
	if !ok {
		return SkipHostHistoryDonor(ctx, "captured repository is not remote", nil)
	}
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return SkipHostHistoryDonor(ctx, "no client metadata", err)
	}
	if owner == "" || owner != md.ClientID {
		return SkipHostHistoryDonor(ctx, "caller is not the capturing owner", nil)
	}
	if path == "" || state == "" {
		return SkipHostHistoryDonor(ctx, "capture has no checkout path or state digest", nil)
	}
	// PackCommit and the importer handle SHA-1 only; a SHA-256 checkout keeps
	// the remote path.
	if len(anchor) != 40 || !IsFullGitSHA(anchor) {
		return SkipHostHistoryDonor(ctx, "captured anchor is not a full SHA-1", nil)
	}
	captured, ok := capturedGitRemote(remoteURL)
	if !ok {
		return SkipHostHistoryDonor(ctx, "captured remote URL is not parseable", nil)
	}
	if remote.URL.Remote() != captured {
		return SkipHostHistoryDonor(ctx, "captured remote route does not match the repository", nil)
	}
	recipe, err := repo.RecipeDigest(ctx)
	if err != nil {
		return SkipHostHistoryDonor(ctx, "repository recipe digest failed", err)
	}
	q.hostHistories().register(hostHistoryKey{recipe, anchor}, hostHistoryDonor{owner, path, state})
	trace.SpanFromContext(ctx).SetAttributes(attribute.Bool(hostHistoryDonorRegisteredAttr, true))
	return true
}

const (
	hostHistoryDonorRegisteredAttr = "git.history.donor_registered"
	hostHistoryDonorSkippedAttr    = "git.history.donor_skipped"
)

// SkipHostHistoryDonor records on the current span why a capture registered
// no approved host history donor, and the error behind it, if any. It always
// returns false, the registration result.
func SkipHostHistoryDonor(ctx context.Context, reason string, err error) bool {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String(hostHistoryDonorSkippedAttr, reason))
	if err != nil {
		span.RecordError(err)
	}
	return false
}

// capturedGitRemote spells a client-reported remote URL the way Query.git
// records it: an ssh:// remote without a user defaults to "git".
func capturedGitRemote(raw string) (string, bool) {
	u, err := gitutil.ParseURL(raw)
	if err != nil {
		return "", false
	}
	if u.Scheme == gitutil.SSHProtocol && u.User == nil {
		u.User = url.User("git")
	}
	return u.Remote(), true
}

func (q *Query) capturedHostHistory(ctx context.Context, parent dagql.ObjectResult[*GitRef]) (hostHistoryDonor, bool, error) {
	ref := parent.Self()
	if ref == nil || ref.Ref == nil || ref.Repo.Self() == nil {
		return hostHistoryDonor{}, false, nil
	}
	remote, ok := ref.Backend.(*RemoteGitRef)
	if !ok || remote.repo != ref.Repo.Self().Backend || (ref.Ref.Name != "" && ref.Ref.Name != ref.Ref.SHA) {
		return hostHistoryDonor{}, false, nil
	}
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return hostHistoryDonor{}, false, err
	}
	recipe, err := parent.Self().Repo.RecipeDigest(ctx)
	if err != nil {
		return hostHistoryDonor{}, false, err
	}
	donor, ok := q.hostHistories().lookup(hostHistoryKey{recipe, parent.Self().Ref.SHA})
	return donor, ok && donor.owner == md.ClientID, nil
}

func (q *Query) approvedHostCommitPack(ctx context.Context, parent dagql.ObjectResult[*GitRef], depth int) (_ *engineutil.GitCheckoutPack, rerr error) {
	// Ordinary promotion already has a warm depth-one mirror. Do not move
	// complete-history work into capture or redundantly repack shallow storage.
	if depth != 0 {
		return nil, nil
	}
	if q.hostHistories().empty() {
		return nil, nil
	}
	donor, ok, err := q.capturedHostHistory(ctx, parent)
	if err != nil || !ok {
		return nil, err
	}
	ctx, span := Tracer(ctx).Start(ctx, "git request approved host commit closure", telemetry.Internal())
	span.SetAttributes(attribute.Int("git.history.depth", depth))
	defer telemetry.EndWithCause(span, &rerr)
	conn, available, err := q.SpecificClientAttachableConn(ctx, donor.owner, SpecificClientAttachableConnOpts{IfAvailable: true})
	if err != nil {
		return nil, hostHistoryFallback(ctx, err)
	}
	if !available {
		return nil, nil
	}
	pack, err := engineutil.ReceiveGitCommitPack(ctx, gitsession.NewGitClient(conn), &gitsession.PackCommitRequest{CheckoutPath: donor.path, CommitSha: parent.Self().Ref.SHA})
	if cause := context.Cause(ctx); cause != nil {
		return nil, errors.Join(cause, pack.Close())
	}
	if err != nil {
		return nil, hostHistoryFallback(ctx, err)
	}
	return pack, nil
}

// hostHistoryFallback turns a failed optional donation into a remote fallback,
// recording why on the current span. Only the caller's own cancellation is
// returned: the donor's pack timeout also surfaces as DeadlineExceeded, so the
// error itself cannot tell the two apart. Any engine-local cleanup must already
// have succeeded; the remote path always starts from a fresh snapshot.
func hostHistoryFallback(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("git.history.donor_fallback", err.Error()))
	span.RecordError(err)
	return nil
}

// importApprovedHostCommitBase imports a donated pack into a new private
// snapshot. A pack the engine rejects is a donor failure: its snapshot is never
// committed and is released before returning nil, so the caller falls back to
// the remote without keeping any object from the rejected pack.
func (q *Query) importApprovedHostCommitBase(ctx context.Context, pack *engineutil.GitCheckoutPack, sha string, remotes []GitRemote) (child bkcache.MutableRef, rerr error) {
	defer func() {
		rerr = errors.Join(rerr, pack.Close())
		if rerr != nil && child != nil {
			rerr = errors.Join(rerr, child.Release(context.WithoutCancel(ctx)))
			child = nil
		}
	}()
	child, err := q.SnapshotManager().New(ctx, nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeGitCheckout),
		bkcache.WithDescription("owned approved host commit closure"))
	if err != nil {
		return nil, err
	}
	err = MountRef(ctx, child, func(dest string, _ *mount.Mount) error {
		return importHostCommitPack(ctx, dest, pack.BundlePath, sha, remotes)
	})
	if err == nil {
		return child, nil
	}
	releaseErr := child.Release(context.WithoutCancel(ctx))
	child = nil
	if releaseErr != nil {
		return nil, errors.Join(err, releaseErr)
	}
	return nil, hostHistoryFallback(ctx, err)
}

// Validate the received pack in an isolated object database before publishing
// any snapshot. No alternates, host refs or mutable mount paths survive. The
// inventory must equal the requested closure exactly, including when a donor
// is buggy: nothing from the donor's other objects, nothing missing.
//
// Validation stays proportional to what the remote path does, which neither
// fscks fetched packs nor lists them, so it never buffers object lists:
//
//   - index-pack verifies the pack and every object's hash. Objects are not
//     fsck-checked: the closure is pinned by SHA, so it is byte-identical to
//     what the remote serves, legacy objects such as zero-padded tree modes
//     included.
//   - A full closure walk (rev-list --objects, missing objects are errors)
//     proves the closure is in the store; its output is only counted.
//   - Every object in the store came from the pack, whose index counts its
//     entries, duplicates included. Equal counts then prove the store holds
//     exactly the closure. Git treats the empty tree as present even when no
//     store holds it, so when the closure has it, the index must too.
func importHostCommitPack(ctx context.Context, dest, packPath, sha string, remotes []GitRemote) (rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "git import approved host commit closure", telemetry.Internal())
	span.SetAttributes(attribute.Int("git.history.depth", 0))
	defer telemetry.EndWithCause(span, &rerr)
	if _, err := runWorkspaceCommitGit(ctx, dest, nil, "init", "--bare", "--template=", "--object-format=sha1", "--ref-format=files"); err != nil {
		return err
	}
	f, err := os.Open(packPath)
	if err != nil {
		return err
	}
	// Without --strict, index-pack does not fsck objects (its fsck would be
	// strict-level) and keeps duplicate entries in the index.
	indexed, indexErr := runWorkspaceCommitGitInput(ctx, dest, nil, f, "index-pack", "--stdin")
	if err := errors.Join(indexErr, f.Close()); err != nil {
		return err
	}
	kind, packHash, _ := strings.Cut(strings.TrimSpace(indexed), "\t")
	if kind != "pack" || len(packHash) != 40 || !IsFullGitSHA(packHash) {
		return fmt.Errorf("unexpected index-pack output %q", indexed)
	}
	index, err := readGitPackIndex(filepath.Join(dest, "objects", "pack", "pack-"+packHash+".idx"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dest, "HEAD"), []byte(sha+"\n"), 0644); err != nil {
		return err
	}
	closure := &gitObjectLineCounter{}
	if err := runWorkspaceCommitGitStream(ctx, dest, nil, nil, closure, "rev-list", "--objects", "--no-object-names", "--missing=error", sha); err != nil {
		return err
	}
	if !closure.valid() {
		return fmt.Errorf("unexpected rev-list output")
	}
	if closure.emptyTree {
		has, err := index.has(gitEmptyTreeSHA1)
		if err != nil {
			return err
		}
		if !has {
			return fmt.Errorf("host history pack is incomplete")
		}
	}
	if closure.objects != uint64(index.objects()) {
		// The closure is in the store, so the pack holds more entries than the
		// closure: objects outside it, or the same object twice.
		return fmt.Errorf("host history pack has %d objects, the authorized closure %d", index.objects(), closure.objects)
	}
	for _, remote := range remotes {
		// Use the same remote writer as remote promotion, never donor config.
		if err := writeGitCheckoutRemote(ctx, gitutil.NewGitCLI(gitutil.WithGitDir(dest)), remote); err != nil {
			return err
		}
	}
	return nil
}

// gitEmptyTreeSHA1 is the SHA-1 empty tree, which Git reports as present
// whether or not an object store holds it.
const gitEmptyTreeSHA1 = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// gitObjectLineCounter counts the object IDs rev-list prints, one per line,
// noting the empty tree, without retaining them. Writes never fail, so the
// command is never left blocked on a full pipe; malformed output is reported
// by valid afterwards.
type gitObjectLineCounter struct {
	objects   uint64
	emptyTree bool
	malformed bool
	line      [40]byte
	partial   int
}

func (c *gitObjectLineCounter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b != '\n' {
			if c.partial < len(c.line) {
				c.line[c.partial] = b
			}
			c.partial++
			continue
		}
		if c.partial != len(c.line) {
			c.malformed = true
		} else if string(c.line[:]) == gitEmptyTreeSHA1 {
			c.emptyTree = true
		}
		c.objects++
		c.partial = 0
	}
	return len(p), nil
}

func (c *gitObjectLineCounter) valid() bool { return !c.malformed && c.partial == 0 }

// gitPackIndex reads a version 2 SHA-1 pack index without loading it: the
// object count from its fan-out table, and lookups by binary search.
type gitPackIndex struct {
	path   string
	fanout [256]uint32
}

const gitPackIndexHeaderBytes = 8 + 256*4

func readGitPackIndex(path string) (*gitPackIndex, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	header := make([]byte, gitPackIndexHeaderBytes)
	if _, err := io.ReadFull(f, header); err != nil {
		return nil, fmt.Errorf("read pack index: %w", err)
	}
	if !bytes.Equal(header[:8], []byte{0xff, 't', 'O', 'c', 0, 0, 0, 2}) {
		return nil, fmt.Errorf("unsupported pack index format")
	}
	index := &gitPackIndex{path: path}
	for i := range index.fanout {
		index.fanout[i] = binary.BigEndian.Uint32(header[8+4*i:])
		if i > 0 && index.fanout[i] < index.fanout[i-1] {
			return nil, fmt.Errorf("corrupt pack index fan-out")
		}
	}
	return index, nil
}

func (x *gitPackIndex) objects() uint32 { return x.fanout[255] }

func (x *gitPackIndex) has(hexID string) (bool, error) {
	id, err := hex.DecodeString(hexID)
	if err != nil || len(id) != 20 {
		return false, fmt.Errorf("invalid SHA-1 object ID %q", hexID)
	}
	f, err := os.Open(x.path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	lo := uint32(0)
	if id[0] > 0 {
		lo = x.fanout[id[0]-1]
	}
	hi := x.fanout[id[0]]
	entry := make([]byte, 20)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if _, err := f.ReadAt(entry, gitPackIndexHeaderBytes+int64(mid)*20); err != nil {
			return false, fmt.Errorf("read pack index: %w", err)
		}
		switch c := bytes.Compare(entry, id); {
		case c == 0:
			return true, nil
		case c < 0:
			lo = mid + 1
		default:
			hi = mid
		}
	}
	return false, nil
}
