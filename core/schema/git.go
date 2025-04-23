package schema

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/dagger/dagger/engine/slog"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/storage/memory"
)

var _ SchemaResolvers = &gitSchema{}

type gitSchema struct {
	srv *dagql.Server
}

func (s *gitSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.NodeFuncWithCacheKey("git", s.git, dagql.CachePerClient).
			View(AllVersion).
			Doc(`Queries a Git repository.`).
			ArgDoc("url",
				`URL of the git repository.`,
				"Can be formatted as `https://{host}/{owner}/{repo}`, `git@{host}:{owner}/{repo}`.",
				`Suffix ".git" is optional.`).
			ArgDeprecated("keepGitDir", `Set to true to keep .git directory.`).
			ArgDoc("sshKnownHosts", `Set SSH known hosts`).
			ArgDoc("sshAuthSocket", `Set SSH auth socket`).
			ArgDoc("experimentalServiceHost", `A service which must be started before the repo is fetched.`),
		dagql.NodeFuncWithCacheKey("git", s.gitLegacy, dagql.CachePerClient).
			View(BeforeVersion("v0.13.4")).
			Doc(`Queries a Git repository.`).
			ArgDoc("url",
				`URL of the git repository.`,
				"Can be formatted as `https://{host}/{owner}/{repo}`, `git@{host}:{owner}/{repo}`.",
				`Suffix ".git" is optional.`).
			ArgDeprecated("keepGitDir", `Set to true to keep .git directory.`).
			ArgDoc("sshKnownHosts", `Set SSH known hosts`).
			ArgDoc("sshAuthSocket", `Set SSH auth socket`).
			ArgDoc("experimentalServiceHost", `A service which must be started before the repo is fetched.`),
	}.Install(s.srv)

	dagql.Fields[*core.GitRepository]{
		dagql.NodeFuncWithCacheKey("head", s.head, dagql.CachePerSession).
			Doc(`Returns details for HEAD.`),
		dagql.NodeFuncWithCacheKey("ref", s.ref, dagql.CachePerSession).
			Doc(`Returns details of a ref.`).
			ArgDoc("name", `Ref's name (can be a commit identifier, a tag name, a branch name, or a fully-qualified ref).`),
		dagql.NodeFuncWithCacheKey("branch", s.branch, dagql.CachePerSession).
			Doc(`Returns details of a branch.`).
			ArgDoc("name", `Branch's name (e.g., "main").`),
		dagql.NodeFuncWithCacheKey("tag", s.tag, dagql.CachePerSession).
			Doc(`Returns details of a tag.`).
			ArgDoc("name", `Tag's name (e.g., "v0.3.9").`),
		dagql.NodeFuncWithCacheKey("commit", s.commit, dagql.CachePerSession).
			Doc(`Returns details of a commit.`).
			// TODO: id is normally a reserved word; we should probably rename this
			ArgDoc("id", `Identifier of the commit (e.g., "b6315d8f2810962c601af73f86831f6866ea798b").`),
		dagql.NodeFuncWithCacheKey("tags", s.tags, dagql.CachePerSession).
			Doc(`tags that match any of the given glob patterns.`).
			ArgDoc("patterns", `Glob patterns (e.g., "refs/tags/v*").`),
		dagql.Func("withAuthToken", s.withAuthToken).
			Doc(`Token to authenticate the remote with.`).
			ArgDoc("token", `Secret used to populate the password during basic HTTP Authorization`),
		dagql.Func("withAuthHeader", s.withAuthHeader).
			Doc(`Header to authenticate the remote with.`).
			ArgDoc("header", `Secret used to populate the Authorization HTTP header`),
	}.Install(s.srv)

	dagql.Fields[*core.GitRef]{
		dagql.NodeFunc("tree", s.tree).
			View(AllVersion).
			Doc(`The filesystem tree at this ref.`).
			ArgDoc("discardGitDir", `Set to true to discard .git directory.`).
			ArgDoc("depth", `The depth of the tree to fetch.`),
		dagql.NodeFunc("tree", s.treeLegacy).
			View(BeforeVersion("v0.12.0")).
			Doc(`The filesystem tree at this ref.`).
			ArgDoc("discardGitDir", `Set to true to discard .git directory.`).
			ArgDoc("depth", `The depth of the tree to fetch.`).
			ArgDeprecated("sshKnownHosts", "This option should be passed to `git` instead.").
			ArgDeprecated("sshAuthSocket", "This option should be passed to `git` instead."),
		dagql.NodeFunc("commit", s.fetchCommit).
			Doc(`The resolved commit id at this ref.`),
		dagql.NodeFunc("ref", s.fetchRef).
			Doc(`The resolved ref name at this ref.`),
	}.Install(s.srv)
}

type gitArgs struct {
	URL                     string
	KeepGitDir              dagql.Optional[dagql.Boolean] `default:"true"`
	ExperimentalServiceHost dagql.Optional[core.ServiceID]

	SSHKnownHosts string                        `name:"sshKnownHosts" default:""`
	SSHAuthSocket dagql.Optional[core.SocketID] `name:"sshAuthSocket"`
}

//nolint:gocyclo
func (s *gitSchema) git(ctx context.Context, parent dagql.Instance[*core.Query], args gitArgs) (inst dagql.Instance[*core.GitRepository], _ error) {
	// 1. Setup experimental service host
	var svcs core.ServiceBindings
	if args.ExperimentalServiceHost.Valid {
		svc, err := args.ExperimentalServiceHost.Value.Load(ctx, s.srv)
		if err != nil {
			return inst, err
		}
		host, err := svc.Self.Hostname(ctx, svc.ID())
		if err != nil {
			return inst, err
		}
		svcs = append(svcs, core.ServiceBinding{
			ID:       svc.ID(),
			Service:  svc.Self,
			Hostname: host,
		})
	}

	// 2. Get client metadata
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get client metadata from context: %w", err)
	}

	// 3. Setup authentication
	var authSock dagql.Instance[*core.Socket]
	var authToken dagql.Instance[*core.Secret]

	// First parse the ref scheme to determine auth strategy
	remote, err := gitutil.ParseURL(args.URL)
	if errors.Is(err, gitutil.ErrUnknownProtocol) {
		remote, err = gitutil.ParseURL("https://" + args.URL)
	}
	if err != nil {
		return inst, fmt.Errorf("failed to parse Git URL: %w", err)
	}
	if remote.Scheme == "ssh" && (remote.User == nil || remote.User.Username() == "") {
		// default to git user for SSH, otherwise weird incorrect defaults like "root" can get
		// applied in various places. This matches the git module source implementation.
		if remote.User == nil {
			remote.User = url.User("git")
		}
		pass, ok := remote.User.Password()
		if ok {
			remote.User = url.UserPassword(remote.User.Username(), pass)
		}
		fixedURL := &url.URL{
			Scheme: remote.Scheme,
			User:   remote.User,
			Host:   remote.Host,
			Path:   remote.Path,
		}
		if remote.Fragment != nil {
			fixedURL.Fragment = remote.Fragment.Ref
			if remote.Fragment.Subdir != "" {
				fixedURL.Fragment += ":" + remote.Fragment.Subdir
			}
		}
		args.URL = fixedURL.String()
	}

	// Handle explicit SSH socket if provided
	switch {
	case args.SSHAuthSocket.Valid:
		sock, err := args.SSHAuthSocket.Value.Load(ctx, s.srv)
		if err != nil {
			return inst, err
		}
		authSock = sock

	case remote.Scheme == "ssh" && clientMetadata != nil && clientMetadata.SSHAuthSocketPath != "":
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
				Value: dagql.NewString(args.URL),
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
				Value: dagql.Opt(dagql.NewString(args.SSHKnownHosts)),
			})
		}
		err = s.srv.Select(ctx, parent, &inst, dagql.Selector{Field: "git", Args: selectArgs})
		return inst, err

	case remote.Scheme == "ssh":
		return inst, fmt.Errorf("SSH URLs are not supported without an SSH socket")
	}

	// For HTTP(S) refs, handle PAT auth if we're the main client
	if (remote.Scheme == "https" || remote.Scheme == "http") && clientMetadata != nil {
		parentClientMetadata, err := parent.Self.NonModuleParentClientMetadata(ctx)
		if err != nil {
			return inst, fmt.Errorf("failed to retrieve non-module parent client metadata: %w", err)
		}

		if clientMetadata.ClientID == parentClientMetadata.ClientID {
			// Check if repo is public
			repo := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{
				Name: "origin",
				URLs: []string{remote.Remote()},
			})

			_, err := repo.ListContext(ctx, &git.ListOptions{Auth: nil})
			switch {
			case err == nil:
				// no auth needed

			case errors.Is(err, transport.ErrAuthenticationRequired):
				// Only proceed with auth if repo requires authentication
				authCtx := engine.ContextWithClientMetadata(ctx, parentClientMetadata)

				bk, err := parent.Self.Buildkit(authCtx)
				if err != nil {
					return inst, fmt.Errorf("failed to get buildkit: %w", err)
				}

				// Retrieve credential from host
				credentials, err := bk.GetCredential(authCtx, remote.Scheme, remote.Host, remote.Path)
				switch {
				case err != nil || credentials == nil || credentials.Password == "":
					// it's possible to provide auth tokens via chained API calls, so warn now but
					// don't fail. Auth will be checked again before relevant operations later.
					slog.Warn("Failed to retrieve git credentials: %v", err)

				default:
					// Credentials found, create and set auth token
					hash := sha256.Sum256([]byte(credentials.Password))
					secretName := hex.EncodeToString(hash[:])
					var secretAuthToken dagql.Instance[*core.Secret]
					if err := s.srv.Select(authCtx, s.srv.Root(), &secretAuthToken,
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
					authToken = secretAuthToken
				}

			default:
				// it's possible to provide auth tokens via chained API calls, so warn now but
				// don't fail. Auth will be checked again before relevant operations later.
				slog.Warn("failed to list remote refs: %v", err)
			}
		}
	}

	// 4. Handle git directory configuration
	discardGitDir := false
	if args.KeepGitDir.Valid {
		slog.Warn("The 'keepGitDir' argument is deprecated. Use `tree`'s `discardGitDir' instead.")
		discardGitDir = !args.KeepGitDir.Value.Bool()
	}

	inst, err = dagql.NewInstanceForCurrentID(ctx, s.srv, parent, &core.GitRepository{
		Query: parent.Self,
		Backend: &core.RemoteGitRepository{
			Query:         parent.Self,
			URL:           remote,
			SSHKnownHosts: args.SSHKnownHosts,
			SSHAuthSocket: authSock.Self,
			Services:      svcs,
			Platform:      parent.Self.Platform(),
		},
		DiscardGitDir: discardGitDir,
	})
	if err != nil {
		return inst, fmt.Errorf("failed to create GitRepository instance: %w", err)
	}

	// set the auth token by selecting withAuthToken so that it shows up in the dagql call
	// as a secret and can thus be passed to functions
	if authToken.Self != nil {
		var instWithToken dagql.Instance[*core.GitRepository]
		err := s.srv.Select(ctx, inst, &instWithToken,
			dagql.Selector{
				Field: "withAuthToken",
				Args: []dagql.NamedInput{
					{
						Name:  "token",
						Value: dagql.NewID[*core.Secret](authToken.ID()),
					},
				},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to set auth token: %w", err)
		}
		inst = instWithToken
	}

	var resourceIDs []*resource.ID
	if authToken.Self != nil {
		resourceIDs = append(resourceIDs, &resource.ID{ID: *authToken.ID()})
	}
	if authSock.Self != nil {
		resourceIDs = append(resourceIDs, &resource.ID{ID: *authSock.ID()})
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

type gitArgsLegacy struct {
	URL                     string
	KeepGitDir              bool `default:"false"`
	ExperimentalServiceHost dagql.Optional[core.ServiceID]

	SSHKnownHosts string                        `name:"sshKnownHosts" default:""`
	SSHAuthSocket dagql.Optional[core.SocketID] `name:"sshAuthSocket"`
}

func (s *gitSchema) gitLegacy(ctx context.Context, parent dagql.Instance[*core.Query], args gitArgsLegacy) (dagql.Instance[*core.GitRepository], error) {
	return s.git(ctx, parent, gitArgs{
		URL:                     args.URL,
		KeepGitDir:              dagql.Opt(dagql.NewBoolean(args.KeepGitDir)),
		ExperimentalServiceHost: args.ExperimentalServiceHost,
		SSHKnownHosts:           args.SSHKnownHosts,
		SSHAuthSocket:           args.SSHAuthSocket,
	})
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
		// include the fully resolved ref + commit, since the remote repo *might* change
		inst = inst.WithDigest(dagql.HashFrom(
			dagql.CurrentID(ctx).Digest().String(),
			ref.FullRef,
			ref.Commit,
		))
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
	if !core.DagOpInContext[core.RawDagOp](ctx) {
		return DagOp(ctx, s.srv, parent, args, s.tags)
	}

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
}

func (s *gitSchema) tree(ctx context.Context, parent dagql.Instance[*core.GitRef], args treeArgs) (inst dagql.Instance[*core.Directory], _ error) {
	if core.DagOpInContext[core.FSDagOp](ctx) {
		dir, err := parent.Self.Tree(ctx, s.srv, args.DiscardGitDir, args.Depth)
		if err != nil {
			return inst, err
		}
		return dagql.NewInstanceForCurrentID(ctx, s.srv, parent, dir)
	}

	inst, err := DagOpDirectory(ctx, s.srv, parent, args, s.tree, nil)
	if err != nil {
		return inst, err
	}

	query, ok := s.srv.Root().(dagql.Instance[*core.Query])
	if !ok {
		return inst, fmt.Errorf("failed to get root query")
	}
	bk, err := query.Self.Buildkit(ctx)
	if err != nil {
		return inst, err
	}

	// use the commit + fully-qualified ref for the base of the content hash
	var dgstInputs []string
	var commit string
	err = s.srv.Select(ctx, parent, &commit, dagql.Selector{Field: "commit"})
	if err != nil {
		return inst, err
	}
	dgstInputs = append(dgstInputs, commit)
	var fullref string
	err = s.srv.Select(ctx, parent, &fullref, dagql.Selector{Field: "ref"})
	if err != nil {
		return inst, err
	}
	dgstInputs = append(dgstInputs, fullref)

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
			dgstInputs = append(dgstInputs, dgst.String(),
				// also include what auth methods are used, currently we can't
				// handle a cache hit where the result has a different auth
				// method than the caller used (i.e. a git repo is pulled w/
				// a token but hits cache for a dir where a ssh sock was used)
				strconv.FormatBool(remoteRepo.AuthToken.Self != nil),
				strconv.FormatBool(remoteRepo.AuthHeader.Self != nil),
				strconv.FormatBool(remoteRepo.SSHAuthSocket != nil),
			)
		}
	}

	dgstInputs = append(dgstInputs, strconv.Itoa(args.Depth))

	includedGitDir := !parent.Self.Repo.DiscardGitDir && !args.DiscardGitDir
	dgstInputs = append(dgstInputs, strconv.FormatBool(includedGitDir))
	if includedGitDir && remoteRepo != nil {
		// the contents of the directory have references to relevant remote git
		// state, so we include the remote URL in the hash
		dgstInputs = append(dgstInputs, remoteRepo.URL.Remote())
	}

	inst = inst.WithDigest(dagql.HashFrom(dgstInputs...))
	return inst, nil
}

type treeArgsLegacy struct {
	DiscardGitDir bool `default:"false"`
	Depth         int  `default:"1"`

	SSHKnownHosts dagql.Optional[dagql.String]  `name:"sshKnownHosts"`
	SSHAuthSocket dagql.Optional[core.SocketID] `name:"sshAuthSocket"`
}

func (s *gitSchema) treeLegacy(ctx context.Context, parent dagql.Instance[*core.GitRef], args treeArgsLegacy) (inst dagql.Instance[*core.Directory], _ error) {
	if !core.DagOpInContext[core.FSDagOp](ctx) {
		return DagOpDirectory(ctx, s.srv, parent, args, s.treeLegacy, nil)
	}

	query, ok := s.srv.Root().(dagql.Instance[*core.Query])
	if !ok {
		return inst, fmt.Errorf("failed to get root query")
	}
	bk, err := query.Self.Buildkit(ctx)
	if err != nil {
		return inst, err
	}

	var authSock *core.Socket
	if args.SSHAuthSocket.Valid {
		sock, err := args.SSHAuthSocket.Value.Load(ctx, s.srv)
		if err != nil {
			return inst, err
		}
		authSock = sock.Self
	}
	res := parent
	if args.SSHKnownHosts.Valid || args.SSHAuthSocket.Valid {
		repo := *res.Self.Repo
		if remote, ok := repo.Backend.(*core.RemoteGitRepository); ok {
			remote := *remote
			remote.SSHKnownHosts = args.SSHKnownHosts.GetOr("").String()
			remote.SSHAuthSocket = authSock
			repo.Backend = &remote
		}
		res.Self.Repo = &repo
	}
	dir, err := res.Self.Tree(ctx, s.srv, args.DiscardGitDir, args.Depth)
	if err != nil {
		return inst, err
	}
	inst, err = dagql.NewInstanceForCurrentID(ctx, s.srv, parent, dir)
	if err != nil {
		return inst, err
	}

	var dgstInputs []string

	// use the commit for the base of the content hash
	var commit string
	err = s.srv.Select(ctx, parent, &commit, dagql.Selector{Field: "commit"})
	if err != nil {
		return inst, err
	}
	dgstInputs = append(dgstInputs, commit)

	usedAuth := false
	remoteURL := ""
	if remoteRepo, ok := parent.Self.Repo.Backend.(*core.RemoteGitRepository); ok {
		remoteURL = remoteRepo.URL.Remote()
		usedAuth = remoteRepo.AuthToken.Self != nil ||
			remoteRepo.AuthHeader.Self != nil ||
			remoteRepo.SSHAuthSocket != nil
	}
	if usedAuth {
		// do a full hash of the actual files/dirs in the private git repo so
		// that the cache key of the returned value can't be known unless the
		// full contents are already known
		dgst, err := core.GetContentHashFromDirectory(ctx, bk, inst)
		if err != nil {
			return inst, fmt.Errorf("failed to get content hash: %w", err)
		}
		dgstInputs = append(dgstInputs, dgst.String())
	}

	includedGitDir := !parent.Self.Repo.DiscardGitDir && !args.DiscardGitDir
	if includedGitDir && remoteURL != "" {
		// the contents of the directory have references to relevant remote git
		// state, so we include the remote URL in the hash
		dgstInputs = append(dgstInputs, remoteURL)
	}

	inst = inst.WithDigest(dagql.HashFrom(dgstInputs...))

	return inst, nil
}

func (s *gitSchema) fetchCommit(ctx context.Context, parent dagql.Instance[*core.GitRef], args struct{}) (dagql.String, error) {
	if !core.DagOpInContext[core.RawDagOp](ctx) {
		return DagOp(ctx, s.srv, parent, args, s.fetchCommit)
	}

	commit, _, err := parent.Self.Resolve(ctx)
	if err != nil {
		return "", err
	}
	return dagql.NewString(commit), nil
}

func (s *gitSchema) fetchRef(ctx context.Context, parent dagql.Instance[*core.GitRef], args struct{}) (dagql.String, error) {
	if !core.DagOpInContext[core.RawDagOp](ctx) {
		return DagOp(ctx, s.srv, parent, args, s.fetchCommit)
	}

	_, ref, err := parent.Self.Resolve(ctx)
	if err != nil {
		return "", err
	}
	return dagql.NewString(ref), nil
}
