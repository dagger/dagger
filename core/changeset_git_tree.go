package core

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
)

// changesetGitTrees returns the Git refs whose canonical source trees
// ch.Before and ch.After are, read from their DirectoryGitTreeLazy (the lazy
// state GitRef.tree attaches to its Directory) as
// GitCommitChangesetNativeBase does: only a direct GitRef.tree result without
// .git qualifies (GitCommit.tree(discardGitDir: true) returns one), or for
// Before an empty directory, the empty tree a root commit is compared with
// (nil before). Their changed paths are then Git's tree diff (see
// gitTreeChangesetPaths), and neither tree needs materializing to list them.
// Evaluation keeps that lazy state, but a tree restored from storage keeps it
// only in encoded form, with no Lazy to read, and is walked.
func changesetGitTrees(ch *Changeset) (after, before *GitRef, ok bool) {
	if ch.Before.Self() == nil || ch.After.Self() == nil {
		return nil, nil, false
	}
	after = gitSourceTreeRef(ch.After.Self())
	if after == nil {
		return nil, nil, false
	}
	if _, empty := ch.Before.Self().Lazy.(*DirectoryScratchLazy); empty {
		return after, nil, true
	}
	before = gitSourceTreeRef(ch.Before.Self())
	return after, before, before != nil
}

// gitSourceTreeRef returns the ref dir is the canonical source tree of, if it
// is one.
func gitSourceTreeRef(dir *Directory) *GitRef {
	lazy, ok := dir.Lazy.(*DirectoryGitTreeLazy)
	if !ok || lazy.KeepGitDir {
		return nil
	}
	ref := lazy.Ref.Self()
	if ref == nil || ref.Ref == nil || ref.Backend == nil || ref.Repo.Self() == nil {
		return nil
	}
	if !lazy.DiscardGitDir && !ref.Repo.Self().DiscardGitDir {
		return nil
	}
	return ref
}

// gitTreeChangesetPaths computes the paths between the source trees of before
// (nil: the empty tree) and after from Git's tree diff, without mounting or
// walking either tree. False means Git can't answer for the materialized trees
// (see gitTreeChanges) or failed: the cause is recorded on the span as
// dagger.changeset.paths.git.fallback and as the wcprof marker
// changeset.paths.git.fallback[<reason>], and the caller walks the trees.
// Only the caller's cancellation surfaces. Success records walk=git, the
// family of markers the tree walks record (see recordChangesetWalk).
func gitTreeChangesetPaths(ctx context.Context, after, before *GitRef) (_ *ChangesetPaths, ok bool, rerr error) {
	start := phaseNow()
	ctx, span := Tracer(ctx).Start(ctx, "diff git trees", telemetry.Internal())
	defer func() {
		if nativeFallback(ctx, span, "dagger.changeset.paths.git.fallback", rerr) {
			ok, rerr = false, nil
		}
		if ok {
			recordChangesetWalk(ctx, "git", nil, start, nil)
		}
		telemetry.EndWithCause(span, &rerr)
	}()

	sameRepository := true
	if before != nil {
		afterRepo, err := after.Repo.RecipeDigest(ctx)
		if err != nil {
			return nil, false, err
		}
		beforeRepo, err := before.Repo.RecipeDigest(ctx)
		if err != nil {
			return nil, false, err
		}
		sameRepository = afterRepo == beforeRepo
	}
	paths, sameTree, err := gitTreeChanges(ctx, after, before, sameRepository)
	if err != nil {
		return nil, false, err
	}
	span.SetAttributes(
		attribute.Bool("dagger.changeset.paths.git.same_tree", sameTree),
		attribute.Int("dagger.changeset.paths.git.changed_paths", changesetPathCount(paths)),
	)
	return paths, true, nil
}

// gitTreeChanges diffs the trees of before (nil: the empty tree) and after
// with diff-tree in after's object storage. It reads only the tree objects
// of the delta (all of after's, against the empty tree), and none when the
// trees are identical (sameTree).
//
// The answer must be what a walk of the canonical checkouts would report, so
// it is refused, as a nativeCommitUnsupportedReason, where they are more than
// the trees' blobs or the objects are not at hand:
//   - a remote commit compared with one of another repository, whose objects
//     its mirror could only share across credentials;
//   - a comparison commit missing from after's storage (e.g. beyond an owned
//     shallow boundary);
//   - a changed gitlink, .gitmodules or .gitattributes, as for incremental
//     checkouts (see parseIncrementalGitCheckoutPlan): checkout initializes
//     submodules and converts content per attributes;
//   - repositories differing with submodules, whose relative URLs resolve
//     against each repository's own remote.
//
// With attributes unchanged, both sides convert alike, so an unchanged blob
// checks out the same. A changed blob converting to the same bytes (eol=crlf
// content already ending in CRLF) is reported as modified, where a walk
// would see no change.
func gitTreeChanges(ctx context.Context, after, before *GitRef, sameRepository bool) (paths *ChangesetPaths, sameTree bool, rerr error) {
	if !IsFullGitSHA(after.Ref.SHA) {
		return nil, false, nativeCommitUnsupportedReason("commit-format")
	}
	if before != nil && (before.Ref == nil || !IsFullGitSHA(before.Ref.SHA) || len(before.Ref.SHA) != len(after.Ref.SHA)) {
		return nil, false, nativeCommitUnsupportedReason("commit-format")
	}

	diff := func(ctx context.Context, git *gitutil.GitCLI) error {
		// Replace refs could substitute any object; checkouts never see them.
		// A missing object is a fallback, never a lazy fetch from a promisor.
		git = git.New(gitutil.WithArgs("--no-replace-objects"), gitutil.WithEnv("GIT_NO_LAZY_FETCH=1"))
		afterTree, err := gitCommitTree(ctx, git, after.Ref.SHA)
		if err != nil {
			return err
		}
		var beforeTree string
		if before == nil {
			// Git knows the empty tree without storing it.
			out, err := git.RunWithStdin(ctx, strings.NewReader(""), "hash-object", "-t", "tree", "--stdin")
			if err != nil {
				return err
			}
			beforeTree = strings.TrimSpace(string(out))
		} else {
			out, err := git.New(gitutil.WithIgnoreError()).Run(ctx, "cat-file", "-t", before.Ref.SHA)
			if err != nil {
				return err
			}
			if strings.TrimSpace(string(out)) != "commit" {
				return nativeCommitUnsupportedReason("comparison-objects")
			}
			beforeTree, err = gitCommitTree(ctx, git, before.Ref.SHA)
			if err != nil {
				return err
			}
		}
		if beforeTree == afterTree {
			paths, sameTree = &ChangesetPaths{}, true
			return nil
		}
		if !sameRepository {
			for _, tree := range []string{beforeTree, afterTree} {
				modules, err := git.New(gitutil.WithIgnoreError()).Run(ctx, "cat-file", "-t", tree+":.gitmodules")
				if err != nil {
					return err
				}
				if len(modules) != 0 {
					return nativeCommitUnsupportedReason("cross-repository-submodules")
				}
			}
		}
		out, err := git.Run(ctx, gitTreeDiffArgs(beforeTree, afterTree)...)
		if err != nil {
			return err
		}
		paths, err = parseGitTreeChangesetPaths(out)
		return err
	}

	switch backend := after.Backend.(type) {
	case *RemoteGitRef:
		if before == nil {
			return paths, sameTree, backend.mount(ctx, 1, false, diff)
		}
		beforeBackend, ok := before.Backend.(*RemoteGitRef)
		if !ok || !sameRepository {
			return nil, false, nativeCommitUnsupportedReason("cross-repository")
		}
		// One mirror with both commits at depth 1: each brings its whole
		// tree, which is all diff-tree reads.
		err := backend.repo.mount(ctx, 1, false, []GitRefBackend{backend, beforeBackend}, diff)
		return paths, sameTree, err
	case *LocalGitRef:
		// The same mount as the commit's metadata. The storage holds all of
		// a commit it has, and a comparison commit it lacks falls back.
		return paths, sameTree, backend.mount(ctx, 1, false, diff)
	default:
		return nil, false, nativeCommitUnsupportedReason("ref-backend")
	}
}

// gitTreeDiffArgs lists every changed leaf between two trees for
// parseGitTreeChangesetPaths, and with -t the changed trees themselves too:
// the directories a walk reports.
func gitTreeDiffArgs(before, after string) []string {
	return []string{"diff-tree", "-r", "-t", "-z", "--raw", "--no-renames", "--no-commit-id", before, after}
}

func gitCommitTree(ctx context.Context, git *gitutil.GitCLI, commit string) (string, error) {
	out, err := git.Run(ctx, "rev-parse", "--verify", "--end-of-options", commit+"^{tree}")
	if err != nil {
		return "", err
	}
	tree := strings.TrimSpace(string(out))
	if !IsFullGitSHA(tree) {
		return "", fmt.Errorf("invalid tree %q for commit %s", tree, commit)
	}
	return tree, nil
}

// parseGitTreeChangesetPaths reads `diff-tree -r -t --raw -z` output,
// ":<src mode> <dst mode> <src sha> <dst sha> <status>" NUL "<path>" NUL, into
// ChangesetPaths as a walk of the two checkouts reports them (see
// computeChangesetPathsDelta): files by path, directories with a trailing
// "/", every removed path in AllRemoved and the outermost ones in Removed. A
// directory on both sides is no change; a file replaced by a directory, or
// the reverse, is a removal and an addition; any other mode or type change
// is a modification.
func parseGitTreeChangesetPaths(changes []byte) (*ChangesetPaths, error) {
	entries := splitOnNul(changes)
	if len(entries)%2 != 0 {
		return nil, fmt.Errorf("invalid git tree diff")
	}
	const treeMode = "040000"
	entry := func(p, mode string) string {
		if mode == treeMode {
			return p + "/"
		}
		return p
	}
	var added, modified, allRemoved []string
	for i := 0; i < len(entries); i += 2 {
		header, p := entries[i], entries[i+1]
		fields := strings.Fields(strings.TrimPrefix(header, ":"))
		if !strings.HasPrefix(header, ":") || len(fields) != 5 {
			return nil, fmt.Errorf("invalid git tree diff entry")
		}
		srcMode, dstMode, status := fields[0], fields[1], fields[4]
		if path.Clean(p) != p || path.IsAbs(p) || p == ".." || strings.HasPrefix(p, "../") || gitMetaPath(p) {
			return nil, nativeCommitUnsupportedReason("unsafe-path")
		}
		if srcMode == "160000" || dstMode == "160000" {
			return nil, nativeCommitUnsupportedReason("gitlink-change")
		}
		if base := path.Base(p); base == ".gitattributes" || base == ".gitmodules" {
			return nil, nativeCommitUnsupportedReason("checkout-controls")
		}
		switch status {
		case "A":
			added = append(added, entry(p, dstMode))
		case "D":
			allRemoved = append(allRemoved, entry(p, srcMode))
		case "M", "T":
			switch {
			case srcMode == treeMode && dstMode == treeMode:
				// Only its contents changed, each listed on its own.
			case srcMode == treeMode || dstMode == treeMode:
				allRemoved = append(allRemoved, entry(p, srcMode))
				added = append(added, entry(p, dstMode))
			default:
				modified = append(modified, p)
			}
		default:
			return nil, fmt.Errorf("unexpected git diff status %q", status)
		}
	}
	slices.Sort(added)
	slices.Sort(modified)
	slices.Sort(allRemoved)
	return &ChangesetPaths{
		Added:      added,
		Modified:   modified,
		Removed:    collapseChildPaths(allRemoved),
		AllRemoved: allRemoved,
	}, nil
}

// changesetPathsTouchFiles reports whether paths change any file rather
// than only directories: the changes IsEmpty counts.
func changesetPathsTouchFiles(paths *ChangesetPaths) bool {
	for _, p := range slices.Concat(paths.Added, paths.Modified, paths.AllRemoved) {
		if !strings.HasSuffix(p, "/") {
			return true
		}
	}
	return false
}
