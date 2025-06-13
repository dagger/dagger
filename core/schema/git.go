package schema

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/sources/netconfhttp"
	"github.com/moby/buildkit/executor/oci"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
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

type gitSchema struct {
	srv *dagql.Server
}

func (s *gitSchema) Install() {
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
	}.Install(s.srv)

	dagql.Fields[*core.GitRepository]{
		dagql.NodeFuncWithCacheKey("head", s.head, dagql.CachePerSession).
			Doc(`Returns details for HEAD.`),
		dagql.NodeFuncWithCacheKey("ref", s.ref, dagql.CachePerSession).
			Doc(`Returns details of a ref.`).
			Args(
				dagql.Arg("name").Doc(`Ref's name (can be a commit identifier, a tag name, a branch name, or a fully-qualified ref).`),
			),
		dagql.NodeFuncWithCacheKey("branch", s.branch, dagql.CachePerSession).
			Doc(`Returns details of a branch.`).
			Args(
				dagql.Arg("name").Doc(`Branch's name (e.g., "main").`),
			),
		dagql.NodeFuncWithCacheKey("tag", s.tag, dagql.CachePerSession).
			Doc(`Returns details of a tag.`).
			Args(
				dagql.Arg("name").Doc(`Tag's name (e.g., "v0.3.9").`),
			),
		dagql.NodeFuncWithCacheKey("commit", s.commit, dagql.CachePerSession).
			Doc(`Returns details of a commit.`).
			Args(
				// TODO: id is normally a reserved word; we should probably rename this
				dagql.Arg("id").Doc(`Identifier of the commit (e.g., "b6315d8f2810962c601af73f86831f6866ea798b").`),
			),
		dagql.NodeFuncWithCacheKey("tags", s.tags, dagql.CachePerSession).
			Doc(`tags that match any of the given glob patterns.`).
			Args(
				dagql.Arg("patterns").Doc(`Glob patterns (e.g., "refs/tags/v*").`),
			),
		dagql.NodeFuncWithCacheKey("branches", s.branches, dagql.CachePerSession).
			Doc(`branches that match any of the given glob patterns.`).
			Args(
				dagql.Arg("patterns").Doc(`Glob patterns (e.g., "refs/tags/v*").`),
			),
		dagql.Func("withAuthToken", s.withAuthToken).
			Doc(`Token to authenticate the remote with.`).
			Deprecated(`Use "httpAuthToken" in the constructor instead.`).
			Args(
				dagql.Arg("token").Doc(`Secret used to populate the password during basic HTTP Authorization`),
			),
		dagql.Func("withAuthHeader", s.withAuthHeader).
			Doc(`Header to authenticate the remote with.`).
			Deprecated(`Use "httpAuthHeader" in the constructor instead.`).
			Args(
				dagql.Arg("header").Doc(`Secret used to populate the Authorization HTTP header`),
			),
	}.Install(s.srv)

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
	}.Install(s.srv)
}

type gitArgs struct {
	URL                     string
	KeepGitDir              dagql.Optional[dagql.Boolean] `default:"false"`
	ExperimentalServiceHost dagql.Optional[core.ServiceID]

	SSHKnownHosts string                        `name:"sshKnownHosts" default:""`
	SSHAuthSocket dagql.Optional[core.SocketID] `name:"sshAuthSocket"`

	HTTPAuthUsername string                        `name:"httpAuthUsername" default:""`
	HTTPAuthToken    dagql.Optional[core.SecretID] `name:"httpAuthToken"`
	HTTPAuthHeader   dagql.Optional[core.SecretID] `name:"httpAuthHeader"`
}

//nolint:gocyclo
func (s *gitSchema) git(ctx context.Context, parent dagql.Instance[*core.Query], args gitArgs) (inst dagql.Instance[*core.GitRepository], _ error) {
	remote, err := gitutil.ParseURL(args.URL)
	if errors.Is(err, gitutil.ErrUnknownProtocol) {
		remote, err = gitutil.ParseURL("https://" + args.URL)
	}
	if err != nil {
		return inst, fmt.Errorf("failed to parse Git URL: %w", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get client metadata from context: %w", err)
	}

	var gitServices core.ServiceBindings
	if args.ExperimentalServiceHost.Valid {
		svc, err := args.ExperimentalServiceHost.Value.Load(ctx, s.srv)
		if err != nil {
			return inst, err
		}
		host, err := svc.Self.Hostname(ctx, svc.ID())
		if err != nil {
			return inst, err
		}
		gitServices = append(gitServices, core.ServiceBinding{
			Service:  svc,
			Hostname: host,
		})
	}

	var (
		sshAuthSock    dagql.Instance[*core.Socket]
		httpAuthToken  dagql.Instance[*core.Secret]
		httpAuthHeader dagql.Instance[*core.Secret]
	)

	switch remote.Scheme {
	case gitutil.SSHProtocol:
		if remote.User == nil {
			// default to git user for SSH, otherwise weird incorrect defaults
			// like "root" can get applied in various places. This matches the
			// git module source implementation.
			remote.User = url.User("git")
		}

		if args.SSHAuthSocket.Valid {
			sshAuthSock, err = args.SSHAuthSocket.Value.Load(ctx, s.srv)
			if err != nil {
				return inst, err
			}
		} else if clientMetadata.SSHAuthSocketPath != "" {
			// For SSH refs, try to load client's SSH socket if no explicit socket was provided
			var sockInst dagql.Instance[*core.Socket]
			if err := s.srv.Select(ctx, s.srv.Root(), &sockInst,
				dagql.Selector{
					Field: "host",
				},
				dagql.Selector{
					Field: "unixSocket",
					Args: []dagql.NamedInput{
						{
							Name:  "path",
							Value: dagql.NewString(clientMetadata.SSHAuthSocketPath),
						},
					},
				},
			); err != nil {
				return inst, fmt.Errorf("failed to select unix socket: %w", err)
			}

			// reinvoke this API with the socket as an explicit arg so it shows up in the DAG
			selectArgs := []dagql.NamedInput{
				{
					Name:  "url",
					Value: dagql.NewString(remote.String()),
				},
				{
					Name:  "sshAuthSocket",
					Value: dagql.Opt(dagql.NewID[*core.Socket](sockInst.ID())),
				},
			}
			if args.KeepGitDir.Valid {
				selectArgs = append(selectArgs, dagql.NamedInput{
					Name:  "keepGitDir",
					Value: dagql.Opt(args.KeepGitDir.Value),
				})
			}
			if args.ExperimentalServiceHost.Valid {
				selectArgs = append(selectArgs, dagql.NamedInput{
					Name:  "experimentalServiceHost",
					Value: dagql.Opt(dagql.NewID[*core.Service](args.ExperimentalServiceHost.Value.ID())),
				})
			}
			if args.SSHKnownHosts != "" {
				selectArgs = append(selectArgs, dagql.NamedInput{
					Name:  "sshKnownHosts",
					Value: dagql.NewString(args.SSHKnownHosts),
				})
			}
			err = s.srv.Select(ctx, parent, &inst, dagql.Selector{
				Field: "git",
				Args:  selectArgs,
				View:  dagql.View(dagql.CurrentID(ctx).View()),
			})
			return inst, err
		} else {
			return inst, fmt.Errorf("SSH URLs are not supported without an SSH socket")
		}
	case gitutil.HTTPProtocol, gitutil.HTTPSProtocol:
		if args.HTTPAuthToken.Valid {
			httpAuthToken, err = args.HTTPAuthToken.Value.Load(ctx, s.srv)
			if err != nil {
				return inst, err
			}
		}
		if args.HTTPAuthHeader.Valid {
			httpAuthHeader, err = args.HTTPAuthHeader.Value.Load(ctx, s.srv)
			if err != nil {
				return inst, err
			}
		}
		if httpAuthToken.Self == nil && httpAuthHeader.Self == nil {
			// For HTTP refs, try to load client credentials from the git helper
			parentClientMetadata, err := parent.Self.NonModuleParentClientMetadata(ctx)
			if err != nil {
				return inst, fmt.Errorf("failed to retrieve non-module parent client metadata: %w", err)
			}
			if clientMetadata.ClientID != parentClientMetadata.ClientID {
				// only handle PAT auth if we're the main client
				break
			}

			// start services if needed, before checking for auth
			var dnsConfig *oci.DNSConfig
			if len(gitServices) > 0 {
				svcs, err := parent.Self.Services(ctx)
				if err != nil {
					return inst, fmt.Errorf("failed to get services: %w", err)
				}
				detach, _, err := svcs.StartBindings(ctx, gitServices)
				if err != nil {
					return inst, err
				}
				defer detach()

				dnsConfig, err = core.DNSConfig(ctx)
				if err != nil {
					return inst, err
				}
			}

			public, err := isRemotePublic(netconfhttp.WithDNSConfig(ctx, dnsConfig), remote)
			if err != nil {
				return inst, err
			}
			if public {
				break
			}

			// Retrieve credential from host
			authCtx := engine.ContextWithClientMetadata(ctx, parentClientMetadata)
			bk, err := parent.Self.Buildkit(authCtx)
			if err != nil {
				return inst, fmt.Errorf("failed to get buildkit: %w", err)
			}
			credentials, err := bk.GetCredential(authCtx, remote.Scheme, remote.Host, remote.Path)
			if err != nil {
				// it's possible to provide auth tokens via chained API calls, so warn now but
				// don't fail. Auth will be checked again before relevant operations later.
				slog.Warn("Failed to retrieve git credentials", "error", err)
				break
			}

			hash := sha256.Sum256([]byte(credentials.Password))
			secretName := hex.EncodeToString(hash[:])
			var authToken dagql.Instance[*core.Secret]
			if err := s.srv.Select(authCtx, s.srv.Root(), &authToken,
				dagql.Selector{
					Field: "setSecret",
					Args: []dagql.NamedInput{
						{
							Name:  "name",
							Value: dagql.NewString(secretName),
						},
						{
							Name:  "plaintext",
							Value: dagql.NewString(credentials.Password),
						},
					},
				},
			); err != nil {
				return inst, fmt.Errorf("failed to create a new secret with the git auth token: %w", err)
			}

			// reinvoke this API with the socket as an explicit arg so it shows up in the DAG
			selectArgs := []dagql.NamedInput{
				{
					Name:  "url",
					Value: dagql.NewString(remote.String()),
				},
				{
					Name:  "httpAuthToken",
					Value: dagql.Opt(dagql.NewID[*core.Secret](authToken.ID())),
				},
			}
			// Omit blank username; adding it would change the selector hash and kill cache hits.
			if credentials.Username != "" {
				selectArgs = append(selectArgs, dagql.NamedInput{
					Name:  "httpAuthUsername",
					Value: dagql.NewString(credentials.Username),
				})
			}
			if args.KeepGitDir.Valid {
				selectArgs = append(selectArgs, dagql.NamedInput{
					Name:  "keepGitDir",
					Value: dagql.Opt(args.KeepGitDir.Value),
				})
			}
			if args.ExperimentalServiceHost.Valid {
				selectArgs = append(selectArgs, dagql.NamedInput{
					Name:  "experimentalServiceHost",
					Value: dagql.Opt(dagql.NewID[*core.Service](args.ExperimentalServiceHost.Value.ID())),
				})
			}
			err = s.srv.Select(ctx, parent, &inst, dagql.Selector{
				Field: "git",
				Args:  selectArgs,
				View:  dagql.View(dagql.CurrentID(ctx).View()),
			})
			return inst, err
		}
	}

	discardGitDir := false
	if args.KeepGitDir.Valid {
		slog.Warn("The 'keepGitDir' argument is deprecated. Use `tree`'s `discardGitDir' instead.")
		discardGitDir = !args.KeepGitDir.Value.Bool()
	}

	inst, err = dagql.NewInstanceForCurrentID(ctx, s.srv, parent, &core.GitRepository{
		Backend: &core.RemoteGitRepository{
			URL:           remote,
			SSHKnownHosts: args.SSHKnownHosts,
			SSHAuthSocket: sshAuthSock.Self,
			AuthUsername:  args.HTTPAuthUsername,
			AuthToken:     httpAuthToken,
			AuthHeader:    httpAuthHeader,
			Services:      gitServices,
			Platform:      parent.Self.Platform(),
		},
		DiscardGitDir: discardGitDir,
	})
	if err != nil {
		return inst, fmt.Errorf("failed to create GitRepository instance: %w", err)
	}

	var resourceIDs []*resource.ID
	if sshAuthSock.Self != nil {
		resourceIDs = append(resourceIDs, &resource.ID{ID: *sshAuthSock.ID()})
	}
	if httpAuthToken.Self != nil {
		resourceIDs = append(resourceIDs, &resource.ID{ID: *httpAuthToken.ID()})
	}
	if httpAuthHeader.Self != nil {
		resourceIDs = append(resourceIDs, &resource.ID{ID: *httpAuthHeader.ID()})
	}
	if len(resourceIDs) > 0 {
		postCall, err := core.ResourceTransferPostCall(ctx, parent.Self, clientMetadata.ClientID, resourceIDs...)
		if err != nil {
			return inst, fmt.Errorf("failed to create post call: %w", err)
		}

		inst = inst.WithPostCall(postCall)
	}

	return inst, nil
}

func isRemotePublic(ctx context.Context, remote *gitutil.GitURL) (bool, error) {
	// check if repo is public
	repo := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{
		Name: "origin",
		URLs: []string{remote.Remote()},
	})
	_, err := repo.ListContext(ctx, &git.ListOptions{Auth: nil})
	if err != nil {
		if errors.Is(err, transport.ErrAuthenticationRequired) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

type refArgs struct {
	Name string
}

func (s *gitSchema) ref(ctx context.Context, parent dagql.Instance[*core.GitRepository], args refArgs) (inst dagql.Instance[*core.GitRef], _ error) {
	result, err := parent.Self.Ref(ctx, args.Name)
	if err != nil {
		return inst, err
	}
	inst, err = dagql.NewInstanceForCurrentID(ctx, s.srv, parent, result)
	if err != nil {
		return inst, err
	}

	if ref, ok := inst.Self.Backend.(*core.RemoteGitRef); ok {
		dgstInputs := []string{
			// include the full remote url
			ref.Repo.URL.String(),
			// include the fully resolved ref + commit, since the remote repo
			// *might* change
			ref.FullRef,
			ref.Commit,
			// also include what auth methods are used, currently we can't
			// handle a cache hit where the result has a different auth
			// method than the caller used (i.e. a git repo is pulled w/
			// a token but hits cache for a dir where a ssh sock was used)
			string(ref.Repo.AuthToken.ID().Digest()),
			string(ref.Repo.AuthHeader.ID().Digest()),
			ref.Repo.SSHAuthSocket.LLBID(),
			// finally, the legacy args
			strconv.FormatBool(inst.Self.Repo.DiscardGitDir),
		}
		inst = inst.WithDigest(dagql.HashFrom(dgstInputs...))
		return inst, nil
	}
	return inst, nil
}

func (s *gitSchema) head(ctx context.Context, parent dagql.Instance[*core.GitRepository], args struct{}) (inst dagql.Instance[*core.GitRef], _ error) {
	return s.ref(ctx, parent, refArgs{Name: "HEAD"})
}

type commitArgs struct {
	ID string
}

func (s *gitSchema) commit(ctx context.Context, parent dagql.Instance[*core.GitRepository], args commitArgs) (dagql.Instance[*core.GitRef], error) {
	// TODO: should enforce gitutil.IsCommitSHA
	return s.ref(ctx, parent, refArgs{Name: args.ID})
}

type branchArgs struct {
	Name string
}

func (s *gitSchema) branch(ctx context.Context, parent dagql.Instance[*core.GitRepository], args branchArgs) (dagql.Instance[*core.GitRef], error) {
	// TODO: should enforce refs/heads/ prefix
	return s.ref(ctx, parent, refArgs(args))
}

type tagArgs struct {
	Name string
}

func (s *gitSchema) tag(ctx context.Context, parent dagql.Instance[*core.GitRepository], args tagArgs) (dagql.Instance[*core.GitRef], error) {
	// TODO: should enforce refs/tags/ prefix
	return s.ref(ctx, parent, refArgs(args))
}

type tagsArgs struct {
	Patterns dagql.Optional[dagql.ArrayInput[dagql.String]] `name:"patterns"`
}

func (s *gitSchema) tags(ctx context.Context, parent dagql.Instance[*core.GitRepository], args tagsArgs) (dagql.Array[dagql.String], error) {
	var patterns []string
	if args.Patterns.Valid {
		for _, pattern := range args.Patterns.Value {
			patterns = append(patterns, pattern.String())
		}
	}
	res, err := parent.Self.Tags(ctx, patterns)
	return dagql.NewStringArray(res...), err
}

type withAuthTokenArgs struct {
	Token core.SecretID
}

type branchesArgs struct {
	Patterns dagql.Optional[dagql.ArrayInput[dagql.String]] `name:"patterns"`
}

func (s *gitSchema) branches(ctx context.Context, parent dagql.Instance[*core.GitRepository], args branchesArgs) (dagql.Array[dagql.String], error) {
	var patterns []string
	if args.Patterns.Valid {
		for _, pattern := range args.Patterns.Value {
			patterns = append(patterns, pattern.String())
		}
	}
	res, err := parent.Self.Branches(ctx, patterns)
	return dagql.NewStringArray(res...), err
}

func (s *gitSchema) withAuthToken(ctx context.Context, parent *core.GitRepository, args withAuthTokenArgs) (*core.GitRepository, error) {
	token, err := args.Token.Load(ctx, s.srv)
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
	header, err := args.Header.Load(ctx, s.srv)
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

func (s *gitSchema) tree(ctx context.Context, parent dagql.Instance[*core.GitRef], args treeArgs) (inst dagql.Instance[*core.Directory], _ error) {
	if args.SSHKnownHosts.Valid {
		return inst, fmt.Errorf("sshKnownHosts is no longer supported on `tree`")
	}
	if args.SSHAuthSocket.Valid {
		return inst, fmt.Errorf("sshAuthSocket is no longer supported on `tree`")
	}

	if args.IsDagOp {
		dir, err := parent.Self.Tree(ctx, s.srv, args.DiscardGitDir, args.Depth)
		if err != nil {
			return inst, err
		}
		return dagql.NewInstanceForCurrentID(ctx, s.srv, parent, dir)
	}

	inst, err := DagOpDirectory(ctx, s.srv, parent, args, "", s.tree, nil)
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

	remoteRepo, isRemoteRepo := parent.Self.Repo.Backend.(*core.RemoteGitRepository)
	if isRemoteRepo {
		usedAuth := remoteRepo.AuthToken.Self != nil ||
			remoteRepo.AuthHeader.Self != nil ||
			remoteRepo.SSHAuthSocket != nil
		if usedAuth {
			// do a full hash of the actual files/dirs in the private git repo so
			// that the cache key of the returned value can't be known unless the
			// full contents are already known
			dgst, err := core.GetContentHashFromDirectory(ctx, bk, inst)
			if err != nil {
				return inst, fmt.Errorf("failed to get content hash: %w", err)
			}
			inst = inst.WithDigest(dagql.HashFrom(dagql.CurrentID(ctx).Digest().String(), dgst.String()))
		}
	}

	return inst, nil
}

func (s *gitSchema) fetchCommit(
	ctx context.Context,
	parent dagql.Instance[*core.GitRef],
	args RawDagOpInternalArgs,
) (dagql.String, error) {
	if !args.IsDagOp {
		return DagOp(ctx, s.srv, parent, args, s.fetchCommit)
	}

	commit, _, err := parent.Self.Resolve(ctx)
	if err != nil {
		return "", err
	}
	return dagql.NewString(commit), nil
}

func (s *gitSchema) fetchRef(
	ctx context.Context,
	parent dagql.Instance[*core.GitRef],
	args RawDagOpInternalArgs,
) (dagql.String, error) {
	if !args.IsDagOp {
		return DagOp(ctx, s.srv, parent, args, s.fetchCommit)
	}

	_, ref, err := parent.Self.Resolve(ctx)
	if err != nil {
		return "", err
	}
	return dagql.NewString(ref), nil
}
