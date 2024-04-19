package common

import (
	"context"
	"os"

	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/tracing/detect"
	"github.com/urfave/cli"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func AttachAppContext(app *cli.App) error {
	ctx := appcontext.Context()

	tp, err := detect.TracerProvider()
	if err != nil {
		return err
	}
	tracer := tp.Tracer("")

	var span trace.Span

	for i, cmd := range app.Commands {
		func(before cli.BeforeFunc) {
			name := cmd.Name
			app.Commands[i].Before = func(clicontext *cli.Context) error {
				if before != nil {
					if err := before(clicontext); err != nil {
						return err
					}
				}

				ctx, span = tracer.Start(ctx, name, trace.WithAttributes(
					attribute.StringSlice("command", os.Args),
				))

				clicontext.App.Metadata["context"] = ctx
				return nil
			}
		}(cmd.Before)
	}

	app.ExitErrHandler = func(clicontext *cli.Context, err error) {
		if span != nil && err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		cli.HandleExitCoder(err)
	}

	after := app.After
	app.After = func(clicontext *cli.Context) error {
		if after != nil {
			if err := after(clicontext); err != nil {
				return err
			}
		}
		if span != nil {
			span.End()
		}
		return detect.Shutdown(context.TODO())
	}
	return nil
}

func CommandContext(c *cli.Context) context.Context {
	return c.App.Metadata["context"].(context.Context)
}
