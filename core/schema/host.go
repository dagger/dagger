package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/labels"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/engine/sources/blob"
	"github.com/moby/buildkit/client/llb"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
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
				return nil, fmt.Errorf("failed to parse digest: %s", err)
			}
			uncompressedDig, err := digest.Parse(args.Uncompressed)
			if err != nil {
				return nil, fmt.Errorf("failed to parse digest: %s", err)
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
			}).Marshal(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal blob source: %s", err)
			}
			return core.NewDirectory(parent, blobDef.ToPB(), "/", parent.Platform, nil), nil
		}).Doc("Retrieves a content-addressed blob."),

		dagql.Func("builtinContainer", func(ctx context.Context, parent *core.Query, args struct {
			Digest string `doc:"Digest of the image manifest"`
		}) (*core.Container, error) {
			st := llb.OCILayout(
				fmt.Sprintf("dagger/import@%s", args.Digest),
				llb.OCIStore("", buildkit.BuiltinContentOCIStoreName),
				llb.Platform(parent.Platform.Spec()),
			)

			execDef, err := st.Marshal(ctx, llb.Platform(parent.Platform.Spec()))
			if err != nil {
				return nil, fmt.Errorf("marshal root: %w", err)
			}

			container, err := core.NewContainer(parent, parent.Platform)
			if err != nil {
				return nil, fmt.Errorf("new container: %w", err)
			}

			container.FS = execDef.ToPB()

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
			Impure("The `directory` field loads data from the local machine.",
				`Despite being impure, this field returns a pure Directory object. It
				does this by uploading the requested path to the internal content store
				and returning a content-addressed Directory using the `+"`blob()` API.").
			Doc(`Accesses a directory on the host.`).
			ArgDoc("path", `Location of the directory to access (e.g., ".").`).
			ArgDoc("exclude", `Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`).
			ArgDoc("include", `Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),

		dagql.Func("file", s.file).
			Impure("The `field` field loads data from the local machine.",
				`Despite being impure, this field returns a pure File object. It does
				this by uploading the requested path to the internal content store and
				returning a content-addressed File using from the `+"`blob()` API.").
			Doc(`Accesses a file on the host.`).
			ArgDoc("path", `Location of the file to retrieve (e.g., "README.md").`),

		dagql.Func("unixSocket", s.socket).
			Doc(`Accesses a Unix socket on the host.`).
			ArgDoc("path", `Location of the Unix socket (e.g., "/var/run/docker.sock").`),

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
			Doc(`Creates a service that forwards traffic to a specified address via the host.`).
			ArgDoc("ports",
				`Ports to expose via the service, forwarding through the host network.`,
				`If a port's frontend is unspecified or 0, it defaults to the same as
				the backend port.`,
				`An empty set of ports is not valid; an error will be returned.`).
			ArgDoc("host", `Upstream host to forward traffic to.`),

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
}

func (s *hostSchema) socket(ctx context.Context, host *core.Host, args hostSocketArgs) (*core.Socket, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get client metadata: %w", err)
	}
	if clientMetadata.ClientID != host.Query.Buildkit.MainClientCallerID {
		return nil, fmt.Errorf("only the main client can access the host's unix sockets")
	}

	return host.Socket(args.Path), nil
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

	return parent.Query.NewTunnelService(inst, ports), nil
}

type hostServiceArgs struct {
	Host  string `default:"localhost"`
	Ports []dagql.InputObject[core.PortForward]
}

func (s *hostSchema) service(ctx context.Context, parent *core.Host, args hostServiceArgs) (*core.Service, error) {
	if len(args.Ports) == 0 {
		return nil, errors.New("no ports specified")
	}

	return parent.Query.NewHostService(args.Host, collectInputsSlice(args.Ports)), nil
}
