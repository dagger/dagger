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
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/server/resource"
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

		dagql.Func("tags", s.tags).
			Doc(`tags that match any of the given glob patterns.`).
			Args(
				dagql.Arg("patterns").Doc(`Glob patterns (e.g., "refs/tags/v*").`),
			),
		dagql.Func("branches", s.branches).
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
	}.Install(srv)

	dagql.Fields[*core.GitRef]{
		dagql.NodeFunc("tree", s.tree).
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
		dagql.NodeFunc("commit", s.fetchCommit).
			Doc(`The resolved commit id at this ref.`),
		dagql.NodeFunc("ref", s.fetchRef).
			Doc(`The resolved ref name at this ref.`),
		dagql.NodeFunc("commonAncestor", s.commonAncestor).
			Doc(`Find the best common ancestor between this ref and another ref.`).
			Args(
				dagql.Arg("other").Doc(`The other ref to compare against.`),
			),
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
		repo.URL.Value.String(),
		// legacy args
		strconv.FormatBool(repo.DiscardGitDir),
		// also include what auth methods are used, currently we can't
		// handle a cache hit where the result has a different auth
		// method than the caller used (i.e. a git repo is pulled w/
		// a token but hits cache for a dir where a ssh sock was used)
		// -> see below
	}

	if args.SSHAuthSocket.Valid {
		dgstInputs = append(dgstInputs, "sshAuthSock", "true")
	}
	if args.HTTPAuthToken.Valid {
		dgstInputs = append(dgstInputs, "authToken", "true")
	}
	if args.HTTPAuthHeader.Valid {
		dgstInputs = append(dgstInputs, "authHeader", "true")
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

type refArgs struct {
	Name   string
	Commit string `default:"" internal:"true"`
}

func (s *gitSchema) ref(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args refArgs) (inst dagql.Result[*core.GitRef], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	repo := parent.Self()

	// ref selector to specify the expected ref / commit
	refSelector := dagql.Selector{
		Field: "ref",
		Args: []dagql.NamedInput{
			{
				Name:  "name",
				Value: dagql.NewString(args.Name),
			},
		},
		View: dagql.CurrentID(ctx).View(),
	}

	if args.Commit != "" {
		refSelector.Args = append(refSelector.Args, dagql.NamedInput{
			Name:  "commit",
			Value: dagql.NewString(args.Commit),
		})
	}

	// --- Ambiguous URL handling: recurse to extend the dag and try both protocols if needed --
	remoteGitRepo, isRemote := repo.Backend.(*core.RemoteGitRepository)

	// reminder: remoteGitRepo.URL is nil when URL is ambiguous
	if isRemote && remoteGitRepo.URL == nil {
		repoURL := repo.URL.Value.String()

		// todo(check)

		fallbacks := []string{
			"https://" + repoURL,
			"ssh://" + repoURL,
			// no HTTP atm for security reasons, if we want to allow it, shall be under an arg
		}

		for _, url := range fallbacks {
			var res dagql.Result[*core.GitRef]

			gitArgs := gitSelectArgs(repo, remoteGitRepo, url)
			if err := selectWithGit(ctx, srv, gitArgs, &res, refSelector); err == nil {
				return res, nil
			} else if errors.Is(err, gitutil.ErrGitAuthFailed) {
				continue
			} else {
				return inst, err
			}
		}

		return inst, fmt.Errorf("failed to determine Git URL protocol")
	}

	// -- SSH AUTH RECURSION --
	// SSH recursion: ensure an auth socket is present
	if isRemote && remoteGitRepo.URL != nil && remoteGitRepo.URL.Scheme == gitutil.SSHProtocol {
		// default to "git" user like old behavior, on a copy
		u := *remoteGitRepo.URL
		if u.User == nil {
			u.User = url.User("git")
		}

		// if user already provided a socket, nothing to do here
		if !remoteGitRepo.SSHAuthSocket.Valid {
			clientMD, err := engine.ClientMetadataFromContext(ctx)
			if err != nil {
				return inst, fmt.Errorf("client metadata: %w", err)
			}
			if clientMD.SSHAuthSocketPath == "" {
				return inst, fmt.Errorf("%w: SSH URLs are not supported without an SSH socket", gitutil.ErrGitAuthFailed)
			}

			// host.unixSocket(path: ...) → Socket
			var sockInst dagql.ObjectResult[*core.Socket]
			if err := srv.Select(ctx, srv.Root(), &sockInst,
				dagql.Selector{Field: "host"},
				dagql.Selector{
					Field: "unixSocket",
					Args: []dagql.NamedInput{
						{Name: "path", Value: dagql.NewString(clientMD.SSHAuthSocketPath)},
					},
				},
			); err != nil {
				return inst, fmt.Errorf("select unix socket: %w", err)
			}

			// Re-enter git(...) with existing repo state + discovered socket
			selectArgs := gitSelectArgs(repo, remoteGitRepo, u.String())
			selectArgs = append(selectArgs, dagql.NamedInput{
				Name:  "sshAuthSocket",
				Value: dagql.Opt(dagql.NewID[*core.Socket](sockInst.ID())),
			})

			var res dagql.Result[*core.GitRef]
			if err := selectWithGit(ctx, srv, selectArgs, &res, refSelector); err != nil {
				return inst, err
			}
			return res, nil
		}
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return inst, fmt.Errorf("client metadata: %w", err)
	}

	// HTTP(S) recursion: use credential helper if no user-provided auth
	if isRemote && remoteGitRepo.URL != nil &&
		(remoteGitRepo.URL.Scheme == gitutil.HTTPProtocol || remoteGitRepo.URL.Scheme == gitutil.HTTPSProtocol) &&
		!remoteGitRepo.AuthToken.Valid && !remoteGitRepo.AuthHeader.Valid {

		// only main client should attempt PAT resolution
		query, err := core.CurrentQuery(ctx)
		if err != nil {
			return inst, err
		}
		parentMD, err := query.NonModuleParentClientMetadata(ctx)
		if err != nil {
			return inst, fmt.Errorf("non-module parent client metadata: %w", err)
		}

		if clientMetadata.ClientID == parentMD.ClientID {
			authCtx := engine.ContextWithClientMetadata(ctx, parentMD)
			bk, err := query.Buildkit(authCtx)
			if err != nil {
				return inst, fmt.Errorf("buildkit: %w", err)
			}
			creds, err := bk.GetCredential(authCtx, remoteGitRepo.URL.Scheme, remoteGitRepo.URL.Host, remoteGitRepo.URL.Path)
			if err == nil && creds.Password != "" {
				// materialize a Secret via setSecret
				sum := sha256.Sum256([]byte(creds.Password))
				secretName := hex.EncodeToString(sum[:])

				var tokenRes dagql.ObjectResult[*core.Secret]
				if err := srv.Select(authCtx, srv.Root(), &tokenRes,
					dagql.Selector{
						Field: "setSecret",
						Args: []dagql.NamedInput{
							{Name: "name", Value: dagql.NewString(secretName)},
							{Name: "plaintext", Value: dagql.NewString(creds.Password)},
						},
					},
				); err != nil {
					return inst, fmt.Errorf("create git auth secret: %w", err)
				}

				// re-enter git(...) with token (+username if present)
				selectArgs := gitSelectArgs(repo, remoteGitRepo, remoteGitRepo.URL.String())
				selectArgs = append(selectArgs, dagql.NamedInput{
					Name:  "httpAuthToken",
					Value: dagql.Opt(dagql.NewID[*core.Secret](tokenRes.ID())),
				})
				if creds.Username != "" {
					selectArgs = append(selectArgs, dagql.NamedInput{
						Name:  "httpAuthUsername",
						Value: dagql.NewString(creds.Username),
					})
				}

				var res dagql.Result[*core.GitRef]
				if err := selectWithGit(ctx, srv, selectArgs, &res, refSelector); err != nil {
					return inst, err
				}
				return res, nil
			} else if err != nil {
				slog.Warn("GetCredential failed", "error", err)
			}
		}
	}

	// feedback loop: auth will work if properly set
	backendRemote, err := repo.Backend.Remote(ctx)
	if err != nil {
		return inst, err
	}

	ref, err := backendRemote.Lookup(args.Name)
	if err != nil {
		return inst, err
	}
	if args.Commit != "" && args.Commit != ref.SHA {
		ref.SHA = args.Commit
	}

	refBackend, err := repo.Backend.Get(ctx, ref)
	if err != nil {
		return inst, err
	}

	result := &core.GitRef{
		Repo:    parent,
		Ref:     ref,
		Backend: refBackend,
	}
	inst, err = dagql.NewResultForCurrentID(ctx, result)
	if err != nil {
		return inst, err
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}

	// all the same as in git, but instead of the *remote* details, just use
	// the *ref* details
	// if the upstream remote changes in a ref we don't care about, it
	// shouldn't be mixed into the cache
	dgstInputs := []string{
		repo.URL.Value.String(),
		string(ref.Digest()),
		strconv.FormatBool(repo.DiscardGitDir),
	}
	var resourceIDs []*resource.ID

	if remoteRepo, ok := repo.Backend.(*core.RemoteGitRepository); ok {
		// mark which auth modes are used (affects digest)
		if remoteRepo.SSHAuthSocket.Valid {
			dgstInputs = append(dgstInputs, "sshAuthSock", "true")
			// NOTE: resource.ID.ID expects a call.ID. Take it from the *ID()* method.
			if id := remoteRepo.SSHAuthSocket.Value.ID(); id != nil {
				resourceIDs = append(resourceIDs, &resource.ID{ID: *id})
			}
		}
		if remoteRepo.AuthToken.Valid {
			dgstInputs = append(dgstInputs, "authToken", "true")
			if id := remoteRepo.AuthToken.Value.ID(); id != nil {
				resourceIDs = append(resourceIDs, &resource.ID{ID: *id})
			}
		}
		if remoteRepo.AuthHeader.Valid {
			dgstInputs = append(dgstInputs, "authHeader", "true")
			if id := remoteRepo.AuthHeader.Value.ID(); id != nil {
				resourceIDs = append(resourceIDs, &resource.ID{ID: *id})
			}
		}

		// Optional: include service binding into digest to avoid cross-service cache hits
		if len(remoteRepo.Services) > 0 && remoteRepo.Services[0].Service.Self() != nil {
			dgstInputs = append(dgstInputs, "svc", remoteRepo.Services[0].Service.ID().Digest().String())
		}
	}
	inst = inst.WithDigest(hashutil.HashStrings(dgstInputs...))
	if len(resourceIDs) > 0 {
		postCall, err := core.ResourceTransferPostCall(ctx, query, clientMetadata.ClientID, resourceIDs...)
		if err != nil {
			return inst, fmt.Errorf("failed to create post call: %w", err)
		}
		inst = inst.ResultWithPostCall(postCall)
	}
	return inst, nil
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
	remote, err := parent.Self().Backend.Remote(ctx)
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

func (s *gitSchema) tags(ctx context.Context, parent *core.GitRepository, args tagsArgs) (dagql.Array[dagql.String], error) {
	var patterns []string
	if args.Patterns.Valid {
		for _, pattern := range args.Patterns.Value {
			patterns = append(patterns, pattern.String())
		}
	}
	remote, err := parent.Backend.Remote(ctx)
	if err != nil {
		return nil, err
	}
	return dagql.NewStringArray(remote.Filter(patterns).Tags().ShortNames()...), nil
}

type branchesArgs struct {
	Patterns dagql.Optional[dagql.ArrayInput[dagql.String]] `name:"patterns"`
}

func (s *gitSchema) branches(ctx context.Context, parent *core.GitRepository, args branchesArgs) (dagql.Array[dagql.String], error) {
	var patterns []string
	if args.Patterns.Valid {
		for _, pattern := range args.Patterns.Value {
			patterns = append(patterns, pattern.String())
		}
	}
	remote, err := parent.Backend.Remote(ctx)
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
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	token, err := args.Token.Load(ctx, srv)
	if err != nil {
		return nil, err
	}
	repo := *parent
	if remote, ok := repo.Backend.(*core.RemoteGitRepository); ok {
		remote := *remote
		remote.AuthToken = token
		repo.Backend = &remote
	}
	return &repo, nil
}

type withAuthHeaderArgs struct {
	Header core.SecretID
}

func (s *gitSchema) withAuthHeader(ctx context.Context, parent *core.GitRepository, args withAuthHeaderArgs) (*core.GitRepository, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	header, err := args.Header.Load(ctx, srv)
	if err != nil {
		return nil, err
	}
	repo := *parent
	if remote, ok := repo.Backend.(*core.RemoteGitRepository); ok {
		remote := *remote
		remote.AuthHeader = header
		repo.Backend = &remote
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
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	if args.SSHKnownHosts.Valid {
		return inst, fmt.Errorf("sshKnownHosts is no longer supported on `tree`")
	}
	if args.SSHAuthSocket.Valid {
		return inst, fmt.Errorf("sshAuthSocket is no longer supported on `tree`")
	}

	if args.IsDagOp {
		dir, err := parent.Self().Tree(ctx, srv, args.DiscardGitDir, args.Depth)
		if err != nil {
			return inst, err
		}
		return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
	}

	dir, err := DagOpDirectory(ctx, srv, parent.Self(), args, "", s.tree)
	if err != nil {
		return inst, err
	}
	inst, err = dagql.NewObjectResultForCurrentID(ctx, srv, dir)
	if err != nil {
		return inst, err
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return inst, err
	}

	remoteRepo, isRemoteRepo := parent.Self().Repo.Self().Backend.(*core.RemoteGitRepository)
	if isRemoteRepo {
		usedAuth := remoteRepo.AuthToken.Self() != nil ||
			remoteRepo.AuthHeader.Self() != nil ||
			remoteRepo.SSHAuthSocket.Self() != nil
		if usedAuth {
			// do a full hash of the actual files/dirs in the private git repo so
			// that the cache key of the returned value can't be known unless the
			// full contents are already known
			dgst, err := core.GetContentHashFromDirectory(ctx, bk, inst)
			if err != nil {
				return inst, fmt.Errorf("failed to get content hash: %w", err)
			}
			inst = inst.WithObjectDigest(hashutil.HashStrings(dagql.CurrentID(ctx).Digest().String(), dgst.String()))
		}
	}

	return inst, nil
}

func (s *gitSchema) fetchCommit(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRef],
	args RawDagOpInternalArgs,
) (dagql.String, error) {
	return dagql.NewString(parent.Self().Ref.SHA), nil
}

func (s *gitSchema) fetchRef(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRef],
	args RawDagOpInternalArgs,
) (dagql.String, error) {
	return dagql.NewString(cmp.Or(parent.Self().Ref.Name, parent.Self().Ref.SHA)), nil
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
	other, err := args.Other.Load(ctx, srv)
	if err != nil {
		return inst, err
	}

	result, err := core.MergeBase(ctx, parent.Self(), other.Self())
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, result)
}

func selectWithGit[T any](
	ctx context.Context,
	srv *dagql.Server,
	args []dagql.NamedInput,
	dest *T,
	sels ...dagql.Selector,
) error {
	route := append([]dagql.Selector{{
		Field: "git",
		Args:  args,
		View:  dagql.CurrentID(ctx).View(),
	}}, sels...)
	return srv.Select(ctx, srv.Root(), dest, route...)
}

func gitSelectArgs(repo *core.GitRepository, remote *core.RemoteGitRepository, urlOverride string) []dagql.NamedInput {
	var args []dagql.NamedInput

	url := urlOverride
	if url == "" {
		switch {
		case remote != nil && remote.URL != nil:
			url = remote.URL.String()
		case repo.URL.Valid:
			url = repo.URL.Value.String()
		}
	}
	if url != "" {
		args = append(args, dagql.NamedInput{
			Name:  "url",
			Value: dagql.NewString(url),
		})
	}

	if remote != nil {
		if remote.SSHKnownHosts != "" {
			args = append(args, dagql.NamedInput{
				Name:  "sshKnownHosts",
				Value: dagql.NewString(remote.SSHKnownHosts),
			})
		}
		if remote.SSHAuthSocket.Self() != nil {
			args = append(args, dagql.NamedInput{
				Name:  "sshAuthSocket",
				Value: dagql.Opt(dagql.NewID[*core.Socket](remote.SSHAuthSocket.ID())),
			})
		}
		if remote.AuthToken.Self() != nil {
			args = append(args, dagql.NamedInput{
				Name:  "httpAuthToken",
				Value: dagql.Opt(dagql.NewID[*core.Secret](remote.AuthToken.ID())),
			})
		}
		if remote.AuthHeader.Self() != nil {
			args = append(args, dagql.NamedInput{
				Name:  "httpAuthHeader",
				Value: dagql.Opt(dagql.NewID[*core.Secret](remote.AuthHeader.ID())),
			})
		}
		if remote.AuthUsername != "" {
			args = append(args, dagql.NamedInput{
				Name:  "httpAuthUsername",
				Value: dagql.NewString(remote.AuthUsername),
			})
		}
		if len(remote.Services) > 0 && remote.Services[0].Service.Self() != nil {
			args = append(args, dagql.NamedInput{
				Name:  "experimentalServiceHost",
				Value: dagql.Opt(dagql.NewID[*core.Service](remote.Services[0].Service.ID())),
			})
		}
	}

	if repo.PinnedHead != nil {
		if repo.PinnedHead.Name != "" {
			args = append(args, dagql.NamedInput{
				Name:  "ref",
				Value: dagql.NewString(repo.PinnedHead.Name),
			})
		}
		if repo.PinnedHead.SHA != "" {
			args = append(args, dagql.NamedInput{
				Name:  "commit",
				Value: dagql.NewString(repo.PinnedHead.SHA),
			})
		}
	}

	if repo.DiscardGitDir {
		args = append(args, dagql.NamedInput{
			Name:  "keepGitDir",
			Value: dagql.Opt(dagql.Boolean(false)),
		})
	}

	return args
}
