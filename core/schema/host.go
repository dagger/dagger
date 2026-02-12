package schema

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/dagger/dagger/internal/buildkit/util/contentutil"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	"github.com/distribution/reference"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/ctrns"
	"github.com/dagger/dagger/util/hashutil"
)

type hostSchema struct{}

var _ SchemaResolvers = &hostSchema{}

func (s *hostSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("host", func(ctx context.Context, parent *core.Query, args struct{}) (*core.Host, error) {
			return parent.NewHost(), nil
		}).Doc(`Queries the host environment.`),

		dagql.NodeFunc("_builtinContainer", s.builtinContainer).Doc("Retrieves a container builtin to the engine."),
	}.Install(srv)

	dagql.Fields[*core.Host]{
		dagql.NodeFuncWithCacheKey("directory",
			DagOpDirectoryWrapper(
				srv, s.directory,
				WithHashContentDir[*core.Host, hostDirectoryArgs](),
			), dagql.CacheAsRequested).
			Doc(`Accesses a directory on the host.`).
			Args(
				dagql.Arg("path").Doc(`Location of the directory to access (e.g., ".").`),
				dagql.Arg("exclude").Doc(`Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`),
				dagql.Arg("include").Doc(`Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),
				dagql.Arg("noCache").Doc(`If true, the directory will always be reloaded from the host.`),
				dagql.Arg("gitignore").Doc(`Apply .gitignore filter rules inside the directory`),
			),

		dagql.NodeFuncWithCacheKey("file", s.file, dagql.CacheAsRequested).
			Doc(`Accesses a file on the host.`).
			Args(
				dagql.Arg("path").Doc(`Location of the file to retrieve (e.g., "README.md").`),
				dagql.Arg("noCache").Doc(`If true, the file will always be reloaded from the host.`),
			),

		dagql.NodeFuncWithCacheKey("findUp", s.findUp, dagql.CacheAsRequested).
			Doc(`Search for a file or directory by walking up the tree from system workdir. Return its relative path. If no match, return null`).
			Args(
				dagql.Arg("name").Doc(`name of the file or directory to search for`),
			),

		dagql.NodeFuncWithCacheKey("unixSocket", s.socket, dagql.CachePerClient).
			Doc(`Accesses a Unix socket on the host.`).
			Args(
				dagql.Arg("path").Doc(`Location of the Unix socket (e.g., "/var/run/docker.sock").`),
			),

		dagql.NodeFuncWithCacheKey("_sshAuthSocket", s.sshAuthSocket, dagql.CachePerCall).
			Doc(`Accesses the SSH auth socket on the host and returns a socket scoped to SSH identities.`).
			Args(
				dagql.Arg("source").Doc(`Optional source socket to scope. If not set, uses the caller's SSH_AUTH_SOCK.`),
			),

		dagql.Func("__internalSocket", s.internalSocket).
			Doc(`(Internal-only) Accesses a socket on the host (unix or ip) with the given internal client resource name.`),

		dagql.FuncWithCacheKey("tunnel", s.tunnel, dagql.CachePerClient).
			Doc(`Creates a tunnel that forwards traffic from the host to a service.`).
			Args(
				dagql.Arg("service").Doc(`Service to send traffic from the tunnel.`),
				dagql.Arg("native").Doc(
					`Map each service port to the same port on the host, as if the service were running natively.`,
					`Note: enabling may result in port conflicts.`),
				dagql.Arg("ports").Doc(
					`Configure explicit port forwarding rules for the tunnel.`,
					`If a port's frontend is unspecified or 0, a random port will be chosen
					by the host.`,
					`If no ports are given, all of the service's ports are forwarded. If
					native is true, each port maps to the same port on the host. If native
					is false, each port maps to a random port chosen by the host.`,
					`If ports are given and native is true, the ports are additive.`),
			),

		dagql.NodeFuncWithCacheKey("service", s.service, dagql.CachePerClient).
			Doc(`Creates a service that forwards traffic to a specified address via the host.`).
			Args(
				dagql.Arg("ports").Doc(
					`Ports to expose via the service, forwarding through the host network.`,
					`If a port's frontend is unspecified or 0, it defaults to the same as
				the backend port.`,
					`An empty set of ports is not valid; an error will be returned.`),
				dagql.Arg("host").Doc(`Upstream host to forward traffic to.`),
			),

		dagql.NodeFuncWithCacheKey("containerImage", s.containerImage, dagql.CachePerClient).
			Doc(`Accesses a container image on the host.`).
			Args(
				dagql.Arg("name").Doc(`Name of the image to access.`),
			),

		// hidden from external clients via the __ prefix
		dagql.Func("__internalService", s.internalService).
			Doc(`(Internal-only) "service" but scoped to the exact right buildkit session ID.`),
	}.Install(srv)
}

type builtinContainerArgs struct {
	Digest string `doc:"Digest of the image manifest"`

	ContainerDagOpInternalArgs
}

func (s *hostSchema) builtinContainer(ctx context.Context, parent dagql.ObjectResult[*core.Query], args builtinContainerArgs) (inst dagql.ObjectResult[*core.Container], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	if !args.InDagOp() {
		dummyCtr := core.NewContainer(parent.Self().Platform())
		ctr, effectID, err := DagOpContainer(ctx, srv, dummyCtr, args, nil)
		if err != nil {
			return inst, err
		}

		err = core.BuiltInContainerUpdateConfig(ctx, ctr, args.Digest)
		if err != nil {
			return inst, err
		}
		if ctr.FS.Self().Dir == "" {
			// Note that this is set inside the dagop; however it doesn't correctly get propagated back, so we need to reset it here
			// containerSchema.from has the same issue and work-around
			ctr.FS.Self().Dir = "/"
		}

		resultID := dagql.CurrentID(ctx)
		if effectID != "" && resultID != nil {
			resultID = resultID.AppendEffectIDs(effectID)
		}
		inst, err = dagql.NewObjectResultForID(ctr, srv, resultID)
		if err != nil {
			return inst, err
		}
		return inst, nil
	}

	ctr, err := core.BuiltInContainer(ctx, parent.Self().Platform(), args.Digest)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentID(ctx, srv, ctr)
}

type hostDirectoryArgs struct {
	Path string

	core.CopyFilter
	HostDirCacheConfig

	GitIgnoreRoot string `internal:"true" default:""`
	Gitignore     bool   `default:"false"`
	DagOpInternalArgs
}

func (s *hostSchema) directory(ctx context.Context, host dagql.ObjectResult[*core.Host], args hostDirectoryArgs) (inst dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current query: %w", err)
	}

	bk, err := query.Buildkit(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	copyPath := path.Clean(args.Path)
	initialAbsCopyPath, err := bk.AbsPath(ctx, copyPath)
	if err != nil {
		return inst, fmt.Errorf("failed to get host absolute path for %s: %w", copyPath, err)
	}

	// Relpath is the actual path the user wants to copy, relative to the absRootCopyPath.
	// If `args.GitIgnore` is set, absRootCopyPath changes to the .git directory location
	// so we can load .gitignore patterns from there.
	// Then we copy everything from relPathFromRoot which is the actual path the caller
	// wants to copy.
	absRootCopyPath := initialAbsCopyPath
	relPathFromRoot := "."

	if args.Gitignore {
		// If `args.GitIgnoreRoot` is set, we use it as the root to load .gitignores
		// patterns from.
		// Otherwise, we search for a .git directory to be the new root.
		if args.GitIgnoreRoot != "" {
			gitRootPath := args.GitIgnoreRoot
			absRootCopyPath, err = bk.AbsPath(ctx, gitRootPath)
			if err != nil {
				return inst, fmt.Errorf("failed to get absolute path from git ignore root %s: %w", gitRootPath, err)
			}
		} else {
			dotGitPath, found, err := host.Self().FindUp(ctx, core.NewCallerStatFS(bk), initialAbsCopyPath, ".git")
			if err != nil {
				return inst, fmt.Errorf("failed to find up .git: %w", err)
			}
			if found {
				absRootCopyPath = dotGitPath
			}
		}

		// Compute the relative path to know what the caller actually wants to copy.
		relPathFromRoot, err = filepath.Rel(absRootCopyPath, initialAbsCopyPath)
		if err != nil {
			return inst, fmt.Errorf("failed to get relative path from %q: %w", initialAbsCopyPath, err)
		}
	}

	// If relPathFromRoot is different from the rootCopyPath, we include everything
	// inside the relPathFromRoot directory by default.
	includePatterns := make([]string, 0, 1+len(args.Include))
	if relPathFromRoot != "." {
		includePatterns = append(includePatterns, "!*", relPathFromRoot)
	}
	for _, include := range args.Include {
		include, negative := strings.CutPrefix(include, "!")
		if !filepath.IsLocal(include) {
			continue
		}
		include = filepath.Join(relPathFromRoot, include)
		if include == "." {
			// we were told to include the directory itself, but include
			// filters seem to ignore ".", so we replace it with the intention
			// of "*"
			include = "*"
		}
		if negative {
			include = "!" + include
		}
		includePatterns = append(includePatterns, include)
	}

	excludePatterns := make([]string, 0, len(args.Exclude))
	for _, exclude := range args.Exclude {
		exclude, negative := strings.CutPrefix(exclude, "!")
		if !filepath.IsLocal(exclude) {
			continue
		}
		exclude = filepath.Join(relPathFromRoot, exclude)
		if exclude == "." {
			// we were told to exclude the directory itself, but exclude
			// filters seem to ignore ".", so we replace it with the intention
			// of "*"
			exclude = "*"
		}
		if negative {
			exclude = "!" + exclude
		}
		excludePatterns = append(excludePatterns, exclude)
	}

	dir, err := host.Self().Directory(ctx, absRootCopyPath, core.CopyFilter{
		Include:   includePatterns,
		Exclude:   excludePatterns,
		Gitignore: args.Gitignore,
	}, args.NoCache, relPathFromRoot)

	if err != nil {
		return inst, fmt.Errorf("failed to get directory: %w", err)
	}

	return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
}

type hostSocketArgs struct {
	Path string
}

func (s *hostSchema) socket(ctx context.Context, host dagql.ObjectResult[*core.Host], args hostSocketArgs) (inst dagql.Result[*core.Socket], err error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}

	accessor, err := core.GetClientResourceAccessor(ctx, query, args.Path)
	if err != nil {
		return inst, fmt.Errorf("failed to get client resource name: %w", err)
	}
	dgst := hashutil.HashStrings(accessor)

	sock := &core.Socket{IDDigest: dgst}
	inst, err = dagql.NewResultForCurrentID(ctx, sock)
	if err != nil {
		return inst, fmt.Errorf("failed to create instance: %w", err)
	}
	inst = inst.WithContentDigest(dgst)

	upsertSocket := func(ctx context.Context) error {
		callerClientMetadata, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return fmt.Errorf("failed to get client metadata: %w", err)
		}
		callerSocketStore, err := query.Sockets(ctx)
		if err != nil {
			return fmt.Errorf("failed to get socket store: %w", err)
		}
		nonModuleParentClientMetadata, err := query.NonModuleParentClientMetadata(ctx)
		if err == nil && nonModuleParentClientMetadata.ClientID != callerClientMetadata.ClientID {
			// In nested module contexts, preserve any pre-imported socket mapping (e.g. an explicitly
			// passed socket argument) instead of clobbering it with the nested client's session.
			if callerSocketStore.HasSocket(sock.IDDigest) {
				return nil
			}
		}
		if err := callerSocketStore.AddUnixSocket(sock, callerClientMetadata.ClientID, args.Path); err != nil {
			return fmt.Errorf("failed to add unix socket to store: %w", err)
		}
		return nil
	}
	if err := upsertSocket(ctx); err != nil {
		return inst, err
	}

	return inst.ResultWithPostCall(upsertSocket), nil
}

type hostSSHAuthSocketArgs struct {
	Source dagql.Optional[core.SocketID] `name:"source"`
}

func (s *hostSchema) sshAuthSocket(ctx context.Context, host dagql.ObjectResult[*core.Host], args hostSSHAuthSocketArgs) (inst dagql.Result[*core.Socket], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	socketStore, err := query.Sockets(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get socket store: %w", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get client metadata: %w", err)
	}

	var sourceSocket *core.Socket
	if args.Source.Valid {
		sourceInst, err := args.Source.Value.Load(ctx, srv)
		if err != nil {
			return inst, fmt.Errorf("failed to load source socket: %w", err)
		}
		sourceSocket = sourceInst.Self()
		if sourceSocket == nil {
			return inst, errors.New("source socket is nil")
		}
		if !socketStore.HasSocket(sourceSocket.IDDigest) {
			return inst, fmt.Errorf("source socket %s not found in socket store", sourceSocket.IDDigest)
		}
	} else {
		if clientMetadata.SSHAuthSocketPath == "" {
			return inst, errors.New("SSH_AUTH_SOCK is not set")
		}
		accessor, err := core.GetClientResourceAccessor(ctx, query, clientMetadata.SSHAuthSocketPath)
		if err != nil {
			return inst, fmt.Errorf("failed to get client resource accessor: %w", err)
		}
		sourceSocket = &core.Socket{
			IDDigest: hashutil.HashStrings(accessor),
		}
		if err := socketStore.AddUnixSocket(sourceSocket, clientMetadata.ClientID, clientMetadata.SSHAuthSocketPath); err != nil {
			return inst, fmt.Errorf("failed to register source SSH auth socket: %w", err)
		}
	}

	scopedDigest, err := core.ScopedSSHAuthSocketDigestFromStore(ctx, query, socketStore, sourceSocket.IDDigest)
	if err != nil {
		return inst, fmt.Errorf("failed to scope SSH auth socket from agent identities: %w", err)
	}

	scopedSocket := &core.Socket{IDDigest: scopedDigest}
	inst, err = dagql.NewResultForCurrentID(ctx, scopedSocket)
	if err != nil {
		return inst, fmt.Errorf("failed to create instance: %w", err)
	}
	inst = inst.WithContentDigest(scopedDigest)

	if err := upsertScopedSSHAuthSocket(ctx, query, args.Source.Valid, scopedSocket, sourceSocket); err != nil {
		return inst, err
	}

	// This postcall may run for different callers that hit the same cached
	// Host._sshAuthSocket result. Resolve caller-specific SSH auth socket metadata
	// at postcall execution time to avoid leaking a previous caller's session/path
	// into a different caller's socket store.
	return inst.ResultWithPostCall(func(ctx context.Context) error {
		return upsertScopedSSHAuthSocket(ctx, query, args.Source.Valid, scopedSocket, sourceSocket)
	}), nil
}

// upsertScopedSSHAuthSocket persists the scoped socket mapping in the caller store.
// If a source socket was provided, it aliases scoped -> source. Otherwise it registers
// the scoped socket as a unix socket using the caller SSH auth socket session/path.
func upsertScopedSSHAuthSocket(
	ctx context.Context,
	query *core.Query,
	hasSource bool,
	scopedSocket *core.Socket,
	sourceSocket *core.Socket,
) error {
	callerSocketStore, err := query.Sockets(ctx)
	if err != nil {
		return fmt.Errorf("failed to get caller socket store: %w", err)
	}

	if hasSource {
		if err := callerSocketStore.AddSocketAlias(scopedSocket, sourceSocket.IDDigest); err != nil {
			return fmt.Errorf("failed to alias scoped SSH auth socket: %w", err)
		}
		return nil
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get client metadata: %w", err)
	}
	if clientMetadata.SSHAuthSocketPath == "" {
		// Cached post-call replay can happen in contexts that don't expose
		// SSH_AUTH_SOCK (for example when loading IDs to transfer client
		// resources). Treat this as a no-op so replay doesn't fail; direct
		// Host._sshAuthSocket calls still fail earlier in the resolver.
		return nil
	}

	nonModuleParentClientMetadata, err := query.NonModuleParentClientMetadata(ctx)
	if err == nil && nonModuleParentClientMetadata.ClientID != clientMetadata.ClientID {
		// In nested module contexts, keep an existing scoped socket mapping from the
		// parent caller (for example an explicitly passed socket arg) instead of
		// clobbering it with this nested client's own session/path.
		if callerSocketStore.HasSocket(scopedSocket.IDDigest) {
			return nil
		}
	}

	if err := callerSocketStore.AddUnixSocket(scopedSocket, clientMetadata.ClientID, clientMetadata.SSHAuthSocketPath); err != nil {
		return fmt.Errorf("failed to register scoped SSH auth socket: %w", err)
	}
	return nil
}

type hostFileArgs struct {
	Path string
	HostDirCacheConfig
}

type HostDirCacheConfig struct {
	NoCache bool `default:"false"`
}

func (cc HostDirCacheConfig) CacheType() dagql.CacheControlType {
	if cc.NoCache {
		return dagql.CacheTypePerCall
	}
	return dagql.CacheTypePerClient
}

func (s *hostSchema) file(ctx context.Context, host dagql.ObjectResult[*core.Host], args hostFileArgs) (i dagql.Result[*core.File], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	fileDir, fileName := filepath.Split(args.Path)

	if err := srv.Select(ctx, srv.Root(), &i, dagql.Selector{
		Field: "host",
	}, dagql.Selector{
		Field: "directory",
		Args: []dagql.NamedInput{
			{
				Name:  "path",
				Value: dagql.NewString(fileDir),
			},
			{
				Name:  "include",
				Value: dagql.ArrayInput[dagql.String]{dagql.NewString(fileName)},
			},
			{
				Name:  "noCache",
				Value: dagql.NewBoolean(args.NoCache),
			},
		},
	}, dagql.Selector{
		Field: "file",
		Args: []dagql.NamedInput{
			{
				Name:  "path",
				Value: dagql.NewString(fileName),
			},
		},
	}); err != nil {
		return i, err
	}
	return i, nil
}

type hostFindUpArgs struct {
	Name string
	HostDirCacheConfig
}

func (s *hostSchema) findUp(ctx context.Context, host dagql.ObjectResult[*core.Host], args hostFindUpArgs) (i dagql.Nullable[dagql.String], err error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return i, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get buildkit client: %w", err)
	}
	cwd, err := bk.AbsPath(ctx, ".")
	if err != nil {
		return i, fmt.Errorf("failed to get cwd: %w", err)
	}
	foundPath, found, err := host.Self().FindUp(ctx, core.NewCallerStatFS(bk), cwd, args.Name)
	if err != nil {
		return i, fmt.Errorf("failed to find %s: %w", args.Name, err)
	}
	if !found {
		return dagql.Null[dagql.String](), nil
	}
	foundPath = path.Join(foundPath, args.Name)
	relPath, err := filepath.Rel(cwd, foundPath)
	if err != nil {
		return i, fmt.Errorf("failed to make path relative to cwd: %w", err)
	}
	return dagql.NonNull(dagql.NewString(relPath)), nil
}

type hostTunnelArgs struct {
	Service core.ServiceID
	Ports   []dagql.InputObject[core.PortForward] `default:"[]"`
	Native  bool                                  `default:"false"`
}

func (s *hostSchema) tunnel(ctx context.Context, parent *core.Host, args hostTunnelArgs) (*core.Service, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	inst, err := args.Service.Load(ctx, srv)
	if err != nil {
		return nil, err
	}

	svc := inst.Self()

	if svc.Container == nil {
		return nil, errors.New("tunneling to non-Container services is not supported")
	}

	ports := []core.PortForward{}

	if args.Native {
		for _, port := range svc.Container.Ports {
			frontend := port.Port
			ports = append(ports, core.PortForward{
				Frontend: &frontend,
				Backend:  port.Port,
				Protocol: port.Protocol,
			})
		}
	}

	if len(args.Ports) > 0 {
		ports = append(ports, collectInputsSlice(args.Ports)...)
	}

	if len(ports) == 0 {
		for _, port := range svc.Container.Ports {
			ports = append(ports, core.PortForward{
				Frontend: nil, // pick a random port on the host
				Backend:  port.Port,
				Protocol: port.Protocol,
			})
		}
	}

	if len(ports) == 0 {
		return nil, errors.New("no ports to forward")
	}

	return &core.Service{
		Creator:        trace.SpanContextFromContext(ctx),
		TunnelUpstream: inst,
		TunnelPorts:    ports,
	}, nil
}

type hostContainerArgs struct {
	Name string
}

func (s *hostSchema) containerImage(ctx context.Context, parent dagql.ObjectResult[*core.Host], args hostContainerArgs) (inst dagql.Result[*core.Container], err error) {
	refName, err := reference.ParseNormalizedNamed(args.Name)
	if err != nil {
		return inst, fmt.Errorf("failed to parse image address %s: %w", args.Name, err)
	}
	refName = reference.TagNameOnly(refName)

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	imageReader, err := bk.ReadImage(ctx, refName.String())
	if err != nil {
		return inst, err
	}

	if imageReader.ContentStore != nil && imageReader.ImagesStore != nil {
		// create and use a lease to write to our content store, prevents
		// content being cleaned up while we're writing
		leaseCtx, leaseDone, err := leaseutil.WithLease(ctx, imageReader.LeaseManager, leaseutil.MakeTemporary)
		if err != nil {
			return inst, err
		}
		defer leaseDone(context.WithoutCancel(leaseCtx))
		leaseID, _ := leases.FromContext(leaseCtx)

		contentStore := ctrns.ContentStoreWithLease(imageReader.ContentStore, leaseID)

		img, err := imageReader.ImagesStore.Get(ctx, refName.String())
		if err != nil {
			return inst, fmt.Errorf("failed to get image from host store: %w", err)
		}
		target, err := core.ResolveIndex(ctx, contentStore, img.Target, query.Platform().Spec(), "")
		if err != nil {
			return inst, fmt.Errorf("failed to resolve image index: %w", err)
		}

		ctx, release, err := leaseutil.WithLease(ctx, query.LeaseManager(), leaseutil.MakeTemporary)
		if err != nil {
			return inst, err
		}
		defer release(context.WithoutCancel(ctx))
		err = contentutil.CopyChain(ctx, query.OCIStore(), contentStore, *target)
		if err != nil {
			return inst, fmt.Errorf("failed to copy image content: %w", err)
		}

		ctr := core.NewContainer(query.Platform())
		ctr, err = ctr.FromInternal(ctx, *target)
		if err != nil {
			return inst, err
		}

		return dagql.NewResultForCurrentID(ctx, ctr)
	}

	if src := imageReader.Tarball; src != nil {
		defer src.Close()

		ctr := core.NewContainer(query.Platform())
		ctr, err := ctr.Import(ctx, src, "")
		if err != nil {
			return inst, err
		}

		return dagql.NewResultForCurrentID(ctx, ctr)
	}

	return inst, errors.New("invalid save config")
}

type hostServiceArgs struct {
	Host  string `default:"localhost"`
	Ports []dagql.InputObject[core.PortForward]
}

func (s *hostSchema) service(ctx context.Context, parent dagql.ObjectResult[*core.Host], args hostServiceArgs) (inst dagql.Result[*core.Service], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	if len(args.Ports) == 0 {
		return inst, errors.New("no ports specified")
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	socketStore, err := query.Sockets(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get socket store: %w", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get client metadata: %w", err)
	}

	ports := collectInputsSlice(args.Ports)
	sockIDs := make([]dagql.ID[*core.Socket], 0, len(ports))
	for _, port := range ports {
		accessor, err := core.GetHostIPSocketAccessor(ctx, query, args.Host, port)
		if err != nil {
			return inst, fmt.Errorf("failed to get host ip socket accessor: %w", err)
		}

		var sockInst dagql.Result[*core.Socket]
		err = srv.Select(ctx, srv.Root(), &sockInst,
			dagql.Selector{
				Field: "host",
			},
			dagql.Selector{
				Field: "__internalSocket",
				Args: []dagql.NamedInput{
					{
						Name:  "accessor",
						Value: dagql.NewString(accessor),
					},
				},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to select internal socket: %w", err)
		}

		if err := socketStore.AddIPSocket(sockInst.Self(), clientMetadata.ClientID, args.Host, port); err != nil {
			return inst, fmt.Errorf("failed to add ip socket to store: %w", err)
		}

		sockIDs = append(sockIDs, dagql.NewID[*core.Socket](sockInst.ID()))
	}

	err = srv.Select(ctx, srv.Root(), &inst,
		dagql.Selector{
			Field: "host",
		},
		dagql.Selector{
			Field: "__internalService",
			Args: []dagql.NamedInput{
				{
					Name:  "socks",
					Value: dagql.ArrayInput[dagql.ID[*core.Socket]](sockIDs),
				},
			},
		},
	)
	return inst, err
}

type hostInternalServiceArgs struct {
	Socks dagql.ArrayInput[dagql.ID[*core.Socket]]
}

func (s *hostSchema) internalService(ctx context.Context, parent *core.Host, args hostInternalServiceArgs) (*core.Service, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	if len(args.Socks) == 0 {
		return nil, errors.New("no host sockets specified")
	}

	socks := make([]*core.Socket, 0, len(args.Socks))
	for _, sockID := range args.Socks {
		sockInst, err := sockID.Load(ctx, srv)
		if err != nil {
			return nil, fmt.Errorf("failed to load socket: %w", err)
		}
		socks = append(socks, sockInst.Self())
	}

	return &core.Service{
		Creator:     trace.SpanContextFromContext(ctx),
		HostSockets: socks,
	}, nil
}

type hostInternalSocketArgs struct {
	// Accessor is the scoped per-module name, which should guarantee uniqueness.
	// It is used to ensure the dagql ID digest is unique per module; the digest is what's
	// used as the actual key for the socket store.
	Accessor string
}

func (s *hostSchema) internalSocket(ctx context.Context, host *core.Host, args hostInternalSocketArgs) (inst dagql.Result[*core.Socket], err error) {
	if args.Accessor == "" {
		return inst, errors.New("socket accessor must be provided")
	}
	dgst := hashutil.HashStrings(args.Accessor)
	sock := &core.Socket{IDDigest: dgst}
	inst, err = dagql.NewResultForCurrentID(ctx, sock)
	if err != nil {
		return inst, fmt.Errorf("failed to create instance: %w", err)
	}
	return inst.WithContentDigest(dgst), nil
}
