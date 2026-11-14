package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/cmd/codegen/introspection/sdl"
	"github.com/dagger/dagger/engine/client"
)

var apiSchemaCmd = &cobra.Command{
	Use:   "schema [options]",
	Short: "Print the API schema in GraphQL schema language",
	Long: strings.ReplaceAll(`Print the API schema served by the Dagger engine, in GraphQL schema
language (SDL).

The schema is the one ´dagger api query´ runs against: Dagger's core types,
plus the types of the modules loaded for the current workspace, or of the
module selected with ´-m´.`,
		"´",
		"`",
	),
	Example: strings.TrimSpace(`
dagger api schema                     # Core API and current workspace modules
dagger -m core api schema             # Core API only
dagger -m github.com/acme/mod api schema > schema.graphqls
`,
	),
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, explicitModRefSet := getExplicitModuleSourceRef()
		return withEngine(cmd.Context(), client.Params{
			LoadWorkspaceModules: !moduleNoURL && !explicitModRefSet,
			SingleQuery:          true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			return printAPISchema(ctx, engineClient, cmd.OutOrStdout())
		})
	},
}

func printAPISchema(ctx context.Context, engineClient *client.Client, w io.Writer) error {
	var res introspection.Response
	if err := engineClient.Do(ctx, introspection.Query, "IntrospectionQuery", nil, &res); err != nil {
		return fmt.Errorf("introspect schema: %w", err)
	}
	if res.Schema == nil {
		return errors.New("introspect schema: empty response")
	}
	sdl.Format(w, res.Schema)
	return nil
}
