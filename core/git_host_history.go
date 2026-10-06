package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

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
	q.hostHistoryMu.Lock()
	defer q.hostHistoryMu.Unlock()
	if q.hostHistory == nil {
		q.hostHistory = make(map[hostHistoryKey]hostHistoryDonor)
	}
	q.hostHistory[hostHistoryKey{recipe, anchor}] = hostHistoryDonor{owner, path, state}
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
	q.hostHistoryMu.Lock()
	donor, ok := q.hostHistory[hostHistoryKey{recipe, parent.Self().Ref.SHA}]
	q.hostHistoryMu.Unlock()
	return donor, ok && donor.owner == md.ClientID, nil
}

func (q *Query) approvedHostCommitPack(ctx context.Context, parent dagql.ObjectResult[*GitRef], depth int) (_ *engineutil.GitCheckoutPack, rerr error) {
	// Ordinary promotion already has a warm depth-one mirror. Do not move
	// complete-history work into capture or redundantly repack shallow storage.
	if depth != 0 {
		return nil, nil
	}
	q.hostHistoryMu.Lock()
	empty := len(q.hostHistory) == 0
	q.hostHistoryMu.Unlock()
	if empty {
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
// inventory must equal the requested closure, including when a donor is buggy.
// Objects are checked at Git's default fsck severity, not --strict: the closure
// is pinned by SHA, so it is byte-identical to what the remote serves, and the
// remote path (which does not fsck fetches) accepts legacy objects such as
// zero-padded tree modes too.
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
	// index-pack verifies the pack and object hashes; its fsck is always
	// strict-level, so object checks are left to the default-severity fsck below.
	_, indexErr := runWorkspaceCommitGitInput(ctx, dest, nil, f, "index-pack", "--stdin")
	if err := errors.Join(indexErr, f.Close()); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dest, "HEAD"), []byte(sha+"\n"), 0644); err != nil {
		return err
	}
	if _, err := runWorkspaceCommitGit(ctx, dest, nil, "fsck", "--no-reflogs", sha); err != nil {
		return err
	}
	closure, err := runWorkspaceCommitGit(ctx, dest, nil, "rev-list", "--objects", "--no-object-names", sha)
	if err != nil {
		return err
	}
	objects, err := runWorkspaceCommitGit(ctx, dest, nil, "cat-file", "--batch-all-objects", "--batch-check=%(objectname)")
	if err != nil {
		return err
	}
	wanted := map[string]bool{}
	for _, id := range strings.Fields(closure) {
		wanted[id] = true
	}
	for _, id := range strings.Fields(objects) {
		if !wanted[id] {
			return fmt.Errorf("host history pack contains an object outside the authorized closure")
		}
		delete(wanted, id)
	}
	if len(wanted) != 0 {
		return fmt.Errorf("host history pack is incomplete")
	}
	for _, remote := range remotes {
		// Use the same remote writer as remote promotion, never donor config.
		if err := writeGitCheckoutRemote(ctx, gitutil.NewGitCLI(gitutil.WithGitDir(dest)), remote); err != nil {
			return err
		}
	}
	return nil
}
