package schema

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type gitRefWithCommitArgs struct {
	Changes        dagql.ID[*core.Changeset]
	Message        string
	Date           string
	AuthorName     string
	AuthorEmail    string
	CommitterName  dagql.Optional[dagql.String]
	CommitterEmail dagql.Optional[dagql.String]
	CommitterDate  dagql.Optional[dagql.String]
	AllowEmpty     bool `default:"false"`
	Signoff        bool `default:"false"`
}

func (args gitRefWithCommitArgs) opts() (core.GitCommitOpts, error) {
	opts := core.GitCommitOpts{Message: args.Message, Date: args.Date, AuthorName: args.AuthorName, AuthorEmail: args.AuthorEmail, AllowEmpty: args.AllowEmpty, Signoff: args.Signoff,
		CommitterName: args.AuthorName, CommitterEmail: args.AuthorEmail, CommitterDate: args.Date}
	if args.CommitterName.Valid {
		opts.CommitterName = args.CommitterName.Value.String()
	}
	if args.CommitterEmail.Valid {
		opts.CommitterEmail = args.CommitterEmail.Value.String()
	}
	if args.CommitterDate.Valid {
		opts.CommitterDate = args.CommitterDate.Value.String()
	}
	for _, date := range []string{opts.Date, opts.CommitterDate} {
		if _, err := time.Parse(time.RFC3339, date); err != nil {
			return opts, fmt.Errorf("commit date must be RFC3339: %w", err)
		}
	}
	if strings.TrimSpace(opts.Message) == "" || strings.ContainsRune(opts.Message, 0) {
		return opts, fmt.Errorf("commit message must be nonempty and contain no NUL")
	}
	for _, identity := range [][2]string{{opts.AuthorName, opts.AuthorEmail}, {opts.CommitterName, opts.CommitterEmail}} {
		if strings.TrimSpace(identity[0]) == "" || strings.TrimSpace(identity[1]) == "" {
			return opts, fmt.Errorf("commit name and email must be nonempty")
		}
		if err := validateWorkspaceGitAuthor(identity[0], identity[1]); err != nil {
			return opts, err
		}
	}
	return opts, nil
}

func (args gitRefWithCommitArgs) selectors() []dagql.NamedInput {
	return []dagql.NamedInput{
		{Name: "changes", Value: args.Changes}, {Name: "message", Value: dagql.NewString(args.Message)},
		{Name: "date", Value: dagql.NewString(args.Date)}, {Name: "authorName", Value: dagql.NewString(args.AuthorName)}, {Name: "authorEmail", Value: dagql.NewString(args.AuthorEmail)},
		{Name: "committerName", Value: args.CommitterName}, {Name: "committerEmail", Value: args.CommitterEmail}, {Name: "committerDate", Value: args.CommitterDate}, {Name: "allowEmpty", Value: dagql.NewBoolean(args.AllowEmpty)},
		{Name: "signoff", Value: dagql.NewBoolean(args.Signoff)},
	}
}

func (s *gitSchema) gitRefWithCommit(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args gitRefWithCommitArgs) (inst dagql.ObjectResult[*core.GitRef], err error) {
	if _, err := args.opts(); err != nil {
		return inst, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	var repo dagql.ObjectResult[*core.GitRepository]
	if err := srv.Select(ctx, parent, &repo, dagql.Selector{Field: "__withCommitRepository", Args: args.selectors()}); err != nil {
		return inst, err
	}
	err = srv.Select(ctx, repo, &inst, dagql.Selector{Field: "head"})
	return inst, err
}

// Keep the parent relationship in a private, replayable repository recipe. A
// source-only checkout can then reuse the parent's canonical materialization;
// public withContents must not infer provenance from arbitrary supplied storage.
func (s *gitSchema) gitRefWithCommitRepository(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args gitRefWithCommitArgs) (inst dagql.ObjectResult[*core.GitRepository], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	var dir dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, parent, &dir, dagql.Selector{Field: "__withCommitDirectory", Args: args.selectors()}); err != nil {
		return inst, err
	}
	repo, err := gitRepositoryWithContents(ctx, srv, parent.Self().Repo, dir)
	if err != nil {
		return inst, err
	}
	if _, local := parent.Self().Backend.(*core.LocalGitRef); !local {
		// Reusing a remote parent after cache eviction could fetch it again,
		// despite the new repository already owning all required objects.
		return repo, nil
	}
	remote, err := repo.Self().LoadRemote(ctx)
	if err != nil {
		return inst, err
	}
	head, err := remote.Lookup("HEAD")
	if err != nil {
		return inst, fmt.Errorf("resolve committed repository HEAD: %w", err)
	}
	backend := &core.LocalGitRepository{
		Directory: dir,
		CheckoutBase: &core.GitCheckoutBase{
			Parent: parent, CommitSHA: head.SHA,
		},
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, repo.Self().CloneWithBackend(backend))
}

// This private field gives the materialized Git storage an identity that can be
// persisted and reconstructed independently of the ref returned by withCommit.
func (s *gitSchema) gitRefWithCommitDirectory(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args gitRefWithCommitArgs) (inst dagql.ObjectResult[*core.Directory], err error) {
	opts, err := args.opts()
	if err != nil {
		return inst, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	changes, err := args.Changes.Load(ctx, srv)
	if err != nil {
		return inst, err
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	dir, err := evaluatedDirectory(ctx, query, &core.DirectoryGitCommitLazy{LazyState: core.NewLazyState(), Parent: parent, Changes: changes, Opts: opts})
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
}
