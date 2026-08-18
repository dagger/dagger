package schema

// Replaying commits out of one workspace into another: the plan
// (Workspace.commitsFrom) and the apply (Workspace.withCommitsFrom). Both run
// the same fold, so a plan and the apply that follows it can never disagree —
// and because every fold step is a real dagql field selection, the apply is
// served from the cache the plan warmed.

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	enginetel "github.com/dagger/dagger/engine/telemetry"
)

type workspaceCommitsFromArgs struct {
	Source  dagql.ID[*core.Workspace]
	Commits []string `default:"[]"`
}

// commitsFrom plans which of the source workspace's staged commits could be
// applied to this one, without applying anything.
func (s *workspaceSchema) commitsFrom(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceCommitsFromArgs,
) (dagql.Array[*core.WorkspaceCommitPick], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	source, err := args.Source.Load(ctx, srv)
	if err != nil {
		return nil, fmt.Errorf("commitsFrom: load source workspace: %w", err)
	}
	candidates, _, err := s.foldCommitsFrom(ctx, parent, source, args.Commits)
	if err != nil {
		return nil, fmt.Errorf("commitsFrom: %w", err)
	}
	picks := make(dagql.Array[*core.WorkspaceCommitPick], 0, len(candidates))
	for i := range candidates {
		picks = append(picks, candidates[i].pick())
	}
	return picks, nil
}

// withCommitsFrom replays the source workspace's staged commits onto this one,
// refusing — loudly, and for the whole batch at once — any the plan classified
// as a conflict.
func (s *workspaceSchema) withCommitsFrom(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceCommitsFromArgs,
) (inst dagql.ObjectResult[*core.Workspace], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	source, err := args.Source.Load(ctx, srv)
	if err != nil {
		return inst, fmt.Errorf("withCommitsFrom: load source workspace: %w", err)
	}
	candidates, staged, err := s.foldCommitsFrom(ctx, parent, source, args.Commits)
	if err != nil {
		return inst, fmt.Errorf("withCommitsFrom: %w", err)
	}

	// Report every conflict, not just the first: an agent handed the whole
	// batch can fix it in one round trip instead of rediscovering the next
	// obstruction on every retry. PICKED and REDUNDANT are silently skipped —
	// by definition applying them would do nothing.
	var conflicts []error
	for i := range candidates {
		c := &candidates[i]
		if c.status != core.WorkspaceCommitConflict {
			continue
		}
		if c.reason == core.WorkspaceCommitPickReasonDirty {
			conflicts = append(conflicts, fmt.Errorf(
				"commit %s (%q): the workspace has uncommitted changes on %s; commit, save or discard them first",
				shortSHA(c.commit.SHA), commitSubject(c.commit.Message), strings.Join(c.conflictPaths, ", ")))
			continue
		}
		conflicts = append(conflicts, fmt.Errorf(
			"commit %s (%q): no longer applies to %s",
			shortSHA(c.commit.SHA), commitSubject(c.commit.Message), strings.Join(c.conflictPaths, ", ")))
	}
	if len(conflicts) > 0 {
		return inst, fmt.Errorf("withCommitsFrom: %d commit(s) cannot be applied: %w",
			len(conflicts), errors.Join(conflicts...))
	}
	return staged, nil
}

// workspaceCommitCandidate is one source commit as the fold sees it.
type workspaceCommitCandidate struct {
	// index is the commit's position in the *source's* staged stack, which is
	// what stagedCommitChanges needs to derive the per-commit delta — so it
	// survives filtering by requested hash.
	index  int
	commit core.WorkspacePendingCommit
	// root is the commit's identity for "have I already got this?": its own
	// origin when it is itself a replay, else its hash. Following the origin
	// makes the relation transitive, so a commit pulled A -> B -> C is still
	// recognised when C later pulls straight from A.
	root string
	// changes is what the commit folded in, as recorded in the source; it is
	// both the patch to replay and what the pick surfaces.
	changes dagql.ObjectResult[*core.Changeset]
	// touched are the workspace-root-relative paths that changeset affects.
	touched []string

	status        core.WorkspaceCommitPickStatus
	reason        core.WorkspaceCommitPickReason
	conflictPaths []string
}

func (c *workspaceCommitCandidate) pick() *core.WorkspaceCommitPick {
	conflictPaths := c.conflictPaths
	if conflictPaths == nil {
		conflictPaths = []string{}
	}
	return &core.WorkspaceCommitPick{
		SHA:           c.commit.SHA,
		Origin:        c.commit.Origin,
		Message:       c.commit.Message,
		Date:          c.commit.Date,
		AuthorName:    c.commit.AuthorName,
		AuthorEmail:   c.commit.AuthorEmail,
		Status:        c.status,
		Reason:        c.reason,
		ConflictPaths: conflictPaths,
		Changes:       c.changes,
	}
}

// foldCommitsFrom walks the source's staged commits oldest first and classifies
// each against the receiver, folding state forward: every candidate is judged
// against the workspace that would exist if all the pickable candidates before
// it had already been applied. It returns the candidates and that final
// workspace.
//
// The speculative workspaces this builds are free — ordinary immutable dagql
// values — which is what lets one implementation serve both the read-only plan
// (which throws the workspace away) and the apply (which returns it).
//
// A skipped candidate never folds, so a later commit that builds on it is
// patched against a tree missing its pre-image and fails: dependent commits
// cascade into conflicts on their own, with no special-casing.
func (s *workspaceSchema) foldCommitsFrom(
	ctx context.Context,
	receiver, source dagql.ObjectResult[*core.Workspace],
	shas []string,
) ([]workspaceCommitCandidate, dagql.ObjectResult[*core.Workspace], error) {
	if _, ok := receiver.Self().SourceGitRef(); ok {
		return nil, receiver, fmt.Errorf("cannot replay commits into a remote git workspace")
	}
	srcCommits := source.Self().PendingCommits()
	candidates, err := selectSourceCommits(srcCommits, shas)
	if err != nil {
		return nil, receiver, err
	}
	if len(candidates) == 0 {
		return candidates, receiver, nil
	}

	// What the receiver can prove it already has without touching git: its own
	// staged hashes, and the origins recorded on commits it previously
	// replayed. The common case lands here — a worker's stack starts as a copy
	// of the chief's, so a pull re-offers the chief's own commits verbatim.
	staged := make(map[string]bool, len(receiver.Self().PendingCommits()))
	origins := map[string]bool{}
	for _, c := range receiver.Self().PendingCommits() {
		staged[c.SHA] = true
		if c.Origin != "" {
			origins[c.Origin] = true
		}
	}

	var probe []string
	for i := range candidates {
		c := &candidates[i]
		if staged[c.commit.SHA] || origins[c.commit.SHA] || origins[c.root] {
			c.status = core.WorkspaceCommitPicked
			continue
		}
		probe = append(probe, c.commit.SHA)
		if c.root != c.commit.SHA {
			probe = append(probe, c.root)
		}
	}

	// Whatever is left gets one git probe, against the newest staged tree when
	// the receiver has staged commits — whose history holds both the checkout's
	// HEAD and the whole staged stack — else the workspace's own materialized
	// repository. One probe therefore answers "already in git history" for a
	// commit the receiver saved and reloaded while the source still carries it.
	if len(probe) > 0 {
		repo, err := s.workspaceCommitBaseRepo(ctx, receiver.Self())
		if err != nil {
			return nil, receiver, err
		}
		inHistory, err := core.WorkspaceRepoContainsCommits(ctx, repo, probe)
		if err != nil {
			return nil, receiver, err
		}
		for i := range candidates {
			c := &candidates[i]
			if c.status == core.WorkspaceCommitPicked {
				continue
			}
			if inHistory[c.commit.SHA] || inHistory[c.root] {
				c.status = core.WorkspaceCommitPicked
			}
		}
	}

	// The receiver's dirty paths, computed ONCE.
	//
	// Invariance (load-bearing): the set does not change across the fold,
	// because every applied commit is staged immediately with a scope equal to
	// exactly the paths it wrote, so the uncommitted remainder never grows. If
	// a future step ever applies without committing, revisit this.
	var dirty []string
	if slices.ContainsFunc(candidates, func(c workspaceCommitCandidate) bool {
		return c.status != core.WorkspaceCommitPicked
	}) {
		dirty, err = s.workspaceDirtyPaths(ctx, receiver)
		if err != nil {
			return nil, receiver, err
		}
	}

	cur := receiver
	for i := range candidates {
		c := &candidates[i]
		// Resolved even for PICKED candidates: it costs only ID plumbing (the
		// changeset stays lazy) and keeps WorkspaceCommitPick.changes populated
		// for every entry the plan reports.
		c.changes, err = s.stagedCommitChanges(ctx, srcCommits, c.index)
		if err != nil {
			return nil, receiver, err
		}
		if c.status == core.WorkspaceCommitPicked {
			continue
		}
		// A commit staged in a workspace that had no engine-side edits records
		// no changeset (withCommit reads such a workspace's pending changes
		// from git instead), so there is no patch to replay. Reporting it as
		// REDUNDANT would silently drop real work, so say so instead.
		if srcCommits[c.index].Committed.Self() == nil {
			return nil, receiver, fmt.Errorf(
				"commit %s (%q) cannot be replayed: it was staged in a workspace with no engine-side edits, "+
					"so the content it folded in was never recorded",
				shortSHA(c.commit.SHA), commitSubject(c.commit.Message))
		}
		c.touched, err = changesetTouchedPaths(ctx, c.changes.Self())
		if err != nil {
			return nil, receiver, fmt.Errorf("commit %s: compute touched paths: %w", shortSHA(c.commit.SHA), err)
		}
		if len(c.touched) == 0 {
			// Nothing a patch can express — an added empty directory, say.
			c.status = core.WorkspaceCommitRedundant
			continue
		}
		// git cherry-pick's rule for a dirty worktree, and the reason the
		// chief's work in progress can never be swept into a commit attributed
		// to someone else.
		if conflicts := overlappingPaths(c.touched, dirty); len(conflicts) > 0 {
			c.status = core.WorkspaceCommitConflict
			c.reason = core.WorkspaceCommitPickReasonDirty
			c.conflictPaths = conflicts
			continue
		}
		next, err := s.replayCommit(ctx, cur, c)
		if err != nil {
			return nil, receiver, err
		}
		if c.status == core.WorkspaceCommitPickable {
			cur = next
		}
	}
	return candidates, cur, nil
}

// replayCommit classifies one candidate against the workspace as it currently
// stands and, when it applies, returns that workspace with the commit staged on
// top. Only a genuine engine failure comes back as an error: a commit that
// cannot be applied is a *result*, recorded on the candidate.
func (s *workspaceSchema) replayCommit(
	ctx context.Context,
	cur dagql.ObjectResult[*core.Workspace],
	c *workspaceCommitCandidate,
) (next dagql.ObjectResult[*core.Workspace], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return next, err
	}

	// The target is the receiver's *current* content, restricted to the paths
	// the commit touches: that is all the patch can reach, and for a host
	// workspace it is the difference between syncing a handful of files and
	// syncing the tree once per replayed commit.
	target, err := s.resolveRootfs(ctx, cur.Self(), ".",
		core.CopyFilter{Include: workspacePathIncludes(c.touched)}, false)
	if err != nil {
		return next, fmt.Errorf("commit %s: resolve target tree: %w", shortSHA(c.commit.SHA), err)
	}

	delta, paths, applyErr := s.probeCommitPatch(ctx, target, c.changes)
	if applyErr != nil {
		// `git apply` refuses a patch that is already applied ("patch does not
		// apply", "already exists in working directory") rather than producing
		// an identical tree, so a forward failure alone cannot tell "this
		// conflicts" from "someone already made this exact change by hand".
		// A patch whose REVERSE applies cleanly is git's own definition of
		// already-applied — what `git am` and `git rebase` use — and the probe
		// costs nothing on the success path, since it only runs after a forward
		// failure. A half-merged change fails both directions and stays a
		// conflict, which is correct.
		reverse, err := s.reverseChangeset(ctx, c.changes)
		if err != nil {
			return next, fmt.Errorf("commit %s: build reverse patch: %w", shortSHA(c.commit.SHA), err)
		}
		if _, _, err := s.probeCommitPatch(ctx, target, reverse); err == nil {
			c.status = core.WorkspaceCommitRedundant
			return next, nil
		}
		c.status = core.WorkspaceCommitConflict
		c.reason = core.WorkspaceCommitPickReasonContent
		c.conflictPaths = parsePatchConflictPaths(applyErr, c.touched)
		return next, nil
	}
	commitPaths := workspaceReplayCommitPaths(paths)
	if len(commitPaths) == 0 {
		// The patch applied but wrote nothing: every hunk landed on content
		// that already matched.
		c.status = core.WorkspaceCommitRedundant
		return next, nil
	}

	// Applying the delta as a whole-file overlay is safe precisely because the
	// delta's Before is the receiver's *current* tree and its After is that
	// tree with the patch applied: patch application is the merge, withChanges
	// is only the write, and nothing older than the receiver's own state is
	// ever written back over it.
	deltaID, err := delta.ID()
	if err != nil {
		return next, err
	}
	replayArgs := workspaceWithCommitArgs{
		Message:     c.commit.Message,
		Paths:       commitPaths,
		Date:        c.commit.Date,
		AuthorName:  dagql.Opt(dagql.NewString(c.commit.AuthorName)),
		AuthorEmail: dagql.Opt(dagql.NewString(c.commit.AuthorEmail)),
	}
	if err := srv.Select(ctx, cur, &next,
		dagql.Selector{
			Field: "withChanges",
			Args:  []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](deltaID)}},
		},
		dagql.Selector{
			Field: "__withReplayedCommit",
			Args:  append(replayArgs.selectors(), dagql.NamedInput{Name: "origin", Value: dagql.NewString(c.root)}),
		},
	); err != nil {
		return next, fmt.Errorf("commit %s: stage replayed commit: %w", shortSHA(c.commit.SHA), err)
	}
	c.status = core.WorkspaceCommitPickable
	return next, nil
}

// probeCommitPatch renders a changeset as a patch, applies it to target, and
// reports what it actually wrote.
//
// The apply is forced here, deliberately: Directory.withPatchFile is lazy, and
// whether the patch still applies IS the classification. ComputePaths is what
// forces it, and it does double duty — it yields emptiness and the applied path
// set in one memoized pass, cheaper than isEmpty (which re-walks both trees)
// and reused downstream by withChanges. The whole probe runs under its own
// internal span so that a *planned* conflict reads as a probe in the trace
// rather than as a broken step.
func (s *workspaceSchema) probeCommitPatch(
	ctx context.Context,
	target dagql.ObjectResult[*core.Directory],
	changes dagql.ObjectResult[*core.Changeset],
) (delta dagql.ObjectResult[*core.Changeset], paths *core.ChangesetPaths, err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return delta, nil, err
	}
	var patch dagql.ObjectResult[*core.File]
	if err := srv.Select(ctx, changes, &patch, dagql.Selector{Field: "asPatch"}); err != nil {
		return delta, nil, err
	}
	patchID, err := patch.ID()
	if err != nil {
		return delta, nil, err
	}
	targetID, err := target.ID()
	if err != nil {
		return delta, nil, err
	}
	var patched dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, target, &patched, dagql.Selector{
		Field: "withPatchFile",
		Args: []dagql.NamedInput{
			{Name: "patch", Value: dagql.NewID[*core.File](patchID)},
			{Name: "onConflict", Value: core.PatchConflictFail},
		},
	}); err != nil {
		return delta, nil, err
	}
	if err := srv.Select(ctx, patched, &delta, dagql.Selector{
		Field: "changes",
		Args:  []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](targetID)}},
	}); err != nil {
		return delta, nil, err
	}
	err = enginetel.Task(ctx, "apply patch", func(ctx context.Context) error {
		var err error
		paths, err = delta.Self().ComputePaths(ctx)
		return err
	})
	if err != nil {
		return delta, nil, err
	}
	return delta, paths, nil
}

// reverseChangeset returns the changeset that undoes cs: the same two trees,
// Before and After swapped.
func (s *workspaceSchema) reverseChangeset(
	ctx context.Context,
	cs dagql.ObjectResult[*core.Changeset],
) (rev dagql.ObjectResult[*core.Changeset], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return rev, err
	}
	afterID, err := cs.Self().After.ID()
	if err != nil {
		return rev, err
	}
	if err := srv.Select(ctx, cs.Self().Before, &rev, dagql.Selector{
		Field: "changes",
		Args:  []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](afterID)}},
	}); err != nil {
		return rev, err
	}
	return rev, nil
}

// workspaceDirtyPaths returns the paths the workspace has pending edits on.
//
// git.uncommitted is the right notion for the refusal: it is exactly what
// withCommit would sweep into a commit. git.unmanaged is folded in alongside it
// because those edits — gitignored, or inside a nested repository — are
// invisible to uncommitted yet would still be clobbered by a whole-file write,
// and withCommit refuses to commit them anyway. The two sets are disjoint by
// construction, and unmanaged short-circuits to empty off the host path, so
// this stays cheap.
func (s *workspaceSchema) workspaceDirtyPaths(
	ctx context.Context,
	ws dagql.ObjectResult[*core.Workspace],
) ([]string, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, field := range []string{"uncommitted", "unmanaged"} {
		var cs dagql.ObjectResult[*core.Changeset]
		if err := srv.Select(ctx, ws, &cs,
			dagql.Selector{Field: "git"},
			dagql.Selector{Field: field},
		); err != nil {
			return nil, fmt.Errorf("resolve %s changes: %w", field, err)
		}
		paths, err := changesetTouchedPaths(ctx, cs.Self())
		if err != nil {
			return nil, fmt.Errorf("compute %s paths: %w", field, err)
		}
		out = append(out, paths...)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// selectSourceCommits picks which of the source's staged commits to consider,
// always IN STACK ORDER: a caller listing hashes cannot accidentally reorder a
// dependent stack. An empty list means the whole stack.
//
// Hashes may be given in full or as an unambiguous prefix of at least 7
// characters, because that is what an agent reads out of a log and hands back.
// An unknown or ambiguous hash is an error rather than a silent no-op: a typo
// must not read as "nothing to do".
func selectSourceCommits(
	commits []core.WorkspacePendingCommit,
	shas []string,
) ([]workspaceCommitCandidate, error) {
	wanted := make(map[int]bool, len(shas))
	for _, sha := range shas {
		index, err := resolveSourceCommit(commits, sha)
		if err != nil {
			return nil, err
		}
		wanted[index] = true
	}
	out := make([]workspaceCommitCandidate, 0, len(commits))
	for i, commit := range commits {
		if len(shas) > 0 && !wanted[i] {
			continue
		}
		root := commit.Origin
		if root == "" {
			root = commit.SHA
		}
		// reason is seeded rather than left at Go's zero value: it is a
		// non-null enum, and NONE is what "nothing obstructs this" means.
		out = append(out, workspaceCommitCandidate{
			index:  i,
			commit: commit,
			root:   root,
			reason: core.WorkspaceCommitPickReasonNone,
		})
	}
	return out, nil
}

// minCommitPrefixLen is the shortest hash prefix accepted, matching the width
// the log renderings agents read from print.
const minCommitPrefixLen = 7

func resolveSourceCommit(commits []core.WorkspacePendingCommit, sha string) (int, error) {
	sha = strings.TrimSpace(sha)
	if sha == "" {
		return 0, fmt.Errorf("commit hash cannot be empty")
	}
	for i, c := range commits {
		if c.SHA == sha {
			return i, nil
		}
	}
	if len(sha) < minCommitPrefixLen {
		return 0, fmt.Errorf("commit %q is not staged in the source workspace (a prefix must be at least %d characters)",
			sha, minCommitPrefixLen)
	}
	var matches []int
	for i, c := range commits {
		if strings.HasPrefix(c.SHA, sha) {
			matches = append(matches, i)
		}
	}
	switch len(matches) {
	case 0:
		return 0, fmt.Errorf("commit %q is not staged in the source workspace", sha)
	case 1:
		return matches[0], nil
	default:
		names := make([]string, 0, len(matches))
		for _, i := range matches {
			names = append(names, shortSHA(commits[i].SHA))
		}
		return 0, fmt.Errorf("commit %q is ambiguous: %s", sha, strings.Join(names, ", "))
	}
}

// workspaceReplayCommitPaths is the scope the replayed commit is staged with,
// taken from the applied DELTA rather than from the commit's own touched paths:
// a hunk that landed on content already matching drops out of the delta, and
// naming such a path in the commit scope would claim a change that was not
// made. Paths are made absolute because withCommit resolves relative ones from
// the workspace cwd, while changeset paths are workspace-root-relative.
//
// A split rename cannot arise here — asPatch renders with --no-renames — so
// withCommit's split-rename refusal can never trigger on this scope.
func workspaceReplayCommitPaths(paths *core.ChangesetPaths) []string {
	all := slices.Concat(paths.Added, paths.Modified, paths.AllRemoved)
	out := make([]string, 0, len(all))
	for _, p := range all {
		p = strings.TrimSuffix(p, "/")
		if p == "" || p == "." {
			continue
		}
		out = append(out, "/"+p)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// workspacePathIncludes turns workspace-root-relative paths into the include
// patterns a sparse read needs — the path itself plus everything beneath it —
// mirroring sparseHostBase.
func workspacePathIncludes(paths []string) []string {
	includes := make([]string, 0, len(paths)*2)
	for _, p := range paths {
		p = strings.TrimSuffix(p, "/")
		if p == "" || p == "." {
			continue
		}
		includes = append(includes, p, p+"/**")
	}
	return includes
}

// overlappingPaths returns the members of dirty that collide with a touched
// path, in EITHER direction: a dirty file under a touched directory, or a
// touched file under a dirty directory. commitPathInScope only tests one of
// those, which is not enough to protect pending work here.
func overlappingPaths(touched, dirty []string) []string {
	out := make([]string, 0, len(dirty))
	for _, d := range dirty {
		d := normalizeCommitPath(d)
		if d == "" {
			continue
		}
		for _, t := range touched {
			t := normalizeCommitPath(t)
			if t == "" {
				continue
			}
			if d == t || strings.HasPrefix(d, t+"/") || strings.HasPrefix(t, d+"/") {
				out = append(out, d)
				break
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func normalizeCommitPath(p string) string {
	p = path.Clean(strings.TrimSuffix(p, "/"))
	if p == "." || p == "/" {
		return ""
	}
	return strings.TrimPrefix(p, "/")
}

// patchConflictPathPatterns are the shapes `git apply` uses to name the file it
// choked on: a hunk that did not apply, a file-level failure, a missing
// pre-image, and an added file that is already there. The formats are stable
// but not contractual, which is why parsePatchConflictPaths only ever narrows
// the answer.
var patchConflictPathPatterns = []*regexp.Regexp{
	regexp.MustCompile(`error: patch failed: (.+):\d+`),
	regexp.MustCompile(`error: (.+): patch does not apply`),
	regexp.MustCompile(`error: (.+): No such file or directory`),
	regexp.MustCompile(`error: (.+): already exists in working directory`),
}

// parsePatchConflictPaths pulls file names out of a `git apply` failure,
// keeping only ones the commit actually touches. It falls back to the whole
// touched set, so conflictPaths is never empty for a conflict — at worst it is
// less precise than git's own diagnosis.
func parsePatchConflictPaths(err error, touched []string) []string {
	want := make(map[string]bool, len(touched))
	fallback := make([]string, 0, len(touched))
	for _, p := range touched {
		if p := normalizeCommitPath(p); p != "" {
			want[p] = true
			fallback = append(fallback, p)
		}
	}
	msg := err.Error()
	out := make([]string, 0, len(touched))
	for _, re := range patchConflictPathPatterns {
		for _, match := range re.FindAllStringSubmatch(msg, -1) {
			p := normalizeCommitPath(strings.TrimSpace(match[1]))
			if p == "" || !want[p] {
				continue
			}
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		out = fallback
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// shortSHA abbreviates a hash for error text, matching what commit logs print.
func shortSHA(sha string) string {
	if len(sha) <= minCommitPrefixLen {
		return sha
	}
	return sha[:minCommitPrefixLen]
}

// commitSubject is a commit message's first line, for error text.
func commitSubject(message string) string {
	subject, _, _ := strings.Cut(message, "\n")
	return strings.TrimSpace(subject)
}
