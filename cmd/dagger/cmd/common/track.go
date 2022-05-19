package common

import (
	"context"
	"strings"

	"github.com/spf13/cobra"
	"go.dagger.io/dagger/analytics"
)

// TrackCommand sends analytics about a command execution
func TrackCommand(ctx context.Context, cmd *cobra.Command, props ...*analytics.Property) chan struct{} {
	props = append([]*analytics.Property{
		{
			Name:  "command",
			Value: commandName(cmd),
		},
	}, props...)

	return analytics.TrackAsync(ctx, "Command Executed", props...)
}

func commandName(cmd *cobra.Command) string {
	parts := []string{}
	for c := cmd; c.Parent() != nil; c = c.Parent() {
		parts = append([]string{c.Name()}, parts...)
	}
	return strings.Join(parts, " ")
}
