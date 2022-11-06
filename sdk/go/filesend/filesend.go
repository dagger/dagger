package filesend

import (
	context "context"
	fmt "fmt"
	io "io"
	"os"

	"github.com/c4milo/unpackit"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

// NewReceiver allows unpacking tar streams dynamically to directories
func NewReceiver() *fsSyncTarget {
	return &fsSyncTarget{}
}

type fsSyncTarget struct {
}

func (sp *fsSyncTarget) Register(server *grpc.Server) {
	RegisterFileSendServer(server, sp)
}

func (sp *fsSyncTarget) TarStream(stream FileSend_TarStreamServer) (err error) {
	msg, err := stream.Recv()
	if err != nil {
		return err
	}

	init := msg.GetInit()
	if init == nil {
		return fmt.Errorf("must initialize stream with destination path")
	}

	dest := init.GetDest()

	ctx := context.Background()
	eg, ctx := errgroup.WithContext(ctx)

	var wc io.WriteCloser
	if init.GetUnpack() {
		var r io.Reader
		r, wc = io.Pipe()

		if err := os.MkdirAll(dest, 0700); err != nil {
			return errors.Wrapf(err, "failed to create synctarget dest dir %s", dest)
		}

		eg.Go(func() error {
			<-ctx.Done()
			return wc.Close()
		})

		eg.Go(func() error {
			// TODO(vito): it would be faster to just shell out to `tar` if
			// available
			return unpackit.Untar(r, dest)
		})
	} else {
		wc, err = os.Create(dest)
		if err != nil {
			return err
		}
	}

	eg.Go(func() error {
		defer wc.Close()

		for {
			msg, err := stream.Recv()
			if err != nil {
				return err
			}

			bytes := msg.GetBytes()
			if bytes == nil {
				return err
			}

			_, err = wc.Write(bytes.GetData())
			if err != nil {
				return err
			}
		}
	})

	return eg.Wait()
}
