package schema

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/platforms"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/buildkit/util/contentutil"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	"github.com/distribution/reference"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/filesync"
)

type hostSchema struct{}

var _ SchemaResolvers = &hostSchema{}

func (s *hostSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("host", func(ctx context.Context, parent *core.Query, args struct{}) (*core.Host, error) {
			return parent.NewHost(), nil
		}).Doc(`Queries the host environment.`),

		dagql.NodeFunc("_builtinContainer", s.builtinContainer).
			IsPersistable().
			Doc("Retrieves a container builtin to the engine."),
	}.Install(srv)

	dagql.Fields[*core.Host]{
		dagql.NodeFunc("directory", s.directory).
			WithInput(dagql.RequestedCacheInput("noCache")).
			Doc(`Accesses a directory on the host.`).
			Args(
				dagql.Arg("path").Doc(`Location of the directory to access (e.g., ".").`),
				dagql.Arg("exclude").Doc(`Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`),
				dagql.Arg("include").Doc(`Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),
				dagql.Arg("noCache").Doc(`If true, the directory will always be reloaded from the host.`),
				dagql.Arg("gitignore").Doc(`Apply .gitignore filter rules inside the directory`),
			),

		dagql.NodeFunc("file", s.file).
			WithInput(dagql.RequestedCacheInput("noCache")).
			Doc(`Accesses a file on the host.`).
			Args(
				dagql.Arg("path").Doc(`Location of the file to retrieve (e.g., "README.md").`),
				dagql.Arg("noCache").Doc(`If true, the file will always be reloaded from the host.`),
			),

		dagql.NodeFunc("findUp", s.findUp).
			WithInput(dagql.RequestedCacheInput("noCache")).
			Doc(`Search for a file or directory by walking up the tree from system workdir. Return its relative path. If no match, return null`).
			Args(
				dagql.Arg("name").Doc(`name of the file or directory to search for`),
			),

		dagql.NodeFunc("unixSocket", s.socket).
			WithInput(dagql.PerClientInput).
			Doc(`Accesses a Unix socket on the host.`).
			Args(
				dagql.Arg("path").Doc(`Location of the Unix socket (e.g., "/var/run/docker.sock").`),
			),

		dagql.NodeFunc("_sshAuthSocket", s.sshAuthSocket).
			WithInput(dagql.PerCallInput).
			Doc(`Accesses the SSH auth socket on the host and returns a socket scoped to SSH identities.`).
			Args(
				dagql.Arg("source").Doc(`Optional source socket to scope. If not set, uses the caller's SSH_AUTH_SOCK.`),
			),

		dagql.Func("tunnel", s.tunnel).
			WithInput(dagql.PerClientInput).
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

		dagql.NodeFunc("service", s.service).
			WithInput(dagql.PerSessionInput).     // host services shouldn't cross sessions
			WithInput(core.CachePerCallerModule). // services should be shared from different function calls in a module
			Doc(`Creates a service that forwards traffic to a specified address via the host.`).
			Args(
				dagql.Arg("ports").Doc(
					`Ports to expose via the service, forwarding through the host network.`,
					`If a port's frontend is unspecified or 0, it defaults to the same as
				the backend port.`,
					`An empty set of ports is not valid; an error will be returned.`),
				dagql.Arg("host").Doc(`Upstream host to forward traffic to.`),
			),

		dagql.NodeFunc("containerImage", s.containerImage).
			WithInput(dagql.PerClientInput).
			Doc(`Accesses a container image on the host.`).
			Args(
				dagql.Arg("name").Doc(`Name of the image to access.`),
			),
	}.Install(srv)
}

type builtinContainerArgs struct {
	Digest string `doc:"Digest of the image manifest"`
}

func (s *hostSchema) builtinContainer(ctx context.Context, parent dagql.ObjectResult[*core.Query], args builtinContainerArgs) (inst dagql.ObjectResult[*core.Container], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	ctr, err := core.BuiltInContainer(ctx, parent.Self().Platform(), args.Digest)
	if err != nil {
		return inst, err
	}

	return dagql.NewObjectResultForCurrentCall(ctx, srv, ctr)
}

type hostDirectoryArgs struct {
	Path string

	core.CopyFilter
	HostDirCacheConfig

	GitIgnoreRoot string   `internal:"true" default:""`
	FollowPaths   []string `internal:"true" default:"[]"`
	Gitignore     bool     `default:"false"`
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

	followPaths := make([]string, 0, len(args.FollowPaths))
	for _, followPath := range args.FollowPaths {
		if !filepath.IsLocal(followPath) {
			continue
		}
		followPaths = append(followPaths, filepath.Join(relPathFromRoot, followPath))
	}

	snapshotOpts := filesync.SnapshotOpts{
		IncludePatterns: includePatterns,
		ExcludePatterns: excludePatterns,
		FollowPaths:     followPaths,
		GitIgnore:       args.Gitignore,
		RelativePath:    relPathFromRoot,
	}
	if args.NoCache {
		snapshotOpts.CacheBuster = rand.Text()
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get client metadata: %w", err)
	}
	callerConn, err := query.SpecificClientAttachableConn(ctx, clientMetadata.ClientID)
	if err != nil {
		return inst, fmt.Errorf("failed to get caller attachable conn: %w", err)
	}
	drive := pathutil.GetDrive(absRootCopyPath)

	var mirror *core.ClientFilesyncMirror
	if clientMetadata.ClientStableID != "" {
		var persistedMirror dagql.ObjectResult[*core.ClientFilesyncMirror]
		if err := srv.Select(ctx, srv.Root(), &persistedMirror, dagql.Selector{
			Field: "_clientFilesyncMirror",
			Args: []dagql.NamedInput{
				{Name: "stableClientID", Value: dagql.String(clientMetadata.ClientStableID)},
				{Name: "drive", Value: dagql.String(drive)},
			},
		}); err != nil {
			return inst, fmt.Errorf("failed to load client filesync mirror: %w", err)
		}
		mirror = persistedMirror.Self()
	} else {
		mirror = &core.ClientFilesyncMirror{
			Drive:       drive,
			EphemeralID: identity.NewID(),
		}
		if err := mirror.EnsureCreated(ctx, query); err != nil {
			return inst, fmt.Errorf("failed to create ephemeral client filesync mirror: %w", err)
		}
	}

	ref, contentDgst, err := mirror.Snapshot(ctx, query, callerConn, absRootCopyPath, snapshotOpts)
	if err != nil {
		return inst, fmt.Errorf("failed to get snapshot: %w", err)
	}
	dagql.TraceEGraphDebug(ctx, "host_directory_snapshot", "phase", "runtime", "path", args.Path, "abs_root_copy_path", absRootCopyPath, "relative_path_from_root", relPathFromRoot, "no_cache", args.NoCache, "cache_buster", snapshotOpts.CacheBuster != "", "content_digest", contentDgst, "snapshot_ref_id", ref.SnapshotID(), "include_patterns", includePatterns, "exclude_patterns", excludePatterns, "follow_paths", followPaths, "gitignore", args.Gitignore)

	dir, err := core.NewDirectoryWithSnapshot("/", query.Platform(), nil, ref)
	if err != nil {
		_ = ref.Release(context.WithoutCancel(ctx))
		return inst, fmt.Errorf("failed to create host directory: %w", err)
	}

	inst, err = dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
	if err != nil {
		_ = dir.OnRelease(context.WithoutCancel(ctx))
		return inst, fmt.Errorf("failed to create directory result: %w", err)
	}
	inst, err = inst.WithContentDigest(ctx, contentDgst)
	if err != nil {
		_ = dir.OnRelease(context.WithoutCancel(ctx))
		return inst, err
	}

	return inst, nil
}

type hostSocketArgs struct {
	Path string
}

func (s *hostSchema) socket(ctx context.Context, host dagql.ObjectResult[*core.Host], args hostSocketArgs) (inst dagql.Result[*core.Socket], err error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dagql cache: %w", err)
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get client metadata: %w", err)
	}

	concreteVal := &core.Socket{
		Kind:           core.SocketKindUnixOpaque,
		URLVal:         (&url.URL{Scheme: "unix", Path: args.Path}).String(),
		SourceClientID: clientMetadata.ClientID,
	}

	handle, err := core.HostUnixSocketHandle(ctx, query, args.Path)
	if err != nil {
		return inst, fmt.Errorf("failed to derive unix socket handle: %w", err)
	}
	if handle == "" {
		return inst, fmt.Errorf("failed to derive unix socket handle")
	}

	handleVal := &core.Socket{
		Kind:   core.SocketKindUnixOpaque,
		Handle: handle,
	}
	inst, err = dagql.NewResultForCurrentCall(ctx, handleVal)
	if err != nil {
		return inst, fmt.Errorf("failed to create unix socket handle result: %w", err)
	}
	inst, err = inst.WithContentDigest(ctx, digest.Digest(handle))
	if err != nil {
		return inst, err
	}
	inst, err = inst.WithSessionResourceHandle(ctx, handle)
	if err != nil {
		return inst, err
	}

	if err := cache.BindSessionResource(ctx, clientMetadata.SessionID, clientMetadata.ClientID, handle, concreteVal); err != nil {
		return inst, err
	}

	return inst, nil
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
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dagql cache: %w", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get client metadata: %w", err)
	}

	var concreteSelf *core.Socket
	if args.Source.Valid {
		concrete, err := args.Source.Value.Load(ctx, srv)
		if err != nil {
			return inst, fmt.Errorf("failed to load source socket: %w", err)
		}
		concreteSelf, _ = dagql.UnwrapAs[*core.Socket](concrete)
		if concreteSelf == nil {
			return inst, errors.New("source socket is nil")
		}
		concreteSelf, err = core.ResolveSessionSocket(ctx, concreteSelf)
		if err != nil {
			return inst, fmt.Errorf("failed to resolve source socket: %w", err)
		}
		if concreteSelf == nil {
			return inst, errors.New("resolved source socket is nil")
		}
	} else {
		if clientMetadata.SSHAuthSocketPath == "" {
			return inst, errors.New("SSH_AUTH_SOCK is not set")
		}
		concreteVal := &core.Socket{
			Kind:           core.SocketKindUnixOpaque,
			URLVal:         (&url.URL{Scheme: "unix", Path: clientMetadata.SSHAuthSocketPath}).String(),
			SourceClientID: clientMetadata.ClientID,
		}
		concreteSelf = concreteVal
	}

	fingerprints, err := concreteSelf.AgentFingerprints(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get SSH auth socket fingerprints: %w", err)
	}
	handle := core.ScopedSSHAuthSocketHandle(query.SecretSalt(), fingerprints)
	mainClientMetadata, err := query.MainClientCallerMetadata(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get main client metadata: %w", err)
	}
	if clientMetadata.ClientID != mainClientMetadata.ClientID {
		handle = core.ScopedNestedSSHAuthSocketHandle(query.SecretSalt(), fingerprints, clientMetadata.ClientID)
	}
	if handle == "" {
		return inst, fmt.Errorf("failed to derive SSH auth socket handle")
	}

	handleVal := &core.Socket{
		Kind:   core.SocketKindSSHHandle,
		Handle: handle,
	}
	inst, err = dagql.NewResultForCurrentCall(ctx, handleVal)
	if err != nil {
		return inst, fmt.Errorf("failed to create instance: %w", err)
	}
	inst, err = inst.WithContentDigest(ctx, digest.Digest(handle))
	if err != nil {
		return inst, err
	}
	inst, err = inst.WithSessionResourceHandle(ctx, handle)
	if err != nil {
		return inst, err
	}
	if err := cache.BindSessionResource(ctx, clientMetadata.SessionID, clientMetadata.ClientID, handle, concreteSelf); err != nil {
		return inst, err
	}

	return inst, nil
}

type hostFileArgs struct {
	Path string
	HostDirCacheConfig
}

type HostDirCacheConfig struct {
	NoCache bool `default:"false"`
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

	if svc.Container.Self() == nil {
		return nil, errors.New("tunneling to non-Container services is not supported")
	}

	ports := []core.PortForward{}

	if args.Native {
		for _, port := range svc.Container.Self().Ports {
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
		for _, port := range svc.Container.Self().Ports {
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

func resolveHostStoreManifest(
	ctx context.Context,
	store content.Store,
	desc ocispec.Descriptor,
	platform ocispec.Platform,
) (*ocispec.Descriptor, error) {
	matcher := platforms.Only(platforms.Normalize(platform))
	target, matched, err := resolveHostStoreManifestMatch(ctx, store, desc, matcher)
	if err != nil {
		return nil, err
	}
	if target != nil {
		return target, nil
	}
	if matched {
		return nil, fmt.Errorf("requested platform manifest %s is not present locally", platforms.Format(platform))
	}
	return nil, fmt.Errorf("no manifest for platform %s in host store", platforms.Format(platform))
}

func resolveHostStoreManifestMatch(
	ctx context.Context,
	store content.Store,
	desc ocispec.Descriptor,
	matcher platforms.MatchComparer,
) (*ocispec.Descriptor, bool, error) {
	switch desc.MediaType {
	case ocispec.MediaTypeImageManifest, images.MediaTypeDockerSchema2Manifest:
		if _, err := store.Info(ctx, desc.Digest); err != nil {
			return nil, true, nil
		}
		return &desc, true, nil

	case ocispec.MediaTypeImageIndex, images.MediaTypeDockerSchema2ManifestList:
		indexBlob, err := content.ReadBlob(ctx, store, desc)
		if err != nil {
			return nil, false, fmt.Errorf("read host image index blob: %w", err)
		}

		var idx ocispec.Index
		if err := json.Unmarshal(indexBlob, &idx); err != nil {
			return nil, false, fmt.Errorf("unmarshal host image index: %w", err)
		}

		matched := false
		for _, manifest := range idx.Manifests {
			switch manifest.MediaType {
			case ocispec.MediaTypeImageManifest, images.MediaTypeDockerSchema2Manifest,
				ocispec.MediaTypeImageIndex, images.MediaTypeDockerSchema2ManifestList:
			default:
				continue
			}
			if manifest.Platform != nil && !matcher.Match(*manifest.Platform) {
				continue
			}
			target, childMatched, err := resolveHostStoreManifestMatch(ctx, store, manifest, matcher)
			if err != nil {
				return nil, false, err
			}
			matched = matched || childMatched
			if target != nil {
				return target, true, nil
			}
		}
		return nil, matched, nil

	default:
		return nil, false, fmt.Errorf("unsupported host image media type %s", desc.MediaType)
	}
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
		img, err := imageReader.ImagesStore.Get(ctx, refName.String())
		if err != nil {
			return inst, fmt.Errorf("failed to get image from host store: %w", err)
		}
		target, err := resolveHostStoreManifest(ctx, imageReader.ContentStore, img.Target, query.Platform().Spec())
		if err != nil {
			return inst, fmt.Errorf("failed to resolve host image manifest: %w", err)
		}

		ctx, release, err := leaseutil.WithLease(ctx, query.LeaseManager(), leaseutil.MakeTemporary)
		if err != nil {
			return inst, err
		}
		defer release(context.WithoutCancel(ctx))
		err = contentutil.CopyChain(ctx, query.OCIStore(), imageReader.ContentStore, *target)
		if err != nil {
			return inst, fmt.Errorf("failed to copy image content: %w", err)
		}

		ctr := core.NewContainer(query.Platform())
		ctr, err = ctr.FromOCIStore(ctx, *target, refName.String())
		if err != nil {
			return inst, err
		}

		return dagql.NewResultForCurrentCall(ctx, ctr)
	}

	if src := imageReader.Tarball; src != nil {
		defer src.Close()

		ctr := core.NewContainer(query.Platform())
		ctr, err := ctr.Import(ctx, src, "")
		if err != nil {
			return inst, err
		}

		return dagql.NewResultForCurrentCall(ctx, ctr)
	}

	return inst, errors.New("invalid save config")
}

type hostServiceArgs struct {
	Host  string `default:"localhost"`
	Ports []dagql.InputObject[core.PortForward]
}

func (s *hostSchema) service(ctx context.Context, parent dagql.ObjectResult[*core.Host], args hostServiceArgs) (inst dagql.ObjectResult[*core.Service], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	if len(args.Ports) == 0 {
		return inst, errors.New("no ports specified")
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get client metadata: %w", err)
	}

	ports := collectInputsSlice(args.Ports)
	socks := make([]*core.Socket, 0, len(ports))
	for _, port := range ports {
		sock := &core.Socket{
			Kind:           core.SocketKindHostIP,
			URLVal:         (&url.URL{Scheme: port.Protocol.Network(), Host: fmt.Sprintf("%s:%d", args.Host, port.Backend)}).String(),
			PortForwardVal: port,
			SourceClientID: clientMetadata.ClientID,
		}
		socks = append(socks, sock)
	}

	svc := &core.Service{
		Creator:     trace.SpanContextFromContext(ctx),
		HostSockets: socks,
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, svc)
}
