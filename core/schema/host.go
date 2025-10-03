package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/leases"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	bkgw "github.com/dagger/dagger/internal/buildkit/frontend/gateway/client"
	"github.com/dagger/dagger/internal/buildkit/util/contentutil"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	bkworker "github.com/dagger/dagger/internal/buildkit/worker"
	"github.com/distribution/reference"
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
	"github.com/dagger/dagger/util/ctrns"
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

			container := core.NewContainer(parent.Platform())
			rootfsDir := core.NewDirectory(ctrDef.ToPB(), "/", container.Platform, container.Services)
			container.FS, err = core.UpdatedRootFS(ctx, rootfsDir)
			if err != nil {
				return nil, fmt.Errorf("failed to update rootfs: %w", err)
			}

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
		dagql.NodeFuncWithCacheKey("directory", s.directory, dagql.CacheAsRequested).
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

	// The operation is not wrapped in dagOp at the schema level, so we manually handle
	// it inside the resolver.
	// That's because it's more convenient to call `MakeDirectoryContentHashed` here
	// that having an option in the `dagOpDirectoryWrapper`
	if args.InDagOp() {
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
			if negative {
				exclude = "!" + exclude
			}
			excludePatterns = append(excludePatterns, exclude)
		}

		dir, err := host.Self().Directory(ctx, absRootCopyPath, core.CopyFilter{
			Include: includePatterns,
			Exclude: excludePatterns,
		}, args.Gitignore, args.NoCache, relPathFromRoot)

		if err != nil {
			return inst, fmt.Errorf("failed to get directory: %w", err)
		}

		return dagql.NewObjectResultForCurrentID(ctx, srv, dir)
	}

	// If outside of DagOp, we create a "promise" of that future directory so
	// the function will be called again by DagQL with `args.InDagOp() == true`
	dir, err := DagOpDirectory(ctx, srv, host.Self(), args, "", s.directory)
	if err != nil {
		return inst, fmt.Errorf("failed to create dagOp for directory: %w", err)
	}

	dirRes, err := dagql.NewObjectResultForCurrentID(ctx, srv, dir)
	if err != nil {
		return inst, fmt.Errorf("failed to compute object result for dag Op directory: %w", err)
	}

	return core.MakeDirectoryContentHashed(ctx, bk, dirRes)
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

func (s *hostSchema) internalSocket(ctx context.Context, host *core.Host, args hostInternalSocketArgs) (*core.Socket, error) {
	if args.Accessor == "" {
		return nil, errors.New("socket accessor must be provided")
	}
	return &core.Socket{IDDigest: dagql.CurrentID(ctx).Digest()}, nil
}
