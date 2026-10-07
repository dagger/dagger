package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
)

type workspaceWithResetArgs struct {
	Commit string
	Hard   bool `default:"false"`
}

// validate rejects malformed targets before withReset crosses the host
// approval boundary. Whether the target exists is only known once it has
// been resolved against the frozen repository.
func (args workspaceWithResetArgs) validate() error {
	if args.Commit == "" {
		return fmt.Errorf("withReset commit must not be empty")
	}
	if _, err := gitutil.ParseRevision(args.Commit); err != nil {
		return fmt.Errorf("withReset commit: %w", err)
	}
	return nil
}

// withReset crosses the host approval boundary once, like withCommit, then
// returns a composition over a frozen receiver: the reset checkout, with the
// previous working tree overlaid as uncommitted changes unless hard.
func (s *workspaceSchema) withReset(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceWithResetArgs) (inst dagql.ObjectResult[*core.Workspace], err error) {
	if err := args.validate(); err != nil {
		return inst, err
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	frozen, err := s.freeze(ctx, parent)
	if err != nil {
		return inst, err
	}
	// Start with the frozen workspace's reachable objects, then materialize
	// the target's reachable history. This validates the target locally and
	// prevents a later reset from recovering orphaned descendant commits.
	base, err := workspaceGitCheckout(ctx, srv, frozen)
	if err != nil {
		return inst, err
	}
	sha, err := resolveWorkspaceResetTarget(ctx, srv, base, args.Commit)
	if err != nil {
		return inst, err
	}
	var dir dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, base, &dir,
		dagql.Selector{Field: "asGit"},
		dagql.Selector{Field: "ref", Args: []dagql.NamedInput{{Name: "name", Value: dagql.NewString(sha)}}},
		dagql.Selector{Field: "tree", Args: []dagql.NamedInput{{Name: "depth", Value: dagql.NewInt(0)}}},
	); err != nil {
		return inst, fmt.Errorf("commit %s is not in this workspace's repository: %w", args.Commit, err)
	}
	var head dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, frozen, &head, dagql.Selector{Field: "git"}, dagql.Selector{Field: "head"}); err != nil {
		return inst, err
	}
	repo, err := gitRepositoryWithContents(ctx, srv, head.Self().Repo, dir)
	if err != nil {
		return inst, err
	}
	if err := srv.Select(ctx, repo, &inst,
		dagql.Selector{Field: "head"},
		dagql.Selector{Field: "asWorkspace", Args: []dagql.NamedInput{
			{Name: "cwd", Value: dagql.NewString(frozen.Self().Cwd)},
		}},
	); err != nil {
		return inst, err
	}

	if !args.Hard {
		// Like git reset --mixed, only HEAD moves: the complete approved
		// working tree is preserved, and diffing it against the target
		// commit's checkout leaves everything since that commit uncommitted.
		// Read the complete frozen source, not git.uncommitted.After: a clean
		// Git-ref workspace represents no changes as scratch -> scratch.
		// Source reads also exclude mounts, which are restored as
		// metadata below rather than becoming uncommitted files.
		root, err := workspaceRootfs(frozen.Self())
		if err != nil {
			return inst, err
		}
		// Directory-backed workspaces can carry .git, unlike Git-ref sources.
		// Keep the target's repository separate from the preserved worktree.
		var workingTree dagql.ObjectResult[*core.Directory]
		if err := srv.Select(ctx, root, &workingTree, dagql.Selector{
			Field: "withoutDirectory", Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString(".git")}},
		}); err != nil {
			return inst, err
		}
		var newBase dagql.ObjectResult[*core.Directory]
		if err := srv.Select(ctx, inst, &newBase, dagql.Selector{
			Field: "directory", Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString("/")}},
		}); err != nil {
			return inst, err
		}
		baseID, err := newBase.ID()
		if err != nil {
			return inst, err
		}
		var remaining dagql.ObjectResult[*core.Changeset]
		if err := srv.Select(ctx, workingTree, &remaining, dagql.Selector{
			Field: "changes", Args: []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](baseID)}},
		}); err != nil {
			return inst, err
		}
		remainingID, err := remaining.ID()
		if err != nil {
			return inst, err
		}
		var overlaid dagql.ObjectResult[*core.Workspace]
		if err := srv.Select(ctx, inst, &overlaid, dagql.Selector{
			Field: "withChanges", Args: []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](remainingID)}},
		}); err != nil {
			return inst, err
		}
		inst = overlaid
	}
	return checkpointWorkspaceMetadataComposition(ctx, srv, inst, frozen.Self(), frozen.Self().SelectedEnv())
}

// resolveWorkspaceResetTarget resolves a reset target (a commit hash or
// abbreviation, a ref name, or either followed by revision suffixes such as
// HEAD~1) against the frozen checkout's repository, like git reset <commit>.
//
// Only the resolved full hash enters the recorded recipe: named targets would
// otherwise check out that ref (e.g. switching to a branch) instead of moving
// HEAD, and the result must not depend on how the target was spelled.
func resolveWorkspaceResetTarget(ctx context.Context, srv *dagql.Server, checkout dagql.ObjectResult[*core.Directory], target string) (string, error) {
	if core.IsFullGitSHA(target) {
		return target, nil
	}
	var sha dagql.String
	if err := srv.Select(ctx, checkout, &sha,
		dagql.Selector{Field: "asGit"},
		dagql.Selector{Field: "ref", Args: []dagql.NamedInput{{Name: "name", Value: dagql.NewString(target)}}},
		dagql.Selector{Field: "commitSHA"},
	); err != nil {
		return "", fmt.Errorf("resolve %q in this workspace's repository: %w", target, err)
	}
	if !core.IsFullGitSHA(sha.String()) {
		return "", fmt.Errorf("resolve %q in this workspace's repository: unexpected commit %q", target, sha)
	}
	return sha.String(), nil
}
