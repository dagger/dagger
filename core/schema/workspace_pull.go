package schema

import (
	"context"
	"fmt"
	"slices"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
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
	if err := resolved.opts().Validate(); err != nil {
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
	return receiver, resolved, nil
}

func (s *workspaceSchema) commitsFrom(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceCommitsFromArgs) (dagql.Array[*core.WorkspaceCommitPick], error) {
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
	local := parent.Self().ClientLocalBase()
	exportBase, exportPath, exportState := parent.Self().ExportBase, parent.Self().ExportPath, parent.Self().ExportStateDigest
	hostPath := parent.Self().HostPath()
	var state string
	if local {
		query, err := core.CurrentQuery(ctx)
		if err != nil {
			return inst, err
		}
		bk, err := query.Engine(ctx)
		if err != nil {
			return inst, err
		}
		state, err = bk.GitCheckoutState(ctx, hostPath)
		if err != nil {
			return inst, err
		}
	}
	parent, resolved, err := s.pullInputs(ctx, parent, args)
	if err != nil {
		return inst, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	var repo dagql.ObjectResult[*core.GitRepository]
	if err := srv.Select(ctx, parent, &repo, dagql.Selector{Field: "__pullRepository", Args: resolved.selectors()}); err != nil {
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
	if local || exportBase.Self() != nil {
		if local {
			exportBase, exportPath, exportState = parent, hostPath, state
		}
		baseID, err := exportBase.ID()
		if err != nil {
			return inst, err
		}
		var bound dagql.ObjectResult[*core.Workspace]
		err = srv.Select(ctx, inst, &bound, dagql.Selector{Field: "__withExportBase", Args: []dagql.NamedInput{
			{Name: "base", Value: dagql.NewID[*core.Workspace](baseID)},
			{Name: "path", Value: dagql.NewString(exportPath)},
			{Name: "stateDigest", Value: dagql.NewString(exportState)},
		}})
		return bound, err
	}
	return inst, nil
}

func (s *workspaceSchema) computeWorkspacePull(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspacePullArgs, apply bool) (*core.Directory, []core.WorkspacePullPick, dagql.ObjectResult[*core.GitRef], error) {
	ctx, cancel := context.WithTimeout(ctx, core.WorkspacePullTimeout)
	defer cancel()
	var head dagql.ObjectResult[*core.GitRef]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, nil, head, err
	}
	source, err := args.Source.Load(ctx, srv)
	if err != nil {
		return nil, nil, head, err
	}
	if !parent.Self().IsValueWorkspace() || !source.Self().IsValueWorkspace() {
		return nil, nil, head, fmt.Errorf("pulling requires frozen workspaces; call snapshot first")
	}
	var base dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, parent, &base, dagql.Selector{Field: "__commitBase"}); err != nil {
		return nil, nil, head, err
	}
	if err := srv.Select(ctx, source, &head, dagql.Selector{Field: "git"}, dagql.Selector{Field: "head"}); err != nil {
		return nil, nil, head, err
	}
	var changes dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, parent, &changes, dagql.Selector{Field: "git"}, dagql.Selector{Field: "uncommitted"}); err != nil {
		return nil, nil, head, err
	}
	dir, picks, err := core.WorkspacePullCommits(ctx, base, head.Self(), changes.Self(), args.opts(), apply)
	return dir, picks, head, err
}

func (s *workspaceSchema) pullDirectory(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspacePullArgs) (dagql.ObjectResult[*core.Directory], error) {
	dir, _, _, err := s.computeWorkspacePull(ctx, parent, args, true)
	if err != nil {
		return dagql.ObjectResult[*core.Directory]{}, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Directory]{}, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
}

func (s *workspaceSchema) pullRepository(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspacePullArgs) (dagql.ObjectResult[*core.GitRepository], error) {
	var inst dagql.ObjectResult[*core.GitRepository]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	var dir dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, parent, &dir, dagql.Selector{Field: "__pullDirectory", Args: args.selectors()}); err != nil {
		return inst, err
	}
	repo, err := core.NewGitRepository(ctx, &core.LocalGitRepository{Directory: dir})
	if err != nil {
		return inst, err
	}
	var head dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, parent, &head, dagql.Selector{Field: "git"}, dagql.Selector{Field: "head"}); err != nil {
		return inst, err
	}
	repo.URL, repo.DiscardGitDir = head.Self().Repo.Self().URL, head.Self().Repo.Self().DiscardGitDir
	repo.PushURLs = slices.Clone(head.Self().Repo.Self().PushURLs)
	return dagql.NewObjectResultForCurrentCall(ctx, srv, repo)
}
