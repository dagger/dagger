package schema

import (
	"context"
	"fmt"

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
	dir, err := evaluatedDirectory(ctx, query, &core.DirectoryWorkspacePullLazy{LazyState: core.NewLazyState(), Parent: parent, Source: source, Opts: opts})
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
}

// pullRepository opens the pulled storage like GitRepository.withContents on
// the receiver HEAD's repository, preserving the workspace's logical origin
// and push routing, and keeps the receiver's HEAD as the new HEAD's checkout
// base. A pull leaves HEAD where it was, cherry-picks commits on top of it or
// fast-forwards to a descendant, so the receiver's HEAD is an ancestor of the
// new one and a source-only checkout of it can apply just the delta to the
// receiver's canonical tree (planIncrementalGitCheckout verifies the ancestry
// in the object database before relying on it). Like
// GitRef.__withCommitRepository, the provenance lives on this private,
// replayable recipe: public withContents must not infer it from arbitrary
// supplied storage.
//
// The pulled storage is a complete checkout (Workspace.git.__checkout carries
// full history), so it retains no owned-shallow HistorySource: shallow
// storage without one is rejected by the incremental planner, which then
// takes the full checkout.
func (s *workspaceSchema) pullRepository(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspacePullArgs) (inst dagql.ObjectResult[*core.GitRepository], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	var dir dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, parent, &dir, dagql.Selector{Field: "__pullDirectory", Args: args.selectors()}); err != nil {
		return inst, err
	}
	var head dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, parent, &head, dagql.Selector{Field: "git"}, dagql.Selector{Field: "head"}); err != nil {
		return inst, err
	}
	repo, err := gitRepositoryWithContents(ctx, srv, head.Self().Repo, dir)
	if err != nil {
		return inst, err
	}
	local, ok := repo.Self().Backend.(*core.LocalGitRepository)
	if !ok || head.Self().Ref == nil || !core.IsFullGitSHA(head.Self().Ref.SHA) {
		return repo, nil
	}
	remote, err := repo.Self().LoadRemote(ctx)
	if err != nil {
		return inst, err
	}
	pulled, err := remote.Lookup("HEAD")
	if err != nil {
		return inst, fmt.Errorf("resolve pulled repository HEAD: %w", err)
	}
	checkoutParent, parentTree, err := pullCheckoutParent(ctx, srv, head)
	if err != nil {
		return inst, err
	}
	backend := *local
	backend.CheckoutBase = &core.GitCheckoutBase{Parent: checkoutParent, CommitSHA: pulled.SHA, Tree: parentTree}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, repo.Self().CloneWithBackend(&backend))
}

// pullCheckoutParent returns the receiver HEAD's exact recipe as the checkout
// base. A remote HEAD is pinned to its resolved SHA, the recipe its
// source-only tree uses, together with that canonical tree, which incremental
// checkout requires of a remote base so it never needs another fetch. Unlike
// GitRef.withCommit, whose base tree is usually already materialized, a pull
// does not evaluate the tree just for this: a cold one, or one that fails, is
// left out, and the pulled HEAD then checks out in full.
func pullCheckoutParent(ctx context.Context, srv *dagql.Server, head dagql.ObjectResult[*core.GitRef]) (parent dagql.ObjectResult[*core.GitRef], tree dagql.ObjectResult[*core.Directory], _ error) {
	if _, remote := head.Self().Backend.(*core.RemoteGitRef); !remote {
		return head, tree, nil
	}
	if err := srv.Select(ctx, head.Self().Repo, &parent, dagql.Selector{Field: "ref", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String(head.Self().Ref.SHA)}}}); err != nil {
		return parent, tree, err
	}
	if err := srv.Select(ctx, parent, &tree, dagql.Selector{Field: "tree", Args: []dagql.NamedInput{{Name: "discardGitDir", Value: dagql.Boolean(true)}}}); err != nil {
		if ctx.Err() != nil {
			return parent, tree, err
		}
		return parent, dagql.ObjectResult[*core.Directory]{}, nil
	}
	if dagql.HasPendingLazyComputation(tree) {
		return parent, dagql.ObjectResult[*core.Directory]{}, nil
	}
	if _, err := tree.Self().Snapshot.GetOrEval(ctx, tree.Result); err != nil {
		if ctx.Err() != nil {
			return parent, tree, err
		}
		return parent, dagql.ObjectResult[*core.Directory]{}, nil
	}
	return parent, tree, nil
}
