package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
	"github.com/spf13/cobra"
)

var (
	cloudOrgFlag string
	cloudJSON    bool

	gitSourcesListAvailable bool
	gitSourceEnableRepos    []string
	gitSourceEnableAll      bool

	integrationsCategory    string
	integrationsEnabledOnly bool
)

var cloudGitSourcesCmd = &cobra.Command{
	Use:     "git-sources",
	Aliases: []string{"git-source", "sources"},
	Short:   "Manage Git sources for Dagger Cloud Modules",
	RunE:    cloudCLI.ListGitSources,
}

var cloudGitSourcesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Git sources configured for an org",
	Args:  cobra.NoArgs,
	RunE:  cloudCLI.ListGitSources,
}

var cloudGitSourcesAvailableCmd = &cobra.Command{
	Use:     "available",
	Aliases: []string{"installations"},
	Short:   "List GitHub App installations available to your account",
	Args:    cobra.NoArgs,
	RunE:    cloudCLI.AvailableGitSources,
}

var cloudGitSourcesReposCmd = &cobra.Command{
	Use:   "repos <source>",
	Short: "List repositories visible from a Git source",
	Args:  cobra.ExactArgs(1),
	RunE:  cloudCLI.ListGitSourceRepositories,
}

var cloudGitSourcesEnableCmd = &cobra.Command{
	Use:     "enable <source>",
	Aliases: []string{"configure", "set"},
	Short:   "Enable or update a Git source for an org",
	Long: `Enable or update a Git source for Dagger Cloud Modules.

Pass a GitHub installation ID, source name, or owner. Without --repo, all
repositories visible to the installation are scanned. With one or more --repo
flags, only those repositories are scanned.`,
	Args: cobra.ExactArgs(1),
	RunE: cloudCLI.EnableGitSource,
}

var cloudGitSourcesDisableCmd = &cobra.Command{
	Use:     "disable <source>",
	Aliases: []string{"remove", "unmap"},
	Short:   "Disable a Git source for an org",
	Args:    cobra.ExactArgs(1),
	RunE:    cloudCLI.DisableGitSource,
}

var cloudGitSourcesScanCmd = &cobra.Command{
	Use:   "scan [source-name...]",
	Short: "Queue a module scan for all or selected Git sources",
	Args:  cobra.ArbitraryArgs,
	RunE:  cloudCLI.ScanGitSources,
}

var cloudGitSourcesIgnoreCmd = &cobra.Command{
	Use:     "ignore-patterns",
	Aliases: []string{"ignore"},
	Short:   "Manage module ignore patterns for Git source scans",
}

var cloudGitSourcesIgnoreListCmd = &cobra.Command{
	Use:   "list",
	Short: "List module ignore patterns",
	Args:  cobra.NoArgs,
	RunE:  cloudCLI.ListGitSourceIgnorePatterns,
}

var cloudGitSourcesIgnoreAddCmd = &cobra.Command{
	Use:   "add <pattern>...",
	Short: "Add module ignore patterns",
	Args:  cobra.MinimumNArgs(1),
	RunE:  cloudCLI.AddGitSourceIgnorePatterns,
}

var cloudGitSourcesIgnoreRemoveCmd = &cobra.Command{
	Use:     "remove <pattern>...",
	Aliases: []string{"rm", "delete"},
	Short:   "Remove module ignore patterns",
	Args:    cobra.MinimumNArgs(1),
	RunE:    cloudCLI.RemoveGitSourceIgnorePatterns,
}

var cloudGithubCmd = &cobra.Command{
	Use:   "github",
	Short: "Inspect your GitHub connection and installations",
	RunE:  cloudCLI.GitHubStatus,
}

var cloudGithubStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the GitHub account connected to your Dagger Cloud user",
	Args:  cobra.NoArgs,
	RunE:  cloudCLI.GitHubStatus,
}

var cloudGithubInstallationsCmd = &cobra.Command{
	Use:     "installations",
	Aliases: []string{"sources"},
	Short:   "List GitHub App installations available to your account",
	Args:    cobra.NoArgs,
	RunE:    cloudCLI.AvailableGitSources,
}

var cloudIntegrationsCmd = &cobra.Command{
	Use:   "integrations",
	Short: "List Dagger Cloud org integrations",
	RunE:  cloudCLI.ListIntegrations,
}

var cloudIntegrationsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List integrations available to an org",
	Args:  cobra.NoArgs,
	RunE:  cloudCLI.ListIntegrations,
}

func init() {
	cloudCmd.PersistentFlags().BoolVar(&cloudJSON, "json", false, "Print JSON output")

	cloudGitSourcesCmd.Flags().BoolVar(&gitSourcesListAvailable, "available", false, "Also show GitHub App installations available to your account")
	cloudGitSourcesListCmd.Flags().BoolVar(&gitSourcesListAvailable, "available", false, "Also show GitHub App installations available to your account")
	cloudGitSourcesEnableCmd.Flags().StringArrayVar(&gitSourceEnableRepos, "repo", nil, "Repository to scan, in owner/name form (repeatable)")
	cloudGitSourcesEnableCmd.Flags().BoolVar(&gitSourceEnableAll, "all", false, "Scan all repositories visible to the source")

	cloudGitSourcesIgnoreCmd.AddCommand(
		cloudGitSourcesIgnoreListCmd,
		cloudGitSourcesIgnoreAddCmd,
		cloudGitSourcesIgnoreRemoveCmd,
	)
	cloudGitSourcesCmd.AddCommand(
		cloudGitSourcesListCmd,
		cloudGitSourcesAvailableCmd,
		cloudGitSourcesReposCmd,
		cloudGitSourcesEnableCmd,
		cloudGitSourcesDisableCmd,
		cloudGitSourcesScanCmd,
		cloudGitSourcesIgnoreCmd,
	)

	cloudGithubCmd.AddCommand(
		cloudGithubStatusCmd,
		cloudGithubInstallationsCmd,
	)

	cloudIntegrationsListCmd.Flags().StringVar(&integrationsCategory, "category", "", "Only show integrations in this category")
	cloudIntegrationsListCmd.Flags().BoolVar(&integrationsEnabledOnly, "enabled", false, "Only show enabled integrations")
	cloudIntegrationsCmd.AddCommand(cloudIntegrationsListCmd)

	cloudCmd.AddCommand(cloudGitSourcesCmd, cloudGithubCmd, cloudIntegrationsCmd)
}

func (cli *CloudCLI) cloudClient(ctx context.Context) (*cloudapi.Client, *cloudauth.Cloud, error) {
	cloudAuth, err := cloudauth.GetCloudAuth(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("cloud auth: %w", err)
	}
	if cloudAuth == nil || cloudAuth.Token == nil {
		return nil, nil, fmt.Errorf("not authenticated; run 'dagger cloud login' or set DAGGER_CLOUD_TOKEN")
	}

	client, err := cloudapi.NewClient(ctx, cloudAuth)
	if err != nil {
		return nil, nil, fmt.Errorf("cloud client: %w", err)
	}
	return client, cloudAuth, nil
}

func (cli *CloudCLI) resolveCloudOrg(ctx context.Context, client *cloudapi.Client, cloudAuth *cloudauth.Cloud) (*cloudapi.OrgResponse, error) {
	orgName := cloudOrgFlag
	if orgName == "" && cloudAuth.Org != nil {
		orgName = cloudAuth.Org.Name
	}
	if orgName == "" {
		if currentOrgName, err := cloudauth.CurrentOrgName(); err == nil {
			orgName = currentOrgName
		}
	}
	if orgName == "" {
		return nil, fmt.Errorf("no org specified; use --org or run 'dagger cloud login <org>'")
	}

	org, err := client.OrgByName(ctx, orgName)
	if err != nil {
		return nil, fmt.Errorf("resolve org %q: %w", orgName, err)
	}
	return org, nil
}

func (cli *CloudCLI) ListGitSources(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, cloudAuth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	org, err := cli.resolveCloudOrg(ctx, client, cloudAuth)
	if err != nil {
		return err
	}

	gitSources, err := client.GitSources(ctx, org.Name)
	if err != nil {
		return err
	}

	if gitSourcesListAvailable {
		available, err := client.Sources(ctx)
		if err != nil {
			return err
		}
		if cloudJSON {
			return writeCloudJSON(cmd, struct {
				Org              any                     `json:"org"`
				MappedSources    []cloudapi.MappedSource `json:"mappedSources"`
				AvailableSources []cloudapi.Source       `json:"availableSources"`
			}{
				Org:              org,
				MappedSources:    gitSources.Org.MappedSources,
				AvailableSources: available,
			})
		}
		printMappedSources(cmd, org.Name, gitSources.Org.MappedSources)
		fmt.Fprintln(cmd.OutOrStdout())
		printAvailableSources(cmd, available)
		return nil
	}

	if cloudJSON {
		return writeCloudJSON(cmd, gitSources)
	}
	printMappedSources(cmd, org.Name, gitSources.Org.MappedSources)
	return nil
}

func (cli *CloudCLI) AvailableGitSources(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, _, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	sources, err := client.Sources(ctx)
	if err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, sources)
	}
	printAvailableSources(cmd, sources)
	return nil
}

func (cli *CloudCLI) ListGitSourceRepositories(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, cloudAuth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	org, err := cli.resolveCloudOrg(ctx, client, cloudAuth)
	if err != nil {
		return err
	}
	installationID, err := cli.resolveGitSourceInstallation(ctx, client, org.Name, args[0])
	if err != nil {
		return err
	}

	repos, err := client.SourceRepositories(ctx, installationID, org.ID)
	if err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, repos)
	}
	printSourceRepositories(cmd, repos)
	return nil
}

func (cli *CloudCLI) EnableGitSource(cmd *cobra.Command, args []string) error {
	if gitSourceEnableAll && len(gitSourceEnableRepos) > 0 {
		return fmt.Errorf("--all cannot be combined with --repo")
	}

	ctx := cmd.Context()
	client, cloudAuth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	org, err := cli.resolveCloudOrg(ctx, client, cloudAuth)
	if err != nil {
		return err
	}
	installationID, err := cli.resolveGitSourceInstallation(ctx, client, org.Name, args[0])
	if err != nil {
		return err
	}

	mode := cloudapi.SourceModeAll
	repos := []string{}
	if len(gitSourceEnableRepos) > 0 {
		mode = cloudapi.SourceModeSelected
		repos = gitSourceEnableRepos
	}

	mapped, err := client.ConfigureOrgSource(ctx, org.ID, cloudapi.SourceSelectionInput{
		InstallationID: installationID,
		Mode:           mode,
		Repositories:   repos,
	})
	if err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, mapped)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Configured %s for %s (%s).\n", mapped.SourceName, org.Name, mapped.Mode)
	return nil
}

func (cli *CloudCLI) DisableGitSource(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, cloudAuth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	org, err := cli.resolveCloudOrg(ctx, client, cloudAuth)
	if err != nil {
		return err
	}
	installationID, err := cli.resolveGitSourceInstallation(ctx, client, org.Name, args[0])
	if err != nil {
		return err
	}

	ok, err := client.UnmapSource(ctx, org.ID, installationID)
	if err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, map[string]any{"ok": ok})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Disabled Git source %s for %s.\n", installationID, org.Name)
	return nil
}

func (cli *CloudCLI) ScanGitSources(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, cloudAuth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	org, err := cli.resolveCloudOrg(ctx, client, cloudAuth)
	if err != nil {
		return err
	}

	var sourceNames []string
	if len(args) > 0 {
		sourceNames = args
	}
	ok, err := client.RefreshModules(ctx, org.ID, sourceNames)
	if err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, map[string]any{
			"ok":          ok,
			"sourceNames": sourceNames,
		})
	}
	if len(sourceNames) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Queued module scan for all Git sources in %s.\n", org.Name)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Queued module scan for %s in %s.\n", strings.Join(sourceNames, ", "), org.Name)
	return nil
}

func (cli *CloudCLI) ListGitSourceIgnorePatterns(cmd *cobra.Command, args []string) error {
	gitSources, _, err := cli.gitSourcesForCommand(cmd)
	if err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, gitSources.Org.ModuleIgnorePatterns)
	}
	printStringList(cmd, "No module ignore patterns configured.", gitSources.Org.ModuleIgnorePatterns)
	return nil
}

func (cli *CloudCLI) AddGitSourceIgnorePatterns(cmd *cobra.Command, args []string) error {
	gitSources, org, client, err := cli.gitSourceMutationContext(cmd)
	if err != nil {
		return err
	}

	patterns := append([]string{}, gitSources.Org.ModuleIgnorePatterns...)
	seen := map[string]struct{}{}
	for _, pattern := range patterns {
		seen[pattern] = struct{}{}
	}
	for _, pattern := range args {
		if _, ok := seen[pattern]; ok {
			continue
		}
		patterns = append(patterns, pattern)
		seen[pattern] = struct{}{}
	}

	ok, err := client.SetModuleIgnorePatterns(cmd.Context(), org.ID, patterns)
	if err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, map[string]any{"ok": ok, "patterns": patterns})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Updated module ignore patterns for %s.\n", org.Name)
	return nil
}

func (cli *CloudCLI) RemoveGitSourceIgnorePatterns(cmd *cobra.Command, args []string) error {
	gitSources, org, client, err := cli.gitSourceMutationContext(cmd)
	if err != nil {
		return err
	}

	remove := map[string]struct{}{}
	for _, pattern := range args {
		remove[pattern] = struct{}{}
	}
	patterns := []string{}
	for _, pattern := range gitSources.Org.ModuleIgnorePatterns {
		if _, ok := remove[pattern]; !ok {
			patterns = append(patterns, pattern)
		}
	}

	ok, err := client.SetModuleIgnorePatterns(cmd.Context(), org.ID, patterns)
	if err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, map[string]any{"ok": ok, "patterns": patterns})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Updated module ignore patterns for %s.\n", org.Name)
	return nil
}

func (cli *CloudCLI) GitHubStatus(cmd *cobra.Command, args []string) error {
	client, _, err := cli.cloudClient(cmd.Context())
	if err != nil {
		return err
	}
	conn, err := client.GitHubConnection(cmd.Context())
	if err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, conn)
	}
	if conn == nil {
		fmt.Fprintln(cmd.OutOrStdout(), "GitHub account is not connected.")
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Connected to GitHub as @%s since %s.\n", conn.GitHubLogin, conn.ConnectedAt)
	return nil
}

func (cli *CloudCLI) ListIntegrations(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, cloudAuth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	org, err := cli.resolveCloudOrg(ctx, client, cloudAuth)
	if err != nil {
		return err
	}
	integrations, err := client.Integrations(ctx, org.ID)
	if err != nil {
		return err
	}

	filtered := make([]cloudapi.Integration, 0, len(integrations))
	for _, integration := range integrations {
		if integrationsCategory != "" && !strings.EqualFold(integration.Category, integrationsCategory) {
			continue
		}
		if integrationsEnabledOnly && !integrationEnabled(integration) {
			continue
		}
		filtered = append(filtered, integration)
	}

	if cloudJSON {
		return writeCloudJSON(cmd, filtered)
	}
	printIntegrations(cmd, filtered)
	return nil
}

func (cli *CloudCLI) gitSourcesForCommand(cmd *cobra.Command) (*cloudapi.GitSourcesResponse, *cloudapi.OrgResponse, error) {
	client, cloudAuth, err := cli.cloudClient(cmd.Context())
	if err != nil {
		return nil, nil, err
	}
	org, err := cli.resolveCloudOrg(cmd.Context(), client, cloudAuth)
	if err != nil {
		return nil, nil, err
	}
	gitSources, err := client.GitSources(cmd.Context(), org.Name)
	if err != nil {
		return nil, nil, err
	}
	return gitSources, org, nil
}

func (cli *CloudCLI) gitSourceMutationContext(cmd *cobra.Command) (*cloudapi.GitSourcesResponse, *cloudapi.OrgResponse, *cloudapi.Client, error) {
	client, cloudAuth, err := cli.cloudClient(cmd.Context())
	if err != nil {
		return nil, nil, nil, err
	}
	org, err := cli.resolveCloudOrg(cmd.Context(), client, cloudAuth)
	if err != nil {
		return nil, nil, nil, err
	}
	gitSources, err := client.GitSources(cmd.Context(), org.Name)
	if err != nil {
		return nil, nil, nil, err
	}
	return gitSources, org, client, nil
}

func (cli *CloudCLI) resolveGitSourceInstallation(ctx context.Context, client *cloudapi.Client, orgName string, ref string) (string, error) {
	gitSources, err := client.GitSources(ctx, orgName)
	if err == nil {
		if source := matchMappedSource(gitSources.Org.MappedSources, ref); source != nil {
			return source.InstallationID, nil
		}
	}

	sources, err := client.Sources(ctx)
	if err != nil {
		return "", err
	}
	if source := matchSource(sources, ref); source != nil {
		return source.ID, nil
	}
	if isDigits(ref) {
		return ref, nil
	}
	return "", fmt.Errorf("git source %q not found; run 'dagger cloud git-sources available'", ref)
}

func matchMappedSource(sources []cloudapi.MappedSource, ref string) *cloudapi.MappedSource {
	for i := range sources {
		source := &sources[i]
		if source.InstallationID == ref ||
			strings.EqualFold(source.SourceName, ref) ||
			strings.EqualFold(source.Owner, ref) {
			return source
		}
	}
	return nil
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

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}

func writeCloudJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printMappedSources(cmd *cobra.Command, orgName string, sources []cloudapi.MappedSource) {
	if len(sources) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No Git sources configured for %s.\n", orgName)
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SOURCE\tINSTALLATION\tMODE\tTYPE\tMAPPED AT\tCONFIG URL")
	for _, source := range sources {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", source.SourceName, source.InstallationID, source.Mode, source.Type, source.MappedAt, source.ConfigURL)
	}
	_ = w.Flush()
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

func printSourceRepositories(cmd *cobra.Command, repos []cloudapi.SourceRepository) {
	if len(repos) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No repositories visible from this source.")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "REPOSITORY\tSELECTED\tELIGIBLE\tVISIBILITY\tCLAIMED BY\tURL")
	for _, repo := range repos {
		visibility := "public"
		if repo.Private != nil && *repo.Private {
			visibility = "private"
		}
		fmt.Fprintf(w, "%s\t%t\t%t\t%s\t%s\t%s\n", repo.Repository, repo.Selected, repo.Eligible, visibility, stringValue(repo.ClaimedByOrgName), stringValue(repo.HTMLURL))
	}
	_ = w.Flush()
}

func printStringList(cmd *cobra.Command, emptyMessage string, values []string) {
	if len(values) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), emptyMessage)
		return
	}
	for _, value := range values {
		fmt.Fprintln(cmd.OutOrStdout(), value)
	}
}

func printIntegrations(cmd *cobra.Command, integrations []cloudapi.Integration) {
	if len(integrations) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No integrations found.")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tCATEGORY\tENABLED\tENABLED AT\tDESCRIPTION")
	for _, integration := range integrations {
		fmt.Fprintf(w, "%s\t%s\t%t\t%s\t%s\n", integration.Name, integration.Category, integrationEnabled(integration), stringValue(integration.EnabledAt), integration.Description)
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
