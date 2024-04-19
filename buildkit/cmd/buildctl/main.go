package main

import (
	"fmt"
	"os"

	_ "github.com/moby/buildkit/client/connhelper/dockercontainer"
	_ "github.com/moby/buildkit/client/connhelper/kubepod"
	_ "github.com/moby/buildkit/client/connhelper/nerdctlcontainer"
	_ "github.com/moby/buildkit/client/connhelper/npipe"
	_ "github.com/moby/buildkit/client/connhelper/podmancontainer"
	_ "github.com/moby/buildkit/client/connhelper/ssh"
	bccommon "github.com/moby/buildkit/cmd/buildctl/common"
	"github.com/moby/buildkit/solver/errdefs"
	"github.com/moby/buildkit/util/apicaps"
	"github.com/moby/buildkit/util/appdefaults"
	"github.com/moby/buildkit/util/profiler"
	"github.com/moby/buildkit/util/stack"
	_ "github.com/moby/buildkit/util/tracing/detect/delegated"
	_ "github.com/moby/buildkit/util/tracing/detect/jaeger"
	_ "github.com/moby/buildkit/util/tracing/env"
	"github.com/moby/buildkit/version"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"go.opentelemetry.io/otel"
)

func init() {
	apicaps.ExportedProduct = "buildkit"

	stack.SetVersionInfo(version.Version, version.Revision)

	// do not log tracing errors to stdio
	otel.SetErrorHandler(skipErrors{})
}

func main() {
	cli.VersionPrinter = func(c *cli.Context) {
		fmt.Println(c.App.Name, version.Package, c.App.Version, version.Revision)
	}
	app := cli.NewApp()
	app.Name = "buildctl"
	app.Usage = "build utility"
	app.Version = version.Version

	defaultAddress := os.Getenv("BUILDKIT_HOST")
	if defaultAddress == "" {
		defaultAddress = appdefaults.Address
	}

	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "debug",
			Usage: "enable debug output in logs",
		},
		cli.StringFlag{
			Name:  "addr",
			Usage: "buildkitd address",
			Value: defaultAddress,
		},
		// Add format flag to control log formatter
		cli.StringFlag{
			Name:  "log-format",
			Usage: "log formatter: json or text",
			Value: "text",
		},
		cli.StringFlag{
			Name:  "tlsservername",
			Usage: "buildkitd server name for certificate validation",
			Value: "",
		},
		cli.StringFlag{
			Name:  "tlscacert",
			Usage: "CA certificate for validation",
			Value: "",
		},
		cli.StringFlag{
			Name:  "tlscert",
			Usage: "client certificate",
			Value: "",
		},
		cli.StringFlag{
			Name:  "tlskey",
			Usage: "client key",
			Value: "",
		},
		cli.StringFlag{
			Name:  "tlsdir",
			Usage: "directory containing CA certificate, client certificate, and client key",
			Value: "",
		},
		cli.IntFlag{
			Name:  "timeout",
			Usage: "timeout backend connection after value seconds",
			Value: 5,
		},
		cli.BoolFlag{
			Name:  "wait",
			Usage: "block RPCs until the connection becomes available",
		},
	}

	app.Commands = []cli.Command{
		diskUsageCommand,
		pruneCommand,
		pruneHistoriesCommand,
		buildCommand,
		debugCommand,
		dialStdioCommand,
	}

	var debugEnabled bool

	app.Before = func(context *cli.Context) error {
		debugEnabled = context.GlobalBool("debug")
		// Use Format flag to control log formatter
		logFormat := context.GlobalString("log-format")
		switch logFormat {
		case "json":
			logrus.SetFormatter(&logrus.JSONFormatter{})
		case "text", "":
			logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
		default:
			return errors.Errorf("unsupported log type %q", logFormat)
		}
		if debugEnabled {
			logrus.SetLevel(logrus.DebugLevel)
		}
		return nil
	}

	if err := bccommon.AttachAppContext(app); err != nil {
		handleErr(debugEnabled, err)
	}

	profiler.Attach(app)

	handleErr(debugEnabled, app.Run(os.Args))
}

func handleErr(debug bool, err error) {
	if err == nil {
		return
	}
	for _, s := range errdefs.Sources(err) {
		s.Print(os.Stderr)
	}
	if debug {
		fmt.Fprintf(os.Stderr, "error: %+v", stack.Formatter(err))
	} else {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	os.Exit(1)
}

type skipErrors struct{}

func (skipErrors) Handle(err error) {}
