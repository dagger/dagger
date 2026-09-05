package schema

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type workspaceWithCommitArgs struct {
	Message     string
	Paths       []string `default:"[]"`
	Date        string
	AuthorName  dagql.Optional[dagql.String]
	AuthorEmail dagql.Optional[dagql.String]
}

func (args workspaceWithCommitArgs) opts(ws *core.Workspace) (core.WorkspaceCommitOpts, error) {
	opts := core.WorkspaceCommitOpts{
		Message: args.Message, Date: args.Date,
		AuthorName: ws.GitAuthorName, AuthorEmail: ws.GitAuthorEmail,
	}
	if _, err := time.Parse(time.RFC3339, args.Date); err != nil {
		return opts, fmt.Errorf("withCommit date must be RFC3339: %w", err)
	}
	if strings.TrimSpace(args.Message) == "" || strings.ContainsRune(args.Message, 0) {
		return opts, fmt.Errorf("withCommit message must be nonempty and contain no NUL")
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
	if err := validateWorkspaceGitAuthor(opts.AuthorName, opts.AuthorEmail); err != nil {
		return opts, err
	}
	for _, p := range args.Paths {
		resolved, err := resolveWorkspacePath(p, ws.Cwd)
		if err != nil {
			return opts, err
		}
		if resolved == ".git" || strings.HasPrefix(resolved, ".git/") {
			return opts, fmt.Errorf("withCommit cannot commit Git metadata")
		}
		opts.Paths = append(opts.Paths, resolved)
	}
	return opts, nil
}

func (args workspaceWithCommitArgs) selectors() []dagql.NamedInput {
	paths := make(dagql.ArrayInput[dagql.String], len(args.Paths))
	for i, p := range args.Paths {
		paths[i] = dagql.NewString(p)
	}
	return []dagql.NamedInput{
		{Name: "message", Value: dagql.NewString(args.Message)},
		{Name: "date", Value: dagql.NewString(args.Date)},
		{Name: "paths", Value: paths},
		{Name: "authorName", Value: args.AuthorName},
		{Name: "authorEmail", Value: args.AuthorEmail},
	}
}

// withCommit crosses the host approval boundary once, then returns a composition
// over a frozen receiver. The cached helper never captures host state, and no
// effectful withCommit/checkpoint call is retained in the resulting recipe.
func (s *workspaceSchema) withCommit(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceWithCommitArgs) (inst dagql.ObjectResult[*core.Workspace], err error) {
	if _, err := args.opts(parent.Self()); err != nil {
		return inst, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	frozen, err := s.checkpoint(ctx, parent, workspaceCheckpointArgs{})
	if err != nil {
		return inst, err
	}

	var changes dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, frozen, &changes,
		dagql.Selector{Field: "git"}, dagql.Selector{Field: "uncommitted"},
	); err != nil {
		return inst, err
	}
	var repo dagql.ObjectResult[*core.GitRepository]
	if err := srv.Select(ctx, frozen, &repo, dagql.Selector{
		Field: "__commitRepository", Args: args.selectors(),
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

	// The complete approved working tree is unchanged by committing. Only its
	// base moves: diffing it against the new HEAD leaves the unselected edits.
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
	return checkpointWorkspaceMetadataComposition(ctx, srv, overlaid, frozen.Self(), frozen.Self().SelectedEnv())
}

// commitBase materializes a clean full-history checkout, regardless of the
// source repository's keepGitDir option. This pure helper owns the snapshot.
func (s *workspaceSchema) commitBase(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], _ struct{}) (inst dagql.ObjectResult[*core.Directory], err error) {
	if !parent.Self().IsValueWorkspace() {
		return inst, fmt.Errorf("commit base requires a frozen workspace; call checkpoint first")
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	var head dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, parent, &head, dagql.Selector{Field: "git"}, dagql.Selector{Field: "head"}); err != nil {
		return inst, fmt.Errorf("withCommit requires a Git-backed workspace: %w", err)
	}
	dir, err := head.Self().Backend.Tree(ctx, srv, false, 0, false)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
}

func (s *workspaceSchema) commitDirectory(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceWithCommitArgs) (inst dagql.ObjectResult[*core.Directory], err error) {
	if !parent.Self().IsValueWorkspace() {
		return inst, fmt.Errorf("commit requires a frozen workspace; call checkpoint first")
	}
	opts, err := args.opts(parent.Self())
	if err != nil {
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
	var changes dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, parent, &changes, dagql.Selector{Field: "git"}, dagql.Selector{Field: "uncommitted"}); err != nil {
		return inst, err
	}
	dir, err := core.WorkspaceCommitChangeset(ctx, base, changes.Self(), opts)
	if err != nil {
		return inst, fmt.Errorf("withCommit: %w", err)
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
}

func (s *workspaceSchema) commitRepository(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceWithCommitArgs) (inst dagql.ObjectResult[*core.GitRepository], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	var dir dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, parent, &dir, dagql.Selector{
		Field: "__commitDirectory", Args: args.selectors(),
	}); err != nil {
		return inst, err
	}
	repo, err := core.NewGitRepository(ctx, &core.LocalGitRepository{Directory: dir})
	if err != nil {
		return inst, err
	}
	// Keep the logical origin even though the objects now live engine-side,
	// just as GitRepository.withBundle does.
	var head dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, parent, &head, dagql.Selector{Field: "git"}, dagql.Selector{Field: "head"}); err != nil {
		return inst, err
	}
	repo.URL = head.Self().Repo.Self().URL
	repo.DiscardGitDir = head.Self().Repo.Self().DiscardGitDir
	return dagql.NewObjectResultForCurrentCall(ctx, srv, repo)
}

func validateWorkspaceGitAuthor(name, email string) error {
	if strings.ContainsAny(name, "\x00\r\n<>") || strings.ContainsAny(email, "\x00\r\n<>") {
		return fmt.Errorf("git author name and email must not contain NUL, newlines, or angle brackets")
	}
	return nil
}

func (s *workspaceSchema) withGitAuthor(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args struct{ Name, Email string }) (dagql.ObjectResult[*core.Workspace], error) {
	if err := validateWorkspaceGitAuthor(args.Name, args.Email); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	ws := parent.Self().Clone()
	ws.GitAuthorName, ws.GitAuthorEmail = args.Name, args.Email
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, ws)
}
