package core

import (
	"context"
	"io"

	bkclient "github.com/moby/buildkit/client"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
)

type SessionManager interface {
	TarSend(ctx context.Context, id string, dest string, unpack bool) (io.WriteCloser, error)
	Export(ctx context.Context, id string, ex bkclient.ExportEntry, fn bkgw.BuildFunc) error
}
