package debug

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/containerd/containerd/platforms"
	"github.com/moby/buildkit/client"
	bccommon "github.com/moby/buildkit/cmd/buildctl/common"
	"github.com/moby/buildkit/util/bklog"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/tonistiigi/units"
	"github.com/urfave/cli"
)

var WorkersCommand = cli.Command{
	Name:   "workers",
	Usage:  "list workers",
	Action: listWorkers,
	Flags: []cli.Flag{
		cli.StringSliceFlag{
			Name:  "filter, f",
			Usage: "containerd-style filter string slice",
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

func listWorkers(clicontext *cli.Context) error {
	c, err := bccommon.ResolveClient(clicontext)
	if err != nil {
		return err
	}

	workers, err := c.ListWorkers(commandContext(clicontext), client.WithFilter(clicontext.StringSlice("filter")))
	if err != nil {
		return err
	}
	if format := clicontext.String("format"); format != "" {
		if clicontext.Bool("verbose") {
			bklog.L.Debug("Ignoring --verbose")
		}
		tmpl, err := bccommon.ParseTemplate(format)
		if err != nil {
			return err
		}
		if err := tmpl.Execute(clicontext.App.Writer, workers); err != nil {
			return err
		}
		_, err = fmt.Fprintf(clicontext.App.Writer, "\n")
		return err
	}

	tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, '\t', 0)

	if clicontext.Bool("verbose") {
		printWorkersVerbose(tw, workers)
	} else {
		printWorkersTable(tw, workers)
	}
	return nil
}

func printWorkersVerbose(tw *tabwriter.Writer, winfo []*client.WorkerInfo) {
	for _, wi := range winfo {
		fmt.Fprintf(tw, "ID:\t%s\n", wi.ID)
		fmt.Fprintf(tw, "Platforms:\t%s\n", joinPlatforms(wi.Platforms))
		fmt.Fprintf(tw, "BuildKit:\t%s %s %s\n", wi.BuildkitVersion.Package, wi.BuildkitVersion.Version, wi.BuildkitVersion.Revision)
		fmt.Fprintf(tw, "Labels:\n")
		for _, k := range sortedKeys(wi.Labels) {
			v := wi.Labels[k]
			fmt.Fprintf(tw, "\t%s:\t%s\n", k, v)
		}
		for i, rule := range wi.GCPolicy {
			fmt.Fprintf(tw, "GC Policy rule#%d:\n", i)
			fmt.Fprintf(tw, "\tAll:\t%v\n", rule.All)
			if len(rule.Filter) > 0 {
				fmt.Fprintf(tw, "\tFilters:\t%s\n", strings.Join(rule.Filter, " "))
			}
			if rule.KeepDuration > 0 {
				fmt.Fprintf(tw, "\tKeep Duration:\t%v\n", rule.KeepDuration.String())
			}
			if rule.KeepBytes > 0 {
				fmt.Fprintf(tw, "\tKeep Bytes:\t%g\n", units.Bytes(rule.KeepBytes))
			}
		}
		fmt.Fprintf(tw, "\n")
	}

	tw.Flush()
}

func printWorkersTable(tw *tabwriter.Writer, winfo []*client.WorkerInfo) {
	fmt.Fprintln(tw, "ID\tPLATFORMS")

	for _, wi := range winfo {
		id := wi.ID
		fmt.Fprintf(tw, "%s\t%s\n", id, joinPlatforms(wi.Platforms))
	}

	tw.Flush()
}

func sortedKeys(m map[string]string) []string {
	s := make([]string, len(m))
	i := 0
	for k := range m {
		s[i] = k
		i++
	}
	sort.Strings(s)
	return s
}

func commandContext(c *cli.Context) context.Context {
	return c.App.Metadata["context"].(context.Context)
}

func joinPlatforms(p []ocispecs.Platform) string {
	str := make([]string, 0, len(p))
	for _, pp := range p {
		str = append(str, platforms.Format(platforms.Normalize(pp)))
	}
	return strings.Join(str, ",")
}
