package main

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

func writeCloudJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
