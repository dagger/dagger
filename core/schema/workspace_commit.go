package schema

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
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
		AuthorName: args.AuthorName.Value.String(), AuthorEmail: args.AuthorEmail.Value.String(),
	}
	if _, err := time.Parse(time.RFC3339, args.Date); err != nil {
		return opts, fmt.Errorf("withCommit date must be RFC3339: %w", err)
	}
	if strings.TrimSpace(args.Message) == "" || strings.ContainsRune(args.Message, 0) {
		return opts, fmt.Errorf("withCommit message must be nonempty and contain no NUL")
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
// effectful withCommit/snapshot call is retained in the resulting recipe.
func (s *workspaceSchema) withCommit(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceWithCommitArgs) (inst dagql.ObjectResult[*core.Workspace], err error) {
	if _, err := args.opts(parent.Self()); err != nil {
		return inst, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	if !args.AuthorName.Valid || !args.AuthorEmail.Valid {
		name, email, err := workspaceGitAuthor(ctx)
		if err != nil {
			return inst, err
		}
		if !args.AuthorName.Valid {
			args.AuthorName = dagql.Opt(dagql.NewString(name))
		}
		if !args.AuthorEmail.Valid {
			args.AuthorEmail = dagql.Opt(dagql.NewString(email))
		}
		// Reselect with explicit identity so neither cached commit helpers nor
		// recipe replay depend on the caller's ambient Git configuration.
		err = srv.Select(ctx, parent, &inst, dagql.Selector{Field: "withCommit", Args: args.selectors()})
		return inst, err
	}

	frozen, err := s.freeze(ctx, parent)
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
		return inst, fmt.Errorf("commit base requires a frozen workspace; call snapshot first")
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
		return inst, fmt.Errorf("commit requires a frozen workspace; call snapshot first")
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

func (s *workspaceSchema) commitRepository(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceWithCommitArgs) (dagql.ObjectResult[*core.GitRepository], error) {
	return workspaceRepositoryFromDirectory(ctx, parent, dagql.Selector{
		Field: "__commitDirectory", Args: args.selectors(),
	})
}

// workspaceRepositoryFromDirectory opens an engine-side directory while
// preserving the workspace's logical origin and push routing.
func workspaceRepositoryFromDirectory(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], directory dagql.Selector) (inst dagql.ObjectResult[*core.GitRepository], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	var dir dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, parent, &dir, directory); err != nil {
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
	repo.PushURLs = slices.Clone(head.Self().Repo.Self().PushURLs)
	repo.DiscardGitDir = head.Self().Repo.Self().DiscardGitDir
	return dagql.NewObjectResultForCurrentCall(ctx, srv, repo)
}

func validateWorkspaceGitAuthor(name, email string) error {
	if strings.ContainsAny(name, "\x00\r\n<>") || strings.ContainsAny(email, "\x00\r\n<>") {
		return fmt.Errorf("git author name and email must not contain NUL, newlines, or angle brackets")
	}
	return nil
}

// workspaceGitAuthor reads the calling client's effective Git configuration at
// its working directory. Outside a checkout, Git uses system/global config.
//
// The effective caller is the nearest non-module ancestor client: module and
// SDK clients run with inert session attachables (the engine denies them host
// access by design), so a commit made from module code — an agent's commit
// tool, for example — samples the same identity a direct CLI call would.
func workspaceGitAuthor(ctx context.Context) (string, string, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return "", "", err
	}
	bk, err := query.Engine(ctx)
	if err != nil {
		return "", "", err
	}
	callerMD, err := query.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return "", "", fmt.Errorf("resolve Git author client: %w", err)
	}
	entries, err := bk.GetGitConfig(engine.ContextWithClientMetadata(ctx, callerMD), ".")
	if err != nil {
		return "", "", fmt.Errorf("read Git author config: %w", err)
	}
	var name, email string
	for _, entry := range entries {
		switch strings.ToLower(entry.GetKey()) {
		case "user.name":
			name = entry.GetValue()
		case "user.email":
			email = entry.GetValue()
		}
	}
	if name == "" {
		name = "Dagger"
	}
	if email == "" {
		email = "dagger@localhost"
	}
	return name, email, nil
}
