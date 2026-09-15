package daggercmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dagger/dagger/core/gitref"
	cloudapi "github.com/dagger/dagger/internal/cloud"
)

// cloudLoadCheckName is the internal "load" check that gates a commit: it
// discovers and runs the commit's checks. It re-runs through rerunLoad rather
// than rerunChecks. Mirrors checks.LoadStatusName in the Cloud API.
const cloudLoadCheckName = "load"

// isCloudLoadCheck reports whether c is the internal load gate. Match on the
// Internal flag as well as the name so a user-defined check that happens to
// be named "load" (module check names are normally namespaced, but the server
// owns the naming) is never misrouted through rerunLoad.
func isCloudLoadCheck(c cloudapi.Check) bool {
	return c.Internal && c.Name == cloudLoadCheckName
}

// errNoCloudRerunTargets signals the benign "nothing failed" case so the command
// can report it without a non-zero exit.
var errNoCloudRerunTargets = errors.New("no failed checks to re-run")

var (
	cloudRerunChecks     []string
	cloudRerunFailed     bool
	cloudRerunAll        bool
	cloudRerunCommit     string
	cloudRerunPR         string
	cloudRerunCleanSlate bool
	cloudRerunDryRun     bool
)

var cloudRerunCmd = &cobra.Command{
	Use:   "rerun [--check NAME ...] [--failed | --all]",
	Short: "Re-run checks on Dagger Cloud for the current commit",
	Long: `Re-run checks on Dagger Cloud, against the commit CI already ran on.

By default this targets the commit at the current HEAD (matched by SHA, falling
back to the branch or PR it belongs to) and re-runs the checks that failed. Pass
--check to pick specific checks by name, --all to re-run everything, or
--commit/--pr to target a different commit.

Only outermost checks can be re-run; sub-checks run as part of their parent
check, so name the parent (e.g. "ci:bootstrap", not "ci:bootstrap:lint").

This re-runs a check that already exists in Cloud for the commit. If CI hasn't
run on the commit yet there's nothing to re-run -- use 'dagger check' to run
checks locally against your working tree.`,
	Args: cobra.NoArgs,
	RunE: cloudCLI.Rerun,
}

func init() {
	cloudRerunCmd.Flags().StringArrayVar(&cloudRerunChecks, "check", nil, "Re-run a specific check by name (repeatable; outermost checks only)")
	cloudRerunCmd.Flags().BoolVar(&cloudRerunFailed, "failed", false, "Re-run the failed checks (the default when no --check is given)")
	cloudRerunCmd.Flags().BoolVar(&cloudRerunAll, "all", false, "Re-run every check, including ones that passed")
	cloudRerunCmd.Flags().StringVar(&cloudRerunCommit, "commit", "", "Target a specific commit SHA instead of the current HEAD")
	cloudRerunCmd.Flags().StringVar(&cloudRerunPR, "pr", "", "Target a specific pull request number")
	cloudRerunCmd.Flags().BoolVar(&cloudRerunCleanSlate, "clean-slate", false, "Re-run without reusing cache (experimental; requires an org feature)")
	cloudRerunCmd.Flags().BoolVar(&cloudRerunDryRun, "dry-run", false, "Show which checks would be re-run without triggering anything")
	cloudRerunCmd.Flags().BoolVar(&cloudJSON, "json", false, "Print JSON output")
	cloudRerunCmd.MarkFlagsMutuallyExclusive("check", "failed")
	cloudRerunCmd.MarkFlagsMutuallyExclusive("check", "all")
	cloudRerunCmd.MarkFlagsMutuallyExclusive("failed", "all")
	cloudRerunCmd.MarkFlagsMutuallyExclusive("commit", "pr")
	cloudCmd.AddCommand(cloudRerunCmd)
}

func (cli *CloudCLI) Rerun(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	address, err := cloudRerunTargetAddress(ctx)
	if err != nil {
		return err
	}

	// Load every check on the target commit (unfiltered) so a bad --check name
	// can be reported against the real list rather than silently matching none.
	res, selectors, err := cli.loadCloudCheckQueryForWorkspace(ctx, address, nil, true)
	if err != nil {
		return err
	}
	if len(res.Rows) == 0 {
		return fmt.Errorf("no Cloud checks found for the target commit; has CI run on it yet? (use 'dagger check' to run locally)")
	}

	commit, _, err := selectCloudCheckCommit(res.Rows, selectors)
	if err != nil {
		renderAmbiguousCloudChecks(cmd, res.Rows)
		return err
	}

	// Re-run candidates come from the commit's full check set (including internal
	// checks like "load"), not the visible-filtered rows, so a failed load gate
	// is re-runnable even when it hides public checks.
	targets, err := cloudRerunTargets(latestCloudChecksByName(commit.Checks))
	if errors.Is(err, errNoCloudRerunTargets) {
		fmt.Fprintln(cmd.OutOrStdout(), "No failed checks to re-run. Use --all to re-run every check, or --check NAME to pick one.")
		return nil
	}
	if err != nil {
		return err
	}

	if cloudRerunDryRun {
		return renderCloudRerunDryRun(cmd, commit, targets)
	}

	queued, skipped, err := cloudRerunTrigger(ctx, res.Client, res.OrgID, targets)
	if err != nil {
		return err
	}

	return renderCloudRerun(cmd, res.OrgName, commit, targets, queued, skipped)
}

// cloudRerunPlan splits targets into the checks that will actually be triggered
// (regular checks batched via rerunChecks, failed load checks via rerunLoad) and
// the ones skipped before triggering, keyed by name with a reason. The only
// pre-skip today is a non-failed load check: the server rejects re-running one,
// and re-running a passing gate is meaningless.
func cloudRerunPlan(targets []cloudapi.Check) (checkTargets, loadTargets []cloudapi.Check, skipped map[string]string) {
	skipped = map[string]string{}
	for _, c := range targets {
		if isCloudLoadCheck(c) {
			if cloudResultForStatus(c.Status) == "red" {
				loadTargets = append(loadTargets, c)
			} else {
				skipped[c.Name] = "load check passed; only a failed load check can be re-run"
			}
			continue
		}
		checkTargets = append(checkTargets, c)
	}
	return checkTargets, loadTargets, skipped
}

// cloudRerunTrigger enqueues the re-runs per the plan, routing failed load checks
// through rerunLoad and batching the rest through rerunChecks.
func cloudRerunTrigger(ctx context.Context, client *cloudapi.Client, orgID string, targets []cloudapi.Check) ([]cloudapi.Check, map[string]string, error) {
	checkTargets, loadTargets, skipped := cloudRerunPlan(targets)

	var queued []cloudapi.Check
	if len(checkTargets) > 0 {
		ids := make([]string, len(checkTargets))
		for i, c := range checkTargets {
			ids[i] = c.ID
		}
		q, err := client.RerunChecks(ctx, orgID, ids, cloudRerunCleanSlate)
		if err != nil {
			return nil, nil, fmt.Errorf("re-run checks: %w", err)
		}
		queued = append(queued, q...)
	}
	for _, c := range loadTargets {
		q, err := client.RerunLoad(ctx, orgID, c.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("re-run load check: %w", err)
		}
		if q != nil {
			queued = append(queued, *q)
		}
	}
	return queued, skipped, nil
}

// cloudRerunTargetAddress resolves the workspace address whose commit should be
// re-run: an explicit --commit/--pr override, otherwise the current local
// checkout's HEAD (honoring -W), matching how 'dagger check' replays past runs.
func cloudRerunTargetAddress(ctx context.Context) (string, error) {
	if cloudRerunCommit != "" || cloudRerunPR != "" {
		version := cloudRerunCommit
		if cloudRerunPR != "" {
			version = "pull/" + cloudRerunPR + "/head"
		}
		info, err := localWorkspaceRemoteInfoForAddress(ctx, workspaceRef)
		if err != nil {
			return "", fmt.Errorf("resolve workspace for --commit/--pr: %w", err)
		}
		return gitref.RefString(info.cloneRef, info.workspacePath, version), nil
	}

	address, ok, reason, err := checkPastWorkspaceAddress(ctx)
	if err != nil {
		return "", err
	}
	if !ok {
		if reason == "" {
			reason = "no remote workspace is known"
		}
		return "", fmt.Errorf("cannot determine the target commit: %s; pass --commit or --pr", reason)
	}
	return address, nil
}

// cloudRerunTargets picks which of the commit's checks to re-run based on the
// selector flags: explicit --check names, --all, or (the default) the failed
// checks only.
func cloudRerunTargets(checks []cloudapi.Check) ([]cloudapi.Check, error) {
	if len(cloudRerunChecks) > 0 {
		byName := make(map[string]cloudapi.Check, len(checks))
		for _, c := range checks {
			byName[c.Name] = c
		}
		var targets []cloudapi.Check
		var missing []string
		for _, name := range cloudRerunChecks {
			if c, ok := byName[name]; ok {
				targets = append(targets, c)
			} else {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("check %s not found for the target commit; available: %s",
				strings.Join(missing, ", "), strings.Join(cloudCheckNames(checks), ", "))
		}
		return targets, nil
	}

	if cloudRerunAll {
		if len(checks) == 0 {
			return nil, fmt.Errorf("no checks found for the target commit")
		}
		return checks, nil
	}

	var failed []cloudapi.Check
	for _, c := range checks {
		if cloudResultForStatus(c.Status) == "red" {
			failed = append(failed, c)
		}
	}
	if len(failed) == 0 {
		return nil, errNoCloudRerunTargets
	}
	return failed, nil
}

func cloudCheckNames(checks []cloudapi.Check) []string {
	names := make([]string, len(checks))
	for i, c := range checks {
		names[i] = c.Name
	}
	return names
}

func renderCloudRerun(cmd *cobra.Command, orgName string, commit cloudapi.CheckCommit, targets, queued []cloudapi.Check, skipped map[string]string) error {
	if cloudJSON {
		return renderCloudRerunJSON(cmd, orgName, queued)
	}

	out := cmd.OutOrStdout()
	enqueued := make(map[string]bool, len(queued))
	for _, c := range queued {
		enqueued[c.Name] = true
	}

	fmt.Fprintf(out, "Re-running %d of %d check(s) on %s:\n", len(queued), len(targets), cloudRerunRefLabel(commit))
	for _, c := range targets {
		switch {
		case enqueued[c.Name]:
			if u := cloudChecksPageURL(orgName, c); u != "" {
				fmt.Fprintf(out, "  %s  %s\n", c.Name, u)
			} else {
				fmt.Fprintf(out, "  %s\n", c.Name)
			}
		case skipped[c.Name] != "":
			fmt.Fprintf(out, "  %s  (skipped: %s)\n", c.Name, skipped[c.Name])
		default:
			// Not enqueued and not pre-skipped: rerunChecks dropped it because it
			// was already running or queued on Cloud.
			fmt.Fprintf(out, "  %s  (skipped; already running)\n", c.Name)
		}
	}
	return nil
}

// renderCloudRerunDryRun reports what a real run would trigger -- which mutation
// each check routes through, and which are skipped and why -- without enqueueing
// anything.
func renderCloudRerunDryRun(cmd *cobra.Command, commit cloudapi.CheckCommit, targets []cloudapi.Check) error {
	_, _, skipped := cloudRerunPlan(targets)
	if cloudJSON {
		type outCheck struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Status  string `json:"status"`
			Via     string `json:"via,omitempty"`
			Skipped string `json:"skipped,omitempty"`
		}
		items := make([]outCheck, 0, len(targets))
		for _, c := range targets {
			oc := outCheck{ID: c.ID, Name: c.Name, Status: c.Status}
			switch {
			case skipped[c.Name] != "":
				oc.Skipped = skipped[c.Name]
			case isCloudLoadCheck(c):
				oc.Via = "rerunLoad"
			default:
				oc.Via = "rerunChecks"
			}
			items = append(items, oc)
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(items)
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Would re-run %d of %d check(s) on %s:\n", len(targets)-len(skipped), len(targets), cloudRerunRefLabel(commit))
	for _, c := range targets {
		if reason := skipped[c.Name]; reason != "" {
			fmt.Fprintf(out, "  %s  [%s] skipped: %s\n", c.Name, strings.ToLower(c.Status), reason)
			continue
		}
		via := "rerunChecks"
		if isCloudLoadCheck(c) {
			via = "rerunLoad"
		}
		fmt.Fprintf(out, "  %s  [%s] via %s\n", c.Name, strings.ToLower(c.Status), via)
	}
	return nil
}

func renderCloudRerunJSON(cmd *cobra.Command, orgName string, queued []cloudapi.Check) error {
	type outCheck struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
		URL    string `json:"url,omitempty"`
	}
	items := make([]outCheck, 0, len(queued))
	for _, c := range queued {
		items = append(items, outCheck{
			ID:     c.ID,
			Name:   c.Name,
			Status: c.Status,
			URL:    cloudChecksPageURL(orgName, c),
		})
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(items)
}

// cloudRerunRefLabel describes the commit being re-run, preferring its PR
// reference and falling back to repo@sha.
func cloudRerunRefLabel(commit cloudapi.CheckCommit) string {
	label := normalizeGitHubRepo(commit.Repo) + "@" + shortCloudSHA(commit.CommitSHA)
	for _, ref := range commit.Refs {
		if ref.Typename == "CheckCommitPullRequestRef" {
			return fmt.Sprintf("PR #%d (%s)", ref.Number, label)
		}
	}
	return label
}

// cloudChecksPageURL builds the Dagger Cloud checks page link for a check. It
// mirrors the API's traceURL format verbatim (unescaped PinnedRef =
// moduleRef@moduleVersion in the path, raw check name in the query) so the link
// matches the ones the backend already emits and the web router resolves.
func cloudChecksPageURL(orgName string, check cloudapi.Check) string {
	if orgName == "" || check.ModuleRef == "" {
		return ""
	}
	return fmt.Sprintf("https://dagger.cloud/%s/checks/%s@%s?check=%s",
		orgName, check.ModuleRef, check.ModuleVersion, check.Name)
}
