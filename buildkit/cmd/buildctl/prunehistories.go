package main

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/hashicorp/go-multierror"
	controlapi "github.com/moby/buildkit/api/services/control"
	bccommon "github.com/moby/buildkit/cmd/buildctl/common"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var pruneHistoriesCommand = cli.Command{
	Name:   "prune-histories",
	Usage:  "clean up build histories",
	Action: pruneHistories,
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "format",
			Usage: "Format the output using the given Go template, e.g, '{{json .}}'",
		},
	},
}

func pruneHistories(clicontext *cli.Context) error {
	c, err := bccommon.ResolveClient(clicontext)
	if err != nil {
		return err
	}

	ctx := appcontext.Context()
	controlClient := c.ControlClient()
	resp, err := controlClient.ListenBuildHistory(ctx, &controlapi.BuildHistoryRequest{
		EarlyExit: true,
	})
	if err != nil {
		return err
	}

	var rerr error
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
			if ev.Record == nil || ev.Record.Pinned {
				continue
			}
			if _, err := controlClient.UpdateBuildHistory(ctx, &controlapi.UpdateBuildHistoryRequest{
				Ref:    ev.Record.Ref,
				Delete: true,
			}); err != nil {
				rerr = multierror.Append(rerr, err).ErrorOrNil()
				continue
			}
			if err := tmpl.Execute(clicontext.App.Writer, ev); err != nil {
				return err
			}
			if _, err = fmt.Fprintf(clicontext.App.Writer, "\n"); err != nil {
				return err
			}
		}
		return rerr
	}
	return pruneHistoriesWithTableOutput(ctx, clicontext.App.Writer, controlClient, resp)
}

func pruneHistoriesWithTableOutput(ctx context.Context, w io.Writer, controlClient controlapi.ControlClient,
	eventReceiver controlapi.Control_ListenBuildHistoryClient) error {
	tw := tabwriter.NewWriter(w, 1, 8, 1, '\t', 0)
	fmt.Fprintln(tw, "TYPE\tREF\tCREATED\tCOMPLETED\tGENERATION")
	var rerr error
	for {
		ev, err := eventReceiver.Recv()
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return err
		}
		r := ev.Record
		if r == nil || r.Pinned {
			continue
		}
		if _, err := controlClient.UpdateBuildHistory(ctx, &controlapi.UpdateBuildHistoryRequest{
			Ref:    ev.Record.Ref,
			Delete: true,
		}); err != nil {
			rerr = multierror.Append(rerr, err).ErrorOrNil()
			continue
		}
		var (
			createdAt   string
			completedAt string
		)
		if r.CreatedAt != nil {
			createdAt = r.CreatedAt.Local().Format(time.RFC3339)
		}
		if r.CompletedAt != nil {
			completedAt = r.CompletedAt.Local().Format(time.RFC3339)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\n", ev.Type, r.Ref, createdAt, completedAt, r.Generation)
		tw.Flush()
	}
	tw.Flush()
	return rerr
}
