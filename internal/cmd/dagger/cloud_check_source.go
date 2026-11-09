package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
)

// cloudCheckProvider is the Git host that Cloud checks a repository through.
type cloudCheckProvider string

const (
	cloudCheckGitHub cloudCheckProvider = "GitHub"
	cloudCheckGitLab cloudCheckProvider = "GitLab"
	cloudCheckEntire cloudCheckProvider = "Entire"
)

const featureCloudEntireForge cloudFeature = "CLOUD_ENTIRE_FORGE"

// entireRepoSegments is the length of an Entire repository path, such as
// et/acme/api.
const entireRepoSegments = 3

// cloudCheckRepo returns the provider of a repository and the key Cloud
// stores it under. GitLab keeps its server and complete namespace; GitHub uses
// "github.com/<owner>/<name>" and Entire "entire.com/<forge>/<owner>/<name>".
// Entire repositories use their entire:// clone URL or entire.com web address.
func cloudCheckRepo(ref string) (cloudCheckProvider, string, error) {
	ref = strings.TrimSpace(ref)
	var path string
	if rest, ok := strings.CutPrefix(ref, "entire://"); ok {
		// The host is the Entire cluster, which Cloud does not key on.
		_, path, _ = strings.Cut(rest, "/")
	} else {
		web := strings.TrimPrefix(strings.TrimPrefix(ref, "https://"), "http://")
		var ok bool
		if path, ok = strings.CutPrefix(web, "entire.com/"); !ok {
			key := normalizeRepository(ref)
			host, path, valid := strings.Cut(key, "/")
			if !valid || path == "" {
				return "", "", fmt.Errorf("invalid Cloud repository %q", ref)
			}
			switch host {
			case "github.com":
				return cloudCheckGitHub, "github.com/" + normalizeGitHubRepo(ref), nil
			case "gitlab.com":
				return cloudCheckGitLab, key, nil
			default:
				// Cloud's saved source identifies a configured server's provider.
				return "", key, nil
			}
		}
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	segments := strings.Split(path, "/")
	if len(segments) < entireRepoSegments || segments[0] == "" || segments[1] == "" || segments[2] == "" {
		return "", "", fmt.Errorf("repository %q on Entire must be named as entire://<cluster-host>/<forge>/<owner>/<repo>", ref)
	}
	return cloudCheckEntire, "entire.com/" + strings.Join(segments[:entireRepoSegments], "/"), nil
}

type workspaceAutocheckState struct {
	Provider cloudCheckProvider
	// Repo is the repository's Cloud key.
	Repo string
	// Enabled reports whether one of the user's organizations has claimed the
	// repository for checks.
	Enabled bool
	// Triggers reports whether DefaultBranch and PullRequest are known; Cloud
	// returns them when the repository has its own settings.
	Triggers      bool
	DefaultBranch bool
	PullRequest   bool
	// OrgID and OrgName identify the organization that owns, or will own, the
	// claim.
	OrgID   string
	OrgName string
	// InstallationID is the Git source ID of the installation that reaches
	// the repository.
	InstallationID string
	// ConfigURL is the page that manages the installation's repository access,
	// when known.
	ConfigURL string
}

var errCloudSourceNotConfigured = errors.New("no Cloud source mapping found")

func loadWorkspaceAutocheckState(ctx context.Context, remote workspaceRemoteAddress) (workspaceAutocheckState, bool, error) {
	client, err := workspaceAutocheckClient(ctx, false)
	if err != nil {
		return workspaceAutocheckState{}, false, err
	}
	return findWorkspaceAutocheckState(ctx, client, remote.CloneRef)
}

// setWorkspaceAutocheckState turns Cloud checks on or off for the remote's
// repository. On runs checks on the default branch and on pull requests (Entire
// trails), claiming the repository for an organization when needed. Off turns
// every trigger off, which releases the claim.
func setWorkspaceAutocheckState(cmd *cobra.Command, remote workspaceRemoteAddress, enabled bool) (workspaceAutocheckState, error) {
	ctx := cmd.Context()
	client, err := workspaceAutocheckClient(ctx, true)
	if err != nil {
		return workspaceAutocheckState{}, err
	}
	current, claimed, err := findWorkspaceAutocheckState(ctx, client, remote.CloneRef)
	if err != nil {
		return workspaceAutocheckState{}, err
	}
	if !enabled {
		if !claimed {
			return current, nil
		}
		off := false
		if err := client.UpdateOrgRepoSettings(ctx, current.OrgID, []cloudapi.RepoSettingInput{{
			Repo:                     current.Repo,
			RunChecksOnDefaultBranch: &off,
			RunChecksOnPullRequest:   &off,
			RunChecksOnBranchPush:    &off,
		}}); err != nil {
			return workspaceAutocheckState{}, fmt.Errorf("turn workspace autocheck off: %w", err)
		}
		current.Enabled = false
		return current, nil
	}

	setting := cloudapi.RepoSettingInput{Repo: current.Repo}
	if claimed {
		// Enabling checks on a claimed repository must still ensure the org
		// has the required features, otherwise checks silently never run.
		if err := ensureCloudCheckFeatures(cmd, client, current.Provider, current.OrgName); err != nil {
			return workspaceAutocheckState{}, err
		}
		if current.Provider == cloudCheckGitLab {
			if err := findGitLabCheckSource(cmd, client, &current); err != nil {
				return workspaceAutocheckState{}, err
			}
		}
		if current.Triggers && current.DefaultBranch && current.PullRequest {
			return current, nil
		}
	} else {
		switch current.Provider {
		case cloudCheckEntire:
			err = findEntireCheckSource(cmd, client, &current)
		case cloudCheckGitLab, "":
			err = findGitLabCheckSource(cmd, client, &current)
		default:
			err = findGitHubCheckSource(cmd, client, &current)
		}
		if err != nil {
			return workspaceAutocheckState{}, err
		}
		setting.SourceID = &current.InstallationID
	}
	on := true
	setting.RunChecksOnDefaultBranch = &on
	setting.RunChecksOnPullRequest = &on
	if err := client.UpdateOrgRepoSettings(ctx, current.OrgID, []cloudapi.RepoSettingInput{setting}); err != nil {
		if isRepoNotInInstallation(err) {
			return workspaceAutocheckState{}, &repoAccessError{provider: current.Provider, settingsURL: installationSettingsURL(current)}
		}
		return workspaceAutocheckState{}, fmt.Errorf("turn workspace autocheck on: %w", err)
	}
	current.Enabled = true
	current.Triggers, current.DefaultBranch, current.PullRequest = true, true, true
	return current, nil
}

// findGitHubCheckSource locates the GitHub App installation that reaches
// state's repository by matching its owner against the user's Cloud sources,
// and the organization that claims it: the installation's organization, or
// the current one when the installation is not mapped yet.
func findGitHubCheckSource(cmd *cobra.Command, client *cloudapi.Client, state *workspaceAutocheckState) error {
	ctx := cmd.Context()
	sources, err := client.Sources(ctx)
	if err != nil {
		return fmt.Errorf("lookup Cloud sources: %w", err)
	}
	source, ok := workspaceSourceForRepo(sources, state.Repo)
	if !ok {
		// No source for the repo's owner means the Dagger Cloud GitHub App is
		// not installed on that user/org yet.
		return gitHubAppNotInstalledError(state.Repo)
	}
	var org *cloudauth.Org
	if source.OrgName != nil {
		// Check membership first for a clear ownership error instead of an
		// opaque "unauthorized" from the org-scoped mutation.
		if err := ensureUserInOrg(ctx, client, *source.OrgName, state.Repo); err != nil {
			return err
		}
		org = &cloudauth.Org{Name: *source.OrgName}
		if source.OrgID != nil {
			org.ID = *source.OrgID
		} else {
			details, err := client.OrgByName(ctx, *source.OrgName)
			if err != nil {
				return err
			}
			org.ID = details.ID
		}
	} else {
		org, err = currentCloudOrg(ctx, client)
		if err != nil {
			return err
		}
	}
	if err := ensureCloudCheckFeatures(cmd, client, cloudCheckGitHub, org.Name); err != nil {
		return err
	}
	state.OrgID, state.OrgName = org.ID, org.Name
	state.InstallationID, state.ConfigURL = source.ID, source.ConfigURL
	return nil
}

// Use saved organization access first, including a token or a grant from
// another admin. The explicit checks-on action may also grant access through
// the caller's own connected account, matching the Sources UI.
func findGitLabCheckSource(cmd *cobra.Command, client *cloudapi.Client, state *workspaceAutocheckState) error {
	ctx := cmd.Context()
	org := &cloudauth.Org{ID: state.OrgID, Name: state.OrgName}
	if org.ID == "" {
		var err error
		org, err = currentCloudOrg(ctx, client)
		if err != nil {
			return err
		}
	}
	if err := ensureCloudCheckFeatures(cmd, client, cloudCheckGitLab, org.Name); err != nil {
		return err
	}
	settings, err := gitLabIntegrationURL(org.Name)
	if err != nil {
		return err
	}
	state.OrgID, state.OrgName, state.ConfigURL = org.ID, org.Name, settings
	sources, err := client.GitLabOrganizationSources(ctx, org.ID)
	if err != nil {
		return fmt.Errorf("lookup GitLab access for organization %q: %w", org.Name, err)
	}
	source, found := gitLabSourceForRepo(sources, state.Repo)
	needsGrant := false
	if !found {
		available, err := client.GitLabSources(ctx, org.ID)
		if err != nil {
			return fmt.Errorf("lookup your GitLab accounts for organization %q: %w", org.Name, err)
		}
		source, found = gitLabSourceForRepo(available, state.Repo)
		needsGrant = found && source.Access == nil
	}
	if found {
		state.Provider = cloudCheckGitLab
		repositories, err := client.SourceRepositories(ctx, source.ID, org.ID)
		if err != nil {
			return fmt.Errorf("lookup repositories of GitLab namespace %q: %w", source.NamespacePath, err)
		}
		for _, repository := range repositories {
			if !sameCloudRepository(repository.Repository, state.Repo) {
				continue
			}
			if !repository.Eligible {
				return fmt.Errorf("%s is already enabled through another Cloud source or organization", state.Repo)
			}
			if needsGrant {
				if err := client.UseGitLabAccount(ctx, org.ID, source.ID); err != nil {
					return fmt.Errorf("grant GitLab access to organization %q: %w", org.Name, err)
				}
			}
			state.Repo, state.InstallationID = repository.Repository, source.ID
			return nil
		}
	}
	return &repoAccessError{provider: cloudCheckGitLab, settingsURL: settings}
}

// A namespace owns its direct repositories; a parent group cannot stand in for
// a subgroup, and equal names on different servers are different sources.
func gitLabSourceForRepo(sources []cloudapi.GitLabSource, repository string) (cloudapi.GitLabSource, bool) {
	for _, source := range sources {
		prefix := normalizeRepository(source.InstanceURL + "/" + source.NamespacePath)
		name, matches := strings.CutPrefix(repository, prefix+"/")
		if matches && name != "" && !strings.Contains(name, "/") {
			return source, true
		}
	}
	return cloudapi.GitLabSource{}, false
}

// GitLab approval is handled by the existing Cloud UI. The CLI never receives
// provider credentials. Preview and local APIs use their matching frontend.
func gitLabIntegrationURL(org string) (string, error) {
	base := os.Getenv("DAGGER_CLOUD_EXTERNAL_URL")
	if base == "" {
		base = "https://dagger.cloud"
		api, err := url.Parse(os.Getenv("DAGGER_CLOUD_URL"))
		if err == nil {
			if prefix, ok := strings.CutSuffix(api.Hostname(), "-api.preview.dagger.cloud"); ok {
				base = "https://" + prefix + "-v3.preview.dagger.cloud"
			} else if api.Hostname() == "localhost" || api.Hostname() == "127.0.0.1" {
				base = "http://localhost:3000"
			}
		}
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("DAGGER_CLOUD_EXTERNAL_URL must be a Cloud web address")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + url.PathEscape(org) + "/integrations/gitlab"
	return u.String(), nil
}

// findEntireCheckSource locates the Entire installation of the current
// organization that reaches state's repository.
func findEntireCheckSource(cmd *cobra.Command, client *cloudapi.Client, state *workspaceAutocheckState) error {
	ctx := cmd.Context()
	org, err := currentCloudOrg(ctx, client)
	if err != nil {
		return err
	}
	if err := ensureCloudCheckFeatures(cmd, client, cloudCheckEntire, org.Name); err != nil {
		return err
	}
	state.OrgID, state.OrgName = org.ID, org.Name
	installations, err := client.EntireInstallations(ctx, org.ID)
	if err != nil {
		return fmt.Errorf("lookup Entire installations of organization %q: %w", org.Name, err)
	}
	for _, installation := range installations {
		repositories, err := client.SourceRepositories(ctx, installation.ID, org.ID)
		if err != nil {
			return fmt.Errorf("lookup repositories of Entire project %q: %w", installation.Name, err)
		}
		for _, repository := range repositories {
			if repository.Repository != state.Repo {
				continue
			}
			if !repository.Eligible {
				owner := "another Dagger Cloud organization"
				if repository.ClaimedByOrgName != nil {
					owner = fmt.Sprintf("the %q Dagger Cloud organization", *repository.ClaimedByOrgName)
				}
				return fmt.Errorf("%s already runs checks in %s", state.Repo, owner)
			}
			state.InstallationID, state.ConfigURL = installation.ID, installation.ManageURL
			return nil
		}
	}
	return &entirePluginNotInstalledError{repo: state.Repo, orgID: org.ID, orgName: org.Name}
}

// entirePluginNotInstalledError reports that no Entire installation of the
// organization reaches the repository: the Dagger plugin is not installed on
// the repository's Entire project yet.
type entirePluginNotInstalledError struct {
	repo    string
	orgID   string
	orgName string
}

func (e *entirePluginNotInstalledError) Error() string {
	return fmt.Sprintf("%s: the Dagger plugin is not installed on the Entire project of %s for organization %q",
		errCloudSourceNotConfigured, e.repo, e.orgName)
}

func (e *entirePluginNotInstalledError) Unwrap() error { return errCloudSourceNotConfigured }

// entireInstallRedirect returns the Cloud page that Entire returns the
// browser to after installing the Dagger plugin. It follows the same
// environment rule as githubOAuthRedirect.
func entireInstallRedirect() string {
	if url := os.Getenv("DAGGER_CLOUD_URL"); url != "" && !strings.Contains(url, "://api.dagger.cloud") {
		return "http://localhost:3000/setup/entire"
	}
	return "https://dagger.cloud/setup/entire"
}

// ensureCloudCheckFeatures verifies orgName has the features cmd requires,
// plus Entire support for Entire repositories.
func ensureCloudCheckFeatures(cmd *cobra.Command, client *cloudapi.Client, provider cloudCheckProvider, orgName string) error {
	features := commandCloudFeatures(cmd)
	if provider == cloudCheckEntire {
		features = append(features, featureCloudEntireForge)
	}
	if provider == cloudCheckGitLab {
		features = append(features, cloudFeature("CLOUD_GITLAB"))
	}
	if orgName == "" {
		return nil
	}
	return ensureCloudFeatures(cmd, client, orgName, features)
}

// currentCloudOrg resolves the org to claim an unclaimed repository for: the
// --org flag when set, otherwise the currently selected Cloud org.
func currentCloudOrg(ctx context.Context, client *cloudapi.Client) (*cloudauth.Org, error) {
	if cloudOrgFlag != "" {
		org, err := client.OrgByName(ctx, cloudOrgFlag)
		if err != nil {
			return nil, err
		}
		return &cloudauth.Org{ID: org.ID, Name: org.Name}, nil
	}
	org, err := cloudauth.CurrentOrg()
	if err != nil || org == nil || org.ID == "" {
		return nil, fmt.Errorf("no current Dagger Cloud organization; run 'dagger login' or pass --org")
	}
	return org, nil
}

// gitHubAppInstallURL returns the URL for installing the Dagger Cloud GitHub
// App on a user/org. It targets the dev app when talking to a non-production
// Cloud (DAGGER_CLOUD_URL points somewhere other than api.dagger.cloud), and
// the production app otherwise.
func gitHubAppInstallURL() string {
	app := "dagger-for-github"
	if url := os.Getenv("DAGGER_CLOUD_URL"); url != "" && !strings.Contains(url, "://api.dagger.cloud") {
		app = "dagger-cloud-dev"
	}
	return "https://github.com/apps/" + app + "/installations/select_target"
}

// gitHubAppNotInstalledError tells the user to install the Dagger Cloud GitHub
// App on the repo's owner before checks can be enabled.
func gitHubAppNotInstalledError(repo string) error {
	owner, _, _ := strings.Cut(normalizeGitHubRepo(repo), "/")
	return fmt.Errorf("%w: the Dagger Cloud GitHub App is not installed on the %q GitHub account; install it here: %s",
		errCloudSourceNotConfigured, owner, gitHubAppInstallURL())
}

// ensureUserInOrg verifies the authenticated user is a member of orgName, the
// Dagger Cloud org that owns the source backing repo. When they are not a
// member it returns a clear ownership error rather than letting an org-scoped
// query fail later with an opaque "unauthorized".
func ensureUserInOrg(ctx context.Context, client *cloudapi.Client, orgName, repo string) error {
	user, err := client.User(ctx)
	if err != nil {
		return fmt.Errorf("lookup Cloud user: %w", err)
	}
	return userOrgMembershipError(user, orgName, repo)
}

// userOrgMembershipError returns nil when the user is a member of orgName, and
// otherwise a clear error explaining that repo is owned by a different Dagger
// Cloud organization.
func userOrgMembershipError(user *cloudapi.UserResponse, orgName, repo string) error {
	for _, org := range user.Orgs {
		if strings.EqualFold(org.Name, orgName) {
			return nil
		}
	}
	return fmt.Errorf("%s is owned by another Dagger Cloud organization which you are not a member of. Contact the organization administrator to get access", repo)
}

// repoAccessError tells the user to grant the installation access to the
// repository on its settings page. It is typed so `checks on` can offer to
// open that page and retry.
type repoAccessError struct {
	provider    cloudCheckProvider
	settingsURL string
}

func (e *repoAccessError) Error() string {
	provider := e.provider
	if provider == "" {
		provider = cloudCheckGitHub
	}
	return fmt.Sprintf("Cloud checks need %s access to this repository. Visit %s to enable it and then run the command again", provider, e.settingsURL)
}

// installationSettingsURL returns the settings page for the state's
// installation, falling back to the canonical GitHub URL format when the
// source's ConfigURL is unknown.
func installationSettingsURL(state workspaceAutocheckState) string {
	if state.ConfigURL != "" {
		return state.ConfigURL
	}
	if state.Provider == cloudCheckGitLab {
		settings, _ := gitLabIntegrationURL(state.OrgName)
		return settings
	}
	return "https://github.com/settings/installations/" + state.InstallationID
}

// isRepoNotInInstallation matches the Cloud API's rejection when claiming a
// repository the installation has no access to.
func isRepoNotInInstallation(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "does not belong to installation") ||
		strings.Contains(err.Error(), "is not available through this"))
}

func workspaceSourceForRepo(sources []cloudapi.Source, repo string) (cloudapi.Source, bool) {
	owner, _, ok := strings.Cut(normalizeGitHubRepo(repo), "/")
	if !ok {
		return cloudapi.Source{}, false
	}
	for _, source := range sources {
		if strings.EqualFold(source.Name, owner) {
			return source, true
		}
	}
	return cloudapi.Source{}, false
}

func workspaceAutocheckClient(ctx context.Context, login bool) (*cloudapi.Client, error) {
	client, _, err := cloudCLI.cloudClientWithLogin(ctx, login)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// findWorkspaceAutocheckState reads the claim of the repository that ref
// names. The boolean is false when none of the user's organizations has
// claimed it; the state then carries the repository's provider and key.
func findWorkspaceAutocheckState(ctx context.Context, client *cloudapi.Client, ref string) (workspaceAutocheckState, bool, error) {
	provider, repo, err := cloudCheckRepo(ref)
	if err != nil {
		return workspaceAutocheckState{}, false, err
	}
	state := workspaceAutocheckState{Provider: provider, Repo: repo}
	repositories, err := client.UserRepositories(ctx, repo)
	if err != nil {
		return workspaceAutocheckState{}, false, fmt.Errorf("lookup Cloud repository %s: %w", repo, err)
	}
	repository, ok := workspaceRepositoryForRef(repositories, provider, repo)
	if !ok || repository.MappedSource == nil || repository.MappedSource.Org == nil {
		return state, false, nil
	}
	// Later writes must name the repository exactly as Cloud stores it.
	state.Repo = repository.Ref
	state.Enabled = true
	mapped := repository.MappedSource
	if mapped.Provider == "GITLAB" {
		state.Provider = cloudCheckGitLab
	}
	state.InstallationID, state.ConfigURL = mapped.InstallationID, mapped.ConfigURL
	state.OrgID, state.OrgName = mapped.Org.ID, mapped.Org.Name
	if settings := repository.Settings; settings != nil {
		state.Triggers = true
		state.DefaultBranch = settings.EffectiveRunChecksOnDefaultBranch
		state.PullRequest = settings.EffectiveRunChecksOnPullRequest
	}
	return state, true, nil
}

// workspaceRepositoryForRef finds repo among Cloud repositories. GitHub names
// match without case; Entire paths match as written.
func workspaceRepositoryForRef(repositories []cloudapi.Repository, provider cloudCheckProvider, repo string) (cloudapi.Repository, bool) {
	for _, repository := range repositories {
		ref := strings.TrimPrefix(strings.TrimPrefix(repository.Ref, "https://"), "http://")
		if provider != cloudCheckGitHub && sameCloudRepository(ref, repo) {
			return repository, true
		}
		if provider == cloudCheckGitHub && strings.EqualFold(normalizeGitHubRepo(ref), normalizeGitHubRepo(repo)) {
			return repository, true
		}
	}
	return cloudapi.Repository{}, false
}

func workspaceAutocheckStateString(state workspaceAutocheckState) string {
	return onOff(state.Enabled)
}

// entireInstallPage returns the Entire page that installs the Dagger plugin
// for the error's organization.
func entireInstallPage(ctx context.Context, client *cloudapi.Client, e *entirePluginNotInstalledError) (string, error) {
	page, err := client.EntireInstallURL(ctx, e.orgID, entireInstallRedirect())
	if err != nil {
		return "", fmt.Errorf("get the Entire install page for organization %q: %w", e.orgName, err)
	}
	return page, nil
}
