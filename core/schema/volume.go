package schema

import (
	"context"
	"fmt"
	"net/url"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type volumeSchema struct{}

var _ SchemaResolvers = &volumeSchema{}

func (s *volumeSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.NodeFunc("engineVolume", s.engineVolume).
			View(AfterVersion("v1.0.0-0")).
			Doc("Constructs an engine-managed volume backed by operator-provided storage beneath the configured engine state root.").
			Args(
				dagql.Arg("name").Doc("Canonical slash-separated volume name beneath the engine volume namespace."),
				dagql.Arg("subdir").Doc("Optional existing subdirectory within the volume payload to mount."),
			),
		dagql.NodeFunc("sshfsVolume", s.sshfsVolume).
			View(AfterVersion("v1.0.0-0")).
			WithInput(dagql.PerSessionInput).
			Doc("Constructs an SSHFS volume.").
			Args(
				dagql.Arg("endpoint").Doc("SSHFS endpoint URL in the form sshfs://user@host[:port]/absolute/path."),
				dagql.Arg("privateKey").Doc("Private key secret used to authenticate to the remote host."),
				dagql.Arg("knownHosts").Doc("known_hosts material used to verify the remote host key. Required unless insecureSkipHostKeyCheck is true."),
				dagql.Arg("cacheKey").Doc("Optional cache equivalence key. If set, volumes with the same cacheKey may be considered equivalent for cache lookups, still subject to their resource dependencies."),
				dagql.Arg("insecureSkipHostKeyCheck").Doc("Disable SSH host key verification. This is insecure and must be explicitly opted into."),
				dagql.Arg("experimentalServiceHost").Doc("Service to use as the SSHFS network endpoint while verifying the original host key."),
			),
	}.Install(srv)

	srv.InstallObject(dagql.NewClass[*core.Volume](srv).View(AfterVersion("v1.0.0-0")))
	dagql.Fields[*core.Volume]{}.Install(srv)
}

type engineVolumeArgs struct {
	Name   string
	Subdir dagql.Optional[dagql.String]
}

func (s *volumeSchema) engineVolume(ctx context.Context, parent dagql.ObjectResult[*core.Query], args engineVolumeArgs) (dagql.ObjectResult[*core.Volume], error) {
	if err := parent.Self().RequireMainClient(ctx); err != nil {
		return dagql.ObjectResult[*core.Volume]{}, err
	}
	if err := core.ValidateEngineVolumeName(args.Name); err != nil {
		return dagql.ObjectResult[*core.Volume]{}, err
	}

	subdir := ""
	if args.Subdir.Valid {
		subdir = string(args.Subdir.Value)
		if err := core.ValidateEngineVolumeSubdir(subdir); err != nil {
			return dagql.ObjectResult[*core.Volume]{}, err
		}
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Volume]{}, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, &core.Volume{
		Backend: core.VolumeBackendKindEngine,
		Engine: &core.EngineVolumeConfig{
			Name:          args.Name,
			Subdir:        subdir,
			LayoutVersion: core.EngineVolumeLayoutVersion,
		},
	})
}

type sshfsVolumeArgs struct {
	Endpoint                 string
	PrivateKey               core.SecretID
	KnownHosts               dagql.Optional[core.SecretID]
	CacheKey                 dagql.Optional[dagql.String]
	InsecureSkipHostKeyCheck bool `default:"false"`
	ExperimentalServiceHost  dagql.Optional[core.ServiceID]
}

func (s *volumeSchema) sshfsVolume(ctx context.Context, parent dagql.ObjectResult[*core.Query], args sshfsVolumeArgs) (dagql.ObjectResult[*core.Volume], error) {
	if err := parent.Self().RequireMainClient(ctx); err != nil {
		return dagql.ObjectResult[*core.Volume]{}, err
	}
	endpoint, hostKeyAlias, err := parseSSHFSVolumeEndpoint(args.Endpoint)
	if err != nil {
		return dagql.ObjectResult[*core.Volume]{}, err
	}
	if !args.KnownHosts.Valid && !args.InsecureSkipHostKeyCheck {
		return dagql.ObjectResult[*core.Volume]{}, fmt.Errorf("knownHosts is required unless insecureSkipHostKeyCheck is true")
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Volume]{}, err
	}
	privateKey, err := args.PrivateKey.Load(ctx, srv)
	if err != nil {
		return dagql.ObjectResult[*core.Volume]{}, fmt.Errorf("load volume private key: %w", err)
	}
	var knownHosts dagql.ObjectResult[*core.Secret]
	if args.KnownHosts.Valid {
		knownHosts, err = args.KnownHosts.Value.Load(ctx, srv)
		if err != nil {
			return dagql.ObjectResult[*core.Volume]{}, fmt.Errorf("load volume known hosts: %w", err)
		}
	}
	var serviceHost dagql.ObjectResult[*core.Service]
	if args.ExperimentalServiceHost.Valid {
		serviceHost, err = args.ExperimentalServiceHost.Value.Load(ctx, srv)
		if err != nil {
			return dagql.ObjectResult[*core.Volume]{}, fmt.Errorf("load volume service host: %w", err)
		}
	}

	vol := &core.Volume{
		Backend: core.VolumeBackendKindSSHFS,
		SSHFS: &core.SSHFSVolumeConfig{
			Endpoint:                 endpoint,
			PrivateKey:               privateKey,
			KnownHosts:               knownHosts,
			InsecureSkipHostKeyCheck: args.InsecureSkipHostKeyCheck,
			HostKeyAlias:             hostKeyAlias,
			ServiceHost:              serviceHost,
		},
	}
	inst, err := dagql.NewObjectResultForCurrentCall(ctx, srv, vol)
	if err != nil {
		return dagql.ObjectResult[*core.Volume]{}, err
	}
	if args.CacheKey.Valid {
		return inst.WithContentDigest(ctx, core.VolumeContentDigestFromCacheKey(string(args.CacheKey.Value)))
	}
	return inst, nil
}

func parseSSHFSVolumeEndpoint(endpoint string) (string, string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", "", fmt.Errorf("parse SSHFS endpoint: %w", err)
	}
	if u.Scheme != "sshfs" {
		return "", "", fmt.Errorf("SSHFS endpoint must use sshfs:// scheme")
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("SSHFS endpoint must include a host")
	}
	if u.User == nil || u.User.Username() == "" {
		return "", "", fmt.Errorf("SSHFS endpoint must include a user")
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		return "", "", fmt.Errorf("SSHFS endpoint must not include a password")
	}
	if u.Path == "" || u.Path[0] != '/' {
		return "", "", fmt.Errorf("SSHFS endpoint must include an absolute path")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", "", fmt.Errorf("SSHFS endpoint must not include query parameters or fragments")
	}
	return u.String(), sshfsHostKeyAlias(u), nil
}

func sshfsHostKeyAlias(u *url.URL) string {
	host := u.Hostname()
	port := u.Port()
	if port == "" || port == "22" {
		return host
	}
	return "[" + host + "]:" + port
}
