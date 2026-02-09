package schema

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
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
		dagql.NodeFuncWithCacheKey("url", s.url, dagql.CachePerClient).
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

		dagql.NodeFuncWithCacheKey("latestVersion", s.latestVersion, dagql.CachePerClient).
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

		dagql.NodeFuncWithCacheKey("__resolve", s.resolveRepository, dagql.CachePerClient).
			Doc(`(Internal-only) Validates this client's access and snapshots remote state for this repo.`),
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
		dagql.NodeFunc("__tree", DagOpDirectoryWrapper(srv, s.checkoutTree)).
			Doc(`(Internal) Tree computation after repo/ref resolution has validated access.`).
			Args(
				dagql.Arg("discardGitDir"),
				dagql.Arg("depth"),
			),

		dagql.NodeFuncWithCacheKey("commit", s.getCommitSHA, dagql.CachePerClient).
			Doc(`The resolved commit id at this ref.`),
		dagql.NodeFuncWithCacheKey("ref", s.getRefName, dagql.CachePerClient).
			Doc(`The resolved ref name at this ref.`),
		dagql.NodeFuncWithCacheKey("commonAncestor", s.commonAncestor, dagql.CachePerClient).
			Doc(`Find the best common ancestor between this ref and another ref.`).
			Args(
				dagql.Arg("other").Doc(`The other ref to compare against.`),
			),

		dagql.NodeFuncWithCacheKey("__resolve", s.resolveRef, dagql.CachePerClient).
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

// git creates a lazy GitRepository handle.
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

	// Keep auth resources available on cache hits served to other clients.
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
		postCall, _, err := core.ResourceTransferPostCall(ctx, parent.Self(), clientMetadata.ClientID, resourceIDs...)
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

//nolint:dupl
func (s *gitSchema) tags(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args tagsArgs) (dagql.Array[dagql.String], error) {
	repo := parent.Self()

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

type branchesArgs struct {
	Patterns dagql.Optional[dagql.ArrayInput[dagql.String]] `name:"patterns"`
}

//nolint:dupl
func (s *gitSchema) branches(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args branchesArgs) (dagql.Array[dagql.String], error) {
	repo := parent.Self()

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
		if err := dag.Select(ctx, parent, &dirty,
			dagql.Selector{Field: "head"},
			dagql.Selector{Field: "tree"},
		); err != nil {
			return inst, fmt.Errorf("failed to select head tree for clean repo: %w", err)
		}
		cleaned = dirty
	} else {
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

	var selectorArgs []dagql.NamedInput
	if args.DiscardGitDir {
		selectorArgs = append(selectorArgs, dagql.NamedInput{Name: "discardGitDir", Value: dagql.Boolean(true)})
	}
	if args.Depth != 1 {
		selectorArgs = append(selectorArgs, dagql.NamedInput{Name: "depth", Value: dagql.Int(args.Depth)})
	}

	if !ref.Resolved {
		remote, isRemote := ref.Repo.Self().Backend.(*core.RemoteGitRepository)
		effectiveDiscard := ref.Repo.Self().DiscardGitDir || args.DiscardGitDir
		needsResolvedCommit := effectiveDiscard && treeCommit(ref) == ""

		if !needsResolvedCommit && (!isRemote || remote.URL != nil) {
			treeSelector := dagql.Selector{Field: "__tree"}
			if len(selectorArgs) > 0 {
				treeSelector.Args = selectorArgs
			}

			var result dagql.ObjectResult[*core.Directory]
			if err := srv.Select(ctx, treeParentForCall(parent, ref, args.DiscardGitDir), &result, treeSelector); err == nil {
				return result, nil
			}
			// Fall back to __resolve->tree to handle auth injection and symbolic refs.
		}

		treeSelector := dagql.Selector{Field: "tree"}
		if len(selectorArgs) > 0 {
			treeSelector.Args = selectorArgs
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

	treeSelector := dagql.Selector{Field: "__tree"}
	if len(selectorArgs) > 0 {
		treeSelector.Args = selectorArgs
	}

	var result dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, treeParentForCall(parent, ref, args.DiscardGitDir), &result, treeSelector); err != nil {
		return inst, err
	}
	return result, nil
}

func treeCommit(ref *core.GitRef) string {
	if ref.Ref == nil {
		return ""
	}
	if ref.Ref.SHA != "" {
		return ref.Ref.SHA
	}
	if gitutil.IsCommitSHA(ref.Ref.Name) {
		return ref.Ref.Name
	}
	return ""
}

func treeParentForCall(parent dagql.ObjectResult[*core.GitRef], ref *core.GitRef, discardGitDir bool) dagql.ObjectResult[*core.GitRef] {
	commit := treeCommit(ref)
	if commit == "" {
		return parent
	}

	effectiveDiscard := ref.Repo.Self().DiscardGitDir || discardGitDir
	inputs := []string{
		"git-ref-tree",
		ref.Ref.Name,
		commit,
		strconv.FormatBool(effectiveDiscard),
	}

	// Keep transport semantics only when .git is preserved.
	if !effectiveDiscard {
		if remote, ok := ref.Repo.Self().Backend.(*core.RemoteGitRepository); ok && remote.URL != nil {
			inputs = append(inputs, remote.URL.String())
		} else {
			inputs = append(inputs, "local")
		}
	}

	return parent.WithObjectDigest(hashutil.HashStrings(inputs...))
}

type treeInternalArgs struct {
	DiscardGitDir bool `default:"false"`
	Depth         int  `default:"1"`
	DagOpInternalArgs
}

func (s *gitSchema) checkoutTree(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args treeInternalArgs) (inst dagql.ObjectResult[*core.Directory], _ error) {
	ref := parent.Self().Clone()
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	if ref.Ref == nil {
		return inst, fmt.Errorf("internal: checkoutTree called with nil ref")
	}
	if ref.Ref.SHA == "" && gitutil.IsCommitSHA(ref.Ref.Name) {
		ref.Ref.SHA = ref.Ref.Name
	}

	if ref.Backend == nil {
		repoBackend := ref.Repo.Self().Backend
		if remote, ok := repoBackend.(*core.RemoteGitRepository); ok {
			remote = remote.Clone()
			if remote.URL != nil && remote.URL.Scheme == gitutil.SSHProtocol && remote.URL.User == nil {
				remote.URL.User = url.User("git")
			}
			// tree() can run before repo.__resolve; inject parent-client HTTP auth here
			// so the fast __tree path can fetch private refs without eager ls-remote.
			if remote.URL != nil &&
				(remote.URL.Scheme == gitutil.HTTPProtocol || remote.URL.Scheme == gitutil.HTTPSProtocol) &&
				!remote.AuthToken.Valid && !remote.AuthHeader.Valid {
				if token, username, ok := s.lookupParentClientHTTPAuth(ctx, srv, remote.URL); ok {
					remote.AuthToken = dagql.Opt(token)
					if remote.AuthUsername == "" {
						remote.AuthUsername = username
					}
				}
			}
			repoBackend = remote
		}

		refBackend, err := repoBackend.Get(ctx, ref.Ref)
		if err != nil {
			return inst, err
		}
		ref.Backend = refBackend
	}

	effectiveDiscard := ref.Repo.Self().DiscardGitDir || args.DiscardGitDir

	dir, err := ref.Tree(ctx, srv, args.DiscardGitDir, args.Depth)
	if err != nil {
		return inst, err
	}
	if ref.Ref.SHA == "" {
		return inst, fmt.Errorf("internal: checkoutTree could not resolve ref commit")
	}

	out, err := dagql.NewObjectResultForCurrentID(ctx, srv, dir)
	if err != nil {
		return inst, err
	}

	// Tree identity is commit-scoped and protocol/auth-agnostic.
	out = out.WithObjectDigest(hashutil.HashStrings(
		"git-tree",
		ref.Ref.SHA,
		strconv.Itoa(args.Depth),
		strconv.FormatBool(effectiveDiscard),
	))

	return out, nil
}

func (s *gitSchema) getCommitSHA(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRef],
	args RawDagOpInternalArgs,
) (dagql.String, error) {
	ref := parent.Self()

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

	return dagql.NewString(ref.Ref.SHA), nil
}

func (s *gitSchema) getRefName(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRef],
	args RawDagOpInternalArgs,
) (dagql.String, error) {
	ref := parent.Self()

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

	other, err := args.Other.Load(ctx, srv)
	if err != nil {
		return inst, err
	}

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

	if !other.Self().Resolved {
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

	result, err := core.MergeBase(ctx, ref, other.Self())
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, result)
}
