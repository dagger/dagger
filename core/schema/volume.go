package schema

import (
	"context"
	"fmt"

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
			),
	}.Install(srv)

	dagql.Fields[*core.Volume]{}.Install(srv)
}

type volumeArgs struct {
	Endpoint   string
	PrivateKey core.SecretID
	PublicKey  core.SecretID
}

func (s *volumeSchema) sshfsVolume(ctx context.Context, parent dagql.ObjectResult[*core.Query], args volumeArgs) (i dagql.ObjectResult[*core.Volume], err error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return i, err
	}
	if err := query.RequireMainClient(ctx); err != nil {
		return i, err
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return i, fmt.Errorf("failed to get dagql server: %w", err)
	}

	// extract secret digests
	privDgst := args.PrivateKey.ID().Digest()
	pubDgst := args.PublicKey.ID().Digest()

	vol, err := query.Server.RegisterSSHFSVolume(ctx, args.Endpoint, privDgst, pubDgst)
	if err != nil {
		return i, fmt.Errorf("failed to register sshfs volume: %w", err)
	}

	return dagql.NewObjectResultForCurrentID(ctx, srv, vol)
}
