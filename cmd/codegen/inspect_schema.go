package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/cmd/codegen/schematool"
	"github.com/spf13/cobra"
)

var inspectSchemaCmd = &cobra.Command{
	Use:   "inspect-schema",
	Short: "Read-only queries against an introspection JSON file",
}

var (
	inspectKind     string
	inspectTypeName string
)

var inspectListTypesCmd = &cobra.Command{
	Use:   "list-types",
	Short: "List type names in the introspection schema",
	RunE:  runInspectListTypes,
}

var inspectHasTypeCmd = &cobra.Command{
	Use:   "has-type",
	Short: "Check whether a type exists in the schema (prints true/false, exits 1 if false)",
	RunE:  runInspectHasType,
}

var inspectDescribeTypeCmd = &cobra.Command{
	Use:   "describe-type",
	Short: "Print the full introspection Type JSON for the given name",
	RunE:  runInspectDescribeType,
}

func init() {
	inspectListTypesCmd.Flags().StringVar(&inspectKind, "kind", "",
		"filter: OBJECT, INTERFACE, ENUM, SCALAR, INPUT_OBJECT")

	inspectHasTypeCmd.Flags().StringVar(&inspectTypeName, "name", "", "type name")
	_ = inspectHasTypeCmd.MarkFlagRequired("name")

	inspectDescribeTypeCmd.Flags().StringVar(&inspectTypeName, "name", "", "type name")
	_ = inspectDescribeTypeCmd.MarkFlagRequired("name")

	inspectSchemaCmd.AddCommand(inspectListTypesCmd)
	inspectSchemaCmd.AddCommand(inspectHasTypeCmd)
	inspectSchemaCmd.AddCommand(inspectDescribeTypeCmd)
}

func loadIntrospection() (*introspection.Schema, error) {
	if introspectionJSONPath == "" {
		return nil, fmt.Errorf("--introspection-json-path is required")
	}
	data, err := os.ReadFile(introspectionJSONPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", introspectionJSONPath, err)
	}
	var resp introspection.Response
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return resp.Schema, nil
}

func runInspectListTypes(cmd *cobra.Command, _ []string) error {
	schema, err := loadIntrospection()
	if err != nil {
		return err
	}
	names := schematool.ListTypes(schema, inspectKind)
	return json.NewEncoder(cmd.OutOrStdout()).Encode(names)
}

func runInspectHasType(cmd *cobra.Command, _ []string) error {
	schema, err := loadIntrospection()
	if err != nil {
		return err
	}
	has := schematool.HasType(schema, inspectTypeName)
	fmt.Fprintln(cmd.OutOrStdout(), has)
	if !has {
		os.Exit(1)
	}
	return nil
}

func runInspectDescribeType(cmd *cobra.Command, _ []string) error {
	schema, err := loadIntrospection()
	if err != nil {
		return err
	}
	t := schematool.DescribeType(schema, inspectTypeName)
	if t == nil {
		return fmt.Errorf("type %q not found", inspectTypeName)
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(t)
}
