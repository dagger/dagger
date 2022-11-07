package filesend

import (
	context "context"
	fmt "fmt"
	io "io"
	"log"
	"os"
	"path/filepath"
	strings "strings"

	"github.com/c4milo/unpackit"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
)

// NewReceiver allows unpacking tar streams dynamically to directories
func NewReceiver(wd string) *fsSyncTarget {
	return &fsSyncTarget{
		workdir: wd,
	}
}

type fsSyncTarget struct {
	workdir string
}

func (sp *fsSyncTarget) Register(server *grpc.Server) {
	RegisterFileSendServer(server, sp)
}

func (sp *fsSyncTarget) TarStream(stream FileSend_TarStreamServer) (err error) {
	defer func() {
		log.Println("TAR STREAM DONE", err)
	}()

	msg, err := stream.Recv()
	if err != nil {
		return err
	}

	init := msg.GetInit()
	if init == nil {
		return fmt.Errorf("must initialize stream with destination path")
	}

	dest, err := sp.normalizeDest(init.GetDest())
	if err != nil {
		return err
	}

	ctx := context.Background()

	if init.GetUnpack() {
		r, wc := io.Pipe()

		if err := os.MkdirAll(dest, 0700); err != nil {
			return errors.Wrapf(err, "failed to create synctarget dest dir %s", dest)
		}

		go func() {
			<-ctx.Done()
			wc.CloseWithError(ctx.Err())
		}()

		go func() {
			err := sp.writeBytes(wc, stream)
			if err != nil {
				wc.CloseWithError(err)
			}
		}()

		err = unpackit.Untar(r, dest)
		if err != nil {
			return err
		}
	} else {
		wc, err := os.Create(dest)
		if err != nil {
			return err
		}

		err = sp.writeBytes(wc, stream)
		if err != nil {
			return err
		}
	}

	return stream.SendAndClose(&StreamResponse{})
}

func (sp *fsSyncTarget) writeBytes(wc io.WriteCloser, stream FileSend_TarStreamServer) error {
	for {
		msg, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			log.Println("STREAM RECV ERR", err)
			return err
		}

		bytes := msg.GetBytes()
		if bytes == nil {
			return fmt.Errorf("got non-bytes message: %s", msg.String())
		}

		log.Println("FILESEND WRITING", len(bytes.GetData()))
		_, err = wc.Write(bytes.GetData())
		if err != nil {
			log.Println("FILESEND WRITE ERR", err)
			return err
		}
	}

	return wc.Close()
}

func (sp *fsSyncTarget) normalizeDest(dest string) (string, error) {
	if filepath.IsAbs(dest) {
		return dest, nil
	}

	wd, err := filepath.EvalSymlinks(sp.workdir)
	if err != nil {
		return "", err
	}

	dest = filepath.Clean(filepath.Join(wd, dest))

	if dest == wd {
		// writing directly to workdir
		return dest, nil
	}

	if !strings.HasPrefix(dest, wd+"/") {
		// writing outside of workdir
		return "", fmt.Errorf("destination %q escapes workdir", dest)
	}

	// writing within workdir
	return dest, nil
}
