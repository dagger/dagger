package schema

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

type workspaceWithCommitArgs struct {
	Changes     dagql.ID[*core.Changeset]
	Message     string
	Date        string
	AuthorName  dagql.Optional[dagql.String]
	AuthorEmail dagql.Optional[dagql.String]
	Signoff     bool `default:"false"`
}

func (args workspaceWithCommitArgs) opts() (core.GitCommitOpts, error) {
	opts := core.GitCommitOpts{
		Message: args.Message, Date: args.Date, Signoff: args.Signoff,
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
	return opts, nil
}

func (args workspaceWithCommitArgs) selectors() []dagql.NamedInput {
	return []dagql.NamedInput{
		{Name: "changes", Value: args.Changes},
		{Name: "message", Value: dagql.NewString(args.Message)},
		{Name: "date", Value: dagql.NewString(args.Date)},
		{Name: "authorName", Value: args.AuthorName},
		{Name: "authorEmail", Value: args.AuthorEmail},
		{Name: "signoff", Value: dagql.NewBoolean(args.Signoff)},
	}
}

// withCommit crosses the host approval boundary once, then returns a composition
// over a frozen receiver. The cached helper never captures host state, and no
// effectful withCommit/snapshot call is retained in the resulting recipe.
func (s *workspaceSchema) withCommit(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceWithCommitArgs) (inst dagql.ObjectResult[*core.Workspace], err error) {
	if _, err := args.opts(); err != nil {
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
	changesID, err := changes.ID()
	if err != nil {
		return inst, err
	}
	// An empty uncommitted changeset may have an empty After tree. Apply the
	// delta to HEAD to recover the complete source tree, excluding mounts.
	var workingTree dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, frozen, &workingTree,
		dagql.Selector{Field: "git"}, dagql.Selector{Field: "head"},
		dagql.Selector{Field: "tree", Args: []dagql.NamedInput{{Name: "discardGitDir", Value: dagql.NewBoolean(true)}}},
		dagql.Selector{Field: "withChanges", Args: []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](changesID)}}},
	); err != nil {
		return inst, err
	}
	opts, err := args.opts()
	if err != nil {
		return inst, err
	}
	incoming, err := args.Changes.Load(ctx, srv)
	if err != nil {
		return inst, err
	}
	beforeID, err := incoming.Self().Before.ID()
	if err != nil {
		return inst, err
	}
	// Merge the same delta into the approved working tree as well as HEAD.
	// Restoring the old tree after committing would undo off-baseline input.
	var working dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, workingTree, &working, dagql.Selector{
		Field: "changes", Args: []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](beforeID)}},
	}); err != nil {
		return inst, err
	}
	var merged dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, working, &merged, dagql.Selector{Field: "withChangeset", Args: []dagql.NamedInput{
		{Name: "changes", Value: args.Changes}, {Name: "onConflict", Value: core.FailOnMergeConflict},
	}}); err != nil {
		return inst, fmt.Errorf("apply commit changes to working tree: %w", err)
	}
	commitArgs := gitRefWithCommitArgs{Changes: args.Changes, Message: opts.Message, Date: opts.Date, AuthorName: opts.AuthorName, AuthorEmail: opts.AuthorEmail, Signoff: opts.Signoff}
	var committed dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, frozen, &committed, dagql.Selector{Field: "git"}, dagql.Selector{Field: "head"}, dagql.Selector{Field: "withCommit", Args: commitArgs.selectors()}); err != nil {
		return inst, err
	}
	if err := srv.Select(ctx, committed, &inst,
		dagql.Selector{Field: "asWorkspace", Args: []dagql.NamedInput{
			{Name: "cwd", Value: dagql.NewString(frozen.Self().Cwd)},
		}},
	); err != nil {
		return inst, err
	}

	// Only the difference between the merged working tree and new HEAD remains
	// pending. Compatible unselected edits and incoming content both survive.
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
	if err := srv.Select(ctx, merged.Self().After, &remaining, dagql.Selector{
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

func (s *workspaceSchema) workspaceGitDirectory(ctx context.Context, parent dagql.ObjectResult[*core.WorkspaceGit], _ struct{}) (inst dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	err = srv.Select(ctx, parent, &inst,
		dagql.Selector{Field: "__checkout"},
		dagql.Selector{Field: "directory", Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString(".git")}}},
	)
	return inst, err
}

func (s *workspaceSchema) workspaceGitFullCheckout(ctx context.Context, parent dagql.ObjectResult[*core.WorkspaceGit], _ struct{}) (inst dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	var head dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, parent, &head, dagql.Selector{Field: "head"}); err != nil {
		return inst, fmt.Errorf("workspace Git checkout requires a HEAD commit: %w", err)
	}
	// Bypass the repository's keepGitDir option and the default shallow depth.
	// Retain the complete immutable snapshot as a dagql result so consumers can
	// use either the worktree or its metadata without reconstructing history.
	dir, err := head.Self().Backend.Tree(ctx, srv, false, 0, false, head.Self().Repo.Self().Remotes)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
}

// workspaceGitCheckout materializes HEAD once, with full history and Git
// metadata regardless of the public repository's shallow/discard-Git defaults.
func workspaceGitCheckout(ctx context.Context, srv *dagql.Server, ws dagql.ObjectResult[*core.Workspace]) (inst dagql.ObjectResult[*core.Directory], err error) {
	err = srv.Select(ctx, ws, &inst,
		dagql.Selector{Field: "git"},
		dagql.Selector{Field: "__checkout"},
	)
	return inst, err
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
	var head dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, parent, &head, dagql.Selector{Field: "git"}, dagql.Selector{Field: "head"}); err != nil {
		return inst, err
	}
	return gitRepositoryWithDirectory(ctx, srv, head.Self().Repo, dir)
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
