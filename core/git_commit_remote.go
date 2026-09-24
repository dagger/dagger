package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
)

// Select by the exact ref recipe, not URL, SHA or filesystem equivalence. This
// shares promotion between reconciliation and commit without widening the
// authorization scope. The returned Directory owns all objects, not an alternate
// pointing into a mutable mirror or an operation-local mount.
func nativeCommitRepository(ctx context.Context, parent dagql.ObjectResult[*GitRef]) (*LocalGitRepository, error) {
	if local, ok := parent.Self().Backend.(*LocalGitRef); ok {
		return local.repo, nil
	}
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	// Source-only trees pin branch names to the resolved SHA. Select that
	// same recipe so reconciliation and construction share one promotion even
	// when the caller originally selected a named ref. Repository identity
	// (including auth and service bindings) is unchanged.
	var pinned dagql.ObjectResult[*GitRef]
	if err := srv.Select(ctx, parent.Self().Repo, &pinned, dagql.Selector{Field: "ref", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String(parent.Self().Ref.SHA)}}}); err != nil {
		return nil, err
	}
	var dir dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, pinned, &dir, dagql.Selector{Field: "__nativeCommitBase"}); err != nil {
		return nil, err
	}
	return &LocalGitRepository{Directory: dir}, nil
}

// GitRemoteCommitBase promotes only the pinned commit's reachable closure. The
// mirror is mutable and shared by URL (including across authentication recipes):
// neither copying its packs wholesale nor inheriting its refs is safe. Hydrate
// through the normal authenticated fetch path, then pack into private storage
// while the mirror lock is held. Publication never writes to the mirror.
func GitRemoteCommitBase(ctx context.Context, parent dagql.ObjectResult[*GitRef]) (_ *Directory, rerr error) {
	ref, ok := parent.Self().Backend.(*RemoteGitRef)
	if !ok || ref.Ref == nil || len(ref.SHA) != 40 || !IsFullGitSHA(ref.SHA) {
		return nil, nativeCommitUnsupportedReason("remote-ref")
	}
	ctx, span := Tracer(ctx).Start(ctx, "git promote remote commit base", telemetry.Internal())
	defer telemetry.EndWithCause(span, &rerr)
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	var child bkcache.MutableRef
	var result *Directory
	defer func() {
		if child != nil {
			rerr = errors.Join(rerr, child.Release(context.WithoutCancel(ctx)))
		}
		rerr = errors.Join(rerr, ctx.Err())
		if rerr != nil && result != nil {
			rerr = errors.Join(rerr, result.OnRelease(context.WithoutCancel(ctx)))
		}
	}()
	// This may unshallow the mirror, just as a retained legacy checkout does.
	// Never shift full-history hydration into Workspace.snapshot.
	err = ref.mount(ctx, 0, false, func(_ *gitutil.GitCLI) error {
		// mount holds both the mirror lock and its snapshot lease. Borrow an
		// actual read-only mount so Git cannot freshen inherited pack mtimes.
		return MountRef(ctx, ref.repo.Mirror.Self().snapshot, func(source string, _ *mount.Mount) error {
			if _, err := nativeCommitGitDir(ctx, source); err != nil {
				return err
			}
			child, err = query.SnapshotManager().New(ctx, nil,
				bkcache.WithRecordType(bkclient.UsageRecordTypeGitCheckout),
				bkcache.WithDescription("owned remote commit closure"))
			if err != nil {
				return err
			}
			return MountRef(ctx, child, func(dest string, _ *mount.Mount) error {
				return packRemoteCommitBase(ctx, source, dest, ref.SHA, MergeGitRemotes([]GitRemote{{Name: "origin", URL: ref.repo.URL.Remote()}}, parent.Self().Repo.Self().Remotes))
			})
		}, mountRefAsReadOnly)
	})
	if err != nil {
		return nil, err
	}
	snap, err := child.Commit(ctx)
	if err != nil {
		return nil, err
	}
	child = nil
	result = &Directory{Platform: query.Platform(), Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory])}
	result.SetPath("/")
	result.SetSnapshot(snap)
	return result, nil
}

// A non-thin pack contains exactly the selected history, even when unrelated
// objects are delta bases in the donor pack. Git reuses compressed objects where
// possible; the cost of traversal/packing is still real and traced separately.
func packRemoteCommitBase(ctx context.Context, source, dest, sha string, remotes []GitRemote) (rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "git pack remote commit closure", telemetry.Internal())
	defer telemetry.EndWithCause(span, &rerr)
	if len(sha) != 40 || !IsFullGitSHA(sha) {
		return fmt.Errorf("remote commit base requires a complete SHA-1")
	}
	gitDir, err := nativeCommitGitDir(ctx, source)
	if err != nil {
		return err
	}
	if _, err := runWorkspaceCommitGit(ctx, dest, nil, "init", "--bare", "--template=", "--object-format=sha1"); err != nil {
		return err
	}
	// pack-objects creates temporary files in its primary object database,
	// even with an absolute output prefix. Keep that database private too.
	env := []string{"GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1", "GIT_ALTERNATE_OBJECT_DIRECTORIES=" + strconv.Quote(filepath.Join(gitDir, "objects"))}
	if _, err := runWorkspaceCommitGitInput(ctx, dest, env, strings.NewReader(sha+"\n"),
		"pack-objects", "--revs", "--delta-base-offset", filepath.Join(dest, "objects", "pack", "pack")); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dest, "HEAD"), []byte(sha+"\n"), 0644); err != nil {
		return err
	}
	git := gitutil.NewGitCLI(gitutil.WithGitDir(dest))
	for _, remote := range remotes {
		if err := writeGitCheckoutRemote(ctx, git, remote); err != nil {
			return err
		}
	}
	return nil
}
