package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
	"github.com/spf13/cobra"
)

var (
	orgCreatePlan            string
	orgCreateEnableAutocheck bool
	orgCreateRepos           []string
	orgCreateSource          string
)

var orgCmd = &cobra.Command{
	Use:   "org",
	Short: "Manage Dagger Cloud organizations",
}

var orgListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Dagger Cloud organizations",
	Args:  cobra.NoArgs,
	RunE:  cloudCLI.OrgList,
}

var orgInfoCmd = &cobra.Command{
	Use:   "info [org]",
	Short: "Show Dagger Cloud organization status",
	Args:  cobra.MaximumNArgs(1),
	RunE:  cloudCLI.OrgInfo,
}

var orgUseCmd = &cobra.Command{
	Use:   "use <org>",
	Short: "Select the current Dagger Cloud organization",
	Args:  cobra.ExactArgs(1),
	RunE:  cloudCLI.OrgUse,
}

var orgCreateCmd = &cobra.Command{
	Use:   "create <org>",
	Short: "Create a Dagger Cloud organization",
	Long: `Create a Dagger Cloud organization.

The CLI can create individual-plan orgs directly. Paid plans require browser
checkout because payment details are collected by Chargebee.

Use --enable-autocheck to create the org and enable autocheck for the current
repository in one step when a matching GitHub installation is already visible.`,
	Args: cobra.ExactArgs(1),
	RunE: cloudCLI.OrgCreate,
}

var orgEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enable Dagger Cloud organization features",
}

var orgEnableCloudModulesCmd = &cobra.Command{
	Use:   "cloud-modules [org]",
	Short: "Start the Cloud Modules trial for an organization",
	Args:  cobra.MaximumNArgs(1),
	RunE:  cloudCLI.OrgEnableCloudModules,
}

func init() {
	orgCmd.PersistentFlags().BoolVar(&cloudJSON, "json", false, "Print JSON output")
	orgCreateCmd.Flags().StringVar(&orgCreatePlan, "plan", "individual-ea-fp", "Plan to use for the new org (CLI supports individual-ea-fp directly)")
	orgCreateCmd.Flags().BoolVar(&orgCreateEnableAutocheck, "enable-autocheck", false, "Enable autocheck for the current repository while creating the org")
	orgCreateCmd.Flags().StringArrayVar(&orgCreateRepos, "repo", nil, "Repository to enable autocheck for, in owner/name form (repeatable)")
	orgCreateCmd.Flags().StringVar(&orgCreateSource, "source", "", "GitHub installation/source name or ID to use when enabling repos")

	orgEnableCmd.AddCommand(orgEnableCloudModulesCmd)
	orgCmd.AddCommand(orgListCmd, orgInfoCmd, orgUseCmd, orgCreateCmd, orgEnableCmd)
	rootCmd.AddCommand(orgCmd)
}

func (cli *CloudCLI) OrgList(cmd *cobra.Command, args []string) error {
	client, _, err := cli.cloudClient(cmd.Context())
	if err != nil {
		return err
	}
	user, err := client.User(cmd.Context())
	if err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, user.Orgs)
	}
	printOrgList(cmd, user.Orgs)
	return nil
}

func (cli *CloudCLI) OrgInfo(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, cloudAuth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	var org *cloudapi.OrgResponse
	if len(args) > 0 {
		org, err = client.OrgByName(ctx, args[0])
	} else {
		org, err = cli.resolveCloudOrg(ctx, client, cloudAuth)
	}
	if err != nil {
		return err
	}
	details, err := client.OrgDetails(ctx, org.Name)
	if err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, details)
	}
	printOrgInfo(cmd, details)
	return nil
}

func (cli *CloudCLI) OrgUse(cmd *cobra.Command, args []string) error {
	client, _, err := cli.cloudClient(cmd.Context())
	if err != nil {
		return err
	}
	org, err := client.OrgByName(cmd.Context(), args[0])
	if err != nil {
		return err
	}
	if err := cloudauth.SetCurrentOrg(&cloudauth.Org{ID: org.ID, Name: org.Name}); err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, org)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Current org: %s\n", org.Name)
	return nil
}

func (cli *CloudCLI) OrgCreate(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	name := strings.ToLower(strings.TrimSpace(args[0]))
	if name == "" {
		return fmt.Errorf("org name is required")
	}

	planID, err := normalizeOrgCreatePlan(orgCreatePlan)
	if err != nil {
		return err
	}
	if planID != "individual-ea-fp" {
		return fmt.Errorf("plan %q requires browser checkout; use `dagger billing plans` to inspect plans, then create it from Dagger Cloud signup", planID)
	}

	client, _, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}

	repos, err := orgCreateRepositories(ctx)
	if err != nil {
		return err
	}
	var selections []cloudapi.SourceSelectionInput
	if len(repos) > 0 {
		selections, err = cli.sourceSelectionsForRepos(ctx, client, repos, orgCreateSource)
		if err != nil {
			return err
		}
	}

	var org *cloudapi.OrgResponse
	if len(selections) > 0 {
		org, err = client.CreateQuickstartOrgWithSourceSelections(ctx, name, selections)
	} else {
		org, err = client.CreateQuickstartOrg(ctx, name)
	}
	if err != nil {
		return err
	}
	if err := cloudauth.SetCurrentOrg(&cloudauth.Org{ID: org.ID, Name: org.Name}); err != nil {
		return err
	}

	result := map[string]any{
		"org":            org,
		"plan":           planID,
		"sourceMappings": selections,
		"repositories":   repos,
	}
	if cloudJSON {
		return writeCloudJSON(cmd, result)
	}
	printOrgCreate(cmd, org, planID, repos)
	return nil
}

func (cli *CloudCLI) OrgEnableCloudModules(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, cloudAuth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	var org *cloudapi.OrgResponse
	if len(args) > 0 {
		org, err = client.OrgByName(ctx, args[0])
	} else {
		org, err = cli.resolveCloudOrg(ctx, client, cloudAuth)
	}
	if err != nil {
		return err
	}
	ok, err := client.EnableCloudModulesTrial(ctx, org.ID)
	if err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, map[string]any{"ok": ok, "org": org, "feature": "cloud-modules"})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Cloud Modules trial enabled for %s.\n", org.Name)
	return nil
}

func normalizeOrgCreatePlan(plan string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(plan)) {
	case "", "individual", "individual-ea-fp":
		return "individual-ea-fp", nil
	case "team", "team-ea-fp":
		return "team-ea-fp", nil
	default:
		return "", fmt.Errorf("unsupported plan %q; run `dagger billing plans`", plan)
	}
}

func orgCreateRepositories(ctx context.Context) ([]string, error) {
	repos := append([]string{}, orgCreateRepos...)
	if orgCreateEnableAutocheck && len(repos) == 0 {
		repo, err := repoFromArgOrGit(ctx, nil)
		if err != nil {
			return nil, err
		}
		repos = append(repos, repo)
	}
	if len(repos) == 0 {
		return nil, nil
	}
	normalized := []string{}
	seen := map[string]struct{}{}
	for _, repo := range repos {
		if repo == "." {
			localRepo, err := repoFromArgOrGit(ctx, nil)
			if err != nil {
				return nil, err
			}
			repo = localRepo
		}
		normalizedRepo, err := normalizeGitHubRepo(repo)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(normalizedRepo)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, normalizedRepo)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func (cli *CloudCLI) sourceSelectionsForRepos(ctx context.Context, client *cloudapi.Client, repos []string, sourceRef string) ([]cloudapi.SourceSelectionInput, error) {
	sources, err := client.Sources(ctx)
	if err != nil {
		return nil, err
	}
	reposBySource := map[string][]string{}
	sourceOrder := []string{}
	for _, repo := range repos {
		source, matchedRepo, err := cli.sourceForRepo(ctx, client, sources, repo, sourceRef)
		if err != nil {
			return nil, err
		}
		if !matchedRepo.Eligible {
			return nil, fmt.Errorf("repo %s is claimed by %s", repo, stringValue(matchedRepo.ClaimedByOrgName))
		}
		if _, ok := reposBySource[source.ID]; !ok {
			sourceOrder = append(sourceOrder, source.ID)
		}
		reposBySource[source.ID] = appendUniqueRepository(reposBySource[source.ID], repo)
	}
	selections := make([]cloudapi.SourceSelectionInput, 0, len(sourceOrder))
	for _, sourceID := range sourceOrder {
		selections = append(selections, cloudapi.SourceSelectionInput{
			InstallationID: sourceID,
			Mode:           cloudapi.SourceModeSelected,
			Repositories:   reposBySource[sourceID],
		})
	}
	return selections, nil
}

func (cli *CloudCLI) sourceForRepo(ctx context.Context, client *cloudapi.Client, sources []cloudapi.Source, repo string, sourceRef string) (*cloudapi.Source, *cloudapi.SourceRepository, error) {
	type repoMatch struct {
		source *cloudapi.Source
		repo   *cloudapi.SourceRepository
	}
	matches := []repoMatch{}
	for i := range sources {
		source := &sources[i]
		if sourceRef != "" && matchSourceRef(source, sourceRef) == nil {
			continue
		}
		repos, err := client.SourceRepositories(ctx, source.ID, "")
		if err != nil {
			return nil, nil, err
		}
		for j := range repos {
			if sameRepository(repos[j].Repository, repo) {
				matches = append(matches, repoMatch{source: source, repo: &repos[j]})
			}
		}
	}
	if len(matches) == 0 {
		if sourceRef != "" {
			return nil, nil, fmt.Errorf("repo %s is not visible from source %q", repo, sourceRef)
		}
		return nil, nil, fmt.Errorf("repo %s is not visible to a GitHub installation; run `dagger integration add github`", repo)
	}
	if len(matches) == 1 {
		return matches[0].source, matches[0].repo, nil
	}

	owner := repoOwner(repo)
	ownerMatches := []repoMatch{}
	for _, match := range matches {
		if sourceMatchesOwner(match.source, owner) {
			ownerMatches = append(ownerMatches, match)
		}
	}
	if len(ownerMatches) == 1 {
		return ownerMatches[0].source, ownerMatches[0].repo, nil
	}
	choices := []string{}
	for _, match := range matches {
		choices = append(choices, fmt.Sprintf("%s (%s)", match.source.Name, match.source.ID))
	}
	sort.Strings(choices)
	return nil, nil, fmt.Errorf("repo %s is visible from multiple GitHub installations; pass --source with one of: %s", repo, strings.Join(choices, ", "))
}

func matchSourceRef(source *cloudapi.Source, ref string) *cloudapi.Source {
	if source == nil {
		return nil
	}
	if source.ID == ref || strings.EqualFold(source.Name, ref) || strings.EqualFold(source.Owner, ref) {
		return source
	}
	return nil
}

func printOrgList(cmd *cobra.Command, orgs []cloudauth.Org) {
	if len(orgs) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No Dagger Cloud organizations found.")
		return
	}
	current, _ := cloudauth.CurrentOrgName()
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tID\tCURRENT")
	for _, org := range orgs {
		fmt.Fprintf(w, "%s\t%s\t%t\n", org.Name, org.ID, strings.EqualFold(org.Name, current))
	}
	_ = w.Flush()
}

func printOrgInfo(cmd *cobra.Command, org *cloudapi.OrgDetails) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Org:          %s\n", org.Name)
	fmt.Fprintf(out, "ID:           %s\n", org.ID)
	fmt.Fprintf(out, "Created:      %s\n", org.CreatedAt)
	fmt.Fprintf(out, "Plan:         %s\n", org.Subscription.PlanID)
	fmt.Fprintf(out, "Subscription: %s\n", org.Subscription.Status)
	fmt.Fprintf(out, "Caching:      %s\n", onOff(org.Subscription.HasCaching))
	if len(org.Features) == 0 {
		return
	}
	fmt.Fprintln(out, "\nFeatures:")
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATUS\tTRIAL END")
	for _, feature := range org.Features {
		fmt.Fprintf(w, "%s\t%s\t%s\n", feature.Name, feature.Status, stringValue(feature.TrialEnd))
	}
	_ = w.Flush()
}

func printOrgCreate(cmd *cobra.Command, org *cloudapi.OrgResponse, planID string, repos []string) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Created org: %s\n", org.Name)
	fmt.Fprintf(out, "Plan:        %s\n", planID)
	fmt.Fprintf(out, "Current org: %s\n", org.Name)
	if len(repos) == 0 {
		return
	}
	fmt.Fprintln(out, "\nEnabled autocheck:")
	for _, repo := range repos {
		fmt.Fprintf(out, "- %s\n", repo)
	}
}
