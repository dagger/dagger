package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

// workspaceResetArgs are the inputs shared by the pure internal reset helpers.
// The public withReset adds hard, which only shapes the composition: the
// scratch-repository reset itself is always a clean checkout of the commit.
type workspaceResetArgs struct {
	Commit string
}

func (args workspaceResetArgs) validate() error {
	if !core.IsFullGitSHA(args.Commit) {
		return fmt.Errorf("withReset commit must be a full lowercase commit hash, got %q", args.Commit)
	}
	return nil
}

func (args workspaceResetArgs) selectors() []dagql.NamedInput {
	return []dagql.NamedInput{{Name: "commit", Value: dagql.NewString(args.Commit)}}
}

type workspaceWithResetArgs struct {
	Commit string
	Hard   bool `default:"false"`
}

// reset narrows the public args to the shared internal-helper form. The two
// structs stay separate rather than embedded: dagql decodes argument structs
// reflectively, and an unexported embedded struct's fields cannot be set that
// way.
func (args workspaceWithResetArgs) reset() workspaceResetArgs {
	return workspaceResetArgs{Commit: args.Commit}
}

// withReset crosses the host approval boundary once, like withCommit, then
// returns a composition over a frozen receiver: the reset checkout, with the
// previous working tree overlaid as uncommitted changes unless hard.
func (s *workspaceSchema) withReset(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceWithResetArgs) (inst dagql.ObjectResult[*core.Workspace], err error) {
	if err := args.reset().validate(); err != nil {
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
	var repo dagql.ObjectResult[*core.GitRepository]
	if err := srv.Select(ctx, frozen, &repo, dagql.Selector{
		Field: "__resetRepository", Args: args.reset().selectors(),
	}); err != nil {
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

func (s *workspaceSchema) resetDirectory(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceResetArgs) (inst dagql.ObjectResult[*core.Directory], err error) {
	if !parent.Self().IsValueWorkspace() {
		return inst, fmt.Errorf("reset requires a frozen workspace; call sync first")
	}
	if err := args.validate(); err != nil {
		return inst, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	var base dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, parent, &base, dagql.Selector{Field: "__commitBase"}); err != nil {
		return inst, err
	}
	dir, err := core.WorkspaceReset(ctx, base, args.Commit)
	if err != nil {
		return inst, fmt.Errorf("withReset: %w", err)
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
}

func (s *workspaceSchema) resetRepository(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceResetArgs) (dagql.ObjectResult[*core.GitRepository], error) {
	return workspaceRepositoryFromDirectory(ctx, parent, dagql.Selector{
		Field: "__resetDirectory", Args: args.selectors(),
	})
}
