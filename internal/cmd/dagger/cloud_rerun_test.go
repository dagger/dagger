package daggercmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	cloudapi "github.com/dagger/dagger/internal/cloud"
)

func cloudRerunCheckList() []cloudapi.Check {
	return []cloudapi.Check{
		{ID: "1", Name: "ci:bootstrap", Status: "FAILURE"},
		{ID: "2", Name: "lint", Status: "SUCCESS"},
		{ID: "3", Name: "test", Status: "FAILURE"},
	}
}

// setCloudRerunFlags sets the package-level selector flags for one test case and
// restores them afterward, since cloudRerunTargets reads them directly.
func setCloudRerunFlags(t *testing.T, checks []string, all bool) {
	t.Helper()
	prevChecks, prevAll := cloudRerunChecks, cloudRerunAll
	cloudRerunChecks, cloudRerunAll = checks, all
	t.Cleanup(func() { cloudRerunChecks, cloudRerunAll = prevChecks, prevAll })
}

func TestCloudRerunTargetsDefaultsToFailed(t *testing.T) {
	setCloudRerunFlags(t, nil, false)
	targets, err := cloudRerunTargets(cloudRerunCheckList())
	require.NoError(t, err)
	require.Equal(t, []string{"ci:bootstrap", "test"}, cloudCheckNames(targets))
}

func TestCloudRerunTargetsAll(t *testing.T) {
	setCloudRerunFlags(t, nil, true)
	targets, err := cloudRerunTargets(cloudRerunCheckList())
	require.NoError(t, err)
	require.Equal(t, []string{"ci:bootstrap", "lint", "test"}, cloudCheckNames(targets))
}

func TestCloudRerunTargetsByName(t *testing.T) {
	setCloudRerunFlags(t, []string{"lint"}, false)
	targets, err := cloudRerunTargets(cloudRerunCheckList())
	require.NoError(t, err)
	require.Equal(t, []string{"lint"}, cloudCheckNames(targets))
}

func TestCloudRerunTargetsByDAGAddress(t *testing.T) {
	// The trace's "RE-RUN IN CI" suggestion prints the check's dag:// address;
	// the command accepts it against the scheme-less Cloud name.
	setCloudRerunFlags(t, []string{"dag://test"}, false)
	targets, err := cloudRerunTargets(cloudRerunCheckList())
	require.NoError(t, err)
	require.Equal(t, []string{"test"}, cloudCheckNames(targets))
}

func TestCloudRerunTargetsByDAGAddressCheckKey(t *testing.T) {
	// Check addresses carry the check name as the "check" key, which is what
	// the trace suggests on current engines.
	setCloudRerunFlags(t, []string{"dag://?check=ci:bootstrap", "dag+check://?check=test"}, false)
	targets, err := cloudRerunTargets(cloudRerunCheckList())
	require.NoError(t, err)
	require.Equal(t, []string{"ci:bootstrap", "test"}, cloudCheckNames(targets))
}

func TestStripDAGScheme(t *testing.T) {
	require.Equal(t, "test-split/test-cache-persistence", stripDAGScheme("dag://test-split/test-cache-persistence"))
	require.Equal(t, "golang/generate-all/stale", stripDAGScheme("dag://golang/generate-all/stale"))
	// A path-less address names the check by its "check" key.
	require.Equal(t, "golang/test-all", stripDAGScheme("dag://?check=golang/test-all"))
	require.Equal(t, "golang/test-all", stripDAGScheme("dag+check://?check=golang/test-all"))
	// A scheme-less Cloud name is returned unchanged.
	require.Equal(t, "ci:bootstrap", stripDAGScheme("ci:bootstrap"))
}

func TestCloudRerunTargetsUnknownNameErrors(t *testing.T) {
	setCloudRerunFlags(t, []string{"ci:bootstrap:lint"}, false)
	_, err := cloudRerunTargets(cloudRerunCheckList())
	require.ErrorContains(t, err, "ci:bootstrap:lint")
	require.ErrorContains(t, err, "available:")
}

func TestCloudRerunTargetsNoFailuresErrors(t *testing.T) {
	setCloudRerunFlags(t, nil, false)
	_, err := cloudRerunTargets([]cloudapi.Check{
		{ID: "1", Name: "lint", Status: "SUCCESS"},
	})
	require.ErrorIs(t, err, errNoCloudRerunTargets)
}

func TestCloudRerunTargetsIncludesFailedLoad(t *testing.T) {
	setCloudRerunFlags(t, nil, false)
	targets, err := cloudRerunTargets([]cloudapi.Check{
		{ID: "1", Name: cloudLoadCheckName, Status: "FAILURE", Internal: true},
	})
	require.NoError(t, err)
	require.Equal(t, []string{cloudLoadCheckName}, cloudCheckNames(targets))
}

func TestCloudRerunPlanRoutesAndSkips(t *testing.T) {
	checkTargets, loadTargets, skipped := cloudRerunPlan([]cloudapi.Check{
		{ID: "1", Name: "ci:bootstrap", Status: "FAILURE"},
		{ID: "2", Name: cloudLoadCheckName, Status: "FAILURE", Internal: true},
	})
	require.Equal(t, []string{"ci:bootstrap"}, cloudCheckNames(checkTargets))
	require.Equal(t, []string{cloudLoadCheckName}, cloudCheckNames(loadTargets))
	require.Empty(t, skipped)
}

func TestCloudRerunPlanIgnoresUserLoadName(t *testing.T) {
	// A non-internal check that happens to be named "load" is a regular check,
	// not the load gate: it must route through rerunChecks, never rerunLoad.
	checkTargets, loadTargets, skipped := cloudRerunPlan([]cloudapi.Check{
		{ID: "1", Name: cloudLoadCheckName, Status: "FAILURE"},
	})
	require.Equal(t, []string{cloudLoadCheckName}, cloudCheckNames(checkTargets))
	require.Empty(t, loadTargets)
	require.Empty(t, skipped)
}

func TestCloudRerunPlanSkipsPassedLoad(t *testing.T) {
	checkTargets, loadTargets, skipped := cloudRerunPlan([]cloudapi.Check{
		{ID: "1", Name: cloudLoadCheckName, Status: "SUCCESS", Internal: true},
	})
	require.Empty(t, checkTargets)
	require.Empty(t, loadTargets)
	require.Contains(t, skipped[cloudLoadCheckName], "only a failed load check")
}

func TestCloudRerunRefLabelPreferspr(t *testing.T) {
	commit := cloudapi.CheckCommit{
		Repo:      "https://github.com/acme/hello",
		CommitSHA: "abcdef1234567890",
		Refs: []cloudapi.CheckCommitRef{{
			Typename: "CheckCommitPullRequestRef",
			Number:   42,
		}},
	}
	require.Equal(t, "PR #42 (acme/hello@abcdef123456)", cloudRerunRefLabel(commit))
}

func TestCloudRerunRefLabelFallsBackToSHA(t *testing.T) {
	commit := cloudapi.CheckCommit{
		Repo:      "https://github.com/acme/hello",
		CommitSHA: "abcdef1234567890",
		Refs: []cloudapi.CheckCommitRef{{
			Typename: "CheckCommitBranchRef",
			Name:     "main",
		}},
	}
	require.Equal(t, "acme/hello@abcdef123456", cloudRerunRefLabel(commit))
}

func TestCloudChecksPageURL(t *testing.T) {
	check := cloudapi.Check{
		Name:          "ci:bootstrap",
		ModuleRef:     "github.com/acme/hello",
		ModuleVersion: "abcdef123456",
	}
	require.Equal(t,
		"https://dagger.cloud/acme/checks/github.com/acme/hello@abcdef123456?check=ci:bootstrap",
		cloudChecksPageURL("acme", check))
}

func TestCloudChecksPageURLEmptyWithoutModule(t *testing.T) {
	require.Empty(t, cloudChecksPageURL("acme", cloudapi.Check{Name: "lint"}))
}

// rerunCloudOrg is one org in the fake Cloud: member says whether the user holds
// a role in it, which decides whether the user-scoped check search covers it.
type rerunCloudOrg struct {
	id      string
	name    string
	member  bool
	commits []cloudapi.CheckCommit
}

// rerunCloudCall records a rerunChecks mutation the fake Cloud received.
type rerunCloudCall struct {
	org      string
	checkIDs []string
}

// newRerunCloud serves the Cloud API calls 'dagger cloud checks rerun' makes.
// Like Cloud, the user-scoped check search covers only the orgs the user is a
// member of, org-scoped queries reach any org, and every check search returns
// only commits whose repo is in the request's repo filter.
func newRerunCloud(t *testing.T, orgs ...rerunCloudOrg) *[]rerunCloudCall {
	t.Helper()
	var reruns []rerunCloudCall
	inRepos := func(commit cloudapi.CheckCommit, repos any) bool {
		list, _ := repos.([]any)
		for _, repo := range list {
			if s, ok := repo.(string); ok && strings.EqualFold(normalizeGitHubRepo(s), normalizeGitHubRepo(commit.Repo)) {
				return true
			}
		}
		return false
	}
	nodes := func(org rerunCloudOrg, repos any, withOrg bool) []cloudapi.CheckCommit {
		var out []cloudapi.CheckCommit
		for _, commit := range org.commits {
			if !inRepos(commit, repos) {
				continue
			}
			if withOrg {
				commit.Org = cloudapi.CheckCommitOrg{ID: org.id, Name: org.name}
			}
			out = append(out, commit)
		}
		return out
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			OperationName string         `json:"operationName"`
			Query         string         `json:"query"`
			Variables     map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		var data any
		switch {
		case request.OperationName == "GetUserChecks":
			var commits []cloudapi.CheckCommit
			for _, org := range orgs {
				if org.member {
					commits = append(commits, nodes(org, request.Variables["repos"], true)...)
				}
			}
			data = map[string]any{"user": map[string]any{"checks": map[string]any{"nodes": commits}}}
		case request.OperationName == "GetOrgChecks":
			data = map[string]any{"org": nil}
			for _, org := range orgs {
				if org.name == request.Variables["org"] {
					data = map[string]any{"org": map[string]any{"checks": map[string]any{"nodes": nodes(org, request.Variables["repos"], false)}}}
				}
			}
		case request.OperationName == "RerunChecks":
			var ids []string
			for _, id := range request.Variables["checkIds"].([]any) {
				ids = append(ids, id.(string))
			}
			org, _ := request.Variables["org"].(string)
			reruns = append(reruns, rerunCloudCall{org: org, checkIDs: ids})
			var queued []cloudapi.Check
			for _, o := range orgs {
				for _, commit := range o.commits {
					for _, check := range commit.Checks {
						for _, id := range ids {
							if check.ID == id {
								check.ID += "-rerun"
								check.Status = "QUEUED"
								queued = append(queued, check)
							}
						}
					}
				}
			}
			data = map[string]any{"rerunChecks": queued}
		case strings.Contains(request.Query, "org(name: $name)"):
			data = map[string]any{"org": nil}
			for _, org := range orgs {
				if org.name == request.Variables["name"] {
					data = map[string]any{"org": map[string]any{"id": org.id, "name": org.name}}
				}
			}
		default:
			t.Errorf("unexpected Cloud request: %s %s", request.OperationName, request.Query)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"data": data}))
	}))
	t.Cleanup(server.Close)
	t.Setenv("DAGGER_CLOUD_URL", server.URL)
	t.Setenv("DAGGER_CLOUD_TOKEN", "test-token")
	return &reruns
}

// rerunPRCommit is a pull request run as Cloud files it: under the PR head
// commit, with the merge commit CI actually tested recorded on the PR ref and
// as the checks' module version.
func rerunPRCommit(headSHA, mergeSHA string) cloudapi.CheckCommit {
	return cloudapi.CheckCommit{
		Repo:      "https://github.com/acme/mono",
		CommitSHA: headSHA,
		Refs: []cloudapi.CheckCommitRef{{
			Typename:          "CheckCommitPullRequestRef",
			Number:            7,
			IntegrationCommit: mergeSHA,
		}},
		Checks: []cloudapi.Check{
			{ID: "lint-id", Name: "lint", Status: "FAILURE", ModuleRef: "github.com/acme/mono", ModuleVersion: mergeSHA},
			{ID: "test-id", Name: "test", Status: "SUCCESS", ModuleRef: "github.com/acme/mono", ModuleVersion: mergeSHA},
		},
	}
}

const rerunMergeSHA = "f00dfeedf00dfeedf00dfeedf00dfeedf00dfeed"

// setCloudRerunTarget sets the flags that pick the rerun target and restores
// them afterward. The rerun is a dry run of the lint check unless the test
// turns dryRun off.
func setCloudRerunTarget(t *testing.T, workspace, commit, pr, org string, dryRun bool) {
	t.Helper()
	prevWorkspace, prevCommit, prevPR, prevOrg, prevDryRun, prevJSON := workspaceRef, cloudRerunCommit, cloudRerunPR, cloudOrgFlag, cloudRerunDryRun, cloudJSON
	workspaceRef, cloudRerunCommit, cloudRerunPR, cloudOrgFlag, cloudRerunDryRun, cloudJSON = workspace, commit, pr, org, dryRun, false
	t.Cleanup(func() {
		workspaceRef, cloudRerunCommit, cloudRerunPR, cloudOrgFlag, cloudRerunDryRun, cloudJSON = prevWorkspace, prevCommit, prevPR, prevOrg, prevDryRun, prevJSON
	})
	setCloudRerunFlags(t, []string{"lint"}, false)
}

// setupRerunCheckout creates a clean git checkout with the given remotes
// (name, URL pairs), changes into it, and returns its HEAD commit.
func setupRerunCheckout(t *testing.T, remotes ...[2]string) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init")
	for _, remote := range remotes {
		runGit(t, repo, "remote", "add", remote[0], remote[1])
	}
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("mono\n"), 0o600))
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.name=Test User", "-c", "user.email=test@example.com", "commit", "-m", "initial")
	sha, err := gitOutput(t.Context(), repo, "rev-parse", "HEAD")
	require.NoError(t, err)
	t.Chdir(repo)
	return sha
}

func runCloudRerun(t *testing.T) (string, error) {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cloudCLI.Rerun(cmd, nil)
	return out.String(), err
}

func wantRerunDryRun(headSHA string) string {
	return fmt.Sprintf("Would re-run 1 of 1 check(s) on PR #7 (acme/mono@%s):\n  lint  [failure] via rerunChecks\n", headSHA[:12])
}

// The repo CI ran on need not be the checkout's origin: in a fork checkout the
// main repo is typically "upstream", and origin may be missing entirely.
func TestCloudRerunFindsChecksThroughAnyRemote(t *testing.T) {
	for _, tc := range []struct {
		name    string
		remotes [][2]string
	}{
		{name: "origin is a fork", remotes: [][2]string{{"origin", "git@github.com:someone/mono.git"}, {"upstream", "https://github.com/acme/mono.git"}}},
		{name: "no origin", remotes: [][2]string{{"upstream", "git@github.com:acme/mono.git"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sha := setupRerunCheckout(t, tc.remotes...)
			newRerunCloud(t, rerunCloudOrg{id: "acme-id", name: "acme", member: true, commits: []cloudapi.CheckCommit{rerunPRCommit(sha, rerunMergeSHA)}})

			setCloudRerunTarget(t, "", "", "", "", true)
			out, err := runCloudRerun(t)
			require.NoError(t, err)
			require.Equal(t, wantRerunDryRun(sha), out)

			setCloudRerunTarget(t, "", "", "7", "", true)
			out, err = runCloudRerun(t)
			require.NoError(t, err)
			require.Equal(t, wantRerunDryRun(sha), out)
		})
	}
}

// Without -W, the target is the clean local checkout's HEAD in the workspace's
// subdirectory, under every remote of the checkout.
func TestCloudRerunTargetAddressesLocalCheckout(t *testing.T) {
	repo, workspaceDir, sha := setupCleanWorkspaceRepo(t)
	runGit(t, repo, "remote", "add", "upstream", "https://github.com/acme/upstream.git")
	t.Chdir(workspaceDir)
	setCloudRerunTarget(t, "", "", "", "", true)

	addresses, err := cloudRerunTargetAddresses(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{
		"github.com/acme/mono/services/api@" + sha,
		"github.com/acme/upstream/services/api@" + sha,
	}, addresses)

	require.NoError(t, os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("dirty\n"), 0o600))
	_, err = cloudRerunTargetAddresses(t.Context())
	require.ErrorContains(t, err, "uncommitted changes")
}

// --pr and --commit name a commit of the selected remote workspace, so they
// work without any local checkout.
func TestCloudRerunTargetsRemoteWorkspace(t *testing.T) {
	const headSHA = "0123456789abcdef0123456789abcdef01234567"
	t.Chdir(t.TempDir())
	newRerunCloud(t, rerunCloudOrg{id: "acme-id", name: "acme", member: true, commits: []cloudapi.CheckCommit{rerunPRCommit(headSHA, rerunMergeSHA)}})

	for _, tc := range []struct {
		name       string
		commit, pr string
	}{
		{name: "pr", pr: "7"},
		{name: "head commit", commit: headSHA},
		// Commit statuses link the merge commit CI tested, not the PR head.
		{name: "merge commit", commit: rerunMergeSHA},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setCloudRerunTarget(t, "github.com/acme/mono", tc.commit, tc.pr, "", true)
			out, err := runCloudRerun(t)
			require.NoError(t, err)
			require.Equal(t, wantRerunDryRun(headSHA), out)
		})
	}
}

// The user-scoped check search only covers orgs the user is a member of; --org
// reaches checks in any org the user can see, and the rerun goes to that org.
func TestCloudRerunOrgFlag(t *testing.T) {
	const headSHA = "0123456789abcdef0123456789abcdef01234567"
	t.Chdir(t.TempDir())
	reruns := newRerunCloud(t,
		rerunCloudOrg{id: "acme-id", name: "acme", member: true},
		rerunCloudOrg{id: "dogfood-id", name: "acme-dogfood", commits: []cloudapi.CheckCommit{rerunPRCommit(headSHA, rerunMergeSHA)}},
	)

	setCloudRerunTarget(t, "github.com/acme/mono", "", "7", "", true)
	_, err := runCloudRerun(t)
	require.ErrorContains(t, err, "no Cloud checks found")
	require.ErrorContains(t, err, "--org")

	setCloudRerunTarget(t, "github.com/acme/mono", "", "7", "acme-dogfood", false)
	out, err := runCloudRerun(t)
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("Re-running 1 of 1 check(s) on PR #7 (acme/mono@%s):\n  lint  https://dagger.cloud/acme-dogfood/checks/github.com/acme/mono@%s?check=lint\n", headSHA[:12], rerunMergeSHA), out)
	require.Equal(t, []rerunCloudCall{{org: "dogfood-id", checkIDs: []string{"lint-id"}}}, *reruns)
}
