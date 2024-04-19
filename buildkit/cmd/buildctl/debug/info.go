package debug

import (
	"fmt"
	"os"
	"text/tabwriter"

	bccommon "github.com/moby/buildkit/cmd/buildctl/common"
	"github.com/urfave/cli"
)

var InfoCommand = cli.Command{
	Name:   "info",
	Usage:  "display internal information",
	Action: info,
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "format",
			Usage: "Format the output using the given Go template, e.g, '{{json .}}'",
		},
	},
}

func info(clicontext *cli.Context) error {
	c, err := bccommon.ResolveClient(clicontext)
	if err != nil {
		return err
	}
	res, err := c.Info(bccommon.CommandContext(clicontext))
	if err != nil {
		return err
	}
	if format := clicontext.String("format"); format != "" {
		tmpl, err := bccommon.ParseTemplate(format)
		if err != nil {
			return err
		}
		if err := tmpl.Execute(clicontext.App.Writer, res); err != nil {
			return err
		}
		_, err = fmt.Fprintf(clicontext.App.Writer, "\n")
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', 0)
	_, _ = fmt.Fprintf(w, "BuildKit:\t%s %s %s\n", res.BuildkitVersion.Package, res.BuildkitVersion.Version, res.BuildkitVersion.Revision)
	return w.Flush()
}
