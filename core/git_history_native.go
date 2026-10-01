package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
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

// mountRefsWithLocalDonor answers a joined history read (log ranges, merge
// bases) from complete local object stores, without touching a remote mirror.
//
// A remote ref, or an owned shallow checkout's boundary, is covered when a
// plain local repository among the refs (typically the host checkout the
// agent started from) has that commit and a complete, non-partial history. A
// commit's ancestry is fixed by its SHA, so the donor's closure is exactly
// what the remote would serve. The view is private and lasts only for this read:
// no remote-to-local alias is published and the mirror is neither fetched nor
// locked. Anything not covered reports unhandled and takes the existing path.
func mountRefsWithLocalDonor(ctx context.Context, refs []*GitRef, fn func(*gitutil.GitCLI, []string) error) (bool, error) {
	var sources []*donorHistorySource
	seen := map[*LocalGitRepository]bool{}
	var needed []string
	shas := make([]string, len(refs))
	for i, ref := range refs {
		if ref == nil || ref.Ref == nil || !IsFullGitSHA(ref.Ref.SHA) {
			return false, nil
		}
		shas[i] = ref.Ref.SHA
		switch backend := ref.Backend.(type) {
		case *RemoteGitRef:
			needed = append(needed, ref.Ref.SHA)
		case *LocalGitRef:
			repo := backend.repo
			if repo == nil {
				return false, nil
			}
			if seen[repo] {
				continue
			}
			seen[repo] = true
			// Raw storage: a complete-history mount of an owned checkout
			// would hydrate through the remote, which this path avoids.
			src := &donorHistorySource{mount: func(ctx context.Context, fn func(*gitutil.GitCLI) error) error {
				return repo.mount(ctx, 0, false, nil, fn)
			}}
			if hs := repo.HistorySource.Self(); hs != nil {
				if hs.Ref == nil {
					return false, nil
				}
				src.anchor = hs.Ref.SHA
				src.gitDir = repo.nativeGitDir
			} else {
				src.donor = true
				src.gitDir = donorGitDir
			}
			sources = append(sources, src)
		default:
			return false, nil
		}
	}
	return joinDonorHistory(ctx, sources, needed, shas, fn)
}

// donorGitDir admits a complete local store as a history donor. Beyond the
// native layout gates (no shallow, alternates or partial history), it rejects
// info/grafts: Git prunes the real parents a graft hides, and the donor view
// walks raw parents, so such a donor could fail the read instead of leaving it
// to the existing join. Replace refs need no gate: prune and pack-objects
// disable them, so the commits they replace stay reachable, and the view
// ignores them.
func donorGitDir(ctx context.Context, root string) (string, error) {
	dir, err := nativeCommitGitDir(ctx, root)
	if err != nil {
		return "", err
	}
	grafts, err := os.ReadFile(filepath.Join(dir, "info", "grafts"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if len(grafts) != 0 {
		return "", nativeCommitUnsupportedReason("grafts")
	}
	return dir, nil
}

type donorHistorySource struct {
	mount func(context.Context, func(*gitutil.GitCLI) error) error
	// gitDir validates the mounted storage and returns its git directory.
	gitDir func(context.Context, string) (string, error)
	// donor: complete local history that may cover other refs.
	donor bool
	// anchor: an owned shallow boundary a donor must cover.
	anchor string
}

func joinDonorHistory(ctx context.Context, sources []*donorHistorySource, needed, shas []string, fn func(*gitutil.GitCLI, []string) error) (bool, error) {
	// Only engage when there is something to cover and something to cover it;
	// purely local comparisons keep their existing path.
	hasDonor := slices.ContainsFunc(sources, func(s *donorHistorySource) bool { return s.donor })
	hasAnchor := slices.ContainsFunc(sources, func(s *donorHistorySource) bool { return s.anchor != "" })
	if !hasDonor || (len(needed) == 0 && !hasAnchor) {
		return false, nil
	}
	var objects []string
	var donors []*gitutil.GitCLI
	anchors := make([]string, len(sources))
	handled := false
	var mountNext func(int) error
	mountNext = func(i int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i == len(sources) {
			for _, sha := range append(slices.Clone(needed), anchors...) {
				if sha == "" {
					continue
				}
				ok, err := donorHasCommit(ctx, donors, sha)
				if err != nil || !ok {
					return err
				}
			}
			handled = true
			return withGitObjectView(ctx, objects, "sha1", func(view *gitutil.GitCLI) error {
				return fn(view, shas)
			})
		}
		src := sources[i]
		return src.mount(ctx, func(git *gitutil.GitCLI) error {
			dir, err := src.gitDir(ctx, git.Dir())
			if err != nil {
				if nativeCommitFallback(err) {
					return nil // unsupported layout: leave the read to refJoin
				}
				return err
			}
			if src.donor {
				donors = append(donors, gitutil.NewGitCLI(gitutil.WithDir(git.Dir()), gitutil.WithGitDir(dir)))
			} else {
				// A boundary other than the recorded anchor is not ours to drop:
				// that history is uncovered, so leave the read to the existing
				// join. (Production sources validate it first, in nativeGitDir.)
				shallow, err := ownedShallowBoundary(dir, src.anchor)
				if err != nil {
					return nil //nolint:nilerr // uncovered, not a read failure
				}
				if shallow {
					anchors[i] = src.anchor
				}
			}
			objects = append(objects, filepath.Join(dir, "objects"))
			return mountNext(i + 1)
		})
	}
	err := mountNext(0)
	return handled, err
}

// donorHasCommit checks raw objects only: replacement refs must not let a
// donor vouch for a different commit.
func donorHasCommit(ctx context.Context, donors []*gitutil.GitCLI, sha string) (bool, error) {
	for _, donor := range donors {
		out, err := donor.New(gitutil.WithIgnoreError(), gitutil.WithArgs("--no-replace-objects")).Run(ctx, "cat-file", "-t", sha)
		if err != nil {
			return false, err
		}
		if strings.TrimSpace(string(out)) == "commit" {
			return true, nil
		}
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
