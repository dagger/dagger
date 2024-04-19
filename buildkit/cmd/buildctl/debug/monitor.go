package debug

import (
	"fmt"

	controlapi "github.com/moby/buildkit/api/services/control"
	bccommon "github.com/moby/buildkit/cmd/buildctl/common"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/urfave/cli"
)

var MonitorCommand = cli.Command{
	Name:   "monitor",
	Usage:  "display build events",
	Action: monitor,
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "completed",
			Usage: "show completed builds",
		},
		cli.StringFlag{
			Name:  "ref",
			Usage: "show events for a specific build",
		},
	},
}

func monitor(clicontext *cli.Context) error {
	c, err := bccommon.ResolveClient(clicontext)
	if err != nil {
		return err
	}
	completed := clicontext.Bool("completed")

	ctx := appcontext.Context()

	cl, err := c.ControlClient().ListenBuildHistory(ctx, &controlapi.BuildHistoryRequest{
		ActiveOnly: !completed,
		Ref:        clicontext.String("ref"),
	})
	if err != nil {
		return err
	}

	for {
		ev, err := cl.Recv()
		if err != nil {
			return err
		}
		fmt.Printf("event: %s ref:%s\n", ev.Type.String(), ev.Record.Ref)
		if ev.Record.NumTotalSteps != 0 {
			fmt.Printf("  cache: %d/%d\n", ev.Record.NumCachedSteps, ev.Record.NumTotalSteps)
		}
		if ev.Record.Logs != nil {
			fmt.Printf("  logs: %s\n", ev.Record.Logs)
		}
		if ev.Record.Trace != nil {
			fmt.Printf("  trace: %s\n", ev.Record.Trace)
		}

		if ev.Record.Result != nil {
			if len(ev.Record.Result.Results) > 0 {
				for _, res := range ev.Record.Result.Results {
					fmt.Printf("  descriptor: %s\n", res)
				}
			} else if ev.Record.Result.ResultDeprecated != nil {
				fmt.Printf("  descriptor: %s\n", ev.Record.Result.ResultDeprecated)
			}
			for _, att := range ev.Record.Result.Attestations {
				fmt.Printf("  attestation: %s\n", att)
			}
		}
		for k, res := range ev.Record.Results {
			if len(ev.Record.Result.Results) > 0 {
				for _, res := range res.Results {
					fmt.Printf("  [%s] descriptor: %s\n", k, res)
				}
			} else if res.ResultDeprecated != nil {
				fmt.Printf("  [%s] descriptor: %s\n", k, res.ResultDeprecated)
			}
			for _, att := range res.Attestations {
				fmt.Printf("  [%s] attestation: %s\n", k, att)
			}
		}
	}
}
