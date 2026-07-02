package daggercmd

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
)

// This file holds the Cloud check query, selection, and listing helpers that
// 'dagger cloud rerun' depends on. They used to live in cloud_checks.go and back
// the old 'dagger check' Cloud-replay path; that path was removed (see
// future/done/simplify-dagger-check.md), which slimmed loadCloudCheckRowsForWorkspace
// down to a rows-only query for the workspace commands. 'dagger cloud rerun' needs
// more than rows — it needs the owning org and Cloud client to trigger the re-run,
// plus the selectors to disambiguate which commit the rows belong to — so the
// fuller query path lives here, scoped to its remaining caller.

// cloudCheckQueryResult bundles the Cloud client and owning org with the resolved
// check rows. The rows-only loadCloudCheckRowsForWorkspace used by the workspace
// commands drops the client/org; 'dagger cloud rerun' needs them to trigger reruns.
type cloudCheckQueryResult struct {
	OrgName string
	OrgID   string
	Client  *cloudapi.Client
	Rows    []cloudCheckRow
}

// loadCloudCheckQueryForWorkspace resolves the Cloud checks for a workspace
// address, returning the owning org and client alongside the rows so the caller
// can act on them (e.g. re-run), plus the selectors used so the caller can
// disambiguate which commit the rows belong to.
func (cli *CloudCLI) loadCloudCheckQueryForWorkspace(ctx context.Context, address string, checks []string, login bool) (*cloudCheckQueryResult, cloudCheckSelectorFlags, error) {
	remote, ok, err := parseWorkspaceRemoteAddress(ctx, address)
	if err != nil {
		return nil, cloudCheckSelectorFlags{}, err
	}
	if !ok {
		return nil, cloudCheckSelectorFlags{}, fmt.Errorf("workspace %q is not remote", address)
	}

	baseSelectors := cloudCheckSelectorFlags{
		GitHubRepo: []string{remote.CloneRef},
		Workspace:  []string{remote.BaseAddress},
		Check:      checks,
	}

	client, _, err := cli.cloudClientWithLogin(ctx, login)
	if err != nil {
		return nil, cloudCheckSelectorFlags{}, err
	}

	commits, err := client.UserChecks(ctx, []string{remote.CloneRef}, cloudCheckFetchLimit)
	if err != nil {
		return nil, cloudCheckSelectorFlags{}, fmt.Errorf("fetch Cloud checks for %s: %w", remote.CloneRef, err)
	}

	commitsByOrg := map[string][]cloudapi.CheckCommit{}
	orgs := make([]cloudauth.Org, 0)
	for _, commit := range commits {
		org := commit.Org
		if _, ok := commitsByOrg[org.ID]; !ok {
			orgs = append(orgs, cloudauth.Org{ID: org.ID, Name: org.Name})
		}
		commitsByOrg[org.ID] = append(commitsByOrg[org.ID], commit)
	}

	ordered, _ := orderCloudOrgsForRepos(orgs, []string{remote.CloneRef})
	for _, org := range ordered {
		rows, selectors, err := cloudRowsAndSelectorsForAddress(ctx, cloudCheckRows(org.Name, commitsByOrg[org.ID]), address, checks)
		if err != nil {
			return nil, cloudCheckSelectorFlags{}, err
		}
		if !selectors.hasCloudSelector() {
			selectors = baseSelectors
		}
		if len(rows) > 0 {
			return &cloudCheckQueryResult{
				OrgName: org.Name,
				OrgID:   org.ID,
				Client:  client,
				Rows:    rows,
			}, selectors, nil
		}
	}
	return &cloudCheckQueryResult{
		Client: client,
	}, baseSelectors, nil
}

// cloudRowsAndSelectorsForAddress filters check rows to those matching the
// workspace address and reports the selector that matched. It mirrors
// cloudRowsForWorkspaceAddress (rows-only) but also returns the selectors, which
// the rerun command needs to disambiguate commits.
func cloudRowsAndSelectorsForAddress(ctx context.Context, rows []cloudCheckRow, address string, checks []string) ([]cloudCheckRow, cloudCheckSelectorFlags, error) {
	remote, ok, err := parseWorkspaceRemoteAddress(ctx, address)
	if err != nil {
		return nil, cloudCheckSelectorFlags{}, err
	}
	if !ok {
		return nil, cloudCheckSelectorFlags{}, nil
	}
	baseSelectors := cloudCheckSelectorFlags{
		GitHubRepo: []string{remote.CloneRef},
		Workspace:  []string{remote.BaseAddress},
		Check:      checks,
	}
	selectors := cloudWorkspaceSelectors(baseSelectors, remote.Version)
	var out []cloudCheckRow
	for _, selector := range selectors {
		out = append(out, filterCloudCheckRows(rows, selector)...)
	}
	return dedupeCloudCheckRows(out), firstNonEmptyCloudSelector(selectors), nil
}

func firstNonEmptyCloudSelector(selectors []cloudCheckSelectorFlags) cloudCheckSelectorFlags {
	for _, selector := range selectors {
		if selector.hasCloudSelector() {
			return selector
		}
	}
	return cloudCheckSelectorFlags{}
}

func (f cloudCheckSelectorFlags) hasCloudSelector() bool {
	return len(f.GitHubRepo) > 0 ||
		len(f.GitHubPR) > 0 ||
		len(f.GitBranch) > 0 ||
		len(f.GitTag) > 0 ||
		len(f.GitSHA) > 0 ||
		len(f.Workspace) > 0
}

func (f cloudCheckSelectorFlags) selected(dim string) bool {
	return len(f.values(dim)) > 0
}

// selectCloudCheckCommit picks a single commit from the matched rows. When the
// rows span one commit it's unambiguous; otherwise it errors if the selectors
// match multiple subjects, and falls back to the most recently updated commit.
func selectCloudCheckCommit(rows []cloudCheckRow, selectors cloudCheckSelectorFlags) (cloudapi.CheckCommit, []cloudCheckRow, error) {
	byCommit := map[string][]cloudCheckRow{}
	for _, row := range rows {
		key := cloudCommitKey(row.Commit)
		byCommit[key] = append(byCommit[key], row)
	}
	if len(byCommit) == 1 {
		for _, commitRows := range byCommit {
			return commitRows[0].Commit, commitRows, nil
		}
	}

	subjects := map[string]struct{}{}
	for _, row := range rows {
		subjects[cloudCheckSubject(row, selectors)] = struct{}{}
	}
	if len(subjects) > 1 {
		return cloudapi.CheckCommit{}, nil, fmt.Errorf("selectors match multiple Cloud check subjects; add more selectors")
	}

	var selected []cloudCheckRow
	for _, commitRows := range byCommit {
		if selected == nil || latestCloudRowTime(commitRows).After(latestCloudRowTime(selected)) {
			selected = commitRows
		}
	}
	return selected[0].Commit, selected, nil
}

func cloudCheckSubject(row cloudCheckRow, selectors cloudCheckSelectorFlags) string {
	repo := row.Dimensions["github-repo"]
	switch {
	case selectors.selected("github-pr") || row.Dimensions["github-pr"] != "":
		return repo + "|pr|" + row.Dimensions["github-pr"]
	case selectors.selected("git-branch") || row.Dimensions["git-branch"] != "":
		return repo + "|branch|" + row.Dimensions["git-branch"]
	case selectors.selected("git-tag") || row.Dimensions["git-tag"] != "":
		return repo + "|tag|" + row.Dimensions["git-tag"]
	case selectors.selected("git-sha"):
		return repo + "|sha|" + row.Dimensions["git-sha"]
	default:
		return repo
	}
}

func renderAmbiguousCloudChecks(cmd *cobra.Command, rows []cloudCheckRow) {
	fmt.Fprintln(cmd.OutOrStdout(), "Selectors match multiple Cloud check subjects. Add more selectors.")
	renderCloudList(cmd, rows, []string{"github-repo", "github-pr", "git-branch", "git-tag", "git-sha"})
}

func renderCloudList(cmd *cobra.Command, rows []cloudCheckRow, columns []string) {
	grouped := groupCloudListRows(rows, columns)
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	headers := make([]string, 0, len(columns)+4)
	for _, col := range columns {
		headers = append(headers, strings.ToUpper(col))
	}
	headers = append(headers, "RESULT")
	if slices.Contains(columns, "check") {
		headers = append(headers, "DURATION", "TRACE")
	}
	headers = append(headers, "UPDATED")
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, row := range grouped {
		fields := make([]string, 0, len(headers))
		for _, col := range columns {
			fields = append(fields, dash(row.Values[col]))
		}
		fields = append(fields, row.Result)
		if slices.Contains(columns, "check") {
			fields = append(fields, formatCloudDuration(row.Duration), dash(row.TraceURL))
		}
		fields = append(fields, relativeTime(row.UpdatedAt))
		fmt.Fprintln(tw, strings.Join(fields, "\t"))
	}
	_ = tw.Flush()
}

func groupCloudListRows(rows []cloudCheckRow, columns []string) []groupedCloudListRow {
	byKey := map[string]*groupedCloudListRow{}
	order := []string{}
	for _, row := range rows {
		values := map[string]string{}
		keyParts := make([]string, len(columns))
		for i, col := range columns {
			values[col] = row.Dimensions[col]
			keyParts[i] = col + "=" + row.Dimensions[col]
		}
		key := strings.Join(keyParts, "\x00")
		group, ok := byKey[key]
		if !ok {
			group = &groupedCloudListRow{
				Values:    values,
				Result:    row.Result,
				UpdatedAt: row.UpdatedAt,
				Duration:  row.Duration,
				TraceURL:  row.TraceURL,
				count:     1,
			}
			byKey[key] = group
			order = append(order, key)
			continue
		}
		group.count++
		group.Result = stricterCloudResult(group.Result, row.Result)
		if row.UpdatedAt.After(group.UpdatedAt) {
			group.UpdatedAt = row.UpdatedAt
		}
		if group.Duration != row.Duration {
			group.Duration = 0
		}
		if group.TraceURL != row.TraceURL {
			group.TraceURL = ""
		}
	}
	out := make([]groupedCloudListRow, 0, len(order))
	for _, key := range order {
		out = append(out, *byKey[key])
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

type groupedCloudListRow struct {
	Values    map[string]string
	Result    string
	UpdatedAt time.Time
	Duration  time.Duration
	TraceURL  string
	count     int
}

func dash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func formatCloudDuration(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d/time.Minute), int((d%time.Minute)/time.Second))
	default:
		return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int((d%time.Hour)/time.Minute))
	}
}

// checkPastWorkspaceAddress resolves the remote workspace address to look up past
// Cloud checks for: an explicit -W workspace ref if it's remote, otherwise the
// clean local checkout's inferred remote address.
func checkPastWorkspaceAddress(ctx context.Context) (string, bool, string, error) {
	address := strings.TrimSpace(workspaceRef)
	if address != "" {
		_, ok, err := parseWorkspaceRemoteAddress(ctx, address)
		if err != nil {
			return "", false, "", err
		}
		if ok {
			return address, true, "", nil
		}
	}

	_, inferred, dirty, inferErr := inferCleanLocalWorkspaceRemoteAddress(ctx, address)
	if inferErr == nil {
		if dirty {
			return "", false, "workspace has uncommitted changes", nil
		}
		if inferred == "" {
			return "", false, "no remote workspace is known", nil
		}
		return inferred, true, "", nil
	}

	return "", false, inferErr.Error(), nil
}
