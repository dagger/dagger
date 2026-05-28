package main

import (
	"testing"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/stretchr/testify/require"
)

func TestIntegrationEntriesFromSources(t *testing.T) {
	orgName := "dagger"
	entries := integrationEntriesFromSources([]cloudapi.Source{
		{
			Name:         "dagger",
			ID:           "123",
			Type:         "Organization",
			OrgName:      &orgName,
			ConfigURL:    "https://github.com/organizations/dagger/settings/installations/123",
			ConfiguredAt: "2026-05-26T00:00:00Z",
		},
		{
			Name:      "acme",
			ID:        "456",
			Type:      "Organization",
			ConfigURL: "https://gitlab.com/acme",
		},
	})

	require.Equal(t, []integrationListEntry{
		{
			ID:           "123",
			Provider:     "GitHub",
			Account:      "dagger",
			Type:         "Organization",
			Org:          "dagger",
			ConfiguredAt: "2026-05-26T00:00:00Z",
			Autocheck:    true,
			ConfigURL:    "https://github.com/organizations/dagger/settings/installations/123",
		},
		{
			ID:        "456",
			Provider:  "GitLab",
			Account:   "acme",
			Type:      "Organization",
			Autocheck: false,
			ConfigURL: "https://gitlab.com/acme",
		},
	}, entries)
}
