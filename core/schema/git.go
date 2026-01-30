package schema

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/dagger/dagger/engine/sources/netconfhttp"
	"golang.org/x/mod/semver"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

func init() {
	// allow injection of custom dns resolver for go-git
	customClient := &http.Client{
		Transport: netconfhttp.NewInjectableTransport(http.DefaultTransport),
	}
	client.InstallProtocol("http", githttp.NewClient(customClient))
	client.InstallProtocol("https", githttp.NewClient(customClient))
}

var _ SchemaResolvers = &gitSchema{}

type gitSchema struct{}

func (s *gitSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.NodeFunc("git", s.git).
			View(AllVersion).
			Doc(`Queries a Git repository.`).
			Args(
				dagql.Arg("url").Doc(
					`URL of the git repository.`,
					"Can be formatted as `https://{host}/{owner}/{repo}`, `git@{host}:{owner}/{repo}`.",
					`Suffix ".git" is optional.`),
				dagql.Arg("keepGitDir").
					View(AllVersion).
					Default(dagql.Opt(dagql.Boolean(true))).
					Doc(`Set to true to keep .git directory.`).Deprecated(),
				dagql.Arg("keepGitDir").
					View(BeforeVersion("v0.13.4")).
					Doc(`Set to true to keep .git directory.`).Deprecated(),
				dagql.Arg("sshKnownHosts").Doc(`Set SSH known hosts`),
				dagql.Arg("sshAuthSocket").Doc(`Set SSH auth socket`),
				dagql.Arg("httpAuthUsername").Doc(`Username used to populate the password during basic HTTP Authorization`),
				dagql.Arg("httpAuthToken").Doc(`Secret used to populate the password during basic HTTP Authorization`),
				dagql.Arg("httpAuthHeader").Doc(`Secret used to populate the Authorization HTTP header`),
				dagql.Arg("experimentalServiceHost").Doc(`A service which must be started before the repo is fetched.`),
			),
	}.Install(srv)

	dagql.Fields[*core.GitRepository]{
		dagql.NodeFunc("url", s.url).
			Doc(`The URL of the git repository.`),
		dagql.NodeFunc("head", s.head).
			Doc(`Returns details for HEAD.`),
		dagql.NodeFunc("ref", s.ref).
			Doc(`Returns details of a ref.`).
			Args(
				dagql.Arg("name").Doc(`Ref's name (can be a commit identifier, a tag name, a branch name, or a fully-qualified ref).`),
			),
		dagql.NodeFunc("branch", s.branch).
			View(AllVersion).
			Doc(`Returns details of a branch.`).
			Args(
				dagql.Arg("name").Doc(`Branch's name (e.g., "main").`),
			),
		dagql.NodeFunc("tag", s.tag).
			View(AllVersion).
			Doc(`Returns details of a tag.`).
			Args(
				dagql.Arg("name").Doc(`Tag's name (e.g., "v0.3.9").`),
			),
		dagql.NodeFunc("commit", s.commit).
			View(AllVersion).
			Doc(`Returns details of a commit.`).
			Args(
				// TODO: id is normally a reserved word; we should probably rename this
				dagql.Arg("id").Doc(`Identifier of the commit (e.g., "b6315d8f2810962c601af73f86831f6866ea798b").`),
			),
		dagql.NodeFunc("latestVersion", s.latestVersion).
			Doc(`Returns details for the latest semver tag.`),

		dagql.NodeFuncWithCacheKey("tags", s.tags, dagql.CachePerClient).
			Doc(`tags that match any of the given glob patterns.`).
			Args(
				dagql.Arg("patterns").Doc(`Glob patterns (e.g., "refs/tags/v*").`),
			),
		dagql.NodeFuncWithCacheKey("branches", s.branches, dagql.CachePerClient).
			Doc(`branches that match any of the given glob patterns.`).
			Args(
				dagql.Arg("patterns").Doc(`Glob patterns (e.g., "refs/tags/v*").`),
			),

		dagql.NodeFunc("__cleaned", DagOpDirectoryWrapper(srv, s.cleaned, WithPathFn(keepParentGitDir[cleanedArgs]))).
			Doc(`(Internal-only) Cleans the git repository by removing untracked files and resetting modifications.`),
		dagql.NodeFunc("uncommitted", s.uncommitted).
			Doc("Returns the changeset of uncommitted changes in the git repository."),

		dagql.Func("withAuthToken", s.withAuthToken).
			Doc(`Token to authenticate the remote with.`).
			View(BeforeVersion("v0.19.0")).
			Deprecated(`Use "httpAuthToken" in the constructor instead.`).
			Args(
				dagql.Arg("token").Doc(`Secret used to populate the password during basic HTTP Authorization`),
			),
		dagql.Func("withAuthHeader", s.withAuthHeader).
			Doc(`Header to authenticate the remote with.`).
			View(BeforeVersion("v0.19.0")).
			Deprecated(`Use "httpAuthHeader" in the constructor instead.`).
			Args(
				dagql.Arg("header").Doc(`Secret used to populate the Authorization HTTP header`),
			),

		dagql.NodeFuncWithCacheKey("__resolve", s.repoResolve, dagql.CachePerClient).
			Doc(`(Internal-only) Resolves URL and auth for this repo.`),
	}.Install(srv)

	dagql.Fields[*core.GitRef]{
		dagql.NodeFuncWithCacheKey("tree", s.tree, dagql.CachePerClient).
			View(AllVersion).
			Doc(`The filesystem tree at this ref.`).
			Args(
				dagql.Arg("discardGitDir").
					Doc(`Set to true to discard .git directory.`),
				dagql.Arg("depth").
					Doc(`The depth of the tree to fetch.`),
				dagql.Arg("sshKnownHosts").
					View(BeforeVersion("v0.12.0")).
					Doc("This option should be passed to `git` instead.").Deprecated(),
				dagql.Arg("sshAuthSocket").
					View(BeforeVersion("v0.12.0")).
					Doc("This option should be passed to `git` instead.").Deprecated(),
			),
		dagql.NodeFunc("__tree", DagOpDirectoryWrapper(srv, s.treeInternal)).
			Doc(`(Internal) Tree computation after auth validation.`).
			Args(
				dagql.Arg("discardGitDir"),
				dagql.Arg("depth"),
			),
		dagql.NodeFunc("commit", s.fetchCommit).
			Doc(`The resolved commit id at this ref.`),
		dagql.NodeFunc("ref", s.fetchRef).
			Doc(`The resolved ref name at this ref.`),
		dagql.NodeFunc("commonAncestor", s.commonAncestor).
			Doc(`Find the best common ancestor between this ref and another ref.`).
			Args(
				dagql.Arg("other").Doc(`The other ref to compare against.`),
			),

		dagql.NodeFuncWithCacheKey("__resolve", s.refResolve, dagql.CachePerClient).
			Doc(`(Internal-only) Resolves repo + ref to SHA.`),
	}.Install(srv)
}

type gitArgs struct {
	URL string

	KeepGitDir              dagql.Optional[dagql.Boolean] `default:"false"`
	ExperimentalServiceHost dagql.Optional[core.ServiceID]

	SSHKnownHosts string                        `name:"sshKnownHosts" default:""`
	SSHAuthSocket dagql.Optional[core.SocketID] `name:"sshAuthSocket"`

	HTTPAuthUsername string                        `name:"httpAuthUsername" default:""`
	HTTPAuthToken    dagql.Optional[core.SecretID] `name:"httpAuthToken"`
	HTTPAuthHeader   dagql.Optional[core.SecretID] `name:"httpAuthHeader"`

	// internal args that can override the HEAD ref+commit
	Commit string `default:"" internal:"true"`
	Ref    string `default:"" internal:"true"`
}

//nolint:gocyclo
func (s *gitSchema) git(ctx context.Context, parent dagql.ObjectResult[*core.Query], args gitArgs) (inst dagql.ObjectResult[*core.GitRepository], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	remote, err := gitutil.ParseURL(args.URL)
	if err != nil && !errors.Is(err, gitutil.ErrUnknownProtocol) {
		return inst, fmt.Errorf("failed to parse git URL: %w", err)
	}

	var gitServices core.ServiceBindings
	if args.ExperimentalServiceHost.Valid {
		svc, err := args.ExperimentalServiceHost.Value.Load(ctx, srv)
		if err != nil {
			return inst, err
		}
		host, err := svc.Self().Hostname(ctx, svc.ID())
		if err != nil {
			return inst, err
		}
		gitServices = append(gitServices, core.ServiceBinding{
			Service:  svc,
			Hostname: host,
		})
	}

	discardGitDir := false
	if args.KeepGitDir.Valid {
		discardGitDir = !args.KeepGitDir.Value.Bool()
	}

	rb := &core.RemoteGitRepository{
		URL:           remote,
		SSHKnownHosts: args.SSHKnownHosts,
		SSHAuthSocket: args.SSHAuthSocket,
		AuthUsername:  args.HTTPAuthUsername,
		AuthToken:     args.HTTPAuthToken,
		AuthHeader:    args.HTTPAuthHeader,
		Services:      gitServices,
		Platform:      parent.Self().Platform(),
	}

	var head *gitutil.Ref
	if args.Ref != "" || args.Commit != "" {
		head = &gitutil.Ref{
			Name: args.Ref,
			SHA:  args.Commit,
		}
	}

	repo, err := core.NewGitRepository(ctx, rb)
	if err != nil {
		return inst, err
	}
	repo.PinnedHead = head
	repo.DiscardGitDir = discardGitDir

	inst, err = dagql.NewObjectResultForCurrentID(ctx, srv, repo)
	if err != nil {
		return inst, err
	}

	dgstInputs := []string{
		// all details of the remote repo
		args.URL,
		// legacy args
		strconv.FormatBool(repo.DiscardGitDir),
		// also include what auth methods are used, currently we can't
		// handle a cache hit where the result has a different auth
		// method than the caller used (i.e. a git repo is pulled w/
		// a token but hits cache for a dir where a ssh sock was used)
		// -> see below
	}

	if args.SSHAuthSocket.Valid {
		dgstInputs = append(dgstInputs, "sshAuthSock", args.SSHAuthSocket.Value.ID().Digest().String())
	}
	if args.HTTPAuthToken.Valid {
		dgstInputs = append(dgstInputs, "authToken", args.HTTPAuthToken.Value.ID().Digest().String())
	}
	if args.HTTPAuthHeader.Valid {
		dgstInputs = append(dgstInputs, "authHeader", args.HTTPAuthHeader.Value.ID().Digest().String())
	}

	if head != nil {
		dgstInputs = append(
			dgstInputs,
			"head-name:"+head.Name,
			"head-sha:"+head.SHA,
		)
	}

	inst = inst.WithObjectDigest(hashutil.HashStrings(dgstInputs...))

	// Resource transfer for secrets/sockets across client boundaries
	var resourceIDs []*resource.ID
	if args.SSHAuthSocket.Valid {
		resourceIDs = append(resourceIDs, &resource.ID{ID: *args.SSHAuthSocket.Value.ID()})
	}
	if args.HTTPAuthToken.Valid {
		resourceIDs = append(resourceIDs, &resource.ID{ID: *args.HTTPAuthToken.Value.ID()})
	}
	if args.HTTPAuthHeader.Valid {
		resourceIDs = append(resourceIDs, &resource.ID{ID: *args.HTTPAuthHeader.Value.ID()})
	}
	if len(resourceIDs) > 0 {
		clientMetadata, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return inst, fmt.Errorf("failed to get client metadata: %w", err)
		}
		postCall, err := core.ResourceTransferPostCall(ctx, parent.Self(), clientMetadata.ClientID, resourceIDs...)
		if err != nil {
			return inst, fmt.Errorf("failed to create post call: %w", err)
		}
		inst = inst.ObjectResultWithPostCall(postCall)
	}

	return inst, nil
}

func (s *gitSchema) url(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args struct{}) (dagql.Nullable[dagql.String], error) {
	repo := parent.Self()

	_, isRemote := repo.Backend.(*core.RemoteGitRepository)
	if !isRemote {
		return dagql.Null[dagql.String](), nil
	}

	// If not resolved, select through __resolve and then url
	if !repo.Resolved {
		srv, err := core.CurrentDagqlServer(ctx)
		if err != nil {
			return dagql.Null[dagql.String](), fmt.Errorf("failed to get current dagql server: %w", err)
		}

		var result dagql.String
		if err := srv.Select(ctx, parent, &result,
			dagql.Selector{Field: "__resolve"},
			dagql.Selector{Field: "url"},
		); err != nil {
			return dagql.Null[dagql.String](), err
		}
		return dagql.NonNull(result), nil
	}

	// Resolved - get the URL directly
	resolvedRemote := repo.Backend.(*core.RemoteGitRepository)
	return dagql.NonNull(dagql.String(resolvedRemote.URL.String())), nil
}

type refArgs struct {
	Name   string
	Commit string `default:"" internal:"true"`
}

func (s *gitSchema) ref(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args refArgs) (inst dagql.Result[*core.GitRef], _ error) {
	result := &core.GitRef{
		Repo: parent,
		Ref: &gitutil.Ref{
			Name: args.Name,
			SHA:  args.Commit,
		},
		Backend: nil,
	}
	return dagql.NewResultForCurrentID(ctx, result)
}

func (s *gitSchema) head(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args struct{}) (inst dagql.Result[*core.GitRef], _ error) {
	repo := parent.Self()
	if repo.PinnedHead != nil {
		name := repo.PinnedHead.Name
		if name == "" {
			name = "HEAD"
		}
		return s.ref(ctx, parent, refArgs{Name: name, Commit: repo.PinnedHead.SHA})
	}
	return s.ref(ctx, parent, refArgs{Name: "HEAD"})
}

func (s *gitSchema) latestVersion(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args struct{}) (inst dagql.Result[*core.GitRef], _ error) {
	repo := parent.Self()

	// If not resolved, select through __resolve and then latestVersion
	if !repo.Resolved {
		srv, err := core.CurrentDagqlServer(ctx)
		if err != nil {
			return inst, fmt.Errorf("failed to get current dagql server: %w", err)
		}

		var result dagql.ObjectResult[*core.GitRef]
		if err := srv.Select(ctx, parent, &result,
			dagql.Selector{Field: "__resolve"},
			dagql.Selector{Field: "latestVersion"},
		); err != nil {
			return inst, err
		}
		return result.Result, nil
	}

	// Resolved - get the latest version
	remote, err := repo.Backend.Remote(ctx)
	if err != nil {
		return inst, err
	}
	tags := remote.Tags().Filter([]string{"refs/tags/v*"}).ShortNames()
	tags = slices.DeleteFunc(tags, func(tag string) bool {
		return !semver.IsValid(tag)
	})
	if len(tags) == 0 {
		return inst, fmt.Errorf("no valid semver tags found")
	}
	semver.Sort(tags)
	tag := tags[len(tags)-1]
	return s.ref(ctx, parent, refArgs{Name: "refs/tags/" + tag})
}

type commitArgs struct {
	ID string
}

func supportsStrictRefs(ctx context.Context) bool {
	return core.Supports(ctx, "v0.19.0")
}

func (s *gitSchema) commit(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args commitArgs) (inst dagql.Result[*core.GitRef], _ error) {
	if supportsStrictRefs(ctx) && !gitutil.IsCommitSHA(args.ID) {
		return inst, fmt.Errorf("invalid commit SHA: %q", args.ID)
	}
	return s.ref(ctx, parent, refArgs{Name: args.ID})
}

type branchArgs refArgs

func (s *gitSchema) branch(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args branchArgs) (dagql.Result[*core.GitRef], error) {
	if supportsStrictRefs(ctx) {
		args.Name = "refs/heads/" + strings.TrimPrefix(args.Name, "refs/heads/")
	}
	return s.ref(ctx, parent, refArgs(args))
}

type tagArgs refArgs

func (s *gitSchema) tag(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args tagArgs) (dagql.Result[*core.GitRef], error) {
	if supportsStrictRefs(ctx) {
		args.Name = "refs/tags/" + strings.TrimPrefix(args.Name, "refs/tags/")
	}
	return s.ref(ctx, parent, refArgs(args))
}

type tagsArgs struct {
	Patterns dagql.Optional[dagql.ArrayInput[dagql.String]] `name:"patterns"`
}

type branchesArgs struct {
	Patterns dagql.Optional[dagql.ArrayInput[dagql.String]] `name:"patterns"`
}

//nolint:dupl
func (s *gitSchema) tags(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args tagsArgs) (dagql.Array[dagql.String], error) {
	repo := parent.Self()

	// If not resolved, select through __resolve and then tags
	if !repo.Resolved {
		srv, err := core.CurrentDagqlServer(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get current dagql server: %w", err)
		}

		tagsSelector := dagql.Selector{Field: "tags"}
		if args.Patterns.Valid {
			tagsSelector.Args = []dagql.NamedInput{{Name: "patterns", Value: args.Patterns}}
		}

		var result dagql.Array[dagql.String]
		if err := srv.Select(ctx, parent, &result,
			dagql.Selector{Field: "__resolve"},
			tagsSelector,
		); err != nil {
			return nil, err
		}
		return result, nil
	}

	// Resolved - get tags directly
	var patterns []string
	if args.Patterns.Valid {
		for _, pattern := range args.Patterns.Value {
			patterns = append(patterns, pattern.String())
		}
	}

	remote, err := repo.Backend.Remote(ctx)
	if err != nil {
		return nil, err
	}
	return dagql.NewStringArray(remote.Filter(patterns).Tags().ShortNames()...), nil
}

//nolint:dupl
func (s *gitSchema) branches(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args branchesArgs) (dagql.Array[dagql.String], error) {
	repo := parent.Self()

	// If not resolved, select through __resolve and then branches
	if !repo.Resolved {
		srv, err := core.CurrentDagqlServer(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get current dagql server: %w", err)
		}

		branchesSelector := dagql.Selector{Field: "branches"}
		if args.Patterns.Valid {
			branchesSelector.Args = []dagql.NamedInput{{Name: "patterns", Value: args.Patterns}}
		}

		var result dagql.Array[dagql.String]
		if err := srv.Select(ctx, parent, &result,
			dagql.Selector{Field: "__resolve"},
			branchesSelector,
		); err != nil {
			return nil, err
		}
		return result, nil
	}

	// Resolved - get branches directly
	var patterns []string
	if args.Patterns.Valid {
		for _, pattern := range args.Patterns.Value {
			patterns = append(patterns, pattern.String())
		}
	}

	remote, err := repo.Backend.Remote(ctx)
	if err != nil {
		return nil, err
	}
	return dagql.NewStringArray(remote.Filter(patterns).Branches().ShortNames()...), nil
}

type cleanedArgs struct {
	DagOpInternalArgs
}

func keepParentGitDir[A any](_ context.Context, repo *core.GitRepository, _ A) (string, error) {
	if local, ok := repo.Backend.(*core.LocalGitRepository); ok {
		return local.Directory.Self().Dir, nil
	}
	return "", nil
}

func (s *gitSchema) cleaned(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args cleanedArgs) (inst dagql.ObjectResult[*core.Directory], _ error) {
	dir, err := parent.Self().Backend.Cleaned(ctx)
	if err != nil {
		return inst, err
	}
	return dir, nil
}

func (s *gitSchema) uncommitted(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args struct{}) (inst dagql.ObjectResult[*core.Changeset], _ error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	var cleaned dagql.ObjectResult[*core.Directory]
	var dirty dagql.ObjectResult[*core.Directory]

	dirty, err = parent.Self().Backend.Dirty(ctx)
	if err != nil {
		return inst, err
	}
	if dirty.Self() == nil {
		// clean repo, so just get head, there'll be no diff later
		if err := dag.Select(ctx, parent, &dirty,
			dagql.Selector{
				Field: "head",
			},
			dagql.Selector{
				Field: "tree",
			},
		); err != nil {
			return inst, fmt.Errorf("failed to select head tree for clean repo: %w", err)
		}
		cleaned = dirty
	} else {
		// wrapped in an internal field to get good caching behavior
		if err := dag.Select(ctx, parent, &cleaned, dagql.Selector{
			Field: "__cleaned",
		}); err != nil {
			return inst, fmt.Errorf("failed to select cleaned: %w", err)
		}
	}

	if err := dag.Select(ctx, dirty, &inst,
		dagql.Selector{
			Field: "changes",
			Args: []dagql.NamedInput{
				{
					Name:  "from",
					Value: dagql.NewID[*core.Directory](cleaned.ID()),
				},
			},
		},
	); err != nil {
		return inst, fmt.Errorf("failed to select cleaned digest: %w", err)
	}
	return inst, nil
}

type withAuthTokenArgs struct {
	Token core.SecretID
}

func (s *gitSchema) withAuthToken(ctx context.Context, parent *core.GitRepository, args withAuthTokenArgs) (*core.GitRepository, error) {
	repo := *parent
	if remote, ok := repo.Backend.(*core.RemoteGitRepository); ok {
		remoteCopy := *remote
		remoteCopy.AuthToken = dagql.Opt(args.Token)
		repo.Backend = &remoteCopy
	}
	return &repo, nil
}

type withAuthHeaderArgs struct {
	Header core.SecretID
}

func (s *gitSchema) withAuthHeader(ctx context.Context, parent *core.GitRepository, args withAuthHeaderArgs) (*core.GitRepository, error) {
	repo := *parent
	if remote, ok := repo.Backend.(*core.RemoteGitRepository); ok {
		remoteCopy := *remote
		remoteCopy.AuthHeader = dagql.Opt(args.Header)
		repo.Backend = &remoteCopy
	}
	return &repo, nil
}

type treeArgs struct {
	DiscardGitDir bool `default:"false"`
	Depth         int  `default:"1"`

	SSHKnownHosts dagql.Optional[dagql.String]  `name:"sshKnownHosts"`
	SSHAuthSocket dagql.Optional[core.SocketID] `name:"sshAuthSocket"`
}

func (s *gitSchema) tree(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args treeArgs) (inst dagql.ObjectResult[*core.Directory], _ error) {
	if args.SSHKnownHosts.Valid {
		return inst, fmt.Errorf("sshKnownHosts is no longer supported on `tree`")
	}
	if args.SSHAuthSocket.Valid {
		return inst, fmt.Errorf("sshAuthSocket is no longer supported on `tree`")
	}

	ref := parent.Self()

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	// Build args for selectors
	buildTreeArgs := func() []dagql.NamedInput {
		treeArgs := []dagql.NamedInput{}
		if args.DiscardGitDir {
			treeArgs = append(treeArgs, dagql.NamedInput{Name: "discardGitDir", Value: dagql.Boolean(true)})
		}
		if args.Depth != 1 {
			treeArgs = append(treeArgs, dagql.NamedInput{Name: "depth", Value: dagql.Int(args.Depth)})
		}
		return treeArgs
	}

	// If not resolved, select through __resolve and then tree (recursion)
	if !ref.Resolved {
		treeSelector := dagql.Selector{Field: "tree"}
		if treeArgs := buildTreeArgs(); len(treeArgs) > 0 {
			treeSelector.Args = treeArgs
		}

		var result dagql.ObjectResult[*core.Directory]
		if err := srv.Select(ctx, parent, &result,
			dagql.Selector{Field: "__resolve"},
			treeSelector,
		); err != nil {
			return inst, err
		}
		return result, nil
	}

	// Resolved - call __tree on parent
	// The parent's ID now includes auth (due to redirect in refResolve),
	// so __tree's CurrentID will also include auth in the chain
	treeSelector := dagql.Selector{Field: "__tree"}
	if treeArgs := buildTreeArgs(); len(treeArgs) > 0 {
		treeSelector.Args = treeArgs
	}

	var result dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, parent, &result, treeSelector); err != nil {
		return inst, err
	}
	return result, nil
}

type treeInternalArgs struct {
	DiscardGitDir bool `default:"false"`
	Depth         int  `default:"1"`
	DagOpInternalArgs
}

func (s *gitSchema) treeInternal(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args treeInternalArgs) (inst dagql.ObjectResult[*core.Directory], _ error) {
	ref := parent.Self()
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	// Do the actual tree fetch
	dir, err := ref.Tree(ctx, srv, args.DiscardGitDir, args.Depth)
	if err != nil {
		return inst, err
	}
	// The DagOpDirectoryWrapper handles all the buildkit caching.
	// CurrentID(ctx) now correctly includes the socket because we're called via
	// Select from the resolved parent (which has the socket in its ID chain).
	return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
}

func (s *gitSchema) fetchCommit(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRef],
	args RawDagOpInternalArgs,
) (dagql.String, error) {
	ref := parent.Self()

	// If not resolved, select through __resolve and then commit
	if !ref.Resolved {
		srv, err := core.CurrentDagqlServer(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to get current dagql server: %w", err)
		}

		var result dagql.String
		if err := srv.Select(ctx, parent, &result,
			dagql.Selector{Field: "__resolve"},
			dagql.Selector{Field: "commit"},
		); err != nil {
			return "", err
		}
		return result, nil
	}

	// Resolved - return the SHA directly
	return dagql.NewString(ref.Ref.SHA), nil
}

func (s *gitSchema) fetchRef(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRef],
	args RawDagOpInternalArgs,
) (dagql.String, error) {
	ref := parent.Self()

	// If not resolved, select through __resolve and then ref
	if !ref.Resolved {
		srv, err := core.CurrentDagqlServer(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to get current dagql server: %w", err)
		}

		var result dagql.String
		if err := srv.Select(ctx, parent, &result,
			dagql.Selector{Field: "__resolve"},
			dagql.Selector{Field: "ref"},
		); err != nil {
			return "", err
		}
		return result, nil
	}

	// Resolved - return the ref name or SHA
	return dagql.NewString(cmp.Or(ref.Ref.Name, ref.Ref.SHA)), nil
}

type mergeBaseArgs struct {
	Other core.GitRefID

	RawDagOpInternalArgs
}

func (s *gitSchema) commonAncestor(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRef],
	args mergeBaseArgs,
) (inst dagql.ObjectResult[*core.GitRef], _ error) {
	ref := parent.Self()

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	// Load the other ref
	other, err := args.Other.Load(ctx, srv)
	if err != nil {
		return inst, err
	}

	// If self is not resolved, select through the chain
	if !ref.Resolved {
		var result dagql.ObjectResult[*core.GitRef]
		if err := srv.Select(ctx, parent, &result,
			dagql.Selector{Field: "__resolve"},
			dagql.Selector{Field: "commonAncestor", Args: []dagql.NamedInput{
				{Name: "other", Value: dagql.NewID[*core.GitRef](other.ID())},
			}},
		); err != nil {
			return inst, err
		}
		return result, nil
	}

	// Self is resolved - check if other is resolved
	if !other.Self().Resolved {
		// Resolve other first, then re-call with resolved other
		var resolvedOther dagql.ObjectResult[*core.GitRef]
		if err := srv.Select(ctx, other, &resolvedOther, dagql.Selector{Field: "__resolve"}); err != nil {
			return inst, err
		}

		var result dagql.ObjectResult[*core.GitRef]
		if err := srv.Select(ctx, parent, &result,
			dagql.Selector{Field: "commonAncestor", Args: []dagql.NamedInput{
				{Name: "other", Value: dagql.NewID[*core.GitRef](resolvedOther.ID())},
			}},
		); err != nil {
			return inst, err
		}
		return result, nil
	}

	// Both resolved - compute merge base
	result, err := core.MergeBase(ctx, ref, other.Self())
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, result)
}

// repoResolve implements GitRepository.__resolve.
//
// Uses a recursive redirect pattern:
// 1. Ambiguous URL (no protocol) → redirect to git(url: "https://...") or git(url: "ssh://...")
// 2. Explicit URL but no auth → redirect to git(url: "...", sshAuthSocket: X) with auth from context
// 3. Explicit URL with auth → actually resolve (leaf case)
//
// This makes auth explicit in the DAG, enabling natural ID-based caching.
func (s *gitSchema) repoResolve(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRepository],
	_ struct{},
) (dagql.ObjectResult[*core.GitRepository], error) {
	var zero dagql.ObjectResult[*core.GitRepository]

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return zero, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	repo := parent.Self()
	remote, isRemote := repo.Backend.(*core.RemoteGitRepository)

	// Local repo - just mark resolved and return
	if !isRemote {
		resolved := repo.Clone()
		resolved.Resolved = true
		return dagql.NewObjectResultForCurrentID(ctx, srv, resolved)
	}

	// Case 1: Ambiguous URL (no protocol) - redirect to explicit URL variant
	if remote.URL == nil {
		return s.resolveAmbiguousURLViaRedirect(ctx, srv, parent, remote)
	}

	// Case 2: Explicit URL but no auth - redirect with auth from context
	if !s.hasExplicitAuth(remote) {
		if result, ok, err := s.resolveWithAuthFromContext(ctx, srv, parent, remote); err != nil {
			return zero, err
		} else if ok {
			return result, nil
		}
		// No auth available from context, continue without auth (public repo)
	}

	// Case 3: Leaf case - explicit URL (and auth if needed), actually resolve
	// Note: SSH URL without socket check is done in git() for explicit URLs. --> todo(remove as it's wrong now)
	// For ambiguous URLs that fall through to SSH, the ls-remote will fail naturally.

	resolved := repo.Clone()
	remoteClone := remote.Clone()
	resolved.Backend = remoteClone

	if err := resolved.Resolve(ctx); err != nil {
		return zero, err
	}
	resolved.Resolved = true

	return dagql.NewObjectResultForCurrentID(ctx, srv, resolved)
}

// hasExplicitAuth returns true if auth is explicitly set on the remote.
func (s *gitSchema) hasExplicitAuth(remote *core.RemoteGitRepository) bool {
	return remote.SSHAuthSocket.Valid || remote.AuthToken.Valid || remote.AuthHeader.Valid
}

// resolveAmbiguousURLViaRedirect handles URLs without a protocol by redirecting
// to an explicit URL variant via srv.Select.
func (s *gitSchema) resolveAmbiguousURLViaRedirect(
	ctx context.Context,
	srv *dagql.Server,
	parent dagql.ObjectResult[*core.GitRepository],
	remote *core.RemoteGitRepository,
) (dagql.ObjectResult[*core.GitRepository], error) {
	var zero dagql.ObjectResult[*core.GitRepository]
	rawURL := parent.ID().Arg("url").Value().ToInput().(string)

	// If auth method is specified, we can infer the scheme
	if remote.SSHAuthSocket.Valid {
		return s.redirectToURL(ctx, srv, parent, remote, "ssh://git@"+rawURL)
	}
	if remote.AuthToken.Valid || remote.AuthHeader.Valid {
		return s.redirectToURL(ctx, srv, parent, remote, "https://"+rawURL)
	}

	// No auth specified - try https first, then ssh
	candidates := []string{"https://" + rawURL, "ssh://git@" + rawURL}

	for _, candidateURL := range candidates {
		var result dagql.ObjectResult[*core.GitRepository]
		err := srv.Select(ctx, srv.Root(), &result,
			dagql.Selector{
				Field: "git",
				Args:  s.buildRedirectArgs(parent, remote, candidateURL, nil),
			},
			dagql.Selector{Field: "__resolve"},
		)
		if err == nil {
			return result, nil
		}
		// If it's an auth error, try next candidate
		if errors.Is(err, gitutil.ErrGitAuthFailed) {
			continue
		}
		// Other errors - return immediately
		return zero, err
	}

	return zero, fmt.Errorf("failed to resolve git URL: tried https and ssh")
}

// resolveWithAuthFromContext attempts to redirect with auth from client context.
// Returns (result, true, nil) if redirect succeeded, (zero, false, nil) if no auth available,
// or (zero, false, err) on error.
func (s *gitSchema) resolveWithAuthFromContext(
	ctx context.Context,
	srv *dagql.Server,
	parent dagql.ObjectResult[*core.GitRepository],
	remote *core.RemoteGitRepository,
) (dagql.ObjectResult[*core.GitRepository], bool, error) {
	var zero dagql.ObjectResult[*core.GitRepository]

	switch remote.URL.Scheme {
	case gitutil.SSHProtocol:
		// Default to git user for SSH, otherwise weird incorrect defaults
		// like "root" can get applied in various places.
		if remote.URL.User == nil {
			remote.URL.User = url.User("git")
		}

		// Try to get SSH socket from client context
		clientMD, err := engine.ClientMetadataFromContext(ctx)
		if err != nil || clientMD.SSHAuthSocketPath == "" {
			// No SSH socket available - return error for explicit SSH URLs
			return zero, false, fmt.Errorf("%w: SSH URLs are not supported without an SSH socket", gitutil.ErrGitAuthFailed)
		}

		// Get the socket object
		var sock dagql.ObjectResult[*core.Socket]
		if err := srv.Select(ctx, srv.Root(), &sock,
			dagql.Selector{Field: "host"},
			dagql.Selector{Field: "unixSocket", Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(clientMD.SSHAuthSocketPath)},
			}},
		); err != nil {
			return zero, false, fmt.Errorf("%w: failed to get SSH socket: %v", gitutil.ErrGitAuthFailed, err)
		}

		// Redirect to git() with explicit sshAuthSocket
		authArgs := []dagql.NamedInput{
			{Name: "sshAuthSocket", Value: dagql.Opt(dagql.NewID[*core.Socket](sock.ID()))},
		}

		var result dagql.ObjectResult[*core.GitRepository]
		if err := srv.Select(ctx, srv.Root(), &result,
			dagql.Selector{
				Field: "git",
				Args:  s.buildRedirectArgs(parent, remote, remote.URL.String(), authArgs),
			},
			dagql.Selector{Field: "__resolve"},
		); err != nil {
			return zero, false, err
		}
		return result, true, nil

	case gitutil.HTTPProtocol, gitutil.HTTPSProtocol:
		// Try to get credentials from store
		token, username, err := s.getCredentialFromStore(ctx, srv, remote.URL)
		if err != nil || token == nil {
			return zero, false, nil // No credentials available
		}

		// Redirect to git() with explicit httpAuthToken
		authArgs := []dagql.NamedInput{
			{Name: "httpAuthToken", Value: dagql.Opt(dagql.NewID[*core.Secret](token.ID()))},
		}
		if username != "" {
			authArgs = append(authArgs, dagql.NamedInput{
				Name: "httpAuthUsername", Value: dagql.NewString(username),
			})
		}

		var result dagql.ObjectResult[*core.GitRepository]
		if err := srv.Select(ctx, srv.Root(), &result,
			dagql.Selector{
				Field: "git",
				Args:  s.buildRedirectArgs(parent, remote, remote.URL.String(), authArgs),
			},
			dagql.Selector{Field: "__resolve"},
		); err != nil {
			return zero, false, err
		}
		return result, true, nil
	}

	return zero, false, nil
}

// redirectToURL redirects to a git() call with the given URL.
func (s *gitSchema) redirectToURL(
	ctx context.Context,
	srv *dagql.Server,
	parent dagql.ObjectResult[*core.GitRepository],
	remote *core.RemoteGitRepository,
	url string,
) (dagql.ObjectResult[*core.GitRepository], error) {
	var result dagql.ObjectResult[*core.GitRepository]
	err := srv.Select(ctx, srv.Root(), &result,
		dagql.Selector{
			Field: "git",
			Args:  s.buildRedirectArgs(parent, remote, url, nil),
		},
		dagql.Selector{Field: "__resolve"},
	)
	return result, err
}

// buildRedirectArgs builds the args for a redirect git() call,
// preserving relevant args from the parent and overriding URL and auth.
func (s *gitSchema) buildRedirectArgs(
	parent dagql.ObjectResult[*core.GitRepository],
	remote *core.RemoteGitRepository,
	url string,
	authArgs []dagql.NamedInput,
) []dagql.NamedInput {
	args := []dagql.NamedInput{
		{Name: "url", Value: dagql.NewString(url)},
	}

	// Add explicit auth args if provided (these override parent's auth)
	args = append(args, authArgs...)

	// If no explicit auth args, preserve auth from remote object
	if len(authArgs) == 0 {
		if remote.AuthToken.Valid {
			args = append(args, dagql.NamedInput{
				Name: "httpAuthToken", Value: dagql.Opt(dagql.NewID[*core.Secret](remote.AuthToken.Value.ID())),
			})
		}
		if remote.AuthHeader.Valid {
			args = append(args, dagql.NamedInput{
				Name: "httpAuthHeader", Value: dagql.Opt(dagql.NewID[*core.Secret](remote.AuthHeader.Value.ID())),
			})
		}
		if remote.AuthUsername != "" {
			args = append(args, dagql.NamedInput{
				Name: "httpAuthUsername", Value: dagql.NewString(remote.AuthUsername),
			})
		}
		if remote.SSHAuthSocket.Valid {
			args = append(args, dagql.NamedInput{
				Name: "sshAuthSocket", Value: dagql.Opt(dagql.NewID[*core.Socket](remote.SSHAuthSocket.Value.ID())),
			})
		}
	}

	// Preserve keepGitDir from parent if set
	if keepGitDir := parent.ID().Arg("keepGitDir"); keepGitDir != nil {
		if v, ok := keepGitDir.Value().ToInput().(bool); ok && v {
			args = append(args, dagql.NamedInput{
				Name: "keepGitDir", Value: dagql.Opt(dagql.Boolean(true)),
			})
		}
	}

	// Preserve sshKnownHosts from parent if set
	if sshKnownHosts := parent.ID().Arg("sshKnownHosts"); sshKnownHosts != nil {
		if v, ok := sshKnownHosts.Value().ToInput().(string); ok && v != "" {
			args = append(args, dagql.NamedInput{
				Name: "sshKnownHosts", Value: dagql.NewString(v),
			})
		}
	}

	// Preserve commit from parent if set (internal arg for pinning)
	if commit := parent.ID().Arg("commit"); commit != nil {
		if v, ok := commit.Value().ToInput().(string); ok && v != "" {
			args = append(args, dagql.NamedInput{
				Name: "commit", Value: dagql.NewString(v),
			})
		}
	}

	// Preserve ref from parent if set (internal arg for pinning)
	if ref := parent.ID().Arg("ref"); ref != nil {
		if v, ok := ref.Value().ToInput().(string); ok && v != "" {
			args = append(args, dagql.NamedInput{
				Name: "ref", Value: dagql.NewString(v),
			})
		}
	}

	// Preserve experimentalServiceHost from parent if set
	if remote.Services != nil {
		if svcArg := parent.ID().Arg("experimentalServiceHost"); svcArg != nil {
			if litID, ok := svcArg.Value().(*call.LiteralID); ok {
				args = append(args, dagql.NamedInput{
					Name: "experimentalServiceHost", Value: dagql.Opt(dagql.NewID[*core.Service](litID.Value())),
				})
			}
		}
	}

	return args
}

// getCredentialFromStore retrieves credentials from the client's credential store.
func (s *gitSchema) getCredentialFromStore(
	ctx context.Context,
	srv *dagql.Server,
	parsedURL *gitutil.GitURL,
) (*dagql.ObjectResult[*core.Secret], string, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, "", nil
	}

	parentMD, err := query.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return nil, "", nil
	}

	clientMD, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, "", nil
	}

	if clientMD.ClientID != parentMD.ClientID {
		return nil, "", nil
	}

	authCtx := engine.ContextWithClientMetadata(ctx, parentMD)
	bk, err := query.Buildkit(authCtx)
	if err != nil {
		return nil, "", nil
	}

	creds, err := bk.GetCredential(authCtx, parsedURL.Scheme, parsedURL.Host, parsedURL.Path)
	if err != nil {
		return nil, "", nil
	}
	if creds.Password == "" {
		return nil, "", nil
	}

	// Create a secret for the token
	sum := sha256.Sum256([]byte(creds.Password))
	secretName := hex.EncodeToString(sum[:])

	var token dagql.ObjectResult[*core.Secret]
	if err := srv.Select(authCtx, srv.Root(), &token,
		dagql.Selector{
			Field: "setSecret",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.NewString(secretName)},
				{Name: "plaintext", Value: dagql.NewString(creds.Password)},
			},
		},
	); err != nil {
		return nil, "", nil // Don't fail, just skip
	}

	return &token, creds.Username, nil
}

// refResolve implements GitRef.__resolve.
//
// Resolves the ref and returns it with a new ID rooted at the resolved repo,
// ensuring auth (sshAuthSocket, httpAuthToken) is in the ID chain.
func (s *gitSchema) refResolve(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRef],
	_ struct{},
) (dagql.ObjectResult[*core.GitRef], error) {
	var zero dagql.ObjectResult[*core.GitRef]

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return zero, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	// Resolve the repo via Select - returns canonical repo with auth in ID
	var resolvedRepo dagql.ObjectResult[*core.GitRepository]
	if err := srv.Select(ctx, parent.Self().Repo, &resolvedRepo,
		dagql.Selector{Field: "__resolve"},
	); err != nil {
		return zero, fmt.Errorf("failed to resolve repo: %w", err)
	}

	// Clone and resolve the ref
	ref := parent.Self().Clone()
	ref.Repo = resolvedRepo

	if err := ref.Resolve(ctx); err != nil {
		return zero, err
	}
	ref.Resolved = true

	// Build new ID: git(url-with-auth).ref(name).__resolve
	// This ensures auth is in the ID chain for subsequent operations (like tree)
	// Use Receiver() to get the repo's ID (without __resolve suffix)
	repoID := resolvedRepo.ID().Receiver()
	newID := repoID.
		Append(ref.Type(), "ref", call.WithArgs(
			call.NewArgument("name", call.NewLiteralString(parent.Self().Ref.Name), false),
		)).
		Append(ref.Type(), "__resolve")

	return dagql.NewObjectResultForID(ref, srv, newID)
}
