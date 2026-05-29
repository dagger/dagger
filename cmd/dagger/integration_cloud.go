package main

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

var integrationCmd = &cobra.Command{
	Use:     "integration",
	Short:   "Manage Dagger Cloud integration providers",
	Args:    cobra.NoArgs,
	GroupID: cloudGroup.ID,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var integrationSetupCmd = &cobra.Command{
	Use:   "setup <provider>",
	Short: "Set up a Dagger Cloud integration provider",
	Args:  cobra.ExactArgs(1),
	RunE:  cloudCLI.IntegrationSetup,
}

var integrationAccountsCmd = &cobra.Command{
	Use:   "accounts <provider>",
	Short: "List accounts visible to a Dagger Cloud integration provider",
	Args:  cobra.ExactArgs(1),
	RunE:  cloudCLI.IntegrationAccounts,
}

func init() {
	integrationCmd.PersistentFlags().BoolVar(&cloudJSON, "json", false, "Print JSON output")
	integrationSetupCmd.Flags().BoolVar(&githubOpen, "open", false, "Open the setup URL in a browser")
	integrationCmd.AddCommand(integrationSetupCmd, integrationAccountsCmd)
	rootCmd.AddCommand(integrationCmd)
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

func (cli *CloudCLI) IntegrationAccounts(cmd *cobra.Command, args []string) error {
	switch strings.ToLower(args[0]) {
	case "github":
		return cli.integrationAccountsGitHub(cmd)
	default:
		return unsupportedIntegrationProvider(args[0])
	}
}

func (cli *CloudCLI) integrationAccountsGitHub(cmd *cobra.Command) error {
	client, _, err := cli.cloudClient(cmd.Context())
	if err != nil {
		return err
	}
	sources, err := client.Sources(cmd.Context())
	if err != nil {
		return err
	}
	entries := integrationAccountEntriesFromSources(sources)
	entries = filterIntegrationAccountEntries(entries, "GitHub")

	if cloudJSON {
		return writeCloudJSON(cmd, entries)
	}
	printIntegrationAccounts(cmd, "GitHub", entries)
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
		fmt.Fprintf(out, "No %s accounts found. Set up %s with: dagger integration setup %s\n", provider, provider, strings.ToLower(provider))
		return
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ACCOUNT\tTYPE\tDAGGER ORG\tAUTOCHECK\tCONFIG URL")
	for _, entry := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
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
