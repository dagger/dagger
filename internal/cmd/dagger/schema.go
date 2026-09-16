package daggercmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/cmd/codegen/introspection/sdl"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
)

var (
	schemaJSONOutput bool
	schemaCoreOnly   bool
)

var apiSchemaCmd = &cobra.Command{
	Use:   "schema [options] [module...]",
	Short: "Print the API schema currently served by the engine",
	Long: `Print the API schema the engine is currently serving, in GraphQL schema
definition language (SDL).

The schema combines Dagger's core types with the schema extensions
contributed by every module loaded in the current workspace, so what it
prints is exactly the API available to this session — the same surface
that "dagger api query" and the generated SDK clients see.

Name one or more modules to print only the types and Query fields they
contribute.`,
	Example: `dagger api schema
dagger api schema golang
dagger api schema --core
dagger api schema --json`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if isPrintTraceLinkEnabled(cmd.Annotations) {
			cmd.SetContext(idtui.WithPrintTraceLink(cmd.Context(), true))
		}

		_, explicitModRefSet := getExplicitModuleSourceRef()
		return withEngine(cmd.Context(), client.Params{
			LoadWorkspaceModules: !moduleNoURL && !explicitModRefSet,
			SingleQuery:          true,
		}, func(ctx context.Context, engineClient *client.Client) error {
			return Schema(ctx, engineClient, cmd, args)
		})
	},
	Annotations: map[string]string{
		printTraceLinkKey: "true",
	},
}

// Schema introspects the schema served to this session and writes it to the
// command's output, as SDL by default or as raw introspection JSON with
// --json.
func Schema(
	ctx context.Context,
	engineClient *client.Client,
	cmd *cobra.Command,
	args []string,
) error {
	resp, err := runIntrospection(ctx, engineClient)
	if err != nil {
		return err
	}
	return renderSchema(cmd.OutOrStdout(), resp, args, schemaCoreOnly, schemaJSONOutput)
}

// renderSchema filters the introspected schema and writes it out. It is split
// from Schema so the filtering and output selection are testable without an
// engine session.
func renderSchema(
	out io.Writer,
	resp *introspection.Response,
	modules []string,
	coreOnly bool,
	asJSON bool,
) error {
	schema := resp.Schema
	switch {
	case len(modules) > 0:
		// Include keeps the named modules' own types plus the Query fields and
		// engine-contributed extensions belonging to them.
		schema = schema.Include(modules...)
	case coreOnly:
		schema = coreOnlySchema(schema)
	}

	if asJSON {
		encoded, err := json.MarshalIndent(introspection.Response{
			Schema:        schema,
			SchemaVersion: resp.SchemaVersion,
		}, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal introspection JSON: %w", err)
		}
		fmt.Fprintf(out, "%s\n", encoded)
		return nil
	}

	sdl.Format(out, schema)
	return nil
}

// coreOnlySchema returns a copy of the schema with every module contribution
// removed: the types a module owns outright, plus module-owned fields, input
// fields and enum values on the core types that remain.
//
// introspection.Exclude is not sufficient on its own. It only strips
// module-owned fields from the types in ExtendableTypes (just Query), so a
// field the engine contributed onto any other core type for a module —
// Binding.asXXX and friends — survives it. --core promises no module surface
// at all, so the field-level strip happens here rather than in Exclude, which
// SDK codegen file-splitting also depends on.
//
// Checking each element's own sourceMap also avoids DependencyNames, which
// only sees type-level attribution and so misses a module that contributes
// fields without owning a type.
func coreOnlySchema(s *introspection.Schema) *introspection.Schema {
	out := &introspection.Schema{
		QueryType:        s.QueryType,
		MutationType:     s.MutationType,
		SubscriptionType: s.SubscriptionType,
		Directives:       s.Directives,
	}
	for _, t := range s.Types {
		if sourceMapModule(t.Directives) != "" {
			continue
		}
		out.Types = append(out.Types, stripModuleMembers(t))
	}
	return out
}

// stripModuleMembers copies t without the members a module contributed. The
// original is left untouched, so the caller's response stays reusable.
func stripModuleMembers(t *introspection.Type) *introspection.Type {
	out := *t

	out.Fields = nil
	for _, f := range t.Fields {
		if sourceMapModule(f.Directives) == "" {
			out.Fields = append(out.Fields, f)
		}
	}

	out.InputFields = nil
	for _, f := range t.InputFields {
		if sourceMapModule(f.Directives) == "" {
			out.InputFields = append(out.InputFields, f)
		}
	}

	out.EnumValues = nil
	for _, v := range t.EnumValues {
		if sourceMapModule(v.Directives) == "" {
			out.EnumValues = append(out.EnumValues, v)
		}
	}

	return &out
}

// sourceMapModule returns the module named by an @sourceMap directive, or ""
// when the element carries no module attribution.
func sourceMapModule(directives introspection.Directives) string {
	if sm := directives.SourceMap(); sm != nil {
		return sm.Module
	}
	return ""
}

func runIntrospection(
	ctx context.Context,
	engineClient *client.Client,
) (*introspection.Response, error) {
	var resp introspection.Response
	if err := engineClient.Do(
		ctx,
		introspection.Query,
		"IntrospectionQuery",
		nil,
		&resp,
	); err != nil {
		return nil, fmt.Errorf("introspection query: %w", err)
	}
	if resp.Schema == nil {
		return nil, fmt.Errorf("introspection response has no __schema field")
	}
	return &resp, nil
}

func init() {
	apiSchemaCmd.Flags().BoolVar(&schemaJSONOutput, "json", false,
		"Print the raw introspection JSON instead of SDL")
	apiSchemaCmd.Flags().BoolVar(&schemaCoreOnly, "core", false,
		"Print only the core API, excluding schema extensions from modules")
	apiSchemaCmd.MarkFlagsMutuallyExclusive("core", "json")
}
