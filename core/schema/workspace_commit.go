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
	Message     string
	Paths       []string `default:"[]"`
	Date        string
	AuthorName  dagql.Optional[dagql.String]
	AuthorEmail dagql.Optional[dagql.String]
	Signoff     bool `default:"false"`
}

type workspaceCommitOpts struct {
	core.GitCommitOpts
	Paths []string
}

func (args workspaceWithCommitArgs) opts(ws *core.Workspace) (workspaceCommitOpts, error) {
	opts := workspaceCommitOpts{GitCommitOpts: core.GitCommitOpts{
		Message: args.Message, Date: args.Date, Signoff: args.Signoff,
		AuthorName: args.AuthorName.Value.String(), AuthorEmail: args.AuthorEmail.Value.String(),
	}}
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
		{Name: "signoff", Value: dagql.NewBoolean(args.Signoff)},
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
	opts, err := args.opts(frozen.Self())
	if err != nil {
		return inst, err
	}
	selected := changes
	if len(opts.Paths) > 0 {
		paths, err := changes.Self().ComputePaths(ctx)
		if err != nil {
			return inst, err
		}
		matches := func(p string) bool {
			for _, scope := range opts.Paths {
				if scope == "." || p == scope || strings.HasPrefix(p, scope+"/") {
					return true
				}
			}
			return false
		}
		for to, from := range paths.Renamed {
			if matches(to) != matches(from) {
				return inst, fmt.Errorf("paths would split the rename %q -> %q; include both paths or neither", from, to)
			}
		}
		includes := make([]string, 0, len(opts.Paths))
		escape := strings.NewReplacer("\\", "\\\\", "*", "\\*", "?", "\\?", "[", "\\[")
		for _, p := range opts.Paths {
			if p == "." {
				includes = nil
				break
			}
			includes = append(includes, escape.Replace(p))
		}
		if err := srv.Select(ctx, changes, &selected, dagql.Selector{Field: "filter", Args: []dagql.NamedInput{{Name: "include", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(includes...))}}}); err != nil {
			return inst, err
		}
	}
	changesID, err := selected.ID()
	if err != nil {
		return inst, err
	}
	commitArgs := gitRefWithCommitArgs{Changes: dagql.NewID[*core.Changeset](changesID), Message: opts.Message, Date: opts.Date, AuthorName: opts.AuthorName, AuthorEmail: opts.AuthorEmail, Signoff: opts.Signoff}
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

func (s *workspaceSchema) workspaceGitDirectory(ctx context.Context, parent dagql.ObjectResult[*core.WorkspaceGit], _ struct{}) (inst dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	var head dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, parent, &head, dagql.Selector{Field: "head"}); err != nil {
		return inst, fmt.Errorf("workspace Git checkout requires a HEAD commit: %w", err)
	}
	// Bypass the repository's keepGitDir option and the default shallow depth.
	// Tree returns a newly owned, materialized snapshot rooted at /; expose its
	// metadata directly without an intermediate private GraphQL field.
	dir, err := head.Self().Backend.Tree(ctx, srv, false, 0, false)
	if err != nil {
		return inst, err
	}
	dir.Dir.SetValue("/.git")
	return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
}

// workspaceGitCheckout composes a clean full-history checkout from the public
// Git metadata API. The repository is local to the engine, so checking it out
// does not fetch history from the original remote again.
func workspaceGitCheckout(ctx context.Context, srv *dagql.Server, ws dagql.ObjectResult[*core.Workspace]) (inst dagql.ObjectResult[*core.Directory], err error) {
	err = srv.Select(ctx, ws, &inst,
		dagql.Selector{Field: "git"},
		dagql.Selector{Field: "directory"},
		dagql.Selector{Field: "asGit"},
		dagql.Selector{Field: "head"},
		dagql.Selector{Field: "tree", Args: []dagql.NamedInput{{Name: "depth", Value: dagql.NewInt(0)}}},
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
