package daggercmd

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"text/tabwriter"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"
)

var githubOpen bool

const githubOAuthRedirect = "https://dagger.cloud/github/callback"

// cloudIntegrationCmd is the `dagger cloud integration` group. The original
// top-level `dagger integration` was singleton-shaped (one provider per type,
// list its accounts). This is the mutable form: each configured integration
// is a discrete entry; list enumerates them, create adds one, rm removes one.
var cloudIntegrationCmd = &cobra.Command{
	Use:   "integration",
	Short: "Manage Dagger Cloud integration providers",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var cloudIntegrationCreateCmd = &cobra.Command{
	Use:     "create <provider>",
	Short:   "Create a new integration of the given provider type",
	Example: "dagger cloud integration create github",
	Args:    cobra.ExactArgs(1),
	RunE:    cloudCLI.IntegrationSetup,
}

var cloudIntegrationRmCmd = &cobra.Command{
	Use:   "rm <id>",
	Short: "Remove a configured integration",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// TODO(cli-1.0): wire to cloud API once a delete endpoint exists.
		return fmt.Errorf("dagger cloud integration rm: not yet implemented (needs cloud API support)")
	},
}

var cloudIntegrationListCmd = &cobra.Command{
	Use:   "list [type]",
	Short: "List configured integrations (optionally filtered by provider type)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  cloudCLI.IntegrationList,
}

func init() {
	cloudIntegrationCmd.PersistentFlags().BoolVar(&cloudJSON, "json", false, "Print JSON output")
	cloudIntegrationCreateCmd.Flags().BoolVar(&githubOpen, "open", false, "Open the setup URL in a browser")
	cloudIntegrationCmd.AddCommand(cloudIntegrationCreateCmd, cloudIntegrationListCmd, cloudIntegrationRmCmd)
	cloudCmd.AddCommand(cloudIntegrationCmd)
}

type integrationAccountEntry struct {
	ID           string `json:"id"`
	Provider     string `json:"provider"`
	Account      string `json:"account"`
	Type         string `json:"type"`
	Org          string `json:"org,omitempty"`
	ConfiguredAt string `json:"configuredAt,omitempty"`
	Autocheck    bool   `json:"autocheck"`
	ConfigURL    string `json:"configUrl,omitempty"`
}

type githubSetupHandoff struct {
	URL         string
	RedirectURI string
}

func (cli *CloudCLI) IntegrationSetup(cmd *cobra.Command, args []string) error {
	switch strings.ToLower(args[0]) {
	case "github":
		return cli.integrationSetupGitHub(cmd)
	default:
		return unsupportedIntegrationProvider(args[0])
	}
}

// IntegrationList lists configured integrations. With no arg, all are listed;
// with one arg, results are filtered by provider type (e.g., "github").
func (cli *CloudCLI) IntegrationList(cmd *cobra.Command, args []string) error {
	client, _, err := cli.cloudClient(cmd.Context())
	if err != nil {
		return err
	}
	sources, err := client.Sources(cmd.Context())
	if err != nil {
		return err
	}
	entries := integrationAccountEntriesFromSources(sources)

	filter := ""
	if len(args) == 1 {
		filter = canonicalProviderName(args[0])
		if filter == "" {
			return unsupportedIntegrationProvider(args[0])
		}
		entries = filterIntegrationAccountEntries(entries, filter)
	}

	if cloudJSON {
		return writeCloudJSON(cmd, entries)
	}
	printIntegrationAccounts(cmd, filter, entries)
	return nil
}

func (cli *CloudCLI) integrationSetupGitHub(cmd *cobra.Command) error {
	client, _, err := cli.cloudClient(cmd.Context())
	if err != nil {
		return err
	}
	setup, err := cli.githubConnectHandoff(cmd.Context(), client)
	if err != nil {
		return err
	}
	openGitHubSetupURL(cmd, setup.URL)
	if cloudJSON {
		return writeCloudJSON(cmd, map[string]string{"url": setup.URL, "redirectURI": setup.RedirectURI})
	}
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Open this URL to set up the GitHub integration:")
	fmt.Fprintln(out, setup.URL)
	return nil
}

func (cli *CloudCLI) githubConnectHandoff(ctx context.Context, client *cloudapi.Client) (*githubSetupHandoff, error) {
	oauthURL, err := client.GitHubOAuthURL(ctx, githubOAuthRedirect)
	if err != nil {
		return nil, err
	}
	return &githubSetupHandoff{
		URL:         oauthURL,
		RedirectURI: githubOAuthRedirect,
	}, nil
}

func openGitHubSetupURL(cmd *cobra.Command, setupURL string) {
	if githubOpen {
		if err := browser.OpenURL(setupURL); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Failed to open browser: %s\n", err)
		}
	}
}

func printIntegrationAccounts(cmd *cobra.Command, provider string, entries []integrationAccountEntry) {
	out := cmd.OutOrStdout()
	if len(entries) == 0 {
		if provider != "" {
			fmt.Fprintf(out, "No %s integrations configured. Run: dagger cloud integration create %s\n", provider, strings.ToLower(provider))
		} else {
			fmt.Fprintln(out, "No integrations configured. Run: dagger cloud integration create <provider>")
		}
		return
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tPROVIDER\tACCOUNT\tTYPE\tDAGGER ORG\tAUTOCHECK\tCONFIG URL")
	for _, entry := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			entry.ID,
			entry.Provider,
			entry.Account,
			entry.Type,
			entry.Org,
			onOff(entry.Autocheck),
			entry.ConfigURL,
		)
	}
	_ = w.Flush()
}

func integrationAccountEntriesFromSources(sources []cloudapi.Source) []integrationAccountEntry {
	entries := make([]integrationAccountEntry, 0, len(sources))
	for _, source := range sources {
		provider := sourceIntegrationProvider(source)
		entries = append(entries, integrationAccountEntry{
			ID:           source.ID,
			Provider:     provider,
			Account:      source.Name,
			Type:         source.Type,
			Org:          stringValue(source.OrgName),
			ConfiguredAt: source.ConfiguredAt,
			Autocheck:    integrationProviderSupportsAutocheck(provider),
			ConfigURL:    source.ConfigURL,
		})
	}
	return entries
}

func filterIntegrationAccountEntries(entries []integrationAccountEntry, provider string) []integrationAccountEntry {
	filtered := make([]integrationAccountEntry, 0, len(entries))
	for _, entry := range entries {
		if strings.EqualFold(entry.Provider, provider) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

// canonicalProviderName normalizes user-supplied provider names ("github",
// "GitHub", "GITHUB") to the canonical form used in entry.Provider.
// Returns "" if the input doesn't match any supported provider.
func canonicalProviderName(input string) string {
	switch strings.ToLower(input) {
	case "github":
		return "GitHub"
	case "gitlab":
		return "GitLab"
	case "bitbucket":
		return "Bitbucket"
	}
	return ""
}

func unsupportedIntegrationProvider(provider string) error {
	return fmt.Errorf("unsupported integration %q; supported integrations: github", provider)
}

func sourceIntegrationProvider(source cloudapi.Source) string {
	configURL, err := url.Parse(source.ConfigURL)
	if err == nil {
		switch strings.ToLower(configURL.Hostname()) {
		case "github.com":
			return "GitHub"
		case "gitlab.com":
			return "GitLab"
		case "bitbucket.org":
			return "Bitbucket"
		}
	}
	return "Git"
}

func integrationProviderSupportsAutocheck(provider string) bool {
	return strings.EqualFold(provider, "GitHub")
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}
