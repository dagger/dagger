package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

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

func printAvailableSources(cmd *cobra.Command, sources []cloudapi.Source) {
	if len(sources) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No GitHub App installations available.")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SOURCE\tINSTALLATION\tTYPE\tOWNER\tDAGGER ORG\tCONFIG URL")
	for _, source := range sources {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", source.Name, source.ID, source.Type, source.Owner, stringValue(source.OrgName), source.ConfigURL)
	}
	_ = w.Flush()
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
