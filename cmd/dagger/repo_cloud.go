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
	repoListEnabled bool
	repoListFeature string
	githubOpen      bool
)

const githubOAuthRedirect = "https://dagger.cloud/github/callback"

var repoCmd = &cobra.Command{
	Use:     "repo [repo]",
	Short:   "Manage Dagger Cloud repositories",
	Args:    cobra.MaximumNArgs(1),
	GroupID: cloudGroup.ID,
	RunE:    cloudCLI.RepoInfo,
}

var repoInfoCmd = &cobra.Command{
	Use:   "info [repo]",
	Short: "Show Dagger Cloud status for a repository",
	Args:  cobra.MaximumNArgs(1),
	RunE:  cloudCLI.RepoInfo,
}

var repoListCmd = &cobra.Command{
	Use:   "list",
	Short: "List repositories visible to this Dagger Cloud org",
	Args:  cobra.NoArgs,
	RunE:  cloudCLI.RepoList,
}

var repoEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enable Dagger Cloud features for a repository",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var repoEnableAutocheckCmd = &cobra.Command{
	Use:   "autocheck [repo]",
	Short: "Enable automatic GitHub checks for a repository",
	Args:  cobra.MaximumNArgs(1),
	RunE:  cloudCLI.RepoEnableAutocheck,
}

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

var integrationGithubCmd = &cobra.Command{
	Use:   "github",
	Short: "Inspect the GitHub integration used by Dagger Cloud",
	Args:  cobra.NoArgs,
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
	repoCmd.PersistentFlags().BoolVar(&cloudJSON, "json", false, "Print JSON output")
	repoListCmd.Flags().BoolVar(&repoListEnabled, "enabled", false, "Only show enabled repositories")
	repoListCmd.Flags().StringVar(&repoListFeature, "feature", "", "Only show repositories with this feature enabled (currently only autocheck)")
	repoEnableAutocheckCmd.Flags().BoolVar(&githubOpen, "open", false, "Open setup URL in a browser")

	repoEnableCmd.AddCommand(repoEnableAutocheckCmd)
	repoCmd.AddCommand(repoInfoCmd, repoListCmd, repoEnableCmd)

	integrationCmd.PersistentFlags().BoolVar(&cloudJSON, "json", false, "Print JSON output")
	integrationAddCmd.Flags().BoolVar(&githubOpen, "open", false, "Open the OAuth URL in a browser")
	integrationGithubConnectCmd.Flags().BoolVar(&githubOpen, "open", false, "Open the OAuth URL in a browser")
	integrationGithubCmd.AddCommand(
		integrationGithubInfoCmd,
		integrationGithubConnectCmd,
		integrationGithubDisconnectCmd,
		integrationGithubInstallationsCmd,
	)
	integrationCmd.AddCommand(integrationAddCmd, integrationGithubCmd)

	rootCmd.AddCommand(repoCmd, integrationCmd)
}

type repoCloudState struct {
	Org          *cloudapi.OrgResponse      `json:"org"`
	Repository   string                     `json:"repository"`
	Remote       string                     `json:"remote,omitempty"`
	Local        string                     `json:"local,omitempty"`
	Status       string                     `json:"status"`
	Features     repoFeatureSet             `json:"features"`
	Source       *cloudapi.Source           `json:"source,omitempty"`
	MappedSource *cloudapi.MappedSource     `json:"mappedSource,omitempty"`
	Repo         *cloudapi.SourceRepository `json:"repo,omitempty"`
	RepoSettings []cloudapi.RepoSetting     `json:"repoSettings,omitempty"`
	Message      string                     `json:"message,omitempty"`
	ActionURL    string                     `json:"actionUrl,omitempty"`
	Mutation     string                     `json:"mutation,omitempty"`
}

type repoFeatureSet struct {
	Autocheck repoFeature `json:"autocheck"`
}

type repoFeature struct {
	Enabled bool `json:"enabled"`
}

type repoListEntry struct {
	Repository  string         `json:"repository"`
	Status      string         `json:"status"`
	Integration string         `json:"integration,omitempty"`
	SourceID    string         `json:"sourceId,omitempty"`
	URL         string         `json:"url,omitempty"`
	Features    repoFeatureSet `json:"features"`
}

type repoRef struct {
	Repository string
	Input      string
	Remote     string
	Local      string
}

type githubSetupHandoff struct {
	Label       string
	URL         string
	RedirectURI string
}

func (cli *CloudCLI) RepoInfo(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	ref, err := resolveRepoRef(ctx, args)
	if err != nil {
		return err
	}
	client, cloudAuth, err := cli.cloudClientNoLogin(ctx)
	if err != nil {
		if cloudJSON {
			response := map[string]any{
				"repository":             ref.Repository,
				"fieldsRequireLogin":     []string{"org", "integration", "autocheck"},
				"recommendedNextCommand": "dagger login",
			}
			if ref.Input != "" {
				response["input"] = ref.Input
			}
			if ref.Remote != "" {
				response["remote"] = ref.Remote
			}
			if ref.Local != "" {
				response["local"] = ref.Local
			}
			return writeCloudJSON(cmd, response)
		}
		printRepoInfoLoggedOut(cmd, ref)
		return nil
	}
	org, err := cli.resolveCloudOrg(ctx, client, cloudAuth)
	if err != nil {
		return err
	}
	state, err := cli.inspectRepo(ctx, client, org, ref.Repository)
	if err != nil {
		return err
	}
	state.Remote = ref.Remote
	state.Local = ref.Local
	settings, err := client.RepoSettings(ctx, org.Name, ref.Repository)
	if err != nil {
		return err
	}
	state.RepoSettings = settings
	state.Message = repoStatusMessage(state)

	if cloudJSON {
		return writeRepoStateJSON(cmd, state)
	}
	printRepoInfo(cmd, state)
	return nil
}

func (cli *CloudCLI) RepoList(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if repoListFeature != "" && !strings.EqualFold(repoListFeature, "autocheck") {
		return fmt.Errorf("unsupported repo feature %q; supported features: autocheck", repoListFeature)
	}
	client, cloudAuth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	org, err := cli.resolveCloudOrg(ctx, client, cloudAuth)
	if err != nil {
		return err
	}
	entries, err := cli.listRepos(ctx, client, org)
	if err != nil {
		return err
	}
	if repoListEnabled || repoListFeature != "" {
		filtered := entries[:0]
		for _, entry := range entries {
			if entry.Features.Autocheck.Enabled {
				filtered = append(filtered, entry)
			}
		}
		entries = filtered
	}
	if cloudJSON {
		return writeCloudJSON(cmd, entries)
	}
	printRepoList(cmd, entries)
	return nil
}

func (cli *CloudCLI) RepoEnableAutocheck(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	repo, err := repoFromArgOrGit(ctx, args)
	if err != nil {
		return err
	}
	client, cloudAuth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	org, err := cli.resolveCloudOrg(ctx, client, cloudAuth)
	if err != nil {
		return err
	}
	state, err := cli.linkRepo(ctx, client, org, repo)
	if err != nil {
		if state != nil && state.Repo == nil {
			setupLabel, setupErr := cli.prepareRepoGitHubSetup(cmd.Context(), client, state)
			if setupErr != nil {
				return fmt.Errorf("%w\nfailed to start GitHub setup: %v", err, setupErr)
			}
			state.Message = fmt.Sprintf("%s required. Complete setup, then rerun `dagger repo enable autocheck %s`.", setupLabel, repo)
			if cloudJSON {
				if jsonErr := writeRepoStateJSON(cmd, state); jsonErr != nil {
					return jsonErr
				}
			} else {
				cli.printRepoGitHubSetup(cmd, setupLabel, state.ActionURL, repo)
			}
		}
		return err
	}
	if cloudJSON {
		return writeRepoStateJSON(cmd, state)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s\n", state.Message)
	return nil
}

func (cli *CloudCLI) IntegrationAdd(cmd *cobra.Command, args []string) error {
	switch strings.ToLower(args[0]) {
	case "github":
		return cli.IntegrationGitHubConnect(cmd, nil)
	default:
		return fmt.Errorf("unsupported integration %q; supported integrations: github", args[0])
	}
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
	fmt.Fprintln(out)
	fmt.Fprintln(out, "After connecting GitHub, rerun:")
	fmt.Fprintln(out, "  dagger repo enable autocheck")
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
			state.Features = repoFeatures(state.Status)
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
			state.Features = repoFeatures(state.Status)
			return state, nil
		}
	}
	state.Features = repoFeatures(state.Status)
	return state, nil
}

func (cli *CloudCLI) listRepos(ctx context.Context, client *cloudapi.Client, org *cloudapi.OrgResponse) ([]repoListEntry, error) {
	gitSources, err := client.GitSources(ctx, org.Name)
	if err != nil {
		return nil, err
	}
	sources, err := client.Sources(ctx)
	if err != nil {
		return nil, err
	}

	mappedByInstallation := map[string]*cloudapi.MappedSource{}
	for i := range gitSources.Org.MappedSources {
		mapped := &gitSources.Org.MappedSources[i]
		mappedByInstallation[mapped.InstallationID] = mapped
	}

	byRepo := map[string]repoListEntry{}
	for i := range sources {
		source := &sources[i]
		repos, err := client.SourceRepositories(ctx, source.ID, org.ID)
		if err != nil {
			return nil, err
		}
		mapped := mappedByInstallation[source.ID]
		for j := range repos {
			repo := &repos[j]
			status := repoStatus(repo)
			entry := repoListEntry{
				Repository:  repo.Repository,
				Status:      repoDisplayStatus(status),
				Integration: source.Name,
				SourceID:    source.ID,
				Features:    repoFeatures(status),
			}
			if repo.HTMLURL != nil {
				entry.URL = *repo.HTMLURL
			}
			if mapped != nil {
				entry.Integration = mapped.SourceName
			}
			key := strings.ToLower(repo.Repository)
			existing, ok := byRepo[key]
			if !ok || (!existing.Features.Autocheck.Enabled && entry.Features.Autocheck.Enabled) {
				byRepo[key] = entry
			}
		}
	}

	entries := make([]repoListEntry, 0, len(byRepo))
	for _, entry := range byRepo {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Repository) < strings.ToLower(entries[j].Repository)
	})
	return entries, nil
}

func (cli *CloudCLI) linkRepo(ctx context.Context, client *cloudapi.Client, org *cloudapi.OrgResponse, repo string) (*repoCloudState, error) {
	state, err := cli.inspectRepo(ctx, client, org, repo)
	if err != nil {
		return nil, err
	}
	if state.Repo == nil {
		return state, repoVisibilityError(state)
	}
	if !state.Repo.Eligible {
		return nil, fmt.Errorf("repo %s is claimed by %s", repo, stringValue(state.Repo.ClaimedByOrgName))
	}
	if state.Repo.Selected {
		state.Mutation = "noop"
		state.Message = fmt.Sprintf("Autocheck is already enabled for %s.", repo)
		return state, nil
	}

	sourceID := repoStateSourceID(state)
	if sourceID == "" {
		return nil, fmt.Errorf("repo %s is visible, but its GitHub integration could not be resolved; run 'dagger integration github installations'", repo)
	}

	selected, err := cli.selectedReposForSource(ctx, client, org, sourceID)
	if err != nil {
		return nil, err
	}
	selected = appendUniqueRepository(selected, repo)
	mapped, err := client.ConfigureOrgSource(ctx, org.ID, cloudapi.SourceSelectionInput{
		InstallationID: sourceID,
		Mode:           cloudapi.SourceModeSelected,
		Repositories:   selected,
	})
	if err != nil {
		return nil, err
	}
	state.MappedSource = mapped
	state.Status = "linked"
	state.Features = repoFeatures(state.Status)
	state.Mutation = "configureOrgSource"
	state.Message = fmt.Sprintf("Enabled autocheck for %s. Module scan queued.", repo)
	return state, nil
}

func (cli *CloudCLI) prepareRepoGitHubSetup(ctx context.Context, client *cloudapi.Client, state *repoCloudState) (string, error) {
	label := "Configure GitHub App access"
	if state.ActionURL == "" {
		setup, err := cli.githubConnectHandoff(ctx, client)
		if err != nil {
			return "", err
		}
		state.ActionURL = setup.URL
		label = setup.Label
	}
	return label, nil
}

func (cli *CloudCLI) printRepoGitHubSetup(cmd *cobra.Command, label, setupURL, repo string) {
	openGitHubSetupURL(cmd, setupURL)
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s: %s\n", label, setupURL)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "After setup, rerun:")
	fmt.Fprintf(out, "  dagger repo enable autocheck %s\n", repo)
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

func repoStateSourceID(state *repoCloudState) string {
	if state.Source != nil {
		return state.Source.ID
	}
	if state.MappedSource != nil {
		return state.MappedSource.InstallationID
	}
	return ""
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
	ref, err := resolveRepoRef(ctx, args)
	if err != nil {
		return "", err
	}
	return ref.Repository, nil
}

func resolveRepoRef(ctx context.Context, args []string) (repoRef, error) {
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		repo, err := normalizeGitHubRepo(args[0])
		if err != nil {
			return repoRef{}, err
		}
		return repoRef{Repository: repo, Input: redactGitRemote(args[0])}, nil
	}
	remote, err := gitRemoteOriginURL(ctx)
	if err != nil {
		return repoRef{}, err
	}
	repo, err := normalizeGitHubRepo(remote)
	if err != nil {
		return repoRef{}, err
	}
	return repoRef{Repository: repo, Remote: redactGitRemote(remote), Local: "."}, nil
}

func gitRemoteOriginURL(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return "", fmt.Errorf("no repo specified and git remote.origin.url could not be read")
	}
	return strings.TrimSpace(string(out)), nil
}

func normalizeGitHubRepo(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("empty repository")
	}

	ref, _, _ = strings.Cut(ref, "?")
	ref, _, _ = strings.Cut(ref, "#")
	host, path, err := splitGitRepoRef(ref)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(host, "github.com") {
		return "", fmt.Errorf("only GitHub repositories are supported, got %s", host)
	}
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("repository must be github.com/owner/name")
	}
	return "github.com/" + parts[0] + "/" + parts[1], nil
}

func splitGitRepoRef(ref string) (string, string, error) {
	if strings.Contains(ref, "://") {
		u, err := url.Parse(ref)
		if err != nil {
			return "", "", err
		}
		return u.Hostname(), strings.TrimPrefix(u.Path, "/"), nil
	}

	before, after, ok := strings.Cut(ref, ":")
	if ok && strings.Contains(before, "@") && !strings.Contains(before, "/") {
		_, host, _ := strings.Cut(before, "@")
		return host, after, nil
	}

	parts := strings.SplitN(strings.TrimPrefix(ref, "/"), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || !strings.Contains(parts[0], ".") {
		return "", "", fmt.Errorf("repository must include a git host, e.g. github.com/owner/name")
	}
	return parts[0], parts[1], nil
}

func redactGitRemote(ref string) string {
	u, err := url.Parse(ref)
	if err != nil || u.Scheme == "" || u.Host == "" {
		ref, _, _ = strings.Cut(ref, "?")
		ref, _, _ = strings.Cut(ref, "#")
		return ref
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
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

func repoDisplayStatus(status string) string {
	if status == "linked" {
		return "enabled"
	}
	return status
}

func repoFeatures(status string) repoFeatureSet {
	return repoFeatureSet{
		Autocheck: repoFeature{Enabled: status == "linked"},
	}
}

func repoStatusMessage(state *repoCloudState) string {
	switch state.Status {
	case "linked":
		return "Repo is enabled for this Dagger Cloud org."
	case "available":
		return "Repo is visible and autocheck can be enabled for this Dagger Cloud org."
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
		return fmt.Errorf("%s Run `dagger integration add github` or install the Dagger GitHub App for the repo owner", repoStatusMessage(state))
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

func printRepoInfo(cmd *cobra.Command, state *repoCloudState) {
	out := cmd.OutOrStdout()
	if state.Remote != "" {
		fmt.Fprintf(out, "remote=%q\n", state.Remote)
	}
	if state.Local != "" {
		fmt.Fprintf(out, "local=%q\n", state.Local)
	}
	fmt.Fprintf(out, "Repository: %s\n", state.Repository)
	fmt.Fprintf(out, "Org:        %s\n", state.Org.Name)
	fmt.Fprintf(out, "Status:     %s\n", repoDisplayStatus(state.Status))
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
	fmt.Fprintf(out, "Autocheck: %s\n", onOff(state.Features.Autocheck.Enabled))
}

func printRepoInfoLoggedOut(cmd *cobra.Command, ref repoRef) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Repository: %s\n", ref.Repository)
	if ref.Input != "" {
		fmt.Fprintf(out, "Input:      %s\n", ref.Input)
	}
	if ref.Remote != "" {
		fmt.Fprintf(out, "Git remote: %s\n", ref.Remote)
	}
	if ref.Local != "" {
		fmt.Fprintf(out, "Local:      %s\n", ref.Local)
	}
	fmt.Fprintln(out, "# Fields below require \"dagger login\":")
	fmt.Fprintln(out, "# org")
	fmt.Fprintln(out, "# integration")
	fmt.Fprintln(out, "# autocheck")
}

func writeRepoStateJSON(cmd *cobra.Command, state *repoCloudState) error {
	display := *state
	display.Status = repoDisplayStatus(state.Status)
	return writeCloudJSON(cmd, &display)
}

func printRepoList(cmd *cobra.Command, entries []repoListEntry) {
	out := cmd.OutOrStdout()
	if len(entries) == 0 {
		fmt.Fprintln(out, "No repositories found.")
		return
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "REPOSITORY\tINTEGRATION\tSTATUS\tAUTOCHECK\tURL")
	for _, entry := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			entry.Repository,
			entry.Integration,
			entry.Status,
			onOff(entry.Features.Autocheck.Enabled),
			entry.URL,
		)
	}
	_ = w.Flush()
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
