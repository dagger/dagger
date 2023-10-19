package schema

import (
	"errors"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/socket"
)

type hostSchema struct {
	*MergedSchemas

	host *core.Host
	svcs *core.Services
}

var _ ExecutableSchema = &hostSchema{}

func (s *hostSchema) Name() string {
	return "host"
}

func (s *hostSchema) Schema() string {
	return Host
}

func (s *hostSchema) Resolvers() Resolvers {
	return Resolvers{
		"Query": ObjectResolver{
			"host": PassthroughResolver,
		},
		"Host": ObjectResolver{
			"directory":     ToResolver(s.directory),
			"file":          ToResolver(s.file),
			"unixSocket":    ToResolver(s.socket),
			"setSecretFile": ToResolver(s.setSecretFile),
			"tunnel":        ToResolver(s.tunnel),
			"service":       ToResolver(s.service),
		},
	}
}

func (s *hostSchema) Dependencies() []ExecutableSchema {
	return nil
}

type setSecretFileArgs struct {
	Name string
	Path string
}

func (s *hostSchema) setSecretFile(ctx *core.Context, _ any, args setSecretFileArgs) (*core.Secret, error) {
	secretFileContent, err := s.bk.ReadCallerHostFile(ctx, args.Path)
	if err != nil {
		return nil, fmt.Errorf("read secret file: %w", err)
	}

	secretID, err := s.secrets.AddSecret(ctx, args.Name, secretFileContent)
	if err != nil {
		return nil, err
	}

	return secretID.Decode()
}

type hostDirectoryArgs struct {
	Path string

	core.CopyFilter
}

func (s *hostSchema) directory(ctx *core.Context, parent *core.Query, args hostDirectoryArgs) (*core.Directory, error) {
	return s.host.Directory(ctx, s.bk, args.Path, parent.PipelinePath(), "host.directory", s.platform, args.CopyFilter)
}

type hostSocketArgs struct {
	Path string
}

func (s *hostSchema) socket(ctx *core.Context, parent any, args hostSocketArgs) (*socket.Socket, error) {
	return s.host.Socket(ctx, args.Path)
}

type hostFileArgs struct {
	Path string
}

func (s *hostSchema) file(ctx *core.Context, parent *core.Query, args hostFileArgs) (*core.File, error) {
	return s.host.File(ctx, s.bk, s.svcs, args.Path, parent.PipelinePath(), s.platform)
}

type hostTunnelArgs struct {
	Service core.ServiceID
	Ports   []core.PortForward
	Native  bool
}

func (s *hostSchema) tunnel(ctx *core.Context, parent any, args hostTunnelArgs) (*core.Service, error) {
	svc, err := args.Service.Decode()
	if err != nil {
		return nil, err
	}

	if svc.Container == nil {
		return nil, errors.New("tunneling to non-Container services is not supported")
	}

	ports := []core.PortForward{}

	if args.Native {
		for _, port := range svc.Container.Ports {
			ports = append(ports, core.PortForward{
				Frontend: port.Port,
				Backend:  port.Port,
				Protocol: port.Protocol,
			})
		}
	}

	if len(args.Ports) > 0 {
		ports = append(ports, args.Ports...)
	}

	if len(ports) == 0 {
		for _, port := range svc.Container.Ports {
			ports = append(ports, core.PortForward{
				Frontend: 0, // pick a random port on the host
				Backend:  port.Port,
				Protocol: port.Protocol,
			})
		}
	}

	if len(ports) == 0 {
		return nil, errors.New("no ports to forward")
	}

	return core.NewTunnelService(svc, ports), nil
}

type hostServiceArgs struct {
	Host  string
	Ports []core.PortForward
}

func (s *hostSchema) service(ctx *core.Context, parent *core.Host, args hostServiceArgs) (*core.Service, error) {
	if len(args.Ports) == 0 {
		return nil, errors.New("no ports specified")
	}

	return core.NewHostService(args.Host, args.Ports), nil
}
