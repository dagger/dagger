package debug

import (
	"io"
	"os"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/proxy"
	controlapi "github.com/moby/buildkit/api/services/control"
	"github.com/moby/buildkit/client"
	bccommon "github.com/moby/buildkit/cmd/buildctl/common"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/progress/progresswriter"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var LogsCommand = cli.Command{
	Name:   "logs",
	Usage:  "display build logs",
	Action: logs,
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "progress",
			Usage: "progress output type",
			Value: "auto",
		},
		cli.BoolFlag{
			Name:  "trace",
			Usage: "show opentelemetry trace",
		},
	},
}

func logs(clicontext *cli.Context) error {
	args := clicontext.Args()
	if len(args) == 0 {
		return errors.Errorf("build ref must be specified")
	}
	ref := args[0]

	c, err := bccommon.ResolveClient(clicontext)
	if err != nil {
		return err
	}

	ctx := appcontext.Context()

	if clicontext.Bool("trace") {
		cl, err := c.ControlClient().ListenBuildHistory(ctx, &controlapi.BuildHistoryRequest{
			Ref: ref,
		})
		if err != nil {
			return err
		}
		he, err := cl.Recv()
		if err != nil {
			if err == io.EOF {
				return errors.Errorf("ref %s not found", ref)
			}
			return err
		}
		if he.Record.Trace == nil {
			return errors.Errorf("ref %s does not have trace", ref)
		}
		store := proxy.NewContentStore(c.ContentClient())
		ra, err := store.ReaderAt(ctx, ocispecs.Descriptor{
			Digest:    he.Record.Trace.Digest,
			Size:      he.Record.Trace.Size_,
			MediaType: he.Record.Trace.MediaType,
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

	cl, err := c.ControlClient().Status(ctx, &controlapi.StatusRequest{
		Ref: ref,
	})
	if err != nil {
		return err
	}

	pw, err := progresswriter.NewPrinter(ctx, os.Stdout, clicontext.String("progress"))
	if err != nil {
		return err
	}

	defer func() {
		<-pw.Done()
	}()

	for {
		resp, err := cl.Recv()
		if err != nil {
			close(pw.Status())
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		pw.Status() <- client.NewSolveStatus(resp)
	}
}
