package main

import (
	"context"

	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/client"
)

var watchCmd = &cobra.Command{
	Use:    "watch [flags] COMMAND",
	Hidden: true,
	Annotations: map[string]string{
		"experimental": "true",
	},
	Aliases: []string{"w"},
	Short:   "Watch activity across all Dagger sessions.",
	Example: `dagger watch`,
	RunE:    Watch,
}

func Watch(cmd *cobra.Command, _ []string) error {
	// HACK: the PubSub service treats the 000000000 trace ID as "subscribe to
	// everything", and the client subscribes to its current trace ID, so let's
	// just zero it out.
	ctx := trace.ContextWithSpanContext(cmd.Context(), trace.SpanContext{})

	return withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
		<-ctx.Done()
		return ctx.Err()
	})
}
