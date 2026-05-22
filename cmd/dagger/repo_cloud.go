package main

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"text/tabwriter"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"
)

var (
	repoTransferFrom string
	repoTransferTo   string
	githubRedirect   string
	githubOpen       bool
)

var repoCmd = &cobra.Command{
	Use:   "repo [repo]",
	Short: "Link repositories to Dagger Cloud",
	Args:  cobra.MaximumNArgs(1),
	RunE:  cloudCLI.RepoInfo,
}

var repoInfoCmd = &cobra.Command{
	Use:   "info [repo]",
	Short: "Show Dagger Cloud status for a repository",
	Args:  cobra.MaximumNArgs(1),
	RunE:  cloudCLI.RepoInfo,
}

var repoLinkCmd = &cobra.Command{
	Use:   "link [repo]",
	Short: "Link a repository to a Dagger Cloud org",
	Args:  cobra.MaximumNArgs(1),
	RunE:  cloudCLI.RepoLink,
}

var repoUnlinkCmd = &cobra.Command{
	Use:   "unlink [repo]",
	Short: "Unlink a repository from a Dagger Cloud org",
	Args:  cobra.MaximumNArgs(1),
	RunE:  cloudCLI.RepoUnlink,
}

var repoTransferCmd = &cobra.Command{
	Use:   "transfer [repo]",
	Short: "Move a repository link from one Dagger Cloud org to another",
	Args:  cobra.MaximumNArgs(1),
	RunE:  cloudCLI.RepoTransfer,
}

var repoAutocheckCmd = &cobra.Command{
	Use:   "autocheck",
	Short: "Manage automatic GitHub checks for a repository",
}

var repoAutocheckOnCmd = &cobra.Command{
	Use:   "on [repo]",
	Short: "Turn automatic GitHub checks on for a repository",
	Args:  cobra.MaximumNArgs(1),
	RunE:  cloudCLI.RepoAutocheckOn,
}

var repoAutocheckOffCmd = &cobra.Command{
	Use:   "off [repo]",
	Short: "Turn automatic GitHub checks off for a repository",
	Args:  cobra.MaximumNArgs(1),
	RunE:  cloudCLI.RepoAutocheckOff,
}

var integrationCmd = &cobra.Command{
	Use:   "integration",
	Short: "Manage Dagger Cloud integrations",
}

var integrationGithubCmd = &cobra.Command{
	Use:   "github",
	Short: "Inspect the GitHub integration used by Dagger Cloud",
	RunE:  cloudCLI.IntegrationGitHubInfo,
}

var integrationGithubInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show GitHub connection and installation status",
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

var integrationGithubInstallationsCmd = &cobra.Command{
	Use:     "installations",
	Aliases: []string{"sources"},
	Short:   "List GitHub App installations visible to this user",
	Args:    cobra.NoArgs,
	RunE:    cloudCLI.IntegrationGitHubInstallations,
}

func init() {
	repoCmd.PersistentFlags().StringVar(&cloudOrgFlag, "org", "", "Dagger Cloud org name (defaults to current org)")
	repoCmd.PersistentFlags().BoolVar(&cloudJSON, "json", false, "Print JSON output")
	repoTransferCmd.Flags().StringVar(&repoTransferFrom, "from", "", "Source Dagger Cloud org (defaults to current or claimed org)")
	repoTransferCmd.Flags().StringVar(&repoTransferTo, "to", "", "Destination Dagger Cloud org")
	_ = repoTransferCmd.MarkFlagRequired("to")

	repoAutocheckCmd.AddCommand(repoAutocheckOnCmd, repoAutocheckOffCmd)
	repoCmd.AddCommand(repoInfoCmd, repoLinkCmd, repoUnlinkCmd, repoTransferCmd, repoAutocheckCmd)

	integrationCmd.PersistentFlags().StringVar(&cloudOrgFlag, "org", "", "Dagger Cloud org name (defaults to current org)")
	integrationCmd.PersistentFlags().BoolVar(&cloudJSON, "json", false, "Print JSON output")
	integrationGithubConnectCmd.Flags().StringVar(&githubRedirect, "redirect-uri", "https://dagger.cloud/github/callback", "OAuth redirect URI")
	integrationGithubConnectCmd.Flags().BoolVar(&githubOpen, "open", false, "Open the OAuth URL in a browser")
	integrationGithubCmd.AddCommand(
		integrationGithubInfoCmd,
		integrationGithubConnectCmd,
		integrationGithubDisconnectCmd,
		integrationGithubInstallationsCmd,
	)
	integrationCmd.AddCommand(integrationGithubCmd)

	rootCmd.AddCommand(repoCmd, integrationCmd)
}

type repoCloudState struct {
	Org          *cloudapi.OrgResponse      `json:"org"`
	Repository   string                     `json:"repository"`
	Status       string                     `json:"status"`
	Autocheck    bool                       `json:"autocheck"`
	Source       *cloudapi.Source           `json:"source,omitempty"`
	MappedSource *cloudapi.MappedSource     `json:"mappedSource,omitempty"`
	Repo         *cloudapi.SourceRepository `json:"repo,omitempty"`
	RepoSettings []cloudapi.RepoSetting     `json:"repoSettings,omitempty"`
	Message      string                     `json:"message,omitempty"`
	ActionURL    string                     `json:"actionUrl,omitempty"`
	Mutation     string                     `json:"mutation,omitempty"`
}

func (cli *CloudCLI) RepoInfo(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, cloudAuth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	org, err := cli.resolveCloudOrg(ctx, client, cloudAuth)
	if err != nil {
		return err
	}
	repo, err := repoFromArgOrGit(ctx, args)
	if err != nil {
		return err
	}
	state, err := cli.inspectRepo(ctx, client, org, repo)
	if err != nil {
		return err
	}
	settings, err := client.RepoSettings(ctx, org.Name, repo)
	if err != nil {
		return err
	}
	state.RepoSettings = settings
	state.Message = repoStatusMessage(state)

	if cloudJSON {
		return writeCloudJSON(cmd, state)
	}
	printRepoInfo(cmd, state)
	return nil
}

func (cli *CloudCLI) RepoLink(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, cloudAuth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	org, err := cli.resolveCloudOrg(ctx, client, cloudAuth)
	if err != nil {
		return err
	}
	repo, err := repoFromArgOrGit(ctx, args)
	if err != nil {
		return err
	}
	state, err := cli.linkRepo(ctx, client, org, repo)
	if err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, state)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s\n", state.Message)
	return nil
}

func (cli *CloudCLI) RepoUnlink(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, cloudAuth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	org, err := cli.resolveCloudOrg(ctx, client, cloudAuth)
	if err != nil {
		return err
	}
	repo, err := repoFromArgOrGit(ctx, args)
	if err != nil {
		return err
	}
	state, err := cli.unlinkRepo(ctx, client, org, repo)
	if err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, state)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s\n", state.Message)
	return nil
}

func (cli *CloudCLI) RepoTransfer(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, cloudAuth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	currentOrg, err := cli.resolveCloudOrg(ctx, client, cloudAuth)
	if err != nil {
		return err
	}
	repo, err := repoFromArgOrGit(ctx, args)
	if err != nil {
		return err
	}

	toOrg, err := client.OrgByName(ctx, repoTransferTo)
	if err != nil {
		return err
	}

	fromName := repoTransferFrom
	if fromName == "" {
		state, err := cli.inspectRepo(ctx, client, currentOrg, repo)
		if err != nil {
			return err
		}
		if state.Repo != nil && state.Repo.ClaimedByOrgName != nil && *state.Repo.ClaimedByOrgName != "" {
			fromName = *state.Repo.ClaimedByOrgName
		} else {
			fromName = currentOrg.Name
		}
	}
	fromOrg, err := client.OrgByName(ctx, fromName)
	if err != nil {
		return err
	}
	if fromOrg.ID == toOrg.ID {
		return fmt.Errorf("--from and --to resolve to the same org %q", fromOrg.Name)
	}

	targetState, err := cli.inspectRepo(ctx, client, toOrg, repo)
	if err != nil {
		return err
	}
	if targetState.Repo == nil {
		return fmt.Errorf("repo %s is not visible to a GitHub installation available to this user", repo)
	}
	if !targetState.Repo.Eligible && !sameString(targetState.Repo.ClaimedByOrgName, fromOrg.Name) {
		return fmt.Errorf("repo %s is claimed by %s, not %s", repo, stringValue(targetState.Repo.ClaimedByOrgName), fromOrg.Name)
	}

	unlinked, err := cli.unlinkRepo(ctx, client, fromOrg, repo)
	if err != nil {
		return err
	}
	linked, err := cli.linkRepo(ctx, client, toOrg, repo)
	if err != nil {
		return err
	}

	result := map[string]any{
		"repository": repo,
		"from":       fromOrg,
		"to":         toOrg,
		"unlink":     unlinked,
		"link":       linked,
	}
	if cloudJSON {
		return writeCloudJSON(cmd, result)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Transferred %s from %s to %s.\n", repo, fromOrg.Name, toOrg.Name)
	return nil
}

func (cli *CloudCLI) RepoAutocheckOn(cmd *cobra.Command, args []string) error {
	repo, err := repoFromArgOrGit(cmd.Context(), args)
	if err != nil {
		return err
	}
	if cloudJSON {
		if err := writeCloudJSON(cmd, map[string]any{
			"repository": repo,
			"autocheck":  true,
			"readOnly":   true,
			"error":      "autocheck is a read-only mock setting and is always on",
		}); err != nil {
			return err
		}
	}
	return fmt.Errorf("autocheck is always on and cannot be changed")
}

func (cli *CloudCLI) RepoAutocheckOff(cmd *cobra.Command, args []string) error {
	repo, err := repoFromArgOrGit(cmd.Context(), args)
	if err != nil {
		return err
	}
	msg := map[string]any{
		"repository": repo,
		"autocheck":  true,
		"readOnly":   true,
		"error":      "autocheck is a read-only mock setting and is always on",
	}
	if cloudJSON {
		if err := writeCloudJSON(cmd, msg); err != nil {
			return err
		}
	}
	return fmt.Errorf("autocheck is always on and cannot be changed")
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
			"githubConnection": conn,
			"installations":    sources,
			"org":              org,
			"integrations":     integrations,
		})
	}
	printGitHubIntegration(cmd, conn, sources, org, integrations)
	return nil
}

func (cli *CloudCLI) IntegrationGitHubInstallations(cmd *cobra.Command, args []string) error {
	client, _, err := cli.cloudClient(cmd.Context())
	if err != nil {
		return err
	}
	sources, err := client.Sources(cmd.Context())
	if err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, sources)
	}
	printAvailableSources(cmd, sources)
	return nil
}

func (cli *CloudCLI) IntegrationGitHubConnect(cmd *cobra.Command, args []string) error {
	client, _, err := cli.cloudClient(cmd.Context())
	if err != nil {
		return err
	}
	oauthURL, err := client.GitHubOAuthURL(cmd.Context(), githubRedirect)
	if err != nil {
		return err
	}
	if githubOpen {
		if err := browser.OpenURL(oauthURL); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Failed to open browser: %s\n", err)
		}
	}
	if cloudJSON {
		return writeCloudJSON(cmd, map[string]string{"url": oauthURL, "redirectURI": githubRedirect})
	}
	fmt.Fprintln(cmd.OutOrStdout(), oauthURL)
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

func (cli *CloudCLI) inspectRepo(ctx context.Context, client *cloudapi.Client, org *cloudapi.OrgResponse, repo string) (*repoCloudState, error) {
	state := &repoCloudState{
		Org:        org,
		Repository: repo,
		Status:     "not_installed",
		Autocheck:  true,
	}

	gitSources, err := client.GitSources(ctx, org.Name)
	if err != nil {
		return nil, err
	}
	sources, err := client.Sources(ctx)
	if err != nil {
		return nil, err
	}

	owner := repoOwner(repo)
	for i := range gitSources.Org.MappedSources {
		mapped := &gitSources.Org.MappedSources[i]
		repos, err := client.SourceRepositories(ctx, mapped.InstallationID, org.ID)
		if err != nil {
			return nil, err
		}
		for j := range repos {
			if !sameRepository(repos[j].Repository, repo) {
				continue
			}
			state.MappedSource = mapped
			state.Repo = &repos[j]
			state.Source = matchSource(sources, mapped.InstallationID)
			if state.Source != nil {
				state.ActionURL = state.Source.ConfigURL
			}
			state.Status = repoStatus(state.Repo)
			if state.Status != "linked" && state.Source != nil {
				return state, nil
			}
			return state, nil
		}
	}

	for i := range sources {
		source := &sources[i]
		if state.Source == nil && sourceMatchesOwner(source, owner) {
			state.Source = source
			state.ActionURL = source.ConfigURL
			state.Status = "not_visible"
		}
		repos, err := client.SourceRepositories(ctx, source.ID, org.ID)
		if err != nil {
			return nil, err
		}
		for j := range repos {
			if !sameRepository(repos[j].Repository, repo) {
				continue
			}
			state.Source = source
			state.Repo = &repos[j]
			state.ActionURL = source.ConfigURL
			state.Status = repoStatus(state.Repo)
			return state, nil
		}
	}
	return state, nil
}

func (cli *CloudCLI) linkRepo(ctx context.Context, client *cloudapi.Client, org *cloudapi.OrgResponse, repo string) (*repoCloudState, error) {
	state, err := cli.inspectRepo(ctx, client, org, repo)
	if err != nil {
		return nil, err
	}
	if state.Repo == nil {
		return nil, repoVisibilityError(state)
	}
	if !state.Repo.Eligible {
		return nil, fmt.Errorf("repo %s is claimed by %s", repo, stringValue(state.Repo.ClaimedByOrgName))
	}
	if state.Repo.Selected {
		state.Mutation = "noop"
		state.Message = fmt.Sprintf("%s is already linked to %s.", repo, org.Name)
		return state, nil
	}

	selected, err := cli.selectedReposForSource(ctx, client, org, state.Source.ID)
	if err != nil {
		return nil, err
	}
	selected = appendUniqueRepository(selected, repo)
	mapped, err := client.ConfigureOrgSource(ctx, org.ID, cloudapi.SourceSelectionInput{
		InstallationID: state.Source.ID,
		Mode:           cloudapi.SourceModeSelected,
		Repositories:   selected,
	})
	if err != nil {
		return nil, err
	}
	state.MappedSource = mapped
	state.Status = "linked"
	state.Mutation = "configureOrgSource"
	state.Message = fmt.Sprintf("Linked %s to %s. Module scan queued.", repo, org.Name)
	return state, nil
}

func (cli *CloudCLI) unlinkRepo(ctx context.Context, client *cloudapi.Client, org *cloudapi.OrgResponse, repo string) (*repoCloudState, error) {
	state, err := cli.inspectRepo(ctx, client, org, repo)
	if err != nil {
		return nil, err
	}
	if state.Repo == nil || !state.Repo.Selected || state.Source == nil {
		state.Mutation = "noop"
		state.Message = fmt.Sprintf("%s is not linked to %s.", repo, org.Name)
		return state, nil
	}

	selected, err := cli.selectedReposForSource(ctx, client, org, state.Source.ID)
	if err != nil {
		return nil, err
	}
	selected = removeRepository(selected, repo)
	if len(selected) == 0 {
		ok, err := client.UnmapSource(ctx, org.ID, state.Source.ID)
		if err != nil {
			return nil, err
		}
		state.Mutation = "unmapSourceFromOrg"
		state.Message = fmt.Sprintf("Unlinked %s from %s and removed empty source mapping (ok=%t).", repo, org.Name, ok)
		return state, nil
	}

	mapped, err := client.ConfigureOrgSource(ctx, org.ID, cloudapi.SourceSelectionInput{
		InstallationID: state.Source.ID,
		Mode:           cloudapi.SourceModeSelected,
		Repositories:   selected,
	})
	if err != nil {
		return nil, err
	}
	state.MappedSource = mapped
	state.Mutation = "configureOrgSource"
	state.Message = fmt.Sprintf("Unlinked %s from %s; %d repos remain linked from %s.", repo, org.Name, len(selected), mapped.SourceName)
	return state, nil
}

func (cli *CloudCLI) selectedReposForSource(ctx context.Context, client *cloudapi.Client, org *cloudapi.OrgResponse, installationID string) ([]string, error) {
	repos, err := client.SourceRepositories(ctx, installationID, org.ID)
	if err != nil {
		return nil, err
	}
	selected := []string{}
	for _, repo := range repos {
		if repo.Selected && repo.Eligible {
			selected = append(selected, repo.Repository)
		}
	}
	sort.Strings(selected)
	return selected, nil
}

func repoFromArgOrGit(ctx context.Context, args []string) (string, error) {
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		return normalizeGitHubRepo(args[0])
	}
	out, err := exec.CommandContext(ctx, "git", "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return "", fmt.Errorf("no repo specified and git remote.origin.url could not be read")
	}
	return normalizeGitHubRepo(strings.TrimSpace(string(out)))
}

func normalizeGitHubRepo(ref string) (string, error) {
	ref = strings.TrimSpace(strings.TrimSuffix(ref, ".git"))
	if ref == "" {
		return "", fmt.Errorf("empty repository")
	}
	if strings.HasPrefix(ref, "git@github.com:") {
		return normalizeGitHubRepo(strings.TrimPrefix(ref, "git@github.com:"))
	}
	if strings.Contains(ref, "://") {
		u, err := url.Parse(ref)
		if err != nil {
			return "", err
		}
		if !strings.EqualFold(u.Hostname(), "github.com") {
			return "", fmt.Errorf("only GitHub repositories are supported, got %s", u.Hostname())
		}
		ref = strings.TrimPrefix(u.Path, "/")
	}
	ref = strings.TrimPrefix(ref, "github.com/")
	parts := strings.Split(ref, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("repository must be github.com/owner/name or owner/name")
	}
	return "github.com/" + parts[0] + "/" + strings.TrimSuffix(parts[1], ".git"), nil
}

func repoOwner(repo string) string {
	parts := strings.Split(repo, "/")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

func sourceMatchesOwner(source *cloudapi.Source, owner string) bool {
	return strings.EqualFold(source.Owner, owner) || strings.EqualFold(source.Name, owner)
}

func sameRepository(a, b string) bool {
	na, errA := normalizeGitHubRepo(a)
	nb, errB := normalizeGitHubRepo(b)
	if errA != nil || errB != nil {
		return strings.EqualFold(a, b)
	}
	return strings.EqualFold(na, nb)
}

func sameString(value *string, want string) bool {
	return value != nil && strings.EqualFold(*value, want)
}

func repoStatus(repo *cloudapi.SourceRepository) string {
	if repo == nil {
		return "not_visible"
	}
	if repo.Selected {
		return "linked"
	}
	if !repo.Eligible {
		return "blocked"
	}
	return "available"
}

func repoStatusMessage(state *repoCloudState) string {
	switch state.Status {
	case "linked":
		return "Repo is linked to this Dagger Cloud org."
	case "available":
		return "Repo is visible and can be linked to this Dagger Cloud org."
	case "blocked":
		return "Repo is visible, but claimed by another Dagger Cloud org."
	case "not_visible":
		return "GitHub App installation exists, but this repo is not visible to it."
	default:
		return "No GitHub App installation for this repo owner is visible to this user."
	}
}

func repoVisibilityError(state *repoCloudState) error {
	switch state.Status {
	case "not_visible":
		return fmt.Errorf("%s Configure GitHub App access: %s", repoStatusMessage(state), state.ActionURL)
	default:
		return fmt.Errorf("%s Run `dagger integration github connect` or install the Dagger GitHub App for the repo owner", repoStatusMessage(state))
	}
}

func appendUniqueRepository(repos []string, repo string) []string {
	for _, existing := range repos {
		if sameRepository(existing, repo) {
			return repos
		}
	}
	repos = append(repos, repo)
	sort.Slice(repos, func(i, j int) bool { return strings.ToLower(repos[i]) < strings.ToLower(repos[j]) })
	return repos
}

func removeRepository(repos []string, repo string) []string {
	filtered := repos[:0]
	for _, existing := range repos {
		if !sameRepository(existing, repo) {
			filtered = append(filtered, existing)
		}
	}
	return filtered
}

func printRepoInfo(cmd *cobra.Command, state *repoCloudState) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Repository: %s\n", state.Repository)
	fmt.Fprintf(out, "Org:        %s\n", state.Org.Name)
	fmt.Fprintf(out, "Status:     %s\n", state.Status)
	if state.Source != nil {
		fmt.Fprintf(out, "Source:     %s (%s)\n", state.Source.Name, state.Source.ID)
	}
	if state.MappedSource != nil {
		fmt.Fprintf(out, "Mode:       %s\n", state.MappedSource.Mode)
	}
	if state.Repo != nil {
		visibility := "public"
		if state.Repo.Private != nil && *state.Repo.Private {
			visibility = "private"
		}
		fmt.Fprintf(out, "Visibility: %s\n", visibility)
		if state.Repo.ClaimedByOrgName != nil {
			fmt.Fprintf(out, "Claimed by: %s\n", *state.Repo.ClaimedByOrgName)
		}
	}
	if len(state.RepoSettings) > 0 {
		fmt.Fprintf(out, "Public:     %t\n", state.RepoSettings[0].IsPublic)
	}
	if state.ActionURL != "" && state.Status != "linked" {
		fmt.Fprintf(out, "GitHub URL: %s\n", state.ActionURL)
	}
	fmt.Fprintf(out, "\n%s\n", state.Message)
	fmt.Fprintln(out, "Autocheck: on (read-only mock setting)")
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
		fmt.Fprintln(out, "Installations: none visible")
		return
	}
	fmt.Fprintln(out, "\nInstallations:")
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SOURCE\tINSTALLATION\tTYPE\tDAGGER ORG\tCONFIG URL")
	for _, source := range sources {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", source.Name, source.ID, source.Type, stringValue(source.OrgName), source.ConfigURL)
	}
	_ = w.Flush()
}
