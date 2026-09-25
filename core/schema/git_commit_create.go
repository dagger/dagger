package schema

import (
	"context"
	"errors"
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

type gitRefNativeCommitBaseArgs struct {
	Depth        int    `default:"0"`
	ParentRecipe string `internal:"true" default:""`
}

func (s *gitSchema) gitRefNativeCommitBaseKey(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], _ gitRefNativeCommitBaseArgs, req *dagql.CallRequest) error {
	// A ref's content digest may intentionally alias equivalent remote refs.
	// Object promotion must instead retain the exact recipe/auth/service scope.
	digest, err := parent.RecipeDigest(ctx)
	if err != nil {
		return err
	}
	return req.SetArgInput(ctx, "parentRecipe", dagql.String(digest.String()), false)
}

func (s *gitSchema) gitRefNativeCommitBase(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args gitRefNativeCommitBaseArgs) (inst dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	dir, err := core.GitRemoteCommitBase(ctx, parent, args.Depth)
	if err != nil {
		return inst, err
	}
	inst, err = dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
	if err != nil {
		return inst, errors.Join(err, dir.OnRelease(context.WithoutCancel(ctx)))
	}
	return inst, nil
}

type gitRefHydrateRepositoryArgs struct {
	Directory dagql.ID[*core.Directory]
	Scope     string `internal:"true" default:""`
}

func (s *gitSchema) gitRefHydrateRepositoryKey(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], _ gitRefHydrateRepositoryArgs, req *dagql.CallRequest) error {
	sourceKey, err := parent.RecipeDigest(ctx)
	if err != nil {
		return err
	}
	arg := req.Arg("directory")
	if arg == nil || arg.Value == nil || arg.Value.ResultRef == nil {
		return fmt.Errorf("hydrate repository requires an exact directory recipe")
	}
	// Use the request's structural input, not ID.Load's content-equivalent
	// value: aliases may share a snapshot but must not share owned provenance.
	dirKey, err := (dagql.ResultCallStructuralInputRef{Result: arg.Value.ResultRef}).InputDigest(ctx)
	if err != nil {
		return err
	}
	return req.SetArgInput(ctx, "scope", dagql.String(sourceKey.String()+" "+dirKey.String()), false)
}

func (s *gitSchema) gitRefHydrateRepository(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args gitRefHydrateRepositoryArgs) (inst dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	local, err := args.Directory.Load(ctx, srv)
	if err != nil {
		return inst, err
	}
	dir, err := core.HydrateGitRepository(ctx, parent, local)
	if err != nil {
		return inst, err
	}
	inst, err = dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
	if err != nil {
		return inst, errors.Join(err, dir.OnRelease(context.WithoutCancel(ctx)))
	}
	return inst, nil
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
	checkoutParent := parent
	var parentTree dagql.ObjectResult[*core.Directory]
	if _, remote := parent.Self().Backend.(*core.RemoteGitRef); remote {
		// Source-only tree recipes pin named refs to their resolved SHA. Retain
		// that same exact pinned recipe, including its repository/auth scope.
		if err := srv.Select(ctx, parent.Self().Repo, &checkoutParent, dagql.Selector{Field: "ref", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String(parent.Self().Ref.SHA)}}}); err != nil {
			return inst, err
		}
		// Pin the exact canonical tree, not a caller-supplied equal directory.
		// It is already materialized for same-base changes; evaluation here
		// makes its snapshot an owned dependency even if the mirror is evicted.
		if err := srv.Select(ctx, checkoutParent, &parentTree, dagql.Selector{Field: "tree", Args: []dagql.NamedInput{{Name: "discardGitDir", Value: dagql.Boolean(true)}}}); err != nil {
			return inst, err
		}
		if _, err := parentTree.Self().Snapshot.GetOrEval(ctx, parentTree.Result); err != nil {
			return inst, err
		}
	}
	remote, err := repo.Self().LoadRemote(ctx)
	if err != nil {
		return inst, err
	}
	head, err := remote.Lookup("HEAD")
	if err != nil {
		return inst, fmt.Errorf("resolve committed repository HEAD: %w", err)
	}
	var historySource dagql.ObjectResult[*core.GitRef]
	if _, remote := parent.Self().Backend.(*core.RemoteGitRef); remote {
		historySource = checkoutParent
	} else if local, ok := parent.Self().Repo.Self().Backend.(*core.LocalGitRepository); ok {
		historySource = local.HistorySource
	}
	backend := &core.LocalGitRepository{
		Directory:     dir,
		HistorySource: historySource,
		CheckoutBase: &core.GitCheckoutBase{
			Parent: checkoutParent, CommitSHA: head.SHA, Tree: parentTree,
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
	// Same-base edits can update an isolated Git index directly. Local storage
	// shares its existing objects through snapshot ancestry; remote inputs first
	// promote a private authorized closure, never a retained checkout. Divergent
	// and unsupported inputs still use the general reconciliation below.
	if dir, supported, err := core.GitCommitChangesetNative(ctx, parent, changes.Self(), opts); err != nil {
		return inst, err
	} else if supported {
		return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
	}
	var tree dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, parent, &tree, dagql.Selector{Field: "tree", Args: []dagql.NamedInput{{Name: "discardGitDir", Value: dagql.NewBoolean(true)}}}); err != nil {
		return inst, err
	}
	beforeID, err := changes.Self().Before.ID()
	if err != nil {
		return inst, err
	}
	var ours dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, tree, &ours, dagql.Selector{Field: "changes", Args: []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](beforeID)}}}); err != nil {
		return inst, err
	}
	var merged dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, ours, &merged, dagql.Selector{Field: "withChangeset", Args: []dagql.NamedInput{
		{Name: "changes", Value: args.Changes}, {Name: "onConflict", Value: core.FailOnMergeConflict},
	}}); err != nil {
		return inst, fmt.Errorf("apply commit changes: %w", err)
	}
	treeID, err := tree.ID()
	if err != nil {
		return inst, err
	}
	var applied dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, merged.Self().After, &applied, dagql.Selector{Field: "changes", Args: []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](treeID)}}}); err != nil {
		return inst, err
	}
	var ws dagql.ObjectResult[*core.Workspace]
	if err := srv.Select(ctx, parent, &ws, dagql.Selector{Field: "asWorkspace"}); err != nil {
		return inst, err
	}
	base, err := workspaceGitCheckout(ctx, srv, ws)
	if err != nil {
		return inst, err
	}
	dir, err := core.GitCommitChangeset(ctx, base, applied.Self(), opts)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
}
