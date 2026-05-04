package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/cmd/codegen/schematool"
	"github.com/spf13/cobra"
)

var (
	mergeModuleTypesPath string
	mergeOutputPath      string
)

var mergeSchemaCmd = &cobra.Command{
	Use:   "merge-schema",
	Short: "Merge module-defined types into an introspection JSON",
	RunE:  runMergeSchema,
}

func init() {
	mergeSchemaCmd.Flags().StringVar(&mergeModuleTypesPath, "module-types-path", "",
		"path to module types JSON (produced by SDK Phase-1 source analysis)")
	_ = mergeSchemaCmd.MarkFlagRequired("module-types-path")
	mergeSchemaCmd.Flags().StringVar(&mergeOutputPath, "output-path", "",
		"path to write the merged introspection JSON (default: stdout)")
}

func runMergeSchema(cmd *cobra.Command, _ []string) error {
	if introspectionJSONPath == "" {
		return fmt.Errorf("--introspection-json-path is required")
	}
	introspectionData, err := os.ReadFile(introspectionJSONPath)
	if err != nil {
		return fmt.Errorf("read introspection JSON: %w", err)
	}
	var resp introspection.Response
	if err := json.Unmarshal(introspectionData, &resp); err != nil {
		return fmt.Errorf("unmarshal introspection JSON: %w", err)
	}
	modFile, err := os.Open(mergeModuleTypesPath)
	if err != nil {
		return fmt.Errorf("open module types: %w", err)
	}
	defer modFile.Close()
	mod, err := schematool.DecodeModuleTypes(modFile)
	if err != nil {
		return fmt.Errorf("decode module types: %w", err)
	}
	if err := schematool.Merge(resp.Schema, mod); err != nil {
		return fmt.Errorf("merge: %w", err)
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if mergeOutputPath == "" {
		_, err := cmd.OutOrStdout().Write(out)
		return err
	}
	return os.WriteFile(mergeOutputPath, out, 0o600)
}
