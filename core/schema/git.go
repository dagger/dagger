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
	"github.com/dagger/dagger/engine/slog"
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
		dagql.NodeFuncWithCacheKey("git", s.git, dagql.CachePerClient).
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

		dagql.NodeFunc("tags", s.tags).
			Doc(`tags that match any of the given glob patterns.`).
			Args(
				dagql.Arg("patterns").Doc(`Glob patterns (e.g., "refs/tags/v*").`),
			),
		dagql.NodeFunc("branches", s.branches).
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
			Doc(`(Internal-only) Canonicalizes protocol and auth for this repo, as a DAG node.`),
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
		dagql.NodeFuncWithCacheKey("commit", s.fetchCommit, dagql.CachePerClient).
			Doc(`The resolved commit id at this ref.`),
		dagql.NodeFuncWithCacheKey("ref", s.fetchRef, dagql.CachePerClient).
			Doc(`The resolved ref name at this ref.`),
		dagql.NodeFunc("commonAncestor", s.commonAncestor).
			Doc(`Find the best common ancestor between this ref and another ref.`).
			Args(
				dagql.Arg("other").Doc(`The other ref to compare against.`),
			),

		dagql.NodeFuncWithCacheKey("__resolve", s.refResolve, dagql.CachePerClient).
			Doc(`(Internal-only) Canonicalizes repo + resolves ref to SHA, as a DAG node.`),
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

func (s *gitSchema) git(ctx context.Context, parent dagql.ObjectResult[*core.Query], args gitArgs) (inst dagql.Result[*core.GitRepository], _ error) {
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
		slog.Warn("The 'keepGitDir' argument is deprecated. Use `tree`'s `discardGitDir' instead.")
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

	inst, err = dagql.NewResultForCurrentID(ctx, repo)
	if err != nil {
		return inst, fmt.Errorf("failed to create GitRepository instance: %w", err)
	}

	// todo(question): do we really care about caching git() now ?
	// below shall be fine, but wondering if not overkill
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

	inst = inst.WithDigest(hashutil.HashStrings(dgstInputs...))
	return inst, nil
}

func (s *gitSchema) url(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args struct{}) (dagql.Nullable[dagql.String], error) {
	repo := parent.Self()

	remoteGitRepo, isRemote := repo.Backend.(*core.RemoteGitRepository)
	if !isRemote {
		return dagql.Null[dagql.String](), nil
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.Null[dagql.String](), fmt.Errorf("failed to get current dagql server: %w", err)
	}

	var resolved dagql.ObjectResult[*core.GitRepository]
	if err := srv.Select(ctx, parent, &resolved, dagql.Selector{Field: "__resolve"}); err != nil {
		return dagql.Null[dagql.String](), err
	}

	canonicalRepoRecv := resolved.ID()
	if canonicalRepoRecv.Digest() != parent.ID().Digest() {
		str, err := reenterType[dagql.String](ctx, canonicalRepoRecv, nil)
		if err != nil {
			return dagql.Null[dagql.String](), err
		}
		return dagql.NonNull(str), nil
	}

	remoteGitRepo = resolved.Self().Backend.(*core.RemoteGitRepository)
	return dagql.NonNull(dagql.String(remoteGitRepo.URL.String())), nil
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
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	var resolved dagql.ObjectResult[*core.GitRepository]
	if err := srv.Select(ctx, parent, &resolved, dagql.Selector{Field: "__resolve"}); err != nil {
		return inst, err
	}

	canonicalRepoRecv := resolved.ID()
	if canonicalRepoRecv.Digest() != parent.ID().Digest() {
		refObj, err := reenterObject[*core.GitRef](ctx, canonicalRepoRecv, nil)
		if err != nil {
			return inst, err
		}
		return refObj.Result, nil
	}

	remote, err := resolved.Self().Backend.Remote(ctx)
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

//nolint:dupl
func (s *gitSchema) tags(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args tagsArgs) (dagql.Array[dagql.String], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	var resolved dagql.ObjectResult[*core.GitRepository]
	if err := srv.Select(ctx, parent, &resolved, dagql.Selector{Field: "__resolve"}); err != nil {
		return nil, err
	}

	canonicalRepoRecv := resolved.ID()
	if canonicalRepoRecv.Digest() != parent.ID().Digest() {
		return reenterType[dagql.Array[dagql.String]](ctx, canonicalRepoRecv, nil)
	}

	var patterns []string
	if args.Patterns.Valid {
		for _, pattern := range args.Patterns.Value {
			patterns = append(patterns, pattern.String())
		}
	}

	remote, err := resolved.Self().Backend.Remote(ctx)
	if err != nil {
		return nil, err
	}
	return dagql.NewStringArray(remote.Filter(patterns).Tags().ShortNames()...), nil
}

type branchesArgs struct {
	Patterns dagql.Optional[dagql.ArrayInput[dagql.String]] `name:"patterns"`
}

//nolint:dupl
func (s *gitSchema) branches(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args branchesArgs) (dagql.Array[dagql.String], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	var resolved dagql.ObjectResult[*core.GitRepository]
	if err := srv.Select(ctx, parent, &resolved, dagql.Selector{Field: "__resolve"}); err != nil {
		return nil, err
	}

	canonicalRepoRecv := resolved.ID()
	if canonicalRepoRecv.Digest() != parent.ID().Digest() {
		return reenterType[dagql.Array[dagql.String]](ctx, canonicalRepoRecv, nil)
	}

	var patterns []string
	if args.Patterns.Valid {
		for _, pattern := range args.Patterns.Value {
			patterns = append(patterns, pattern.String())
		}
	}

	remote, err := resolved.Self().Backend.Remote(ctx)
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

	DagOpInternalArgs
}

func (s *gitSchema) tree(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args treeArgs) (inst dagql.ObjectResult[*core.Directory], _ error) {
	if args.SSHKnownHosts.Valid {
		return inst, fmt.Errorf("sshKnownHosts is no longer supported on `tree`")
	}
	if args.SSHAuthSocket.Valid {
		return inst, fmt.Errorf("sshAuthSocket is no longer supported on `tree`")
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	var resolved dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, parent, &resolved, dagql.Selector{Field: "__resolve"}); err != nil {
		return inst, err
	}

	canonicalRefRecv := resolved.ID()
	if canonicalRefRecv.Digest() != parent.ID().Digest() {
		return reenterObject[*core.Directory](ctx, canonicalRefRecv, nil)
	}

	ref := resolved.Self()

	if args.IsDagOp {
		dir, err := ref.Tree(ctx, srv, args.DiscardGitDir, args.Depth)
		if err != nil {
			return inst, err
		}
		return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
	}

	dir, err := DagOpDirectory(ctx, srv, ref, args, "", s.tree)
	if err != nil {
		return inst, err
	}
	inst, err = dagql.NewObjectResultForCurrentID(ctx, srv, dir)
	if err != nil {
		return inst, err
	}

	remoteGitRepo, isRemote := ref.Repo.Self().Backend.(*core.RemoteGitRepository)
	usedAuth := false
	if isRemote {
		usedAuth = remoteGitRepo.AuthToken.Valid ||
			remoteGitRepo.AuthHeader.Valid ||
			remoteGitRepo.SSHAuthSocket.Valid
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current query: %w", err)
	}

	bk, err := query.Buildkit(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	excludes := []string{".git", ".git/**"}
	contentDigest, err := core.GetContentHashFromDirectoryFiltered(ctx, bk, inst, excludes)
	if err != nil {
		return inst, fmt.Errorf("failed to get content hash: %w", err)
	}

	scope := []string{contentDigest.String()}

	if args.DiscardGitDir {
		scope = append(scope, "discardGitDir:true")
	}
	if args.Depth != 1 {
		scope = append(scope, fmt.Sprintf("depth:%d", args.Depth))
	}

	if isRemote && usedAuth {
		if remoteGitRepo.AuthToken.Valid && remoteGitRepo.AuthToken.Value.ID() != nil {
			scope = append(scope, "authToken:"+remoteGitRepo.AuthToken.Value.ID().Digest().String())
		}
		if remoteGitRepo.AuthHeader.Valid && remoteGitRepo.AuthHeader.Value.ID() != nil {
			scope = append(scope, "authHeader:"+remoteGitRepo.AuthHeader.Value.ID().Digest().String())
		}
		if remoteGitRepo.SSHAuthSocket.Valid && remoteGitRepo.SSHAuthSocket.Value.ID() != nil {
			scope = append(scope, "sshAuthSock:"+remoteGitRepo.SSHAuthSocket.Value.ID().Digest().String())
		}
		if remoteGitRepo.AuthUsername != "" {
			scope = append(scope, "authUser:"+remoteGitRepo.AuthUsername)
		}
	}

	finalDigest := hashutil.HashStrings(scope...)
	inst = inst.WithObjectDigest(finalDigest)

	return inst, nil
}

var errNoAuthResolutionNeeded = errors.New("no auth resolution needed")

func (s *gitSchema) repoResolve(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRepository],
	_ struct{},
) (dagql.ObjectResult[*core.GitRepository], error) {
	var zero dagql.ObjectResult[*core.GitRepository]

	if parent.ID().Field() == "__resolve" {
		return parent, nil
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return zero, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	repo := parent.Self()
	remote, isRemote := repo.Backend.(*core.RemoteGitRepository)
	if !isRemote {
		return dagql.NewObjectResultForCurrentID(ctx, srv, repo)
	}

	curLeaf := dagql.CurrentID(ctx)
	repoRecv := parent.ID()

	loadResolveOn := func(newRepoRecv *call.ID) (dagql.ObjectResult[*core.GitRepository], error) {
		leaf := newRepoRecv.Append(
			curLeaf.Type().ToAST(),
			curLeaf.Field(),
			call.WithArgs(curLeaf.Args()...),
			call.WithView(curLeaf.View()),
		)
		loaded, err := srv.Load(ctx, leaf)
		if err != nil {
			return zero, err
		}
		return loaded.(dagql.ObjectResult[*core.GitRepository]), nil
	}

	if remote.URL == nil {
		raw := repoRecv.Arg("url").Value().ToInput().(string)

		if remote.SSHAuthSocket.Valid {
			next := repoRecv.WithArgument(call.NewArgument("url", call.NewLiteralString("ssh://git@"+raw), false))
			return loadResolveOn(next)
		}
		if remote.AuthToken.Valid || remote.AuthHeader.Valid {
			next := repoRecv.WithArgument(call.NewArgument("url", call.NewLiteralString("https://"+raw), false))
			return loadResolveOn(next)
		}

		candidates := []string{
			"https://" + raw,
			"ssh://git@" + raw,
		}

		var lastAuthErr error
		for _, cand := range candidates {
			next := repoRecv.WithArgument(call.NewArgument("url", call.NewLiteralString(cand), false))
			resolved, err := loadResolveOn(next)
			if err == nil {
				return resolved, nil
			}
			if errors.Is(err, gitutil.ErrGitAuthFailed) {
				lastAuthErr = err
				continue
			}
			return zero, err
		}

		if lastAuthErr != nil {
			return zero, fmt.Errorf("failed to determine Git URL protocol")
		}
		return zero, fmt.Errorf("failed to determine Git URL protocol")
	}

	if remote.NeedsAuthResolution() {
		auth, err := s.resolveAuthArgs(ctx, srv, remote)
		if err == nil {
			next := repoRecv.WithArgument(call.NewArgument("url", call.NewLiteralString(auth.urlOverride), false))
			for _, ni := range auth.extraArgs {
				next = next.WithArgument(call.NewArgument(ni.Name, ni.Value.ToLiteral(), false))
			}
			return loadResolveOn(next)
		}
		if !errors.Is(err, errNoAuthResolutionNeeded) {
			return zero, err
		}
	}

	if _, err := repo.Backend.Remote(ctx); err != nil {
		return zero, err
	}

	return dagql.NewObjectResultForCurrentID(ctx, srv, repo)
}

func (s *gitSchema) refResolve(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRef],
	_ struct{},
) (dagql.ObjectResult[*core.GitRef], error) {
	var zero dagql.ObjectResult[*core.GitRef]

	if parent.ID().Field() == "__resolve" {
		return parent, nil
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return zero, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	curLeaf := dagql.CurrentID(ctx)
	refRecv := parent.ID()
	curRepoID := parent.Self().Repo.ID()

	var resolvedRepo dagql.ObjectResult[*core.GitRepository]
	if err := srv.Select(ctx, parent.Self().Repo, &resolvedRepo, dagql.Selector{Field: "__resolve"}); err != nil {
		return zero, err
	}
	canonicalRepoID := resolvedRepo.ID()

	if canonicalRepoID.Digest() != curRepoID.Digest() {
		newRefRecv := canonicalRepoID.Append(
			(*core.GitRef)(nil).Type(),
			refRecv.Field(),
			call.WithArgs(refRecv.Args()...),
			call.WithView(refRecv.View()),
		)

		newLeaf := newRefRecv.Append(
			curLeaf.Type().ToAST(),
			curLeaf.Field(),
			call.WithArgs(curLeaf.Args()...),
			call.WithView(refRecv.View()),
		)

		loaded, err := srv.Load(ctx, newLeaf)
		if err != nil {
			return zero, err
		}
		return loaded.(dagql.ObjectResult[*core.GitRef]), nil
	}

	if err := parent.Self().Resolve(ctx); err != nil {
		return zero, err
	}

	return dagql.NewObjectResultForCurrentID(ctx, srv, parent.Self())
}

type idPatch func(*call.ID) *call.ID

func reenterObject[T dagql.Typed](
	ctx context.Context,
	newReceiver *call.ID,
	patch idPatch,
) (dagql.ObjectResult[T], error) {
	var zero dagql.ObjectResult[T]

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return zero, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	cur := dagql.CurrentID(ctx)

	newLeafID := newReceiver.Append(
		cur.Type().ToAST(),
		cur.Field(),
		call.WithArgs(cur.Args()...),
		call.WithView(cur.View()),
	)

	if patch != nil {
		newLeafID = patch(newLeafID)
	}

	loaded, err := srv.Load(ctx, newLeafID)
	if err != nil {
		return zero, err
	}
	return loaded.(dagql.ObjectResult[T]), nil
}

func reenterType[T any](
	ctx context.Context,
	newReceiver *call.ID,
	patch idPatch,
) (T, error) {
	var zero T

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return zero, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	cur := dagql.CurrentID(ctx)

	newLeafID := newReceiver.Append(
		cur.Type().ToAST(),
		cur.Field(),
		call.WithArgs(cur.Args()...),
		call.WithView(cur.View()),
	)

	if patch != nil {
		newLeafID = patch(newLeafID)
	}

	loaded, err := srv.LoadType(ctx, newLeafID)
	if err != nil {
		return zero, err
	}
	return loaded.Unwrap().(T), nil
}

type authResolution struct {
	urlOverride string
	extraArgs   []dagql.NamedInput
}

func (s *gitSchema) resolveAuthArgs(
	ctx context.Context,
	srv *dagql.Server,
	remoteGitRepo *core.RemoteGitRepository,
) (*authResolution, error) {
	if remoteGitRepo.URL.Scheme == gitutil.SSHProtocol && !remoteGitRepo.SSHAuthSocket.Valid {
		clientMD, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("client metadata: %w", err)
		}
		if clientMD.SSHAuthSocketPath == "" {
			return nil, fmt.Errorf("%w: SSH URLs are not supported without an SSH socket", gitutil.ErrGitAuthFailed)
		}

		var sock dagql.ObjectResult[*core.Socket]
		if err := srv.Select(ctx, srv.Root(), &sock,
			dagql.Selector{Field: "host"},
			dagql.Selector{
				Field: "unixSocket",
				Args:  []dagql.NamedInput{{Name: "path", Value: dagql.NewString(clientMD.SSHAuthSocketPath)}},
			},
		); err != nil {
			return nil, fmt.Errorf("select unix socket: %w", err)
		}

		urlCopy := *remoteGitRepo.URL
		if urlCopy.User == nil {
			urlCopy.User = url.User("git")
		}

		return &authResolution{
			urlOverride: urlCopy.String(),
			extraArgs: []dagql.NamedInput{
				{Name: "sshAuthSocket", Value: dagql.Opt(dagql.NewID[*core.Socket](sock.ID()))},
			},
		}, nil
	}

	if (remoteGitRepo.URL.Scheme == gitutil.HTTPProtocol || remoteGitRepo.URL.Scheme == gitutil.HTTPSProtocol) &&
		!remoteGitRepo.AuthToken.Valid && !remoteGitRepo.AuthHeader.Valid {
		query, err := core.CurrentQuery(ctx)
		if err != nil {
			return nil, err
		}

		parentMD, err := query.NonModuleParentClientMetadata(ctx)
		if err != nil {
			return nil, fmt.Errorf("non-module parent client metadata: %w", err)
		}

		clientMD, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("client metadata: %w", err)
		}

		if clientMD.ClientID == parentMD.ClientID {
			authCtx := engine.ContextWithClientMetadata(ctx, parentMD)
			bk, err := query.Buildkit(authCtx)
			if err != nil {
				return nil, fmt.Errorf("buildkit: %w", err)
			}

			creds, err := bk.GetCredential(authCtx, remoteGitRepo.URL.Scheme, remoteGitRepo.URL.Host, remoteGitRepo.URL.Path)
			if err == nil && creds.Password != "" {
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
					return nil, fmt.Errorf("create git auth secret: %w", err)
				}

				extraArgs := []dagql.NamedInput{
					{Name: "httpAuthToken", Value: dagql.Opt(dagql.NewID[*core.Secret](token.ID()))},
				}
				if creds.Username != "" {
					extraArgs = append(extraArgs, dagql.NamedInput{Name: "httpAuthUsername", Value: dagql.NewString(creds.Username)})
				}
				return &authResolution{
					urlOverride: remoteGitRepo.URL.String(),
					extraArgs:   extraArgs,
				}, nil
			} else if err != nil {
				slog.Warn("GetCredential failed", "error", err)
			}
		}
	}

	return nil, errNoAuthResolutionNeeded
}

func (s *gitSchema) fetchCommit(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRef],
	args RawDagOpInternalArgs,
) (dagql.String, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get current dagql server: %w", err)
	}

	var resolved dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, parent, &resolved, dagql.Selector{Field: "__resolve"}); err != nil {
		return "", err
	}

	canonicalRefRecv := resolved.ID()
	if canonicalRefRecv.Digest() != parent.ID().Digest() {
		return reenterType[dagql.String](ctx, canonicalRefRecv, nil)
	}

	return dagql.NewString(resolved.Self().Ref.SHA), nil
}

func (s *gitSchema) fetchRef(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRef],
	args RawDagOpInternalArgs,
) (dagql.String, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get current dagql server: %w", err)
	}

	var resolved dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, parent, &resolved, dagql.Selector{Field: "__resolve"}); err != nil {
		return "", err
	}

	canonicalRefRecv := resolved.ID()
	if canonicalRefRecv.Digest() != parent.ID().Digest() {
		return reenterType[dagql.String](ctx, canonicalRefRecv, nil)
	}

	r := resolved.Self().Ref
	return dagql.NewString(cmp.Or(r.Name, r.SHA)), nil
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
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	var selfResolved dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, parent, &selfResolved, dagql.Selector{Field: "__resolve"}); err != nil {
		return inst, err
	}
	selfCanonicalRecv := selfResolved.ID()
	selfChanged := selfCanonicalRecv.Digest() != parent.ID().Digest()

	other, err := args.Other.Load(ctx, srv)
	if err != nil {
		return inst, err
	}

	var otherResolved dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, other, &otherResolved, dagql.Selector{Field: "__resolve"}); err != nil {
		return inst, err
	}
	otherCanonicalRecv := otherResolved.ID()
	otherChanged := otherCanonicalRecv.Digest() != other.ID().Digest()

	if selfChanged || otherChanged {
		return reenterObject[*core.GitRef](ctx, selfCanonicalRecv, func(id *call.ID) *call.ID {
			otherLit := dagql.NewID[*core.GitRef](otherCanonicalRecv).ToLiteral()
			return id.WithArgument(call.NewArgument("other", otherLit, false))
		})
	}

	result, err := core.MergeBase(ctx, selfResolved.Self(), otherResolved.Self())
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, result)
}
