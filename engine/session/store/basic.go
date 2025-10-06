package store

import (
	"errors"
	"fmt"
	io "io"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/dagger/dagger/engine/client/imageload"
	grpc "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

type basicStoreAttachable struct {
	write imageload.TarballWriter
	read  imageload.TarballReader

	UnimplementedBasicStoreServer
}

var _ BasicStoreServer = (*basicStoreAttachable)(nil)

const ImageTagKey = "dagger.store.tag"

func (loader basicStoreAttachable) Register(srv *grpc.Server) {
	RegisterBasicStoreServer(srv, loader)
}

var BasicStore_serviceDesc = _BasicStore_serviceDesc //nolint:stylecheck

func (loader basicStoreAttachable) WriteTarball(srv BasicStore_WriteTarballServer) error {
	md, ok := metadata.FromIncomingContext(srv.Context())
	if !ok {
		return fmt.Errorf("request lacks metadata: %w", cerrdefs.ErrInvalidArgument)
	}

	values := md[ImageTagKey]
	if len(values) == 0 {
		return fmt.Errorf("request lacks metadata %q: %w", ImageTagKey, cerrdefs.ErrInvalidArgument)
	}
	tag := values[0]

	reader := &TarballReader{ReadF: srv.Recv, CloseF: func() error {
		return srv.SendAndClose(&emptypb.Empty{})
	}}
	err := loader.write(srv.Context(), tag, reader)
	if err != nil {
		return err
	}
	return srv.SendAndClose(&emptypb.Empty{})
}

func (loader basicStoreAttachable) ReadTarball(req *emptypb.Empty, srv BasicStore_ReadTarballServer) error {
	md, ok := metadata.FromIncomingContext(srv.Context())
	if !ok {
		return fmt.Errorf("request lacks metadata: %w", cerrdefs.ErrInvalidArgument)
	}

	values := md[ImageTagKey]
	if len(values) == 0 {
		return fmt.Errorf("request lacks metadata %q: %w", ImageTagKey, cerrdefs.ErrInvalidArgument)
	}
	tag := values[0]

	writer := &TarballWriter{SendF: srv.Send}
	err := loader.read(srv.Context(), tag, writer)
	if err != nil {
		return err
	}
	return nil
}

type TarballReader struct {
	ReadF  func() (*Data, error)
	CloseF func() error
	rem    []byte // remainder buffer
}

func (pio *TarballReader) Read(p []byte) (n int, err error) {
	n = copy(p, pio.rem)
	p = p[n:]
	pio.rem = pio.rem[n:]
	if len(p) == 0 || n != 0 {
		return n, nil
	}
	req, err := pio.ReadF()
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

func (pio *TarballReader) Close() error {
	if pio.CloseF != nil {
		return pio.CloseF()
	}
	return nil
}

type TarballWriter struct {
	SendF  func(*Data) error
	CloseF func() error
}

func (pio *TarballWriter) Write(p []byte) (n int, err error) {
	err = pio.SendF(&Data{Data: p})
	if err != nil {
		return 0, fmt.Errorf("error writing dagger tarball: %w", err)
	}
	return len(p), nil
}

func (pio *TarballWriter) Close() error {
	if pio.CloseF != nil {
		return pio.CloseF()
	}
	return nil
}
