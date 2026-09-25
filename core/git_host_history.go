package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
	gitsession "github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
	"github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/attribute"
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
func (q *Query) RegisterCapturedHostHistory(ctx context.Context, repo dagql.ObjectResult[*GitRepository], owner, path, state, anchor, remoteURL string) error {
	if repo.Self() == nil {
		return nil
	}
	remote, ok := repo.Self().Backend.(*RemoteGitRepository)
	if !ok {
		return nil
	}
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}
	if owner == "" || owner != md.ClientID || path == "" || state == "" || len(anchor) != 40 || !IsFullGitSHA(anchor) || remote.URL.Remote() != remoteURL {
		return fmt.Errorf("host history registration requires the captured owner's exact remote anchor")
	}
	recipe, err := repo.RecipeDigest(ctx)
	if err != nil {
		return err
	}
	q.hostHistoryMu.Lock()
	defer q.hostHistoryMu.Unlock()
	if q.hostHistory == nil {
		q.hostHistory = make(map[hostHistoryKey]hostHistoryDonor)
	}
	q.hostHistory[hostHistoryKey{recipe, anchor}] = hostHistoryDonor{owner, path, state}
	return nil
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
	conn, available, err := q.SpecificClientAttachableConn(ctx, donor.owner, SpecificClientAttachableConnOpts{IfAvailable: true})
	if err != nil {
		return nil, err
	}
	if !available {
		return nil, nil
	}
	ctx, span := Tracer(ctx).Start(ctx, "git request approved host commit closure", telemetry.Internal())
	span.SetAttributes(attribute.Int("git.history.depth", depth))
	defer telemetry.EndWithCause(span, &rerr)
	pack, err := engineutil.ReceiveGitCommitPack(ctx, gitsession.NewGitClient(conn), &gitsession.PackCommitRequest{CheckoutPath: donor.path, ExpectedStateDigest: donor.state, CommitSha: parent.Self().Ref.SHA, Depth: int32(depth)})
	if cause := context.Cause(ctx); cause != nil {
		return nil, errors.Join(cause, pack.Close())
	}
	if errors.Is(err, engineutil.ErrGitHistoryUnavailable) {
		return nil, nil
	}
	return pack, err
}

// Validate the received pack in an isolated object database before publishing
// any snapshot. No alternates, host refs or mutable mount paths survive. The
// inventory must equal the requested closure, including when a donor is buggy.
func importHostCommitPack(ctx context.Context, dest, packPath, sha string, remotes []GitRemote) (rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "git import approved host commit closure", telemetry.Internal())
	span.SetAttributes(attribute.Int("git.history.depth", 0))
	defer telemetry.EndWithCause(span, &rerr)
	if _, err := runWorkspaceCommitGit(ctx, dest, nil, "init", "--bare", "--template=", "--object-format=sha1"); err != nil {
		return err
	}
	f, err := os.Open(packPath)
	if err != nil {
		return err
	}
	_, indexErr := runWorkspaceCommitGitInput(ctx, dest, nil, f, "index-pack", "--stdin", "--strict")
	if err := errors.Join(indexErr, f.Close()); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dest, "HEAD"), []byte(sha+"\n"), 0644); err != nil {
		return err
	}
	if _, err := runWorkspaceCommitGit(ctx, dest, nil, "fsck", "--strict", "--no-reflogs", sha); err != nil {
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
