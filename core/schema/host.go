package schema

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	bkworker "github.com/moby/buildkit/worker"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/slog"
)

type hostSchema struct{}

var _ SchemaResolvers = &hostSchema{}

func (s *hostSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.Func("host", func(ctx context.Context, parent *core.Query, args struct{}) (*core.Host, error) {
			return parent.NewHost(), nil
		}).Doc(`Queries the host environment.`),

		dagql.Func("_builtinContainer", func(ctx context.Context, parent *core.Query, args struct {
			Digest string `doc:"Digest of the image manifest"`
		}) (*core.Container, error) {
			st := llb.OCILayout(
				fmt.Sprintf("dagger/import@%s", args.Digest),
				llb.OCIStore("", buildkit.BuiltinContentOCIStoreName),
				llb.Platform(parent.Platform().Spec()),
				buildkit.WithTracePropagation(ctx),
			)

			ctrDef, err := st.Marshal(ctx, llb.Platform(parent.Platform().Spec()))
			if err != nil {
				return nil, fmt.Errorf("marshal root: %w", err)
			}

			// synchronously solve+unlazy so we don't have to deal with lazy blobs in any subsequent calls
			// that don't handle them (i.e. buildkit's cache volume code)
			// TODO: can be deleted once https://github.com/dagger/dagger/pull/8871 is closed
			bk, err := parent.Buildkit(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to get buildkit client: %w", err)
			}
			res, err := bk.Solve(ctx, bkgw.SolveRequest{
				Definition: ctrDef.ToPB(),
				Evaluate:   true,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to solve builtin container: %w", err)
			}
			resultProxy, err := res.SingleRef()
			if err != nil {
				return nil, fmt.Errorf("failed to get single ref: %w", err)
			}
			cachedRes, err := resultProxy.Result(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to get result: %w", err)
			}
			workerRef, ok := cachedRes.Sys().(*bkworker.WorkerRef)
			if !ok {
				return nil, fmt.Errorf("invalid ref: %T", cachedRes.Sys())
			}
			layerRefs := workerRef.ImmutableRef.LayerChain()
			defer layerRefs.Release(context.WithoutCancel(ctx))
			var eg errgroup.Group
			for _, layerRef := range layerRefs {
				layerRef := layerRef
				eg.Go(func() error {
					// FileList is the secret method that actually forces an unlazy of blobs in the cases
					// we want here...
					_, err := layerRef.FileList(ctx, nil)
					return err
				})
			}
			if err := eg.Wait(); err != nil {
				// this is a best effort attempt to unlazy the refs, it fails yell about it
				// but not worth being fatal
				slog.ErrorContext(ctx, "failed to unlazy layers", "err", err)
			}

			container, err := core.NewContainer(parent.Platform())
			if err != nil {
				return nil, fmt.Errorf("new container: %w", err)
			}

			container.FS = ctrDef.ToPB()

			goSDKContentStore, err := local.NewStore(distconsts.EngineContainerBuiltinContentDir)
			if err != nil {
				return nil, fmt.Errorf("failed to create go sdk content store: %w", err)
			}

			manifestBlob, err := content.ReadBlob(ctx, goSDKContentStore, specs.Descriptor{
				Digest: digest.Digest(args.Digest),
			})
			if err != nil {
				return nil, fmt.Errorf("image archive read manifest blob: %w", err)
			}

			var man specs.Manifest
			err = json.Unmarshal(manifestBlob, &man)
			if err != nil {
				return nil, fmt.Errorf("image archive unmarshal manifest: %w", err)
			}

			configBlob, err := content.ReadBlob(ctx, goSDKContentStore, man.Config)
			if err != nil {
				return nil, fmt.Errorf("image archive read image config blob %s: %w", man.Config.Digest, err)
			}

			var imgSpec specs.Image
			err = json.Unmarshal(configBlob, &imgSpec)
			if err != nil {
				return nil, fmt.Errorf("load image config: %w", err)
			}

			container.Config = imgSpec.Config

			return container, nil
		}).Doc("Retrieves a container builtin to the engine."),
	}.Install(srv)

	dagql.Fields[*core.Host]{
		// NOTE: (for near future) we can support force reloading by adding a new arg to this function and providing
		// a custom cache key function that uses a random value when that arg is true.
		dagql.NodeFuncWithCacheKey("directory", s.directory, dagql.CacheAsRequested).
			Doc(`Accesses a directory on the host.`).
			Args(
				dagql.Arg("path").Doc(`Location of the directory to access (e.g., ".").`),
				dagql.Arg("exclude").Doc(`Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`),
				dagql.Arg("include").Doc(`Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),
				dagql.Arg("noCache").Doc(`If true, the directory will always be reloaded from the host.`),
				dagql.Arg("noGitAutoIgnore").Doc(`Don't apply .gitignore filter rules inside the directory`),
			),

		dagql.NodeFuncWithCacheKey("file", s.file, dagql.CacheAsRequested).
			Doc(`Accesses a file on the host.`).
			Args(
				dagql.Arg("path").Doc(`Location of the file to retrieve (e.g., "README.md").`),
				dagql.Arg("noCache").Doc(`If true, the file will always be reloaded from the host.`),
			),

		dagql.NodeFuncWithCacheKey("unixSocket", s.socket, s.socketCacheKey).
			Doc(`Accesses a Unix socket on the host.`).
			Args(
				dagql.Arg("path").Doc(`Location of the Unix socket (e.g., "/var/run/docker.sock").`),
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

		// hidden from external clients via the __ prefix
		dagql.Func("__internalService", s.internalService).
			Doc(`(Internal-only) "service" but scoped to the exact right buildkit session ID.`),

		dagql.NodeFuncWithCacheKey("setSecretFile", s.setSecretFile, dagql.CachePerClient).
			Deprecated(`setSecretFile is superceded by use of the secret API with file:// URIs`).
			Doc(
				`Sets a secret given a user-defined name and the file path on the host,
				and returns the secret.`,
				`The file is limited to a size of 512000 bytes.`).
			Args(
				dagql.Arg("name").Doc(`The user defined name for this secret.`),
				dagql.Arg("path").Doc(`Location of the file to set as a secret.`),
			),
	}.Install(srv)
}

type setSecretFileArgs struct {
	Name string
	Path string
}

func (s *hostSchema) setSecretFile(ctx context.Context, host dagql.ObjectResult[*core.Host], args setSecretFileArgs) (inst dagql.Result[*core.Secret], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	err = srv.Select(ctx, srv.Root(), &inst,
		dagql.Selector{
			Field: "secret",
			Args: []dagql.NamedInput{{
				Name:  "uri",
				Value: dagql.NewString("file://" + args.Path),
			}},
		},
	)
	return inst, err
}

type hostDirectoryArgs struct {
	Path string

	core.CopyFilter
	HostDirCacheConfig

	GitIgnoreRoot   string `internal:"true" default:""`
	NoGitAutoIgnore bool   `default:"false"`
}

func (s *hostSchema) directory(ctx context.Context, host dagql.ObjectResult[*core.Host], args hostDirectoryArgs) (i dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	args.Path = path.Clean(args.Path)
	if args.Path == ".." || strings.HasPrefix(args.Path, "../") {
		return i, fmt.Errorf("path %q escapes workdir; use an absolute path instead", args.Path)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get requester session ID: %w", err)
	}

	stableID := clientMetadata.ClientStableID
	if stableID == "" {
		slog.WarnContext(ctx, "client stable ID not set, using random value")
		stableID = identity.NewID()
	}

	localOpts := []llb.LocalOption{
		llb.SessionID(clientMetadata.ClientID),
		llb.SharedKeyHint(stableID),
		buildkit.WithTracePropagation(ctx),
	}

	// HACK(cwlbraa): to bust the cache with noCache:true, we exclude a random text path.
	// No real chance of excluding something real this changes both the name and the cache key without modification to localsource.
	// To be removed via dagopification.
	if args.NoCache {
		args.Exclude = append(args.Exclude, rand.Text())
	}

	localName := fmt.Sprintf("upload %s from %s (client id: %s, session id: %s)", args.Path, stableID, clientMetadata.ClientID, clientMetadata.SessionID)
	if len(args.Include) > 0 {
		localName += fmt.Sprintf(" (include: %s)", strings.Join(args.Include, ", "))
		localOpts = append(localOpts, llb.IncludePatterns(args.Include))
	}

	excludesPatterns := []string{}

	// If NoGitAutoIgnore is not set, we load all the .gitgnore patterns inside the context directory
	// (if ContextDirectoryPath is set) or the git repo if .git is found.
	var dotGitIgnoreParentPath string
	if !args.NoGitAutoIgnore {
		if args.GitIgnoreRoot != "" {
			dotGitIgnoreParentPath = args.GitIgnoreRoot
		} else {
			query, err := core.CurrentQuery(ctx)
			if err != nil {
				return i, fmt.Errorf("failed to get current query: %w", err)
			}

			bk, err := query.Buildkit(ctx)
			if err != nil {
				return i, fmt.Errorf("failed to get buildkit client: %w", err)
			}

			dotGitPath, found, err := findUp(ctx, core.NewCallerStatFS(bk), args.Path, ".git")
			if err != nil {
				return i, fmt.Errorf("failed to find up .git: %w", err)
			}

			if found {
				dotGitIgnoreParentPath = dotGitPath
			}
		}
	}

	if dotGitIgnoreParentPath != "" {
		hostPath := args.Path
		if !filepath.IsAbs(hostPath) {
			query, err := core.CurrentQuery(ctx)
			if err != nil {
				return i, fmt.Errorf("failed to get current query: %w", err)
			}

			bk, err := query.Buildkit(ctx)
			if err != nil {
				return i, fmt.Errorf("failed to get buildkit client: %w", err)
			}

			hostPath, err = bk.AbsPath(ctx, hostPath)
			if err != nil {
				return i, fmt.Errorf("failed to get absolute path from host path %s: %w", hostPath, err)
			}
		}

		ignorePatterns, err := loadDirectoryGitIgnorePatterns(ctx, dotGitIgnoreParentPath, hostPath)
		if err != nil {
			return i, fmt.Errorf("failed to load .gitignore patterns: %w", err)
		}

		rebasedIgnorePatterns, err := rebaseGitIgnorePatterns(dotGitIgnoreParentPath, hostPath, ignorePatterns)
		if err != nil {
			return i, fmt.Errorf("failed to rebase .gitignore patterns: %w", err)
		}

		excludesPatterns = append(excludesPatterns, rebasedIgnorePatterns...)
	}

	// Add the exclude args from directive after the .gitignore so they can
	// be overridden.
	if len(args.Exclude) > 0 {
		excludesPatterns = append(excludesPatterns, args.Exclude...)
	}

	if len(excludesPatterns) > 0 {
		localName += fmt.Sprintf(" (exclude: %s)", strings.Join(excludesPatterns, ", "))
		localOpts = append(localOpts, llb.ExcludePatterns(excludesPatterns))
	}

	localOpts = append(localOpts, llb.WithCustomName(localName))

	localLLB := llb.Local(args.Path, localOpts...)

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return i, err
	}

	localDef, err := localLLB.Marshal(ctx, llb.Platform(query.Platform().Spec()))
	if err != nil {
		return i, fmt.Errorf("failed to marshal local LLB: %w", err)
	}
	localPB := localDef.ToPB()

	dir, err := dagql.NewObjectResultForCurrentID(ctx, srv,
		core.NewDirectory(localPB, "/", query.Platform(), nil),
	)
	if err != nil {
		return i, fmt.Errorf("failed to create instance: %w", err)
	}

	bk, err := query.Buildkit(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get buildkit client: %w", err)
	}
	return core.MakeDirectoryContentHashed(ctx, bk, dir)
}

func loadDirectoryGitIgnorePatterns(ctx context.Context, parentPath string, hostPath string) (patterns []string, rerr error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	gitIgnoreIncludePath, err := getGitIgnoreIncludePaths(parentPath, hostPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get git ignore include paths: %w", err)
	}

	var result dagql.ResultArray[dagql.String]
	err = srv.Select(ctx, srv.Root(), &result,
		dagql.Selector{Field: "host"},
		dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(parentPath)},
				{Name: "include", Value: dagql.ArrayInput[dagql.String](
					dagql.NewStringArray(gitIgnoreIncludePath...),
				)},
				{Name: "noGitAutoIgnore", Value: dagql.Boolean(true)},
			},
		},
		dagql.Selector{
			Field: "__gitIgnoreFor",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load directory ignore patterns: %w", err)
	}

	for i := 1; i <= result.Len(); i++ {
		entry, err := result.Nth(i)
		if err != nil {
			return nil, fmt.Errorf("failed to get entry %d in directory ignore patterns: %w", i, err)
		}

		entryValue, ok := dagql.UnwrapAs[dagql.String](entry)
		if !ok {
			return nil, fmt.Errorf("expected string, got %T", entry)
		}

		patterns = append(patterns, string(entryValue))
	}

	return patterns, nil
}

func rebaseGitIgnorePatterns(parentPath string, hostPath string, patterns []string) ([]string, error) {
	relativePathToParent, err := filepath.Rel(parentPath, hostPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get relative path from %q: %w", parentPath, err)
	}

	result := []string{}
	// Rebased the ignore patterns to be relative to the mounted path from the context directory.
	// - If the contextual path is /foo/bar, all /foo/bar/.gitignore patterns must become the root or they'll not be applied correctly
	// - Recursive patterns are not affected
	// - Patterns that are outside the contextual path are ignored.
	// - If relativePathToCtx is "." (meaning the contextual path is the same as the mounted path), then all ignore
	// patterns are applied.
	//
	// Example for context dir /foo/bar:
	//   - !foo/bar/baz -> baz -> !baz
	//   - foo/bar/baz -> baz -> baz
	//   - foo/bar/baz/qux -> baz/qux
	for _, pattern := range patterns {
		if !strings.HasPrefix(pattern, "**") &&
			!strings.HasPrefix(pattern, relativePathToParent+"/") &&
			!strings.HasPrefix(pattern, "!"+relativePathToParent+"/") &&
			relativePathToParent != "." {
			continue
		}

		// If the pattern is negative, we need to trim the leading "!" before removing the relative path
		// and add it again after.
		// !foo/bar/baz -> baz -> !baz
		if strings.HasPrefix(pattern, "!") {
			pattern = strings.TrimPrefix(pattern, "!")
			result = append(result, "!"+strings.TrimPrefix(pattern, relativePathToParent+"/"))
			continue
		}

		result = append(result, strings.TrimPrefix(pattern, relativePathToParent+"/"))
	}

	return result, nil
}

type hostSocketArgs struct {
	Path string
}

func (s *hostSchema) socketCacheKey(
	ctx context.Context,
	host dagql.ObjectResult[*core.Host],
	args hostSocketArgs,
	cacheCfg dagql.CacheConfig,
) (*dagql.CacheConfig, error) {
	cc, err := dagql.CachePerClient(ctx, host, args, cacheCfg)
	if err != nil {
		return nil, err
	}
	return cc, nil
}

func (s *hostSchema) socket(ctx context.Context, host dagql.ObjectResult[*core.Host], args hostSocketArgs) (inst dagql.Result[*core.Socket], err error) {
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

	accessor, err := core.GetClientResourceAccessor(ctx, query, args.Path)
	if err != nil {
		return inst, fmt.Errorf("failed to get client resource name: %w", err)
	}
	dgst := dagql.HashFrom(accessor)

	sock := &core.Socket{IDDigest: dgst}
	inst, err = dagql.NewResultForCurrentID(ctx, sock)
	if err != nil {
		return inst, fmt.Errorf("failed to create instance: %w", err)
	}
	inst = inst.WithDigest(dgst)
	if err := socketStore.AddUnixSocket(sock, clientMetadata.ClientID, args.Path); err != nil {
		return inst, fmt.Errorf("failed to add unix socket to store: %w", err)
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

func (s *hostSchema) internalSocket(ctx context.Context, host *core.Host, args hostInternalSocketArgs) (*core.Socket, error) {
	if args.Accessor == "" {
		return nil, errors.New("socket accessor must be provided")
	}
	return &core.Socket{IDDigest: dagql.CurrentID(ctx).Digest()}, nil
}
