package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type workspaceWithResetArgs struct {
	Commit string
	Hard   bool `default:"false"`
}

func (args workspaceWithResetArgs) validate() error {
	if !core.IsFullGitSHA(args.Commit) {
		return fmt.Errorf("withReset commit must be a full lowercase commit hash, got %q", args.Commit)
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
	var dir dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, base, &dir,
		dagql.Selector{Field: "asGit"},
		dagql.Selector{Field: "ref", Args: []dagql.NamedInput{{Name: "name", Value: dagql.NewString(args.Commit)}}},
		dagql.Selector{Field: "tree", Args: []dagql.NamedInput{{Name: "depth", Value: dagql.NewInt(0)}}},
	); err != nil {
		return inst, fmt.Errorf("commit %s is not in this workspace's repository: %w", args.Commit, err)
	}
	var head dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, frozen, &head, dagql.Selector{Field: "git"}, dagql.Selector{Field: "head"}); err != nil {
		return inst, err
	}
	repo, err := gitRepositoryWithDirectory(ctx, srv, head.Self().Repo, dir)
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
		var changes dagql.ObjectResult[*core.Changeset]
		if err := srv.Select(ctx, frozen, &changes,
			dagql.Selector{Field: "git"}, dagql.Selector{Field: "uncommitted"},
		); err != nil {
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
		if err := srv.Select(ctx, changes.Self().After, &remaining, dagql.Selector{
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
