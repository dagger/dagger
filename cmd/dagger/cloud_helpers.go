package main

import (
	"encoding/json"
	"strings"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/spf13/cobra"
)

func writeCloudJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func matchSource(sources []cloudapi.Source, ref string) *cloudapi.Source {
	for i := range sources {
		source := &sources[i]
		if source.ID == ref ||
			strings.EqualFold(source.Name, ref) ||
			strings.EqualFold(source.Owner, ref) {
			return source
		}
	}
	return nil
}

func integrationEnabled(integration cloudapi.Integration) bool {
	if integration.EnabledAt == nil {
		return false
	}
	enabledAt := *integration.EnabledAt
	return enabledAt != "" && !strings.HasPrefix(enabledAt, "0001-01-01")
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
