package core

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/util/gitutil"
)

// A direct-parent comparison is complete even with unknown older ancestry:
// either range excludes the shared boundary. Borrow only the child's store and
// its exact shallow file; never union independently shallow repositories.
func mountOwnedShallowParentHistory(ctx context.Context, refs []*GitRef, fn func(*gitutil.GitCLI, []string) error) (bool, error) {
	if len(refs) != 2 {
		return false, nil
	}
	for i, child := range refs {
		local, ok := child.Backend.(*LocalGitRef)
		if !ok || local.repo.HistorySource.Self() == nil {
			continue
		}
		parent := refs[1-i]
		matches, err := nativeParentHistoryCandidate(ctx, child, parent)
		if err != nil {
			return false, err
		}
		if !matches {
			continue
		}
		handled := false
		err = local.repo.mount(ctx, 0, false, nil, func(source *gitutil.GitCLI) error {
			dir, err := local.repo.nativeGitDir(ctx, source.Dir())
			if err != nil {
				return err
			}
			shallow, err := ownedShallowBoundary(dir, local.repo.HistorySource.Self().Ref.SHA)
			if err != nil || !shallow {
				return err
			}
			valid, err := validateNativeParentHistory(ctx, source, parent.Ref.SHA, child.Ref.SHA, true)
			if err != nil || !valid {
				return err
			}
			handled = true
			return withGitObjectView(ctx, []string{filepath.Join(dir, "objects")}, "sha1", func(view *gitutil.GitCLI) error {
				if err := copyGitShallowBoundary(dir, view.Dir()); err != nil {
					return err
				}
				return fn(view, []string{refs[0].Ref.SHA, refs[1].Ref.SHA})
			})
		})
		return handled, err
	}
	return false, nil
}

// nativeParentHistoryRefs borrows an owned child's objects for comparisons with
// its exact remote parent capability. It does not promote arbitrary remote refs
// or publish a remote-to-local alias: the substitution lasts only for this read.
func nativeParentHistoryRefs(ctx context.Context, refs []*GitRef) ([]*GitRef, error) {
	if len(refs) != 2 {
		return refs, nil
	}
	for i, candidate := range refs {
		local, ok := candidate.Backend.(*LocalGitRef)
		if !ok {
			continue
		}
		remote := refs[1-i]
		matches, err := nativeParentHistoryCandidate(ctx, candidate, remote)
		if err != nil || !matches {
			return refs, err
		}
		valid := false
		err = local.repo.mount(ctx, 0, false, nil, func(git *gitutil.GitCLI) error {
			var err error
			valid, err = validateNativeParentHistory(ctx, git, remote.Ref.SHA, candidate.Ref.SHA)
			return err
		})
		if err != nil || !valid {
			return refs, err
		}
		borrowed := *remote
		borrowed.Backend = &LocalGitRef{Ref: remote.Ref, repo: local.repo}
		result := append([]*GitRef(nil), refs...)
		result[1-i] = &borrowed
		return result, nil
	}
	return refs, nil
}

func nativeParentHistoryCandidate(ctx context.Context, child, remote *GitRef) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if child == nil || remote == nil {
		return false, nil
	}
	local, ok := child.Backend.(*LocalGitRef)
	if !ok || local.repo == nil || child.Ref == nil || child.Repo.Self() == nil || child.Repo.Self().Backend != local.repo {
		return false, nil
	}
	if remote.Ref == nil || remote.Repo.Self() == nil {
		return false, nil
	}
	switch remote.Backend.(type) {
	case *RemoteGitRef, *LocalGitRef:
	default:
		return false, nil
	}
	base := local.repo.CheckoutBase
	if base == nil || child.Ref.SHA != base.CommitSHA || base.Parent.Self() == nil {
		return false, nil
	}
	parent := base.Parent.Self()
	if parent.Ref == nil || parent.Repo.Self() == nil || parent.Ref.SHA != remote.Ref.SHA {
		return false, nil
	}
	switch parent.Backend.(type) {
	case *RemoteGitRef, *LocalGitRef:
	default:
		return false, nil
	}
	parentRecipe, err := parent.Repo.RecipeDigest(ctx)
	if err != nil {
		return false, err
	}
	remoteRecipe, err := remote.Repo.RecipeDigest(ctx)
	if err != nil {
		return false, err
	}
	return parentRecipe == remoteRecipe, nil
}

func validateNativeParentHistory(ctx context.Context, source *gitutil.GitCLI, parent, child string, ownedShallow ...bool) (bool, error) {
	if len(parent) != 40 || len(child) != 40 || !IsFullGitSHA(parent) || !IsFullGitSHA(child) {
		return false, nil
	}
	if _, err := nativeCommitGitDirWithShallow(ctx, source.Dir(), len(ownedShallow) > 0 && ownedShallow[0]); err != nil {
		if nativeCommitFallback(err) {
			return false, nil
		}
		return false, err
	}
	// Raw headers bypass both replacement refs and info/grafts. A stale or
	// forged annotation must not confer access to a different remote recipe.
	object, err := source.New(gitutil.WithArgs("--no-replace-objects")).Run(ctx, "cat-file", "commit", child)
	if err != nil {
		return false, err
	}
	headers, _, _ := strings.Cut(string(object), "\n\n")
	var parents []string
	for _, line := range strings.Split(headers, "\n") {
		if sha, ok := strings.CutPrefix(line, "parent "); ok {
			parents = append(parents, sha)
		}
	}
	return len(parents) == 1 && parents[0] == parent, nil
}
