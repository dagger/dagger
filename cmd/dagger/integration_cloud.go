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
	Short:   "Manage Dagger Cloud integrations",
	Args:    cobra.NoArgs,
	GroupID: cloudGroup.ID,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var integrationAddCmd = &cobra.Command{
	Use:   "add <provider>",
	Short: "Add a Dagger Cloud integration",
	Args:  cobra.ExactArgs(1),
	RunE:  cloudCLI.IntegrationAdd,
}

var integrationListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Dagger Cloud integrations",
	Args:  cobra.NoArgs,
	RunE:  cloudCLI.IntegrationList,
}

var integrationGithubCmd = &cobra.Command{
	Use:   "github",
	Short: "Inspect the GitHub integration used by Dagger Cloud",
	Args:  cobra.NoArgs,
	RunE:  cloudCLI.IntegrationGitHubInfo,
}

var integrationGithubInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show GitHub connection and integration status",
	Args:  cobra.NoArgs,
	RunE:  cloudCLI.IntegrationGitHubInfo,
}

var integrationGithubConnectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Print the GitHub OAuth URL for this Dagger Cloud user",
	Args:  cobra.NoArgs,
	RunE:  cloudCLI.IntegrationGitHubConnect,
}

var integrationGithubDisconnectCmd = &cobra.Command{
	Use:   "disconnect",
	Short: "Disconnect GitHub OAuth from this Dagger Cloud user",
	Args:  cobra.NoArgs,
	RunE:  cloudCLI.IntegrationGitHubDisconnect,
}

func init() {
	integrationCmd.PersistentFlags().BoolVar(&cloudJSON, "json", false, "Print JSON output")
	integrationAddCmd.Flags().BoolVar(&githubOpen, "open", false, "Open the OAuth URL in a browser")
	integrationGithubConnectCmd.Flags().BoolVar(&githubOpen, "open", false, "Open the OAuth URL in a browser")
	integrationGithubCmd.AddCommand(
		integrationGithubInfoCmd,
		integrationGithubConnectCmd,
		integrationGithubDisconnectCmd,
	)
	integrationCmd.AddCommand(integrationAddCmd, integrationListCmd, integrationGithubCmd)
	rootCmd.AddCommand(integrationCmd)
}

type integrationListEntry struct {
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
	Label       string
	URL         string
	RedirectURI string
}

func (cli *CloudCLI) IntegrationAdd(cmd *cobra.Command, args []string) error {
	switch strings.ToLower(args[0]) {
	case "github":
		return cli.IntegrationGitHubConnect(cmd, nil)
	default:
		return fmt.Errorf("unsupported integration %q; supported integrations: github", args[0])
	}
}

func (cli *CloudCLI) IntegrationList(cmd *cobra.Command, args []string) error {
	client, _, err := cli.cloudClient(cmd.Context())
	if err != nil {
		return err
	}
	sources, err := client.Sources(cmd.Context())
	if err != nil {
		return err
	}
	entries := integrationEntriesFromSources(sources)

	if cloudJSON {
		return writeCloudJSON(cmd, entries)
	}
	printIntegrationList(cmd, entries)
	return nil
}

func (cli *CloudCLI) IntegrationGitHubInfo(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, cloudAuth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	conn, err := client.GitHubConnection(ctx)
	if err != nil {
		return err
	}
	sources, err := client.Sources(ctx)
	if err != nil {
		return err
	}

	var org *cloudapi.OrgResponse
	var integrations []cloudapi.Integration
	if cloudOrgFlag != "" || cloudAuth.Org != nil {
		org, err = cli.resolveCloudOrg(ctx, client, cloudAuth)
		if err != nil {
			return err
		}
		integrations, err = client.Integrations(ctx, org.ID)
		if err != nil {
			return err
		}
	}

	if cloudJSON {
		return writeCloudJSON(cmd, map[string]any{
			"githubConnection":   conn,
			"githubIntegrations": integrationEntriesFromSources(sources),
			"org":                org,
			"catalog":            integrations,
		})
	}
	printGitHubIntegration(cmd, conn, sources, org, integrations)
	return nil
}

func (cli *CloudCLI) IntegrationGitHubConnect(cmd *cobra.Command, args []string) error {
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
	fmt.Fprintln(out, "Open this URL to connect your GitHub account:")
	fmt.Fprintln(out, setup.URL)
	return nil
}

func (cli *CloudCLI) IntegrationGitHubDisconnect(cmd *cobra.Command, args []string) error {
	client, _, err := cli.cloudClient(cmd.Context())
	if err != nil {
		return err
	}
	ok, err := client.DisconnectGitHub(cmd.Context())
	if err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, map[string]bool{"ok": ok})
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Disconnected GitHub from this Dagger Cloud user.")
	return nil
}

func (cli *CloudCLI) githubConnectHandoff(ctx context.Context, client *cloudapi.Client) (*githubSetupHandoff, error) {
	oauthURL, err := client.GitHubOAuthURL(ctx, githubOAuthRedirect)
	if err != nil {
		return nil, err
	}
	return &githubSetupHandoff{
		Label:       "Connect GitHub",
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

func printIntegrationList(cmd *cobra.Command, entries []integrationListEntry) {
	out := cmd.OutOrStdout()
	if len(entries) == 0 {
		fmt.Fprintln(out, "No integrations found. Add one with: dagger integration add github")
		return
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "INTEGRATION\tACCOUNT\tTYPE\tDAGGER ORG\tAUTOCHECK\tCONFIG URL")
	for _, entry := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
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

func integrationEntriesFromSources(sources []cloudapi.Source) []integrationListEntry {
	entries := make([]integrationListEntry, 0, len(sources))
	for _, source := range sources {
		provider := sourceIntegrationProvider(source)
		entries = append(entries, integrationListEntry{
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

func printGitHubIntegration(cmd *cobra.Command, conn *cloudapi.GitHubConnection, sources []cloudapi.Source, org *cloudapi.OrgResponse, integrations []cloudapi.Integration) {
	out := cmd.OutOrStdout()
	if conn == nil {
		fmt.Fprintln(out, "GitHub account: not connected")
	} else {
		fmt.Fprintf(out, "GitHub account: @%s since %s\n", conn.GitHubLogin, conn.ConnectedAt)
	}
	if org != nil {
		for _, integration := range integrations {
			if strings.EqualFold(integration.Name, "GitHub") {
				fmt.Fprintf(out, "Org integration: %s enabled=%t\n", org.Name, integrationEnabled(integration))
				break
			}
		}
	}
	if len(sources) == 0 {
		fmt.Fprintln(out, "GitHub integrations: none visible")
		return
	}
	fmt.Fprintln(out, "\nGitHub integrations:")
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ACCOUNT\tID\tTYPE\tDAGGER ORG\tCONFIG URL")
	for _, source := range sources {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", source.Name, source.ID, source.Type, stringValue(source.OrgName), source.ConfigURL)
	}
	_ = w.Flush()
}
