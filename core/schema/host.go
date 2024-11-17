package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/labels"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bkworker "github.com/moby/buildkit/worker"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/sources/blob"
)

type hostSchema struct {
	srv *dagql.Server
}

var _ SchemaResolvers = &hostSchema{}

func (s *hostSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.Func("host", func(ctx context.Context, parent *core.Query, args struct{}) (*core.Host, error) {
			return parent.NewHost(), nil
		}).Doc(`Queries the host environment.`),

		dagql.Func("blob", func(ctx context.Context, parent *core.Query, args struct {
			Digest       string `doc:"Digest of the blob"`
			Size         int64  `doc:"Size of the blob"`
			MediaType    string `doc:"Media type of the blob"`
			Uncompressed string `doc:"Digest of the uncompressed blob"`
		}) (*core.Directory, error) {
			dig, err := digest.Parse(args.Digest)
			if err != nil {
				return nil, fmt.Errorf("failed to parse digest: %w", err)
			}
			uncompressedDig, err := digest.Parse(args.Uncompressed)
			if err != nil {
				return nil, fmt.Errorf("failed to parse digest: %w", err)
			}
			blobDef, err := blob.LLB(specs.Descriptor{
				MediaType: args.MediaType,
				Digest:    dig,
				Size:      args.Size,
				Annotations: map[string]string{
					// uncompressed label is required to be set by buildkit's GetByBlob
					// implementation
					labels.LabelUncompressed: uncompressedDig.String(),
				},
			}).Marshal(ctx, buildkit.WithTracePropagation(ctx))
			if err != nil {
				return nil, fmt.Errorf("failed to marshal blob source: %w", err)
			}
			return core.NewDirectory(parent, blobDef.ToPB(), "/", parent.Platform(), nil), nil
		}).Doc("Retrieves a content-addressed blob."),

		dagql.Func("builtinContainer", func(ctx context.Context, parent *core.Query, args struct {
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

			container, err := core.NewContainer(parent, parent.Platform())
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
	}.Install(s.srv)

	dagql.Fields[*core.Host]{
		dagql.Func("directory", s.directory).
			Impure("Loads data from the local machine.",
				`Despite being impure, this field returns a pure Directory object. It
				does this by uploading the requested path to the internal content store
				and returning a content-addressed Directory using the `+"`blob()` API.").
			Doc(`Accesses a directory on the host.`).
			ArgDoc("path", `Location of the directory to access (e.g., ".").`).
			ArgDoc("exclude", `Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`).
			ArgDoc("include", `Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),

		dagql.Func("file", s.file).
			Impure("Loads data from the local machine.",
				`Despite being impure, this field returns a pure File object. It does
				this by uploading the requested path to the internal content store and
				returning a content-addressed File using from the `+"`blob()` API.").
			Doc(`Accesses a file on the host.`).
			ArgDoc("path", `Location of the file to retrieve (e.g., "README.md").`),

		dagql.NodeFunc("unixSocket", s.unixSocket).
			Doc(`Accesses a Unix socket on the host.`).
			ArgDoc("path", `Location of the Unix socket (e.g., "/var/run/docker.sock").`),

		dagql.NodeFunc("ipSocket", s.ipSocket).
			Doc(`Accesses a IP socket on the host.`).
			ArgDoc("host", `Hostname or IP address for the socket to dial.`).
			ArgDoc("port", `Port number for the socket to dial.`).
			ArgDoc("protocol", `Network protocol of the socket.`),

		dagql.Func("tunnel", s.tunnel).
			Doc(`Creates a tunnel that forwards traffic from the host to a service.`).
			ArgDoc("service", `Service to send traffic from the tunnel.`).
			ArgDoc("ports", `List of frontend/backend port mappings to forward.`,
				`Frontend is the port accepting traffic on the host, backend is the service port.`).
			ArgDoc("native",
				`Map each service port to the same port on the host, as if the service were running natively.`,
				`Note: enabling may result in port conflicts.`).
			ArgDoc("ports",
				`Configure explicit port forwarding rules for the tunnel.`,
				`If a port's frontend is unspecified or 0, a random port will be chosen
				by the host.`,
				`If no ports are given, all of the service's ports are forwarded. If
				native is true, each port maps to the same port on the host. If native
				is false, each port maps to a random port chosen by the host.`,
				`If ports are given and native is true, the ports are additive.`),

		dagql.Func("service", s.service).
			Impure("Value depends on the caller as it points to their host.").
			Doc(`Creates a service that forwards traffic to a specified address via the host.`).
			ArgDoc("ports",
				`Ports to expose via the service, forwarding through the host network.`,
				`If a port's frontend is unspecified or 0, it defaults to the same as
				the backend port.`,
				`An empty set of ports is not valid; an error will be returned.`).
			ArgDoc("host", `Upstream host to forward traffic to.`),

		// hidden from external clients via the __ prefix
		dagql.Func("__internalService", s.internalService).
			Doc(`(Internal-only) "service" but scoped to the exact right buildkit session ID.`),

		dagql.Func("setSecretFile", s.setSecretFile).
			Impure("`setSecretFile` reads its value from the local machine.").
			Doc(
				`Sets a secret given a user-defined name and the file path on the host,
				and returns the secret.`,
				`The file is limited to a size of 512000 bytes.`).
			ArgDoc("name", `The user defined name for this secret.`).
			ArgDoc("path", `Location of the file to set as a secret.`),
	}.Install(s.srv)
}

type setSecretFileArgs struct {
	Name string
	Path string
}

func (s *hostSchema) setSecretFile(ctx context.Context, host *core.Host, args setSecretFileArgs) (dagql.Instance[*core.Secret], error) {
	return host.SetSecretFile(ctx, s.srv, args.Name, args.Path)
}

type hostDirectoryArgs struct {
	Path string

	core.CopyFilter
}

func (s *hostSchema) directory(ctx context.Context, host *core.Host, args hostDirectoryArgs) (dagql.Instance[*core.Directory], error) {
	return host.Directory(ctx, s.srv, args.Path, "host.directory", args.CopyFilter)
}

type hostSocketArgs struct {
	Path string
	// Accessor is the scoped per-module name, which should guarantee uniqueness.
	// It is used to ensure the dagql ID digest is unique per module; the digest is what's
	// used as the actual key for the socket store.
	Accessor string `default:""`
}

func (s *hostSchema) unixSocket(ctx context.Context, parent dagql.Instance[*core.Host], args hostSocketArgs) (inst dagql.Instance[*core.Socket], err error) {
	socketStore, err := parent.Self.Query.Sockets(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get socket store: %w", err)
	}

	if args.Accessor == "" {
		dagql.Taint(ctx)
		accessor, err := core.GetClientResourceAccessor(ctx, parent.Self.Query, args.Path)
		if err != nil {
			return inst, fmt.Errorf("failed to get client resource name: %w", err)
		}
		err = s.srv.Select(ctx, parent, &inst,
			dagql.Selector{
				Field: "unixSocket",
				Args: []dagql.NamedInput{
					{
						Name:  "path",
						Value: dagql.NewString(args.Path),
					},
					{
						Name:  "accessor",
						Value: dagql.NewString(accessor),
					},
				},
			},
		)
		return inst, err
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get client metadata: %w", err)
	}

	socket := &core.Socket{
		IDDigest: dagql.CurrentID(ctx).Digest(),
	}

	if err := socketStore.AddUnixSocket(socket, clientMetadata.ClientID, args.Path); err != nil {
		return inst, fmt.Errorf("failed to add unix socket to store: %w", err)
	}

	return dagql.NewInstanceForCurrentID(ctx, s.srv, parent, socket)
}

func (s *hostSchema) ipSocket(ctx context.Context, parent dagql.Instance[*core.Host], args struct {
	Host     string
	Port     int
	Protocol core.NetworkProtocol `default:"TCP"`
	Accessor string               `default:""`
}) (inst dagql.Instance[*core.Socket], err error) {
	socketStore, err := parent.Self.Query.Sockets(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get socket store: %w", err)
	}

	port := core.PortForward{
		Backend:  args.Port,
		Protocol: args.Protocol,
	}

	if args.Accessor == "" {
		dagql.Taint(ctx)
		accessor, err := core.GetHostIPSocketAccessor(ctx, parent.Self.Query, args.Host, port)
		if err != nil {
			return inst, fmt.Errorf("failed to get host ip socket accessor: %w", err)
		}
		err = s.srv.Select(ctx, parent, &inst,
			dagql.Selector{
				Field: "ipSocket",
				Args: []dagql.NamedInput{
					{
						Name:  "host",
						Value: dagql.NewString(args.Host),
					},
					{
						Name:  "port",
						Value: dagql.NewInt(args.Port),
					},
					{
						Name:  "protocol",
						Value: args.Protocol,
					},
					{
						Name:  "accessor",
						Value: dagql.NewString(accessor),
					},
				},
			},
		)
		return inst, err
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get client metadata: %w", err)
	}

	socket := &core.Socket{
		IDDigest: dagql.CurrentID(ctx).Digest(),
	}

	if err := socketStore.AddIPSocket(socket, clientMetadata.ClientID, args.Host, port); err != nil {
		return inst, fmt.Errorf("failed to add ip socket to store: %w", err)
	}

	return dagql.NewInstanceForCurrentID(ctx, s.srv, parent, socket)
}

type hostFileArgs struct {
	Path string
}

func (s *hostSchema) file(ctx context.Context, host *core.Host, args hostFileArgs) (dagql.Instance[*core.File], error) {
	return host.File(ctx, s.srv, args.Path)
}

type hostTunnelArgs struct {
	Service core.ServiceID
	Ports   []dagql.InputObject[core.PortForward] `default:"[]"`
	Native  bool                                  `default:"false"`
}

func (s *hostSchema) tunnel(ctx context.Context, parent *core.Host, args hostTunnelArgs) (*core.Service, error) {
	inst, err := args.Service.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}

	svc := inst.Self

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

	return parent.Query.NewTunnelService(ctx, inst, ports), nil
}

type hostServiceArgs struct {
	Host  string `default:"localhost"`
	Ports []dagql.InputObject[core.PortForward]
}

func (s *hostSchema) service(ctx context.Context, parent *core.Host, args hostServiceArgs) (inst dagql.Instance[*core.Service], err error) {
	if len(args.Ports) == 0 {
		return inst, errors.New("no ports specified")
	}

	ports := collectInputsSlice(args.Ports)
	sockIDs := make([]dagql.ID[*core.Socket], 0, len(ports))
	for _, port := range ports {
		var sockInst dagql.Instance[*core.Socket]
		err = s.srv.Select(ctx, s.srv.Root(), &sockInst,
			dagql.Selector{
				Field: "host",
			},
			dagql.Selector{
				Field: "ipSocket",
				Args: []dagql.NamedInput{
					{
						Name:  "host",
						Value: dagql.NewString(args.Host),
					},
					{
						Name:  "port",
						Value: dagql.NewInt(port.Backend),
					},
					{
						Name:  "protocol",
						Value: port.Protocol,
					},
				},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to select internal socket: %w", err)
		}

		sockIDs = append(sockIDs, dagql.NewID[*core.Socket](sockInst.ID()))
	}

	err = s.srv.Select(ctx, s.srv.Root(), &inst,
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
	if len(args.Socks) == 0 {
		return nil, errors.New("no host sockets specified")
	}

	socks := make([]*core.Socket, 0, len(args.Socks))
	for _, sockID := range args.Socks {
		sockInst, err := sockID.Load(ctx, s.srv)
		if err != nil {
			return nil, fmt.Errorf("failed to load socket: %w", err)
		}
		socks = append(socks, sockInst.Self)
	}

	return parent.Query.NewHostService(ctx, socks), nil
}
