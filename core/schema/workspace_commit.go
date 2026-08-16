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
) (dagql.ObjectResult[*core.Workspace], error) {
	inst, err := s.stageCommit(ctx, parent, args, "")
	if err == nil || !errors.Is(err, core.ErrNothingToCommit) {
		return inst, err
	}

	// withCommit is idempotent only once its work is already represented in the
	// receiver's effective history. Always try to stage first: an earlier commit
	// with the same message must not suppress new committable changes.
	repo, repoErr := s.workspaceCommitBaseRepo(ctx, parent.Self())
	if repoErr != nil {
		return inst, fmt.Errorf("withCommit: resolve commit history: %w", repoErr)
	}
	contains, containsErr := core.WorkspaceRepoContainsCommitMessage(ctx, repo, args.Message)
	if containsErr != nil {
		return inst, fmt.Errorf("withCommit: inspect commit history: %w", containsErr)
	}
	if contains {
		return parent, nil
	}
	return inst, err
}

// workspaceReplayCommitArgs is workspaceWithCommitArgs plus the provenance a
// replayed commit records. Origin is deliberately NOT part of
// workspaceWithCommitArgs: selectors() is what reaches __stagedCommit, so
// provenance can never change the commit that gets staged, its hash, or its
// cache identity.
//
// The shared fields are spelled out rather than embedded: dagql assigns an
// embedded arg struct by reflection, which cannot set a field whose name comes
// from an unexported type.
type workspaceReplayCommitArgs struct {
	Message     string
	Paths       []string `default:"[]"`
	Date        string
	AuthorName  dagql.Optional[dagql.String]
	AuthorEmail dagql.Optional[dagql.String]
	Origin      string
}

func (args workspaceReplayCommitArgs) commitArgs() workspaceWithCommitArgs {
	return workspaceWithCommitArgs{
		Message:     args.Message,
		Paths:       args.Paths,
		Date:        args.Date,
		AuthorName:  args.AuthorName,
		AuthorEmail: args.AuthorEmail,
	}
}

// withReplayedCommit is __withReplayedCommit: withCommit plus the record of
// where the commit came from. It is a field of its own rather than an argument
// on withCommit because provenance is meaningless to a normal caller, and it is
// a field at all rather than an in-resolver loop because
// Workspace.withCommitsFrom stages one commit per fold step: every step has to
// be a real dagql call so the intermediate workspaces get distinct IDs.
func (s *workspaceSchema) withReplayedCommit(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceReplayCommitArgs,
) (inst dagql.ObjectResult[*core.Workspace], err error) {
	if args.Origin == "" {
		return inst, fmt.Errorf("__withReplayedCommit: origin is required")
	}
	return s.stageCommit(ctx, parent, args.commitArgs(), args.Origin)
}

// stageCommit is the body of Workspace.withCommit, parameterized by the
// provenance to record: origin is empty for a commit authored here, and the
// hash of the original commit for one replayed out of another workspace.
func (s *workspaceSchema) stageCommit(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceWithCommitArgs,
	origin string,
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
	// Before anything is written into history — including the replay path, which
	// reaches this through __withReplayedCommit and is how one workspace's
	// mistake crosses into another's.
	if err := s.assertOverlayRemovalsIntended(ctx, ws); err != nil {
		return inst, fmt.Errorf("withCommit: %w", err)
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
		Origin:      origin,
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
	// state. Workspace.changes (the export payload) diffs the overlay's tree
	// against base+that, which leaves precisely the uncommitted remainder of
	// the *overlay* — including for a later path-scoped commit, whose
	// remainder is then computed from the staged state rather than from the
	// base checkout. Workspaces with no overlay read their pending changes
	// from the repository, which now resolves against the staged commit, so
	// there is nothing to record.
	//
	// The record is deliberately anchored on the overlay, not on the commit's
	// own (repository-anchored) scope: a commit can also fold in changes the
	// checkout already carried before the overlay existed, and those are not
	// part of the overlay's sparse before/after trees. Mixing the two anchors
	// would make the export remainder claim the untouched rest of the
	// workspace was deleted. Those changes still drop out of
	// Workspace.git.uncommitted, which diffs against the staged HEAD.
	if overlay, ok := ws.OverlayChanges(); ok {
		overlayBaseID, err := overlay.Self().Before.ID()
		if err != nil {
			return inst, err
		}
		// The overlay's own pending remainder (what is left of it on top of
		// any previously staged commit), scoped down to the paths this commit
		// actually folded in. Paths outside the overlay are ignored.
		overlayPending, _, err := s.workspaceOverlayChanges(ctx, ws)
		if err != nil {
			return inst, fmt.Errorf("record staged changes: %w", err)
		}
		committedPaths, err := changesetTouchedPaths(ctx, scope.scoped.Self())
		if err != nil {
			return inst, fmt.Errorf("record staged changes: %w", err)
		}
		_, stagedTree, err := s.scopeChangesetToPaths(ctx, overlayPending, committedPaths)
		if err != nil {
			return inst, fmt.Errorf("record staged changes: %w", err)
		}
		var committed dagql.ObjectResult[*core.Changeset]
		if err := srv.Select(ctx, stagedTree, &committed, dagql.Selector{
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
			return inst, fmt.Errorf("withCommit: %w", err)
		}
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
}

// stagedCommits exposes the workspace's engine-side staged commit stack, oldest
// first, so clients can show what is waiting to be saved to the local checkout.
func (s *workspaceSchema) stagedCommits(
	ctx context.Context,
	parent dagql.ObjectResult[*core.WorkspaceGit],
	_ struct{},
) (dagql.ObjectResultArray[*core.WorkspaceStagedCommit], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	commits := parent.Self().Workspace.Self().PendingCommits()
	results := make(dagql.ObjectResultArray[*core.WorkspaceStagedCommit], 0, len(commits))
	for i := range commits {
		var result dagql.ObjectResult[*core.WorkspaceStagedCommit]
		if err := srv.Select(ctx, parent, &result, dagql.Selector{
			Field: "__stagedCommitEntry",
			Args:  []dagql.NamedInput{{Name: "index", Value: dagql.NewInt(i)}},
		}); err != nil {
			return nil, fmt.Errorf("staged commits: entry %d: %w", i, err)
		}
		results = append(results, result)
	}
	return results, nil
}

// stagedCommitEntry is the internal field backing one element of
// WorkspaceGit.stagedCommits. It is a field of its own so each entry is an
// ordinary dagql result with a real ID (like Workspace.__workspaceModule).
func (s *workspaceSchema) stagedCommitEntry(
	ctx context.Context,
	parent dagql.ObjectResult[*core.WorkspaceGit],
	args struct {
		Index int
	},
) (inst dagql.ObjectResult[*core.WorkspaceStagedCommit], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	commits := parent.Self().Workspace.Self().PendingCommits()
	if args.Index < 0 || args.Index >= len(commits) {
		return inst, fmt.Errorf("staged commit index %d out of range (%d staged)", args.Index, len(commits))
	}
	commit := commits[args.Index]
	changes, err := s.stagedCommitChanges(ctx, commits, args.Index)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, &core.WorkspaceStagedCommit{
		SHA:         commit.SHA,
		Origin:      commit.Origin,
		Message:     commit.Message,
		Date:        commit.Date,
		AuthorName:  commit.AuthorName,
		AuthorEmail: commit.AuthorEmail,
		Changes:     changes,
	})
}

// stagedTreeOver reconstructs the workspace state a cumulative staged-commit
// record describes, expressed over the given base tree: the base with
// everything staged so far applied to it.
//
// Both callers must build this tree the same way, which is why they share one
// implementation. withCommit seeds a commit's cumulative record from this tree
// (workspaceOverlayChanges), so stagedCommitChanges can only recover a single
// commit's own step by rebuilding it identically over the base that commit was
// anchored on.
//
// Taking the previous record's own After tree instead — which is what it once
// did — reads as the same state but is not: each record is anchored on the
// overlay's Before as it stood when THAT commit was staged, and for a
// host-backed workspace that base is sparse, holding only the paths touched so
// far (sparseHostBase). The touched set grows with every edit, so the earlier
// tree is anchored on a strictly narrower base, and a path first edited after
// that commit was staged is absent from it altogether. The step then reports
// the path as a whole-file add at its full content, though it is long-tracked
// and modified by a line. That verdict is not cosmetic: this changeset is also
// what withCommitsFrom replays as a patch, and an add-patch does not apply
// against a receiver that already has the file.
func stagedTreeOver(
	ctx context.Context,
	base dagql.ObjectResult[*core.Directory],
	staged dagql.ObjectResult[*core.Changeset],
) (tree dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return tree, err
	}
	stagedID, err := staged.ID()
	if err != nil {
		return tree, err
	}
	if err := srv.Select(ctx, base, &tree, dagql.Selector{
		Field: "withChanges",
		Args:  []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](stagedID)}},
	}); err != nil {
		return tree, fmt.Errorf("resolve staged tree: %w", err)
	}
	return tree, nil
}

// stagedCommitChanges derives what one staged commit alone folded in.
//
// WorkspacePendingCommit.Committed is *cumulative*: it records the content of
// every commit staged so far, as a changeset from the workspace overlay's base
// to the staged state (that is what the uncommitted views need to diff
// against). The per-commit delta is therefore the step between consecutive
// staged states: from the previous commit's staged tree to this one's.
func (s *workspaceSchema) stagedCommitChanges(
	ctx context.Context,
	commits []core.WorkspacePendingCommit,
	index int,
) (inst dagql.ObjectResult[*core.Changeset], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	commit := commits[index]

	// Commits staged on a workspace with no overlay record nothing: their
	// pending changes are read from the repository instead. Report an empty
	// changeset rather than a null one.
	if commit.Committed.Self() == nil {
		repoID, err := commit.Repo.ID()
		if err != nil {
			return inst, err
		}
		if err := srv.Select(ctx, commit.Repo, &inst, dagql.Selector{
			Field: "changes",
			Args:  []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](repoID)}},
		}); err != nil {
			return inst, fmt.Errorf("staged commit changes: %w", err)
		}
		return inst, nil
	}

	// The state staged before this commit, expressed over THIS commit's base.
	// For the first commit that base is the whole answer, since nothing was
	// staged before it.
	before := commit.Committed.Self().Before
	if index > 0 && commits[index-1].Committed.Self() != nil {
		before, err = stagedTreeOver(ctx, before, commits[index-1].Committed)
		if err != nil {
			return inst, fmt.Errorf("staged commit changes: %w", err)
		}
	}
	beforeID, err := before.ID()
	if err != nil {
		return inst, err
	}
	if err := srv.Select(ctx, commit.Committed.Self().After, &inst, dagql.Selector{
		Field: "changes",
		Args:  []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](beforeID)}},
	}); err != nil {
		return inst, fmt.Errorf("staged commit changes: %w", err)
	}
	return inst, nil
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
			return scope, fmt.Errorf("withCommit: %w", core.ErrNothingToCommit)
		}
		scope.scoped = uncommitted
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
		// The paths may still have pending edits that git cannot see at all
		// (gitignored, or inside a nested repository). Those are written to
		// the checkout on save, but git has nothing to commit for them, so
		// say that rather than reporting a bare no-op.
		if unmanaged, err := s.unmanagedPathsInScope(ctx, ws, uncommitted, resolved); err == nil && len(unmanaged) > 0 {
			return scope, fmt.Errorf(
				"withCommit: %v have pending changes git cannot track (gitignored, or inside a nested repository); "+
					"they will still be written to the checkout on save, but cannot be committed",
				unmanaged)
		}
		return scope, fmt.Errorf("withCommit: %w for paths %v", core.ErrNothingToCommit, args.Paths)
	}

	scoped, _, err := s.scopeChangesetToPaths(ctx, uncommitted, resolved)
	if err != nil {
		return scope, err
	}
	scope.scoped = scoped
	return scope, nil
}

// scopeChangesetToPaths rebuilds a changeset restricted to the given resolved
// paths: the result keeps the original Before, and its After is that Before
// with exactly the in-scope additions, modifications and removals applied.
// Paths in the scope that the changeset does not mention are simply ignored,
// so the same scope can be projected onto several changesets (the workspace's
// uncommitted set and the overlay's own pending edits).
func (s *workspaceSchema) scopeChangesetToPaths(
	ctx context.Context,
	cs dagql.ObjectResult[*core.Changeset],
	resolved []string,
) (scoped dagql.ObjectResult[*core.Changeset], scopedAfter dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return scoped, scopedAfter, err
	}
	paths, err := cs.Self().ComputePaths(ctx)
	if err != nil {
		return scoped, scopedAfter, fmt.Errorf("compute changeset paths: %w", err)
	}
	added := commitPathsInScope(paths.Added, resolved)
	modified := commitPathsInScope(paths.Modified, resolved)
	// AllRemoved is deliberately uncollapsed: every path beneath a removed
	// directory carries its own entry (changesetDelta.appendRemovedTree
	// re-expands what the walker collapsed). Scope first, so a path-scoped
	// commit still removes only what it was asked to, then collapse — removing
	// a directory already removes everything beneath it, so the descendants are
	// pure no-ops, and each one used to cost its own (empty) snapshot layer.
	removed := core.CollapseChildPaths(commitPathsInScope(paths.AllRemoved, resolved))

	before := cs.Self().Before
	after := cs.Self().After
	scopedAfter = before
	if len(removed) > 0 {
		// One call, one layer, however many paths: chaining a call per path
		// stacks a snapshot per path, and overlayfs runs out of lowerdirs
		// (mount options exceed a page) a few hundred deep.
		removePaths := make(dagql.ArrayInput[dagql.String], len(removed))
		for i, p := range removed {
			removePaths[i] = dagql.String(strings.TrimSuffix(p, "/"))
		}
		if err := srv.Select(ctx, scopedAfter, &scopedAfter, dagql.Selector{
			Field: "withoutDirectories",
			Args:  []dagql.NamedInput{{Name: "paths", Value: removePaths}},
		}); err != nil {
			return scoped, scopedAfter, err
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
				return scoped, scopedAfter, err
			}
			srcID, err := src.ID()
			if err != nil {
				return scoped, scopedAfter, err
			}
			if err := srv.Select(ctx, scopedAfter, &scopedAfter, dagql.Selector{
				Field: "withDirectory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.NewString(p)},
					{Name: "source", Value: dagql.NewID[*core.Directory](srcID)},
				},
			}); err != nil {
				return scoped, scopedAfter, err
			}
			continue
		}
		var src dagql.ObjectResult[*core.File]
		if err := srv.Select(ctx, after, &src, dagql.Selector{
			Field: "file",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString(p)}},
		}); err != nil {
			return scoped, scopedAfter, err
		}
		srcID, err := src.ID()
		if err != nil {
			return scoped, scopedAfter, err
		}
		if err := srv.Select(ctx, scopedAfter, &scopedAfter, dagql.Selector{
			Field: "withFile",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(p)},
				{Name: "source", Value: dagql.NewID[*core.File](srcID)},
			},
		}); err != nil {
			return scoped, scopedAfter, err
		}
	}

	beforeID, err := before.ID()
	if err != nil {
		return scoped, scopedAfter, err
	}
	if err := srv.Select(ctx, scopedAfter, &scoped, dagql.Selector{
		Field: "changes",
		Args:  []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](beforeID)}},
	}); err != nil {
		return scoped, scopedAfter, err
	}
	return scoped, scopedAfter, nil
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
