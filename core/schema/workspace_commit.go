package schema

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
)

// workspaceWithCommitArgs is shared by Workspace.withCommit and the internal
// Workspace.__stagedCommit that does the actual git work, so both derive the
// same commit from the same inputs.
type workspaceWithCommitArgs struct {
	Message     string
	Paths       []string `default:"[]"`
	Date        string
	AuthorName  dagql.Optional[dagql.String]
	AuthorEmail dagql.Optional[dagql.String]
}

func (args workspaceWithCommitArgs) selectors() []dagql.NamedInput {
	paths := make(dagql.ArrayInput[dagql.String], 0, len(args.Paths))
	for _, p := range args.Paths {
		paths = append(paths, dagql.String(p))
	}
	inputs := []dagql.NamedInput{
		{Name: "message", Value: dagql.NewString(args.Message)},
		{Name: "paths", Value: paths},
		{Name: "date", Value: dagql.NewString(args.Date)},
	}
	if args.AuthorName.Valid {
		inputs = append(inputs, dagql.NamedInput{Name: "authorName", Value: args.AuthorName})
	}
	if args.AuthorEmail.Valid {
		inputs = append(inputs, dagql.NamedInput{Name: "authorEmail", Value: args.AuthorEmail})
	}
	return inputs
}

// commitOpts resolves the commit identity: explicit arguments win, then the
// identity recorded on the workspace when it was loaded, then the same
// defaults the engine's other git plumbing uses.
func (args workspaceWithCommitArgs) commitOpts(ws *core.Workspace) core.WorkspaceCommitOpts {
	opts := core.WorkspaceCommitOpts{
		Message:     args.Message,
		Date:        args.Date,
		AuthorName:  ws.GitAuthorName,
		AuthorEmail: ws.GitAuthorEmail,
	}
	if args.AuthorName.Valid {
		opts.AuthorName = args.AuthorName.Value.String()
	}
	if args.AuthorEmail.Valid {
		opts.AuthorEmail = args.AuthorEmail.Value.String()
	}
	if opts.AuthorName == "" {
		opts.AuthorName = "Dagger"
	}
	if opts.AuthorEmail == "" {
		opts.AuthorEmail = "dagger@localhost"
	}
	return opts
}

// withCommit stages a commit engine-side, on top of the workspace's base HEAD
// plus any previously staged commit. The user's checkout is never touched: the
// commit lives in a repository tree carried by the returned workspace, and the
// changes it folded in disappear from Workspace.git.uncommitted.
func (s *workspaceSchema) withCommit(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceWithCommitArgs,
) (inst dagql.ObjectResult[*core.Workspace], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	ws := parent.Self()
	if args.Date == "" {
		return inst, fmt.Errorf("withCommit: date is required")
	}
	if _, ok := ws.SourceGitRef(); ok {
		return inst, fmt.Errorf("withCommit: cannot stage a commit on a remote git workspace")
	}

	scope, err := s.workspaceCommitScope(ctx, parent, args)
	if err != nil {
		return inst, err
	}

	// Record where the local checkout was when the stack started, so a later
	// export can tell whether it has moved out from under the staged commits.
	baseHead := ws.BaseHeadSHA
	if len(ws.PendingCommits()) == 0 {
		var head dagql.String
		if err := srv.Select(ctx, parent, &head,
			dagql.Selector{Field: "git"},
			dagql.Selector{Field: "head"},
			dagql.Selector{Field: "commit"},
		); err != nil {
			return inst, fmt.Errorf("resolve base HEAD: %w", err)
		}
		baseHead = head.String()
	}

	var repo dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, parent, &repo, dagql.Selector{
		Field: "__stagedCommit",
		Args:  args.selectors(),
	}); err != nil {
		return inst, err
	}

	var sha dagql.String
	if err := srv.Select(ctx, repo, &sha,
		dagql.Selector{Field: "asGit"},
		dagql.Selector{Field: "head"},
		dagql.Selector{Field: "commit"},
	); err != nil {
		return inst, fmt.Errorf("resolve staged commit: %w", err)
	}

	opts := args.commitOpts(ws)
	pending := core.WorkspacePendingCommit{
		SHA:         sha.String(),
		Message:     opts.Message,
		Date:        opts.Date,
		AuthorName:  opts.AuthorName,
		AuthorEmail: opts.AuthorEmail,
		Paths:       slices.Clone(args.Paths),
		Repo:        repo,
	}

	// The committed changes must stop showing up as pending — but the workspace
	// tree must not change at all: for a host workspace the overlay changeset is
	// also what reconstructs the tree (resolveHostOverlayRootfs), so draining it
	// would delete just-committed content from Workspace.file / .directory and
	// from every container mounting the workspace.
	//
	// So the overlay is left exactly as it is, and instead the commit records
	// what it folded in, as a changeset from the overlay's base to the staged
	// state. The diff views (Workspace.changes, WorkspaceGit.uncommitted) diff
	// the overlay's tree against base+that, which leaves precisely the
	// uncommitted remainder — including for a later path-scoped commit, whose
	// remainder is then computed from the staged state rather than from the
	// base checkout. Workspaces with no overlay read their pending changes from
	// the repository, which now resolves against the staged commit, so there is
	// nothing to record.
	if overlay, ok := ws.OverlayChanges(); ok {
		overlayBaseID, err := overlay.Self().Before.ID()
		if err != nil {
			return inst, err
		}
		var committed dagql.ObjectResult[*core.Changeset]
		if err := srv.Select(ctx, scope.scopedAfter, &committed, dagql.Selector{
			Field: "changes",
			Args: []dagql.NamedInput{
				{Name: "from", Value: dagql.NewID[*core.Directory](overlayBaseID)},
			},
		}); err != nil {
			return inst, fmt.Errorf("record staged changes: %w", err)
		}
		pending.Committed = committed
	}

	newWS := ws.WithPendingCommit(pending)
	newWS.BaseHeadSHA = baseHead

	return dagql.NewObjectResultForCurrentCall(ctx, srv, newWS)
}

// stagedCommit is the internal field that performs the commit and returns the
// resulting repository tree. It is a field of its own (rather than inline work
// inside withCommit) so the repository tree is an ordinary cached dagql result
// with a real ID, which the workspace can then reference and persist.
func (s *workspaceSchema) stagedCommit(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceWithCommitArgs,
) (inst dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	scope, err := s.workspaceCommitScope(ctx, parent, args)
	if err != nil {
		return inst, err
	}
	dir, err := core.WorkspaceCommitChangeset(ctx, scope.repo, scope.scoped.Self(), args.commitOpts(parent.Self()))
	if err != nil {
		if errors.Is(err, core.ErrNothingToCommit) {
			return inst, fmt.Errorf("withCommit: nothing to commit")
		}
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
}

// exportPendingCommits lands the workspace's engine-side staged commits on the
// user's local checkout, as a fast-forward of whatever ref HEAD points at.
//
// Mechanism: the engine packs exactly the staged commits into a git bundle and
// hands it to the client over the session; the client's *own* git fetches the
// bundle and fast-forwards the checkout. Doing it with host git is what makes
// the result a normal git operation — reflog entries, an updated index, an
// updated work tree — and what makes worktree and submodule checkouts work,
// since their .git is a pointer file whose real repository lives elsewhere and
// only host git knows how to write it. The client re-checks the checkout's
// HEAD immediately before applying, so a checkout that moves mid-save is still
// refused, and git itself refuses anything that is not a fast-forward or that
// would clobber local work.
//
// It runs *before* the remaining overlay changeset is written to the work
// tree: the fast-forward writes the committed content, and the changeset —
// which is diffed against the staged tree — then adds exactly the uncommitted
// remainder on top.
func (s *workspaceSchema) exportPendingCommits(ctx context.Context, ws *core.Workspace) error {
	latest, ok := ws.LatestPendingCommit()
	if !ok || latest.Repo.Self() == nil {
		return nil
	}

	// Preconditions first: nothing is written to the host until every check
	// below has passed, so a rejected save leaves the checkout exactly as it
	// was.
	hostPath, err := ws.ExportHostPath()
	if err != nil {
		return fmt.Errorf("cannot save staged commits: %w", err)
	}
	if err := s.ensureWorkspaceGitDirectory(ctx, ws); err != nil {
		return fmt.Errorf("cannot save staged commits: %w", err)
	}

	clientCtx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return err
	}
	query, err := core.CurrentQuery(clientCtx)
	if err != nil {
		return err
	}
	bk, err := query.Engine(clientCtx)
	if err != nil {
		return fmt.Errorf("buildkit: %w", err)
	}

	// Early, clear rejection before anything is packed or transferred. The
	// client repeats this check under its own lock right before applying; this
	// one exists to fail fast with an actionable message.
	curHead, err := bk.GetGitHead(clientCtx, hostPath)
	if err != nil {
		return fmt.Errorf("cannot save staged commits: resolve local HEAD: %w", err)
	}
	if ws.BaseHeadSHA != "" && curHead != ws.BaseHeadSHA {
		return fmt.Errorf(
			"cannot save staged commits: local branch moved from %s to %s since the workspace was loaded; "+
				"commit or stash local changes and reload the workspace",
			ws.BaseHeadSHA, curHead)
	}

	bundle, err := core.WorkspaceStagedCommitsBundle(ctx, latest.Repo, latest.SHA, ws.BaseHeadSHA)
	if err != nil {
		return fmt.Errorf("cannot save staged commits: %w", err)
	}

	newHead, err := bk.ApplyGitBundle(
		clientCtx, hostPath, latest.SHA, ws.BaseHeadSHA, core.WorkspaceStagedCommitsRef, bundle)
	if err != nil {
		return fmt.Errorf("cannot save staged commits: %w", err)
	}
	if newHead != latest.SHA {
		return fmt.Errorf("cannot save staged commits: local HEAD is %s after saving, expected %s",
			newHead, latest.SHA)
	}

	// The checkout's HEAD changed, so the client's cached workspace detection
	// and cached host reads are stale. Best-effort, like the base export's:
	// bookkeeping must not fail a save that already landed.
	if err := core.InvalidateCurrentWorkspace(clientCtx); err != nil {
		slog.Warn("could not invalidate workspace after saving staged commits", "error", err)
	}
	if err := core.BumpWorkspaceReadEpoch(clientCtx); err != nil {
		slog.Warn("could not bump workspace read epoch after saving staged commits", "error", err)
	}
	return nil
}

// workspaceCommitScope is the resolved input to one staged commit.
type workspaceCommitScope struct {
	// repo is the repository tree the commit is built on: the newest staged
	// commit's tree if the stack is non-empty, else the workspace's own
	// repository tree.
	repo dagql.ObjectResult[*core.Directory]
	// scoped is the portion of the workspace's uncommitted changes this commit
	// records.
	scoped dagql.ObjectResult[*core.Changeset]
	// scopedAfter is scoped's "after" tree: the pending base with exactly the
	// committed changes applied.
	scopedAfter dagql.ObjectResult[*core.Directory]
}

func (s *workspaceSchema) workspaceCommitScope(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceWithCommitArgs,
) (scope workspaceCommitScope, err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return scope, err
	}
	ws := parent.Self()

	var uncommitted dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, parent, &uncommitted,
		dagql.Selector{Field: "git"},
		dagql.Selector{Field: "uncommitted"},
	); err != nil {
		return scope, err
	}

	scope.repo, err = s.workspaceCommitBaseRepo(ctx, ws)
	if err != nil {
		return scope, err
	}

	resolved := make([]string, 0, len(args.Paths))
	for _, p := range args.Paths {
		r, err := resolveWorkspacePath(p, ws.Cwd)
		if err != nil {
			return scope, err
		}
		resolved = append(resolved, r)
	}

	paths, err := uncommitted.Self().ComputePaths(ctx)
	if err != nil {
		return scope, fmt.Errorf("compute uncommitted paths: %w", err)
	}

	if commitScopeCoversAll(resolved) {
		if len(paths.Added)+len(paths.Modified)+len(paths.AllRemoved) == 0 {
			return scope, fmt.Errorf("withCommit: nothing to commit")
		}
		scope.scoped = uncommitted
		scope.scopedAfter = uncommitted.Self().After
		return scope, nil
	}

	// A rename is one change spread over two paths. Committing only half of it
	// would record a deletion with no matching addition (or vice versa), so
	// refuse rather than write a half-applied rename.
	for newPath, oldPath := range paths.Renamed {
		if commitPathInScope(newPath, resolved) != commitPathInScope(oldPath, resolved) {
			return scope, fmt.Errorf(
				"withCommit: paths would split the rename %q -> %q; include both paths or neither",
				oldPath, newPath)
		}
	}

	added := commitPathsInScope(paths.Added, resolved)
	modified := commitPathsInScope(paths.Modified, resolved)
	removed := commitPathsInScope(paths.AllRemoved, resolved)
	if len(added)+len(modified)+len(removed) == 0 {
		return scope, fmt.Errorf("withCommit: nothing to commit for paths %v", args.Paths)
	}

	before := uncommitted.Self().Before
	after := uncommitted.Self().After
	scopedAfter := before
	for _, p := range removed {
		if err := srv.Select(ctx, scopedAfter, &scopedAfter, dagql.Selector{
			Field: "withoutDirectory",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString(strings.TrimSuffix(p, "/"))}},
		}); err != nil {
			return scope, err
		}
	}
	for _, p := range slices.Concat(added, modified) {
		if strings.HasSuffix(p, "/") {
			p := strings.TrimSuffix(p, "/")
			var src dagql.ObjectResult[*core.Directory]
			if err := srv.Select(ctx, after, &src, dagql.Selector{
				Field: "directory",
				Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString(p)}},
			}); err != nil {
				return scope, err
			}
			srcID, err := src.ID()
			if err != nil {
				return scope, err
			}
			if err := srv.Select(ctx, scopedAfter, &scopedAfter, dagql.Selector{
				Field: "withDirectory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.NewString(p)},
					{Name: "source", Value: dagql.NewID[*core.Directory](srcID)},
				},
			}); err != nil {
				return scope, err
			}
			continue
		}
		var src dagql.ObjectResult[*core.File]
		if err := srv.Select(ctx, after, &src, dagql.Selector{
			Field: "file",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString(p)}},
		}); err != nil {
			return scope, err
		}
		srcID, err := src.ID()
		if err != nil {
			return scope, err
		}
		if err := srv.Select(ctx, scopedAfter, &scopedAfter, dagql.Selector{
			Field: "withFile",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(p)},
				{Name: "source", Value: dagql.NewID[*core.File](srcID)},
			},
		}); err != nil {
			return scope, err
		}
	}

	beforeID, err := before.ID()
	if err != nil {
		return scope, err
	}
	var scoped dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, scopedAfter, &scoped, dagql.Selector{
		Field: "changes",
		Args:  []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](beforeID)}},
	}); err != nil {
		return scope, err
	}
	scope.scoped = scoped
	scope.scopedAfter = scopedAfter
	return scope, nil
}

// workspaceCommitBaseRepo returns the repository tree the next staged commit
// builds on. Once commits are staged, that is the newest staged tree — its .git
// already holds the whole stack. Otherwise it is the workspace's own repository
// tree, with any .git pointer file (worktree/submodule checkout) flattened, as
// Workspace.git.__repository does.
func (s *workspaceSchema) workspaceCommitBaseRepo(
	ctx context.Context,
	ws *core.Workspace,
) (dir dagql.ObjectResult[*core.Directory], err error) {
	if latest, ok := ws.LatestPendingCommit(); ok && latest.Repo.Self() != nil {
		return latest.Repo, nil
	}
	if err := s.ensureWorkspaceGitDirectory(ctx, ws); err != nil {
		return dir, err
	}
	dir, err = s.resolveRootfs(ctx, ws, ".", core.CopyFilter{}, false)
	if err != nil {
		return dir, fmt.Errorf("workspace git directory: %w", err)
	}
	dir, err = s.flattenWorkspaceGitPointer(ctx, ws, dir)
	if err != nil {
		return dir, fmt.Errorf("workspace git directory: %w", err)
	}
	return dir, nil
}

// commitScopeCoversAll reports whether a resolved path scope is the whole
// workspace: either no paths at all, or the workspace root itself.
func commitScopeCoversAll(resolved []string) bool {
	if len(resolved) == 0 {
		return true
	}
	return slices.Contains(resolved, ".")
}

func commitPathInScope(p string, resolved []string) bool {
	if len(resolved) == 0 {
		return true
	}
	p = path.Clean(strings.TrimSuffix(p, "/"))
	for _, scope := range resolved {
		if scope == "." || p == scope || strings.HasPrefix(p, scope+"/") {
			return true
		}
	}
	return false
}

func commitPathsInScope(paths, resolved []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if commitPathInScope(p, resolved) {
			out = append(out, p)
		}
	}
	return out
}
