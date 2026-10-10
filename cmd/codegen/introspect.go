package main

import (
	"encoding/json"
	"fmt"
	"os"

	"dagger.io/dagger"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/spf13/cobra"
)

var (
	outputSchema string
	namesOut     string
	namesFormats string
)

var introspectCmd = &cobra.Command{
	Use:   "introspect",
	Short: "Write the engine's introspection JSON, and optionally its formatted names",
	Long: `Write the engine's introspection JSON, and optionally its formatted names.

With --names-out and --names, also write a sidecar JSON file with every schema
name (type, field, argument, input field and enum value names, except
introspection names) formatted by the engine in each requested format, for
SDK codegen that runs without an engine connection:

  codegen introspect -o schema.json \
    --names-out names.json --names SNAKE:UPPERCASE,PASCAL:CAPITALIZED

writes

  {
    "PASCAL:CAPITALIZED": {"httpClient": "HttpClient", ...},
    "SNAKE:UPPERCASE": {"httpClient": "http_client", ...}
  }

A format is CASING:ACRONYMS, with the values of the engine's Casing and
AcronymStyle enums. A schema without Query.formatIdentifiers (engines or
schema views before v1.0.0-0) gets an empty object: codegen then keeps its
legacy converter. A name missing from a format's map (like "_") also falls back
to it. The introspection JSON itself is unchanged.`,
	RunE: Introspect,
}

func Introspect(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	formats, err := introspection.ParseNameFormats(namesFormats)
	if err != nil {
		return fmt.Errorf("--names: %w", err)
	}
	if (namesOut == "") != (len(formats) == 0) {
		return fmt.Errorf("--names-out and --names must be used together")
	}

	dag, err := dagger.Connect(ctx)
	if err != nil {
		return err
	}
	defer dag.Close()

	var data introspection.Response
	err = dag.Do(ctx, &dagger.Request{
		Query: introspection.Query,
	}, &dagger.Response{
		Data: &data,
	})
	if err != nil {
		return fmt.Errorf("introspection query: %w", err)
	}
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal introspection json: %w", err)
	}

	if namesOut != "" {
		if err := data.Schema.LoadFormattedNames(ctx, dag, data.SchemaVersion, formats...); err != nil {
			return err
		}
		namesData, err := json.MarshalIndent(data.Schema.NamesFile(), "", "  ")
		if err != nil {
			return fmt.Errorf("marshal names json: %w", err)
		}
		if err := os.WriteFile(namesOut, append(namesData, '\n'), 0o644); err != nil {
			return err
		}
	}

	if outputSchema != "" {
		return os.WriteFile(outputSchema, jsonData, 0o644)
	}
	cmd.Println(string(jsonData))
	return nil
}

func init() {
	introspectCmd.Flags().StringVarP(&outputSchema, "output", "o", "", "save introspection result to file")
	introspectCmd.Flags().StringVar(&namesOut, "names-out", "", "also save the schema's names, formatted by the engine in each of --names, to this file")
	introspectCmd.Flags().StringVar(&namesFormats, "names", "", "comma-separated name formats (CASING:ACRONYMS, e.g. SNAKE:UPPERCASE,PASCAL:CAPITALIZED) for --names-out")
}
