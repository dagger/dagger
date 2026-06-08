package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	ctdmount "github.com/containerd/containerd/v2/core/mount"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/util/gitutil"
	"golang.org/x/sys/unix"

	bkcache "github.com/dagger/dagger/engine/snapshots"
)

func newBareGitDirectory(ctx context.Context, description string, materialize func(string, *ctdmount.Mount) error) (_ *Directory, rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	cache := query.SnapshotManager()

	bkref, err := cache.New(ctx, nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeGitCheckout),
		bkcache.WithDescription(description))
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil && bkref != nil {
			bkref.Release(context.WithoutCancel(ctx))
		}
	}()

	if err := MountRef(ctx, bkref, materialize); err != nil {
		return nil, err
	}

	snap, err := bkref.Commit(ctx)
	if err != nil {
		return nil, err
	}
	bkref = nil
	defer func() {
		if rerr != nil {
			snap.Release(context.WithoutCancel(ctx))
		}
	}()

	dir := &Directory{
		Platform: query.Platform(),
		Dir:      new(LazyAccessor[string, *Directory]),
		Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
	}
	dir.Dir.setValue("/")
	dir.Snapshot.setValue(snap)
	return dir, nil
}

func bareOriginRemote(gitURL *gitutil.GitURL) string {
	if gitURL == nil {
		return ""
	}
	origin := *gitURL
	origin.Fragment = nil
	if origin.User != nil {
		switch origin.Scheme {
		case gitutil.HTTPProtocol, gitutil.HTTPSProtocol:
			origin.User = nil
		default:
			if _, ok := origin.User.Password(); ok {
				origin.User = url.User(origin.User.Username())
			}
		}
	}
	return origin.String()
}

func doGitBare(
	ctx context.Context,
	sourceGit *gitutil.GitCLI,
	bareGitDir string,
	remoteURL string,
	ref *gitutil.Ref,
	depth int,
	includeTags bool,
) error {
	if ref == nil {
		return fmt.Errorf("cannot create bare git repo: missing ref")
	}
	if ref.SHA == "" {
		return fmt.Errorf("cannot create bare git repo: ref %q has no resolved SHA", ref.Name)
	}

	sourceURL, err := sourceGit.URL(ctx)
	if err != nil {
		return fmt.Errorf("could not find git url: %w", err)
	}

	if _, err := gitutil.NewGitCLI().Run(ctx, "-c", "init.defaultBranch=main", "init", "--bare", "--quiet", bareGitDir); err != nil {
		return fmt.Errorf("failed to init bare git repo: %w", err)
	}

	git := sourceGit.New(
		gitutil.WithDir(bareGitDir),
		gitutil.WithGitDir(bareGitDir),
	)

	tmpref := "refs/dagger.tmp/" + identity.NewID()
	fetchArgs := []string{"fetch", "--no-tags", "--update-head-ok", "--force"}
	if depth > 0 {
		fetchArgs = append(fetchArgs, fmt.Sprintf("--depth=%d", depth))
	}
	fetchArgs = append(fetchArgs, sourceURL, ref.SHA+":"+tmpref)
	if err := runGitFetch(ctx, git, fetchArgs); err != nil {
		return fmt.Errorf("failed to fetch ref %s: %w", ref.SHA, err)
	}

	if ref.Name != "" {
		if _, err := git.Run(ctx, "update-ref", ref.Name, ref.SHA); err != nil {
			return fmt.Errorf("failed to update ref %s: %w", ref.Name, err)
		}
		if strings.HasPrefix(ref.Name, "refs/heads/") {
			if _, err := git.Run(ctx, "symbolic-ref", "HEAD", ref.Name); err != nil {
				return fmt.Errorf("failed to point HEAD at %s: %w", ref.Name, err)
			}
		} else if _, err := git.Run(ctx, "update-ref", "--no-deref", "HEAD", ref.SHA); err != nil {
			return fmt.Errorf("failed to detach HEAD at %s: %w", ref.SHA, err)
		}
	} else if _, err := git.Run(ctx, "update-ref", "--no-deref", "HEAD", ref.SHA); err != nil {
		return fmt.Errorf("failed to detach HEAD at %s: %w", ref.SHA, err)
	}

	if _, err := git.Run(ctx, "update-ref", "-d", tmpref); err != nil {
		return fmt.Errorf("failed to delete tmp ref: %w", err)
	}

	if includeTags {
		tagArgs := []string{"fetch", "--no-tags", "--force"}
		if depth > 0 {
			tagArgs = append(tagArgs, fmt.Sprintf("--depth=%d", depth))
		}
		tagArgs = append(tagArgs, sourceURL, "refs/tags/*:refs/tags/*")
		if err := runGitFetch(ctx, git, tagArgs); err != nil {
			return fmt.Errorf("failed to fetch tags: %w", err)
		}
	}

	if remoteURL != "" {
		if _, err := git.Run(ctx, "remote", "add", "origin", remoteURL); err != nil {
			return fmt.Errorf("failed to set remote origin to %s: %w", remoteURL, err)
		}
	}

	if _, err := git.Run(ctx, "reflog", "expire", "--all", "--expire=now"); err != nil {
		return fmt.Errorf("failed to expire reflog: %w", err)
	}

	if err := os.Remove(filepath.Join(bareGitDir, "FETCH_HEAD")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to remove FETCH_HEAD: %w", err)
	}

	return normalizeGitTimestamps(bareGitDir)
}

func runGitFetch(ctx context.Context, git *gitutil.GitCLI, args []string) error {
	if _, err := git.Run(ctx, args...); err != nil {
		if errors.Is(err, gitutil.ErrShallowNotSupported) {
			args = slicesDeleteDepth(args)
			_, err = git.Run(ctx, args...)
		}
		return err
	}
	return nil
}

func slicesDeleteDepth(args []string) []string {
	filtered := args[:0]
	for _, arg := range args {
		if strings.HasPrefix(arg, "--depth") {
			continue
		}
		filtered = append(filtered, arg)
	}
	return filtered
}

func normalizeGitTimestamps(root string) error {
	normalizedTime := []unix.Timespec{{Sec: 1}, {Sec: 1}}
	if err := filepath.WalkDir(root, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return unix.UtimesNanoAt(unix.AT_FDCWD, path, normalizedTime, unix.AT_SYMLINK_NOFOLLOW)
	}); err != nil {
		return fmt.Errorf("failed to normalize git timestamps: %w", err)
	}
	return nil
}
