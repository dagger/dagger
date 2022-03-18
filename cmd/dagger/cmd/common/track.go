package common

import (
	"context"
	"strings"

	"github.com/spf13/cobra"
	"go.dagger.io/dagger/telemetry"
)

// TrackCommand sends telemetry about a command execution
func TrackCommand(ctx context.Context, cmd *cobra.Command, props ...*telemetry.Property) chan struct{} {
	props = append([]*telemetry.Property{
		{
			Name:  "command",
			Value: commandName(cmd),
		},
	}, props...)

	return telemetry.TrackAsync(ctx, "Command Executed", props...)
}

func commandName(cmd *cobra.Command) string {
	parts := []string{}
	for c := cmd; c.Parent() != nil; c = c.Parent() {
		parts = append([]string{c.Name()}, parts...)
	}
	return strings.Join(parts, " ")
}
