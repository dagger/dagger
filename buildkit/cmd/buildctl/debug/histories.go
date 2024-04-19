package debug

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	controlapi "github.com/moby/buildkit/api/services/control"
	bccommon "github.com/moby/buildkit/cmd/buildctl/common"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var HistoriesCommand = cli.Command{
	Name:   "histories",
	Usage:  "list build records",
	Action: histories,
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "format",
			Usage: "Format the output using the given Go template, e.g, '{{json .}}'",
		},
	},
}

func histories(clicontext *cli.Context) error {
	c, err := bccommon.ResolveClient(clicontext)
	if err != nil {
		return err
	}

	ctx := appcontext.Context()
	resp, err := c.ControlClient().ListenBuildHistory(ctx, &controlapi.BuildHistoryRequest{
		EarlyExit: true,
	})
	if err != nil {
		return err
	}

	if format := clicontext.String("format"); format != "" {
		tmpl, err := bccommon.ParseTemplate(format)
		if err != nil {
			return err
		}
		for {
			ev, err := resp.Recv()
			if errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return err
			}
			if err := tmpl.Execute(clicontext.App.Writer, ev); err != nil {
				return err
			}
			if _, err = fmt.Fprintf(clicontext.App.Writer, "\n"); err != nil {
				return err
			}
		}
		return nil
	}
	return printRecordsTable(clicontext.App.Writer, resp)
}

func printRecordsTable(w io.Writer, eventReceiver controlapi.Control_ListenBuildHistoryClient) error {
	tw := tabwriter.NewWriter(w, 1, 8, 1, '\t', 0)
	fmt.Fprintln(tw, "TYPE\tREF\tCREATED\tCOMPLETED\tGENERATION\tPINNED")
	for {
		ev, err := eventReceiver.Recv()
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return err
		}
		var (
			ref         string
			createdAt   string
			completedAt string
			generation  int32
			pinned      string
		)
		if r := ev.Record; r != nil {
			ref = r.Ref
			if r.CreatedAt != nil {
				createdAt = r.CreatedAt.Local().Format(time.RFC3339)
			}
			if r.CompletedAt != nil {
				completedAt = r.CompletedAt.Local().Format(time.RFC3339)
			}
			generation = r.Generation
			if r.Pinned {
				pinned = "*"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n", ev.Type, ref, createdAt, completedAt, generation, pinned)
		tw.Flush()
	}
	return tw.Flush()
}
