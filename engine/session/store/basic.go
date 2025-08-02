package store

import (
	"errors"
	"fmt"
	io "io"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/engine/client/imageload"
	grpc "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type basicStoreAttachable struct {
	load imageload.TarballLoader

	UnimplementedBasicStoreServer
}

var _ BasicStoreServer = (*basicStoreAttachable)(nil)

const ImageLoadTag = "dagger.imageload"

func (loader basicStoreAttachable) Register(srv *grpc.Server) {
	RegisterBasicStoreServer(srv, loader)
}

var BasicStore_serviceDesc = _BasicStore_serviceDesc //nolint:stylecheck

func (loader basicStoreAttachable) LoadTarball(srv BasicStore_LoadTarballServer) error {
	md, ok := metadata.FromIncomingContext(srv.Context())
	if !ok {
		return fmt.Errorf("request lacks metadata: %w", cerrdefs.ErrInvalidArgument)
	}

	values := md[ImageLoadTag]
	if len(values) == 0 {
		return fmt.Errorf("request lacks metadata %q: %w", ImageLoadTag, cerrdefs.ErrInvalidArgument)
	}
	tag := values[0]

	reader := &TarballReader{GRPC: srv}
	err := loader.load(srv.Context(), tag, reader)
	if err != nil {
		return err
	}
	return srv.SendAndClose(&LoadResponse{})
}

type TarballReader struct {
	GRPC BasicStore_LoadTarballServer
	rem  []byte // remainder buffer
}

func (pio *TarballReader) Read(p []byte) (n int, err error) {
	n = copy(p, pio.rem)
	p = p[n:]
	pio.rem = pio.rem[n:]
	if len(p) == 0 || n != 0 {
		return n, nil
	}
	req, err := pio.GRPC.Recv()
	if errors.Is(err, io.EOF) {
		return 0, io.EOF
	}
	if err != nil {
		return 0, fmt.Errorf("error reading dagger tarball: %w", err)
	}
	pio.rem = req.Data
	n = copy(p, pio.rem)
	pio.rem = pio.rem[n:]
	return n, nil
}

type TarballWriter struct {
	GRPC BasicStore_LoadTarballClient
}

func (pio *TarballWriter) Write(p []byte) (n int, err error) {
	err = pio.GRPC.Send(&Data{Data: p})
	if err != nil {
		return 0, fmt.Errorf("error writing dagger tarball: %w", err)
	}
	return len(p), nil
}

func (pio *TarballWriter) Close() error {
	_, err := pio.GRPC.CloseAndRecv()
	if err != nil {
		return err
	}
	return nil
}
