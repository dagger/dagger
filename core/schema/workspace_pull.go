package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

type workspaceCommitsFromArgs struct {
	Source     dagql.ID[*core.Workspace]
	Commits    []string `default:"[]"`
	MaxCommits int      `default:"100"`
}

// workspacePullArgs records the resolved committer only on the internal,
// cacheable pull helpers; public calls sample the caller's Git config.
type workspacePullArgs struct {
	Source         dagql.ID[*core.Workspace]
	Commits        []string `default:"[]"`
	MaxCommits     int      `default:"100"`
	CommitterName  string
	CommitterEmail string
}

func (args workspacePullArgs) selectors() []dagql.NamedInput {
	commits := make(dagql.ArrayInput[dagql.String], len(args.Commits))
	for i, sha := range args.Commits {
		commits[i] = dagql.NewString(sha)
	}
	return []dagql.NamedInput{
		{Name: "source", Value: args.Source},
		{Name: "commits", Value: commits},
		{Name: "maxCommits", Value: dagql.NewInt(args.MaxCommits)},
		{Name: "committerName", Value: dagql.NewString(args.CommitterName)},
		{Name: "committerEmail", Value: dagql.NewString(args.CommitterEmail)},
	}
}

func (args workspacePullArgs) opts() core.WorkspacePullOpts {
	return core.WorkspacePullOpts{Commits: args.Commits, MaxCommits: args.MaxCommits, CommitterName: args.CommitterName, CommitterEmail: args.CommitterEmail}
}

// Capture local receivers at the explicit integration boundary, then select
// pure helpers over pinned values. Source capture is always explicit.
func (s *workspaceSchema) pullInputs(ctx context.Context, receiver dagql.ObjectResult[*core.Workspace], args workspaceCommitsFromArgs) (dagql.ObjectResult[*core.Workspace], workspacePullArgs, error) {
	resolved := workspacePullArgs{Source: args.Source, Commits: args.Commits, MaxCommits: args.MaxCommits}
	if err := resolved.opts().ValidateSelection(); err != nil {
		return receiver, resolved, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return receiver, resolved, err
	}
	source, err := args.Source.Load(ctx, srv)
	if err != nil {
		return receiver, resolved, err
	}
	if !source.Self().IsValueWorkspace() {
		return receiver, resolved, fmt.Errorf("pulling requires a frozen source workspace; call snapshot on the source first")
	}
	receiver, err = s.freeze(ctx, receiver)
	if err != nil {
		return receiver, resolved, err
	}
	resolved.CommitterName, resolved.CommitterEmail, err = workspaceGitAuthor(ctx)
	if err != nil {
		return receiver, resolved, err
	}
	if err := validateWorkspaceGitAuthor(resolved.CommitterName, resolved.CommitterEmail); err != nil {
		return receiver, resolved, err
	}
	source, err = s.freeze(ctx, source)
	if err != nil {
		return receiver, resolved, err
	}
	id, err := source.ID()
	if err != nil {
		return receiver, resolved, err
	}
	resolved.Source = dagql.NewID[*core.Workspace](id)
	if len(args.Commits) > 0 {
		ctx, cancel := context.WithTimeout(ctx, core.WorkspacePullTimeout)
		defer cancel()
		var repo dagql.ObjectResult[*core.GitRepository]
		if err := srv.Select(ctx, source, &repo, dagql.Selector{Field: "git"}, dagql.Selector{Field: "__repository"}); err != nil {
			return receiver, resolved, err
		}
		// Resolve against the frozen source's object database, not a history
		// scan or the receiver. Only canonical hashes enter the recorded helper.
		resolved.Commits = make([]string, len(args.Commits))
		for i, sha := range args.Commits {
			if !core.IsFullGitSHA(sha) {
				full, err := repo.Self().ResolveShortSHA(ctx, sha)
				if err != nil {
					return receiver, resolved, fmt.Errorf("resolve selected commit %q: %w", sha, err)
				}
				sha = full
			}
			resolved.Commits[i] = sha
		}
	}
	if err := resolved.opts().Validate(); err != nil {
		return receiver, resolved, err
	}
	return receiver, resolved, nil
}

func (s *workspaceSchema) compareCommitsFrom(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceCommitsFromArgs) (dagql.Array[*core.WorkspaceCommitPick], error) {
	parent, resolved, err := s.pullInputs(ctx, parent, args)
	if err != nil {
		return nil, err
	}
	dir, picks, source, err := s.computeWorkspacePull(ctx, parent, resolved, false)
	if err != nil {
		return nil, err
	}
	defer dir.OnRelease(context.WithoutCancel(ctx))
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	result := make(dagql.Array[*core.WorkspaceCommitPick], 0, len(picks))
	for _, pick := range picks {
		var commit dagql.ObjectResult[*core.GitCommit]
		if err := srv.Select(ctx, source.Self().Repo, &commit, dagql.Selector{Field: "commit", Args: []dagql.NamedInput{{Name: "id", Value: dagql.NewString(pick.SHA)}}}); err != nil {
			return nil, err
		}
		result = append(result, &core.WorkspaceCommitPick{Commit: commit, Status: pick.Status, Reason: pick.Reason, ConflictPaths: pick.ConflictPaths})
	}
	return result, nil
}

func (s *workspaceSchema) withCommitsFrom(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceCommitsFromArgs) (inst dagql.ObjectResult[*core.Workspace], err error) {
	parent, resolved, err := s.pullInputs(ctx, parent, args)
	if err != nil {
		return inst, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	repo, err := workspaceRepositoryFromDirectory(ctx, parent, dagql.Selector{Field: "__pullDirectory", Args: resolved.selectors()})
	if err != nil {
		return inst, err
	}
	if err := srv.Select(ctx, repo, &inst, dagql.Selector{Field: "head"}, dagql.Selector{Field: "asWorkspace", Args: []dagql.NamedInput{{Name: "cwd", Value: dagql.NewString(parent.Self().Cwd)}}}); err != nil {
		return inst, err
	}
	var changes dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, repo, &changes, dagql.Selector{Field: "uncommitted"}); err != nil {
		return inst, err
	}
	changesID, err := changes.ID()
	if err != nil {
		return inst, err
	}
	var overlaid dagql.ObjectResult[*core.Workspace]
	if err := srv.Select(ctx, inst, &overlaid, dagql.Selector{Field: "withChanges", Args: []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](changesID)}}}); err != nil {
		return inst, err
	}
	inst, err = checkpointWorkspaceMetadataComposition(ctx, srv, overlaid, parent.Self(), parent.Self().SelectedEnv())
	if err != nil {
		return inst, err
	}
	return inst, nil
}

func (s *workspaceSchema) computeWorkspacePull(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspacePullArgs, apply bool) (*core.Directory, []core.WorkspacePullPick, dagql.ObjectResult[*core.GitRef], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, nil, dagql.ObjectResult[*core.GitRef]{}, err
	}
	source, err := args.Source.Load(ctx, srv)
	if err != nil {
		return nil, nil, dagql.ObjectResult[*core.GitRef]{}, err
	}
	return core.ComputeWorkspacePull(ctx, parent, source, args.opts(), apply)
}

func (s *workspaceSchema) pullDirectory(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspacePullArgs) (inst dagql.ObjectResult[*core.Directory], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	source, err := args.Source.Load(ctx, srv)
	if err != nil {
		return inst, err
	}
	if !parent.Self().IsValueWorkspace() || !source.Self().IsValueWorkspace() {
		return inst, fmt.Errorf("pulling requires frozen workspaces; call snapshot first")
	}
	opts := args.opts()
	if err := opts.Validate(); err != nil {
		return inst, err
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, &core.Directory{
		Platform: query.Platform(),
		Dir:      new(core.LazyAccessor[string, *core.Directory]),
		Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory]),
		Lazy:     &core.DirectoryWorkspacePullLazy{LazyState: core.NewLazyState(), Parent: parent, Source: source, Opts: opts},
	})
}
