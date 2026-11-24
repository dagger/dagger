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

var (
	integrationOpen      bool
	integrationNamespace string
)

var cloudIntegrationCmd = &cobra.Command{
	Use:   "integration",
	Short: "Manage Dagger Cloud integration providers",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var cloudIntegrationCreateCmd = &cobra.Command{
	Use:   "create <provider>",
	Short: "Start connecting a forge to Dagger Cloud",
	Long: `Return the browser step to connect GitHub, GitLab.com, or Entire to Dagger Cloud.
With --json, the action_required status includes the next action and its URL.
For GitLab.com, pass --namespace to grant access to a specific account or group.
After setup, use integration list <provider> to verify the installation or grant.
Use cloud checks on separately to enable checks for a repository.`,
	Example: `dagger cloud integration create gitlab --org my-org --namespace team/group --json`,
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
	Short: "List integrations for a provider (defaults to GitHub)",
	Long:  "List GitHub installations visible to the signed-in account, GitLab grants saved for the selected Cloud organization, or Entire installations available to that organization.",
	Args:  cobra.MaximumNArgs(1),
	RunE:  cloudCLI.IntegrationList,
}

func init() {
	cloudIntegrationCmd.PersistentFlags().BoolVar(&cloudJSON, "json", false, "Print JSON output")
	cloudIntegrationCreateCmd.Flags().BoolVar(&integrationOpen, "open", false, "Open the setup URL in a browser")
	cloudIntegrationCreateCmd.Flags().StringVar(&integrationNamespace, "namespace", "", "GitLab.com account or group path to grant to the Cloud organization")
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
	Autocheck    *bool  `json:"autocheck,omitempty"` // Legacy GitHub capability field.
	ConfigURL    string `json:"configUrl,omitempty"`
}

type githubSetupHandoff struct {
	URL         string
	RedirectURI string
}

type integrationSetupStep struct {
	Status      string `json:"status"`
	Provider    string `json:"provider"`
	Org         string `json:"org"`
	Action      string `json:"action,omitempty"`
	URL         string `json:"url,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	ID          string `json:"id,omitempty"`
	RedirectURI string `json:"redirectURI,omitempty"`
	GitHubLogin string `json:"githubLogin,omitempty"`
}

func (cli *CloudCLI) IntegrationSetup(cmd *cobra.Command, args []string) error {
	provider := strings.ToLower(args[0])
	switch provider {
	case "github", "gitlab", "entire":
	default:
		return unsupportedIntegrationProvider(args[0])
	}

	if integrationNamespace != "" && provider != "gitlab" {
		return fmt.Errorf("--namespace is only supported for GitLab.com")
	}
	ctx := cmd.Context()
	client, auth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	org, err := cli.resolveCloudOrg(ctx, client, auth)
	if err != nil {
		return err
	}

	step := integrationSetupStep{Status: "action_required", Provider: provider, Org: org.Name}
	var instruction string
	switch provider {
	case "github":
		connected, login := cli.githubConnected(ctx, client)
		step.GitHubLogin = login
		if connected {
			step.Action = "install_app"
			step.URL, err = cloudIntegrationURL(org.Name, provider)
			instruction = "Install the Dagger for GitHub app from your Cloud organization's GitHub integration page."
		} else {
			setup, setupErr := cli.githubConnectHandoff(ctx, client)
			if setupErr != nil {
				return setupErr
			}
			step.Action = "authorize_account"
			step.URL, step.RedirectURI = setup.URL, setup.RedirectURI
			instruction = "Authorize your GitHub account, then run this command again to install the app."
		}
	case "gitlab":
		instruction, err = gitLabIntegrationSetup(ctx, client, org, &step)
	case "entire":
		step.Action = "install_plugin"
		step.URL, err = entireInstallPage(ctx, client, org.ID, org.Name)
		instruction = "Install the Dagger plugin on your Entire project."
	}
	if err != nil {
		return err
	}
	if step.Status == "action_required" && step.URL == "" {
		return fmt.Errorf("%s did not return an integration setup URL", provider)
	}
	if integrationOpen && step.URL != "" {
		if err := browser.OpenURL(step.URL); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Failed to open browser: %s\n", err)
		}
	}
	if cloudJSON {
		return writeCloudJSON(cmd, step)
	}
	fmt.Fprintln(cmd.OutOrStdout(), instruction)
	if step.URL != "" {
		fmt.Fprintln(cmd.OutOrStdout(), step.URL)
	}
	return nil
}

// Grant access only to the explicitly selected GitLab.com namespace.
func gitLabIntegrationSetup(ctx context.Context, client *cloudapi.Client, org *cloudapi.OrgResponse, step *integrationSetupStep) (string, error) {
	step.Namespace = integrationNamespace
	if step.Namespace != "" {
		sources, err := client.GitLabSources(ctx, org.ID)
		if err != nil {
			return "", err
		}
		for _, source := range sources {
			if source.InstanceURL != "https://gitlab.com" || source.NamespacePath != step.Namespace {
				continue
			}
			if source.Access == nil {
				if err := client.UseGitLabAccount(ctx, org.ID, source.ID); err != nil {
					return "", err
				}
			}
			step.Status, step.ID = "connected", source.ID
			return fmt.Sprintf("GitLab.com namespace %s is connected to %s.", step.Namespace, org.Name), nil
		}
	}

	var err error
	step.Action = "authorize_account"
	step.URL, err = cloudIntegrationURL(org.Name, "gitlab")
	return "Connect a GitLab.com account, then rerun with --namespace <path> to grant access.", err
}

func (cli *CloudCLI) IntegrationList(cmd *cobra.Command, args []string) error {
	filter := ""
	if len(args) == 1 {
		filter = canonicalProviderName(args[0])
		if filter == "" {
			return unsupportedIntegrationProvider(args[0])
		}
	}
	ctx := cmd.Context()
	client, auth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}

	var entries []integrationAccountEntry
	switch filter {
	case "GitLab", "Entire":
		org, err := cli.resolveCloudOrg(ctx, client, auth)
		if err != nil {
			return err
		}
		entries, err = orgIntegrationAccounts(ctx, client, org, filter)
		if err != nil {
			return err
		}
	default:
		sources, err := client.Sources(ctx)
		if err != nil {
			return err
		}
		entries = integrationAccountEntriesFromSources(sources)
		if filter != "" {
			entries = filterIntegrationAccountEntries(entries, filter)
		}
	}

	if cloudJSON {
		return writeCloudJSON(cmd, entries)
	}
	printIntegrationAccounts(cmd, filter, entries)
	return nil
}

// Organization grants and installations available to the selected organization
// are separate from the signed-in user's GitHub installation inventory.
func orgIntegrationAccounts(ctx context.Context, client *cloudapi.Client, org *cloudapi.OrgResponse, provider string) ([]integrationAccountEntry, error) {
	entries := []integrationAccountEntry{}
	if provider == "GitLab" {
		sources, err := client.GitLabOrganizationSources(ctx, org.ID)
		if err != nil {
			return nil, err
		}
		for _, source := range sources {
			entries = append(entries, integrationAccountEntry{
				ID: source.ID, Provider: provider, Account: source.NamespacePath, Type: "Namespace",
				Org: org.Name, ConfigURL: strings.TrimRight(source.InstanceURL, "/") + "/" + source.NamespacePath,
			})
		}
		return entries, nil
	}

	installations, err := client.EntireInstallations(ctx, org.ID)
	if err != nil {
		return nil, err
	}
	for _, installation := range installations {
		entries = append(entries, integrationAccountEntry{
			ID: installation.ID, Provider: provider, Account: installation.Name, Type: "Installation",
			ConfigURL: installation.ManageURL,
		})
	}
	return entries, nil
}

// githubConnected reports whether the user already has a usable GitHub identity,
// along with the connected login when known.
//
// A GitHub identity can come from two places (see the Cloud API's Sources
// resolver): an explicit connection stored in user_github_connections, or the
// Auth0 GitHub identity of a user who logged into Cloud with GitHub. The latter
// has no stored row, so a githubConnection lookup alone misses it. Since
// Sources() needs a working GitHub token from either path, a successful
// Sources() call is the definitive "GitHub is usable" signal.
//
// Best-effort: any lookup error is treated as "not connected", so the caller
// falls back to offering the OAuth flow.
func (cli *CloudCLI) githubConnected(ctx context.Context, client *cloudapi.Client) (bool, string) {
	// Explicit connection: cheap, and carries the login for a nicer message.
	if conn, err := client.GitHubConnection(ctx); err == nil && conn != nil {
		return true, conn.GitHubLogin
	}
	// No stored connection, but a GitHub identity may be available via Auth0.
	if _, err := client.Sources(ctx); err == nil {
		return true, ""
	}
	return false, ""
}

func (cli *CloudCLI) githubConnectHandoff(ctx context.Context, client *cloudapi.Client) (*githubSetupHandoff, error) {
	frontend, err := cloudFrontendURL()
	if err != nil {
		return nil, err
	}
	redirectURI := frontend.JoinPath("github", "callback").String()
	oauthURL, err := client.GitHubOAuthURL(ctx, redirectURI)
	if err != nil {
		return nil, err
	}
	return &githubSetupHandoff{
		URL:         oauthURL,
		RedirectURI: redirectURI,
	}, nil
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
	fmt.Fprintln(w, "ID\tPROVIDER\tACCOUNT\tTYPE\tDAGGER ORG\tSUPPORTS CHECKS\tCONFIG URL")
	for _, entry := range entries {
		checkSupport := "-"
		if entry.Autocheck != nil {
			checkSupport = onOff(*entry.Autocheck)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			entry.ID,
			entry.Provider,
			entry.Account,
			entry.Type,
			entry.Org,
			checkSupport,
			entry.ConfigURL,
		)
	}
	_ = w.Flush()
}

func integrationAccountEntriesFromSources(sources []cloudapi.Source) []integrationAccountEntry {
	entries := make([]integrationAccountEntry, 0, len(sources))
	for _, source := range sources {
		provider := sourceIntegrationProvider(source)
		checkSupport := integrationProviderSupportsAutocheck(provider)
		entries = append(entries, integrationAccountEntry{
			ID:           source.ID,
			Provider:     provider,
			Account:      source.Name,
			Type:         source.Type,
			Org:          stringValue(source.OrgName),
			ConfiguredAt: source.ConfiguredAt,
			Autocheck:    &checkSupport,
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
	case "entire":
		return "Entire"
	case "bitbucket":
		return "Bitbucket"
	}
	return ""
}

func unsupportedIntegrationProvider(provider string) error {
	return fmt.Errorf("unsupported integration %q; supported integrations: github, gitlab, entire", provider)
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
