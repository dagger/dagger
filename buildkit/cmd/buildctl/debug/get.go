package debug

import (
	"io"
	"os"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/proxy"
	bccommon "github.com/moby/buildkit/cmd/buildctl/common"
	"github.com/moby/buildkit/util/appcontext"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var GetCommand = cli.Command{
	Name:   "get",
	Usage:  "retrieve a blob from contentstore",
	Action: get,
}

func get(clicontext *cli.Context) error {
	args := clicontext.Args()
	if len(args) == 0 {
		return errors.Errorf("blob digest must be specified")
	}

	dgst, err := digest.Parse(args[0])
	if err != nil {
		return err
	}

	c, err := bccommon.ResolveClient(clicontext)
	if err != nil {
		return err
	}

	ctx := appcontext.Context()

	store := proxy.NewContentStore(c.ContentClient())
	ra, err := store.ReaderAt(ctx, ocispecs.Descriptor{
		Digest: dgst,
	})
	if err != nil {
		return err
	}
	defer ra.Close()

	// use 1MB buffer like we do for ingesting
	buf := make([]byte, 1<<20)
	_, err = io.CopyBuffer(os.Stdout, content.NewReader(ra), buf)
	return err
}
