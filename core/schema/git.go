package schema

// Git objects are lazy — git() and ref() do zero I/O. Content-accessing fields
// redirect through __resolve on first access:
//
//   tags()   → repo.__resolve → back to tags()
//   commit() → ref.__resolve  → repo.__resolve → back to commit()
//   tree()   → ref.__resolve  → repo.__resolve → __tree
//
// repo.__resolve creates a NEW git() call with explicit URL + auth in the args.
// This makes auth part of the DAG ID. Since DagOp cache keys derive from the ID,
// each client's auth path produces a distinct key — without this, one client could
// get another's cached result without going through auth.
//
// ObjectDigest overrides control cache sharing at each level:
//
//   Client A (token1):  git("github.com/foo", token=t1)
//   Client B (token2):  git("github.com/foo", token=t2)
//   Client C (SSH):     git("git@github.com:foo", ssh=sock)
//
//   repo.__resolve:  hash(url, AUTH, ls-remote)  → A ≠ B ≠ C  (per-client)
//     → tags, branches, url read from here — no further sharing
//
//   ref.__resolve:   hash(url, ref, sha)         → A = B ≠ C  (auth stripped)
//     → commit, ref read from here — shared across tokens for same URL
//
//   __tree:          hash(sha, depth, discard)    → A = B = C  (URL stripped)
//     → tree reads from here — shared across all protocols
//
// CachePerClient on every field that triggers __resolve ensures each client
// goes through its own auth path before touching data.

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
		dagql.NodeFunc("__tree", DagOpDirectoryWrapper(srv, s.checkoutTree)).
			Doc(`(Internal) Tree computation after auth validation.`).
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

// git creates a lazy GitRepository handle. No I/O — resolution happens
// via __resolve when a content-accessing field is called.
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

	// ObjectDigest: hash(url, auth, config) — the default ID digest includes
	// Optional wrapper structure, so the same call with/without explicit defaults
	// would get different digests. This normalizes to semantic values only.
	dgstInputs := []string{
		args.URL,
		strconv.FormatBool(repo.DiscardGitDir),
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
	if args.Ref != "" {
		dgstInputs = append(dgstInputs, "pinnedRef", args.Ref)
	}
	if args.Commit != "" {
		dgstInputs = append(dgstInputs, "pinnedCommit", args.Commit)
	}
	inst = inst.WithObjectDigest(hashutil.HashStrings(dgstInputs...))

	// Secrets and sockets live in the client that created them. If this result
	// gets served from cache to a different client, that client won't have the
	// secret in its store. PostCall runs on every return (cached or fresh) and
	// copies the secrets over so downstream operations can actually use them.
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

	buildTreeSelectorArgs := func() []dagql.NamedInput {
		var selectorArgs []dagql.NamedInput
		if args.DiscardGitDir {
			selectorArgs = append(selectorArgs, dagql.NamedInput{Name: "discardGitDir", Value: dagql.Boolean(true)})
		}
		if args.Depth != 1 {
			selectorArgs = append(selectorArgs, dagql.NamedInput{Name: "depth", Value: dagql.Int(args.Depth)})
		}
		return selectorArgs
	}

	if !ref.Resolved {
		treeSelector := dagql.Selector{Field: "tree"}
		if selectorArgs := buildTreeSelectorArgs(); len(selectorArgs) > 0 {
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
	if selectorArgs := buildTreeSelectorArgs(); len(selectorArgs) > 0 {
		treeSelector.Args = selectorArgs
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

func (s *gitSchema) checkoutTree(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args treeInternalArgs) (inst dagql.ObjectResult[*core.Directory], _ error) {
	ref := parent.Self()
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	if ref.Ref == nil || ref.Ref.SHA == "" {
		return inst, fmt.Errorf("internal: checkoutTree called before ref was resolved")
	}

	effectiveDiscard := ref.Repo.Self().DiscardGitDir || args.DiscardGitDir

	dir, err := ref.Tree(ctx, srv, args.DiscardGitDir, args.Depth)
	if err != nil {
		return inst, err
	}

	out, err := dagql.NewObjectResultForCurrentID(ctx, srv, dir)
	if err != nil {
		return inst, err
	}

	// ObjectDigest: hash(sha, depth, discard) — see file header.
	// URL stripped: same commit produces the same checkout regardless of protocol.
	// .git contents excluded: they vary between fetches for the same commit.
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

	// Self is resolved but other might not be.
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

// resolveRepository resolves a lazy git() handle by figuring out the protocol,
// injecting auth, and running ls-remote. Steps 1-2 redirect by creating a new
// git() call with more info; step 3 is the leaf that actually hits the network.
func (s *gitSchema) resolveRepository(
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

	// Local repos skip URL/auth resolution but still need ls-remote for ref lookup.
	if !isRemote {
		resolved := repo.Clone()
		lsRemote, err := resolved.Backend.Remote(ctx)
		if err == nil {
			resolved.Remote = lsRemote
		}
		resolved.Resolved = true
		return dagql.NewObjectResultForCurrentID(ctx, srv, resolved)
	}

	// Step 1: No protocol? Try to figure it out.
	// remote.URL is nil when the URL couldn't be parsed (e.g. "github.com/foo/bar").
	if remote.URL == nil {
		return s.resolveAmbiguousURL(ctx, srv, parent, remote)
	}

	// Step 2: We have a URL but no auth was passed explicitly.
	// Try to grab credentials from the client's environment (SSH agent, git credential store).
	if !remote.SSHAuthSocket.Valid && !remote.AuthToken.Valid && !remote.AuthHeader.Valid {
		if result, ok, err := s.injectAuthFromContext(ctx, srv, parent, remote); err != nil {
			return zero, err
		} else if ok {
			return result, nil
		}
		// No auth found — continue anyway, might be a public repo.
	}

	// Step 3: We have URL + auth (or it's public). Actually talk to the remote.
	resolved := repo.Clone()
	remoteClone := remote.Clone()
	resolved.Backend = remoteClone

	lsRemote, err := resolved.Backend.Remote(ctx)
	if err != nil {
		return zero, err
	}
	resolved.Remote = lsRemote
	resolved.Resolved = true

	result, err := dagql.NewObjectResultForCurrentID(ctx, srv, resolved)
	if err != nil {
		return zero, err
	}

	// ObjectDigest: hash(url, AUTH, ls-remote) — see file header.
	// Auth stays so each client gets its own DagOp key.
	// ls-remote stays so new pushes bust the cache.
	if resolved.Remote != nil {
		dgstInputs := []string{
			remote.URL.String(),
			strconv.FormatBool(resolved.DiscardGitDir),
		}
		if remote.SSHAuthSocket.Valid {
			dgstInputs = append(dgstInputs, "sshAuthSock", remote.SSHAuthSocket.Value.ID().Digest().String())
		}
		if remote.AuthToken.Valid {
			dgstInputs = append(dgstInputs, "authToken", remote.AuthToken.Value.ID().Digest().String())
		}
		if remote.AuthHeader.Valid {
			dgstInputs = append(dgstInputs, "authHeader", remote.AuthHeader.Value.ID().Digest().String())
		}
		dgstInputs = append(dgstInputs, "remote", resolved.Remote.Digest().String())
		if resolved.PinnedHead != nil {
			if resolved.PinnedHead.Name != "" {
				dgstInputs = append(dgstInputs, "pinnedRef", resolved.PinnedHead.Name)
			}
			if resolved.PinnedHead.SHA != "" {
				dgstInputs = append(dgstInputs, "pinnedCommit", resolved.PinnedHead.SHA)
			}
		}
		result = result.WithObjectDigest(hashutil.HashStrings(dgstInputs...))
	}

	return result, nil
}

// resolveAmbiguousURL handles URLs like "github.com/foo/bar" (no protocol).
//
// If the user passed an SSH socket, we know it's ssh://. If they passed a token,
// it's https://. Otherwise we try https first, then fall back to ssh.
//
// Each attempt creates a NEW git() call with the explicit URL and chains
// __resolve on it, which re-enters resolveRepository at step 2 or 3.
func (s *gitSchema) resolveAmbiguousURL(
	ctx context.Context,
	srv *dagql.Server,
	parent dagql.ObjectResult[*core.GitRepository],
	remote *core.RemoteGitRepository,
) (dagql.ObjectResult[*core.GitRepository], error) {
	var zero dagql.ObjectResult[*core.GitRepository]
	rawURL := parent.ID().Arg("url").Value().ToInput().(string)

	// Auth type tells us the protocol.
	if remote.SSHAuthSocket.Valid {
		var result dagql.ObjectResult[*core.GitRepository]
		err := srv.Select(ctx, srv.Root(), &result,
			dagql.Selector{
				Field: "git",
				Args:  s.buildGitCallArgs(parent, remote, "ssh://git@"+rawURL, nil),
			},
			dagql.Selector{Field: "__resolve"},
		)
		return result, err
	}
	if remote.AuthToken.Valid || remote.AuthHeader.Valid {
		var result dagql.ObjectResult[*core.GitRepository]
		err := srv.Select(ctx, srv.Root(), &result,
			dagql.Selector{
				Field: "git",
				Args:  s.buildGitCallArgs(parent, remote, "https://"+rawURL, nil),
			},
			dagql.Selector{Field: "__resolve"},
		)
		return result, err
	}

	// No auth hint — try https, fall back to ssh.
	candidates := []string{"https://" + rawURL, "ssh://git@" + rawURL}

	for _, candidateURL := range candidates {
		var result dagql.ObjectResult[*core.GitRepository]
		err := srv.Select(ctx, srv.Root(), &result,
			dagql.Selector{
				Field: "git",
				Args:  s.buildGitCallArgs(parent, remote, candidateURL, nil),
			},
			dagql.Selector{Field: "__resolve"},
		)
		if err == nil {
			return result, nil
		}
		if errors.Is(err, gitutil.ErrGitAuthFailed) {
			continue
		}
		return zero, err
	}

	return zero, fmt.Errorf("failed to resolve git URL: tried https and ssh")
}

// injectAuthFromContext looks for credentials in the calling client's environment
// (SSH agent socket, git credential store) and creates a new git() call with
// those credentials made explicit. This is the "step 2" redirect — it makes auth
// part of the DAG ID so caching works correctly.
//
// Returns (result, true, nil) if we found auth and the redirect worked.
// Returns (zero, false, nil) if no auth is available — caller should continue without.
func (s *gitSchema) injectAuthFromContext(
	ctx context.Context,
	srv *dagql.Server,
	parent dagql.ObjectResult[*core.GitRepository],
	remote *core.RemoteGitRepository,
) (dagql.ObjectResult[*core.GitRepository], bool, error) {
	var zero dagql.ObjectResult[*core.GitRepository]

	r := remote.Clone()

	switch r.URL.Scheme {
	case gitutil.SSHProtocol:
		// Default to git user for SSH — without this you get weird defaults like "root".
		if r.URL.User == nil {
			r.URL.User = url.User("git")
		}

		clientMD, err := engine.ClientMetadataFromContext(ctx)
		if err != nil || clientMD.SSHAuthSocketPath == "" {
			return zero, false, fmt.Errorf("%w: SSH URLs are not supported without an SSH socket", gitutil.ErrGitAuthFailed)
		}

		// Turn the socket path into a dagql Socket object so it can be an arg.
		var sock dagql.ObjectResult[*core.Socket]
		if err := srv.Select(ctx, srv.Root(), &sock,
			dagql.Selector{Field: "host"},
			dagql.Selector{Field: "unixSocket", Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(clientMD.SSHAuthSocketPath)},
			}},
		); err != nil {
			return zero, false, fmt.Errorf("%w: failed to get SSH socket: %w", gitutil.ErrGitAuthFailed, err)
		}

		// Redirect: new git() call with the socket made explicit.
		authArgs := []dagql.NamedInput{
			{Name: "sshAuthSocket", Value: dagql.Opt(dagql.NewID[*core.Socket](sock.ID()))},
		}
		var result dagql.ObjectResult[*core.GitRepository]
		if err := srv.Select(ctx, srv.Root(), &result,
			dagql.Selector{
				Field: "git",
				Args:  s.buildGitCallArgs(parent, r, r.URL.String(), authArgs),
			},
			dagql.Selector{Field: "__resolve"},
		); err != nil {
			return zero, false, err
		}
		return result, true, nil

	case gitutil.HTTPProtocol, gitutil.HTTPSProtocol:
		token, username := s.getCredentialFromStore(ctx, srv, r.URL)
		if token == nil {
			return zero, false, nil
		}

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
				Args:  s.buildGitCallArgs(parent, r, r.URL.String(), authArgs),
			},
			dagql.Selector{Field: "__resolve"},
		); err != nil {
			return zero, false, err
		}
		return result, true, nil
	}

	return zero, false, nil
}

// buildGitCallArgs builds arguments for a new git() call, preserving everything
// from the parent (sshKnownHosts, keepGitDir, services, pinned ref/commit) while
// overriding the URL and optionally the auth method.
//
// We pull args from the parent's ID rather than Go structs because the redirect
// creates a fresh git() call via srv.Select, which needs dagql-level args.
func (s *gitSchema) buildGitCallArgs(
	parent dagql.ObjectResult[*core.GitRepository],
	remote *core.RemoteGitRepository,
	url string,
	authArgs []dagql.NamedInput,
) []dagql.NamedInput {
	args := []dagql.NamedInput{
		{Name: "url", Value: dagql.NewString(url)},
	}

	args = append(args, authArgs...)

	// No explicit auth override — keep whatever the parent had.
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

	// Carry over non-auth config from the original call.

	if keepGitDir := parent.ID().Arg("keepGitDir"); keepGitDir != nil {
		if v, ok := keepGitDir.Value().ToInput().(bool); ok && v {
			args = append(args, dagql.NamedInput{
				Name: "keepGitDir", Value: dagql.Opt(dagql.Boolean(true)),
			})
		}
	}

	if sshKnownHosts := parent.ID().Arg("sshKnownHosts"); sshKnownHosts != nil {
		if v, ok := sshKnownHosts.Value().ToInput().(string); ok && v != "" {
			args = append(args, dagql.NamedInput{
				Name: "sshKnownHosts", Value: dagql.NewString(v),
			})
		}
	}

	if commit := parent.ID().Arg("commit"); commit != nil {
		if v, ok := commit.Value().ToInput().(string); ok && v != "" {
			args = append(args, dagql.NamedInput{
				Name: "commit", Value: dagql.NewString(v),
			})
		}
	}

	if ref := parent.ID().Arg("ref"); ref != nil {
		if v, ok := ref.Value().ToInput().(string); ok && v != "" {
			args = append(args, dagql.NamedInput{
				Name: "ref", Value: dagql.NewString(v),
			})
		}
	}

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

// getCredentialFromStore checks the client's git credential store for HTTP
// credentials. Returns nil if nothing is found — this is fine, it just means
// we'll try without auth (which works for public repos).
func (s *gitSchema) getCredentialFromStore(
	ctx context.Context,
	srv *dagql.Server,
	parsedURL *gitutil.GitURL,
) (*dagql.ObjectResult[*core.Secret], string) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, ""
	}

	parentMD, err := query.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return nil, ""
	}

	clientMD, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, ""
	}

	// Only trust credentials from the direct parent client, not nested modules.
	if clientMD.ClientID != parentMD.ClientID {
		return nil, ""
	}

	authCtx := engine.ContextWithClientMetadata(ctx, parentMD)
	bk, err := query.Buildkit(authCtx)
	if err != nil {
		return nil, ""
	}

	creds, err := bk.GetCredential(authCtx, parsedURL.Scheme, parsedURL.Host, parsedURL.Path)
	if err != nil {
		return nil, ""
	}
	if creds.Password == "" {
		return nil, ""
	}

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
		return nil, ""
	}

	return &token, creds.Username
}

// resolveRef resolves a lazy GitRef: resolves the parent repo, looks up the
// ref in ls-remote, and rebuilds the ID rooted at the resolved repo so
// downstream operations inherit auth in their cache key.
func (s *gitSchema) resolveRef(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRef],
	_ struct{},
) (dagql.ObjectResult[*core.GitRef], error) {
	var zero dagql.ObjectResult[*core.GitRef]

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return zero, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	// Resolve the parent repo first.
	var resolvedRepo dagql.ObjectResult[*core.GitRepository]
	if err := srv.Select(ctx, parent.Self().Repo, &resolvedRepo,
		dagql.Selector{Field: "__resolve"},
	); err != nil {
		return zero, fmt.Errorf("failed to resolve repo: %w", err)
	}

	ref := parent.Self().Clone()
	ref.Repo = resolvedRepo

	// Look up the ref in the remote's ref listing, or rev-parse for local repos.
	repo := resolvedRepo.Self()
	if ref.Ref.Name != "" && repo.Remote != nil {
		resolvedRefInfo, err := repo.Remote.Lookup(ref.Ref.Name)
		if err != nil {
			return zero, err
		}
		if ref.Ref.SHA == "" {
			ref.Ref.SHA = resolvedRefInfo.SHA
		}
		if resolvedRefInfo.Name != "" {
			ref.Ref.Name = resolvedRefInfo.Name
		}
	}

	// Initialize the backend for git operations (checkout, etc.).
	if ref.Backend == nil {
		refBackend, err := repo.Backend.Get(ctx, ref.Ref)
		if err != nil {
			return zero, err
		}
		ref.Backend = refBackend
	}
	ref.Resolved = true

	// Rebuild the ID rooted at the resolved repo, including the commit SHA
	// so new pushes produce a fresh cache key for everything downstream.
	repoID := resolvedRepo.ID().Receiver()

	refCallArgs := []*call.Argument{
		call.NewArgument("name", call.NewLiteralString(parent.Self().Ref.Name), false),
	}
	if ref.Ref != nil && ref.Ref.SHA != "" {
		refCallArgs = append(refCallArgs,
			call.NewArgument("commit", call.NewLiteralString(ref.Ref.SHA), false),
		)
	}

	newID := repoID.
		Append(ref.Type(), "ref", call.WithArgs(refCallArgs...)).
		Append(ref.Type(), "__resolve")

	result, err := dagql.NewObjectResultForID(ref, srv, newID)
	if err != nil {
		return zero, err
	}

	// ObjectDigest: hash(url, ref, sha) — see file header.
	// Auth stripped: different tokens for the same URL+commit share downstream DagOps.
	// URL kept: SSH and HTTPS are different checkout operations.
	if ref.Ref != nil && ref.Ref.SHA != "" {
		dgstInputs := []string{
			ref.Ref.Name, // canonical: refs/heads/main, refs/tags/v1.0
			ref.Ref.SHA,
			strconv.FormatBool(resolvedRepo.Self().DiscardGitDir),
		}
		if remote, isRemote := resolvedRepo.Self().Backend.(*core.RemoteGitRepository); isRemote && remote.URL != nil {
			dgstInputs = append([]string{remote.URL.String()}, dgstInputs...)
		}
		result = result.WithObjectDigest(hashutil.HashStrings(dgstInputs...))
	}

	return result, nil
}
