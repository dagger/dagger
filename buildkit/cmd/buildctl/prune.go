package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/moby/buildkit/client"
	bccommon "github.com/moby/buildkit/cmd/buildctl/common"
	"github.com/moby/buildkit/util/bklog"
	"github.com/tonistiigi/units"
	"github.com/urfave/cli"
)

var pruneCommand = cli.Command{
	Name:   "prune",
	Usage:  "clean up build cache",
	Action: prune,
	Flags: []cli.Flag{
		cli.DurationFlag{
			Name:  "keep-duration",
			Usage: "Keep data newer than this limit",
		},
		cli.Float64Flag{
			Name:  "keep-storage",
			Usage: "Keep data below this limit (in MB)",
		},
		cli.StringSliceFlag{
			Name:  "filter, f",
			Usage: "Filter records",
		},
		cli.BoolFlag{
			Name:  "all",
			Usage: "Include internal/frontend references",
		},
		cli.BoolFlag{
			Name:  "verbose, v",
			Usage: "Verbose output",
		},
		cli.StringFlag{
			Name:  "format",
			Usage: "Format the output using the given Go template, e.g, '{{json .}}'",
		},
	},
}

func prune(clicontext *cli.Context) error {
	c, err := bccommon.ResolveClient(clicontext)
	if err != nil {
		return err
	}

	ch := make(chan client.UsageInfo)
	printed := make(chan struct{})
	var summarizer func()

	opts := []client.PruneOption{
		client.WithFilter(clicontext.StringSlice("filter")),
		client.WithKeepOpt(clicontext.Duration("keep-duration"), int64(clicontext.Float64("keep-storage")*1e6)),
	}

	if clicontext.Bool("all") {
		opts = append(opts, client.PruneAll)
	}

	if format := clicontext.String("format"); format != "" {
		if clicontext.Bool("verbose") {
			bklog.L.Debug("Ignoring --verbose")
		}
		tmpl, err := bccommon.ParseTemplate(format)
		if err != nil {
			return err
		}
		go func() {
			defer close(printed)
			for du := range ch {
				// Unlike `buildctl du`, the template is applied to a UsageInfo, not to a slice of UsageInfo
				if err := tmpl.Execute(clicontext.App.Writer, du); err != nil {
					panic(err)
				}
				if _, err = fmt.Fprintf(clicontext.App.Writer, "\n"); err != nil {
					panic(err)
				}
			}
		}()
	} else {
		tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, '\t', 0)
		first := true
		total := int64(0)
		go func() {
			defer close(printed)
			for du := range ch {
				total += du.Size
				if clicontext.Bool("verbose") {
					printVerbose(tw, []*client.UsageInfo{&du})
				} else {
					if first {
						printTableHeader(tw)
						first = false
					}
					printTableRow(tw, &du)
					tw.Flush()
				}
			}
		}()
		summarizer = func() {
			tw = tabwriter.NewWriter(os.Stdout, 1, 8, 1, '\t', 0)
			fmt.Fprintf(tw, "Total:\t%.2f\n", units.Bytes(total))
			tw.Flush()
		}
	}

	err = c.Prune(bccommon.CommandContext(clicontext), ch, opts...)
	close(ch)
	<-printed
	if err != nil {
		return err
	}
	if summarizer != nil {
		summarizer()
	}
	return nil
}
