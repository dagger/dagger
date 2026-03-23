package schema

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type volumeSchema struct{}

var _ SchemaResolvers = &volumeSchema{}

func (s *volumeSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.NodeFunc("sshfsVolume", s.sshfsVolume).
			Doc("Create or retrieve an engine-managed SSHFS volume. Endpoint must be a parseable SSH URL, e.g. 'ssh://user@host:2222/path'.").
			Args(
				dagql.Arg("endpoint").Doc("SSH endpoint URL, e.g. ssh://user@host[:port]/absolute/path"),
				dagql.Arg("privateKey").Doc("The private key to use for authentication"),
				dagql.Arg("publicKey").Doc("The public key to use for authentication"),
				dagql.Arg("experimentalServiceHost").Doc("A service which must be started before the SSHFS volume is mounted."),
			),
	}.Install(srv)

	dagql.Fields[*core.Volume]{}.Install(srv)
}

type volumeArgs struct {
	Endpoint                string
	PrivateKey              core.SecretID
	PublicKey               core.SecretID
	ExperimentalServiceHost dagql.Optional[core.ServiceID]
}

func (s *volumeSchema) sshfsVolume(ctx context.Context, parent dagql.ObjectResult[*core.Query], args volumeArgs) (i dagql.ObjectResult[*core.Volume], err error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return i, err
	}

	endpoint := args.Endpoint

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get dagql server: %w", err)
	}

	if args.ExperimentalServiceHost.Valid {
		svc, err := args.ExperimentalServiceHost.Value.Load(ctx, srv)
		if err != nil {
			return i, err
		}
		host, err := svc.Self().Hostname(ctx, svc.ID())
		if err != nil {
			return i, err
		}
		svcs, err := query.Services(ctx)
		if err != nil {
			return i, fmt.Errorf("failed to get services: %w", err)
		}
		detach, running, err := svcs.StartBindings(ctx, core.ServiceBindings{{
			Service:  svc,
			Hostname: host,
		}})
		if err != nil {
			return i, err
		}
		defer detach()

		if len(running) == 0 || running[0] == nil {
			return i, fmt.Errorf("failed to resolve running service for sshfs host")
		}

		endpoint, err = withSSHFSRunningHost(endpoint, running[0].Host)
		if err != nil {
			return i, err
		}
	}

	privDgst := core.SecretIDDigest(args.PrivateKey.ID())
	pubDgst := core.SecretIDDigest(args.PublicKey.ID())

	vol, err := query.Server.RegisterSSHFSVolume(ctx, endpoint, privDgst, pubDgst)
	if err != nil {
		return i, fmt.Errorf("failed to register sshfs volume: %w", err)
	}

	return dagql.NewObjectResultForCurrentID(ctx, srv, vol)
}

func withSSHFSRunningHost(endpoint, host string) (string, error) {
	if strings.HasPrefix(endpoint, "ssh://") {
		u, err := url.Parse(endpoint)
		if err != nil {
			return "", fmt.Errorf("parse sshfs endpoint: %w", err)
		}
		port := u.Port()
		if port == "" {
			port = "22"
		}
		u.Host = net.JoinHostPort(host, port)
		return u.String(), nil
	}

	atIdx := strings.Index(endpoint, "@")
	if atIdx < 0 {
		return "", fmt.Errorf("invalid sshfs endpoint %q: missing @", endpoint)
	}

	rest := endpoint[atIdx+1:]
	slashIdx := strings.Index(rest, "/")
	path := "/"
	hostPart := rest
	if slashIdx >= 0 {
		hostPart = rest[:slashIdx]
		path = rest[slashIdx:]
	}

	port := ""
	if colonIdx := strings.Index(hostPart, ":"); colonIdx >= 0 {
		port = hostPart[colonIdx+1:]
	}

	rewrittenHost := host
	if port != "" {
		rewrittenHost = net.JoinHostPort(host, port)
	}

	return endpoint[:atIdx+1] + rewrittenHost + path, nil
}
