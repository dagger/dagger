package daggercmd

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
)

const cloudCheckFetchLimit = 100

type cloudCheckSelectorFlags struct {
	GitHubRepo []string
	GitHubPR   []string
	GitBranch  []string
	GitTag     []string
	GitSHA     []string
	Workspace  []string
	Check      []string
}

func (f cloudCheckSelectorFlags) values(dim string) []string {
	switch dim {
	case "github-repo":
		return f.GitHubRepo
	case "github-pr":
		return f.GitHubPR
	case "git-branch":
		return f.GitBranch
	case "git-tag":
		return f.GitTag
	case "git-sha":
		return f.GitSHA
	case "workspace":
		return f.Workspace
	case "check":
		return f.Check
	default:
		return nil
	}
}

var cloudCheckDimensions = []string{
	"github-repo",
	"github-pr",
	"git-branch",
	"git-tag",
	"git-sha",
	"workspace",
	"check",
}

type cloudCheckRow struct {
	Dimensions map[string]string `json:"dimensions"`
	Result     string            `json:"result"`
	UpdatedAt  time.Time         `json:"updatedAt"`
	Duration   time.Duration     `json:"-"`
	TraceID    string            `json:"traceID,omitempty"`
	TraceURL   string            `json:"traceURL,omitempty"`

	Commit cloudapi.CheckCommit `json:"-"`
	Check  cloudapi.Check       `json:"-"`
}

func (cli *CloudCLI) loadCloudCheckRowsAcrossUserOrgs(ctx context.Context, selectors cloudCheckSelectorFlags, login bool) ([]cloudCheckRow, error) {
	client, _, err := cli.cloudClientWithLogin(ctx, login)
	if err != nil {
		return nil, err
	}
	user, err := client.User(ctx)
	if err != nil {
		return nil, err
	}

	orgs, preferred := orderCloudOrgsForRepos(user.Orgs, selectors.GitHubRepo)
	for _, org := range orgs[:preferred] {
		rows, err := loadCloudCheckRowsForOrg(ctx, client, org.Name, selectors)
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			return rows, nil
		}
	}

	var (
		mu   sync.Mutex
		rows []cloudCheckRow
	)
	eg, egctx := errgroup.WithContext(ctx)
	eg.SetLimit(8)
	for _, org := range orgs[preferred:] {
		org := org
		eg.Go(func() error {
			orgRows, err := loadCloudCheckRowsForOrg(egctx, client, org.Name, selectors)
			if err != nil {
				return err
			}
			mu.Lock()
			rows = append(rows, orgRows...)
			mu.Unlock()
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return rows, nil
}

func loadCloudCheckRowsForOrg(ctx context.Context, client *cloudapi.Client, orgName string, selectors cloudCheckSelectorFlags) ([]cloudCheckRow, error) {
	commits, err := client.OrgChecks(ctx, orgName, selectors.GitHubRepo, cloudCheckFetchLimit)
	if err != nil {
		return nil, fmt.Errorf("fetch Cloud checks for org %q: %w", orgName, err)
	}
	return filterCloudCheckRows(cloudCheckRows(orgName, commits), selectors), nil
}

func orderCloudOrgsForRepos(orgs []cloudauth.Org, repos []string) ([]cloudauth.Org, int) {
	if len(orgs) == 0 || len(repos) == 0 {
		return orgs, 0
	}
	owners := map[string]struct{}{}
	for _, repo := range repos {
		owner := cloudRepoOwner(repo)
		if owner != "" {
			owners[strings.ToLower(owner)] = struct{}{}
		}
	}
	if len(owners) == 0 {
		return orgs, 0
	}
	ordered := make([]cloudauth.Org, 0, len(orgs))
	for _, org := range orgs {
		if _, ok := owners[strings.ToLower(org.Name)]; ok {
			ordered = append(ordered, org)
		}
	}
	preferred := len(ordered)
	for _, org := range orgs {
		if _, ok := owners[strings.ToLower(org.Name)]; !ok {
			ordered = append(ordered, org)
		}
	}
	return ordered, preferred
}

func cloudRepoOwner(repo string) string {
	repo = strings.TrimPrefix(strings.TrimPrefix(repo, "https://"), "http://")
	parts := strings.Split(repo, "/")
	if len(parts) >= 3 && parts[0] == "github.com" {
		return parts[1]
	}
	if len(parts) >= 2 && !strings.Contains(parts[0], ".") {
		return parts[0]
	}
	return ""
}

func (cli *CloudCLI) loadCloudCheckRowsForWorkspace(ctx context.Context, address string, checks []string, login bool) ([]cloudCheckRow, error) {
	remote, ok, err := parseWorkspaceRemoteAddress(ctx, address)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("workspace %q is not remote", address)
	}

	client, _, err := cli.cloudClientWithLogin(ctx, login)
	if err != nil {
		return nil, err
	}

	commits, err := client.UserChecks(ctx, []string{remote.CloneRef}, cloudCheckFetchLimit)
	if err != nil {
		return nil, fmt.Errorf("fetch Cloud checks for %s: %w", remote.CloneRef, err)
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
		rows, err := cloudRowsForWorkspaceAddress(ctx, cloudCheckRows(org.Name, commitsByOrg[org.ID]), address, checks)
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			return rows, nil
		}
	}
	return nil, nil
}

func cloudRowsForWorkspaceAddress(ctx context.Context, rows []cloudCheckRow, address string, checks []string) ([]cloudCheckRow, error) {
	remote, ok, err := parseWorkspaceRemoteAddress(ctx, address)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
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
	return dedupeCloudCheckRows(out), nil
}

func cloudWorkspaceSelectors(base cloudCheckSelectorFlags, version string) []cloudCheckSelectorFlags {
	if version == "" {
		return []cloudCheckSelectorFlags{base}
	}
	var selectors []cloudCheckSelectorFlags
	if prNumber := cloudPullRequestNumber(version); prNumber != "" {
		sel := base
		sel.GitHubPR = []string{prNumber}
		selectors = append(selectors, sel)
	}
	branch := base
	branch.GitBranch = []string{version}
	selectors = append(selectors, branch)

	tag := base
	tag.GitTag = []string{version}
	selectors = append(selectors, tag)

	sha := base
	sha.GitSHA = []string{version}
	selectors = append(selectors, sha)
	return selectors
}

func cloudPullRequestNumber(version string) string {
	if rest, ok := strings.CutPrefix(version, "pull/"); ok {
		number, suffix, ok := strings.Cut(rest, "/")
		if ok && suffix == "head" {
			return number
		}
	}
	return ""
}

func dedupeCloudCheckRows(rows []cloudCheckRow) []cloudCheckRow {
	seen := map[string]struct{}{}
	out := make([]cloudCheckRow, 0, len(rows))
	for _, row := range rows {
		key := cloudCommitKey(row.Commit) + "\x00" +
			row.Check.Name + "\x00" +
			row.Dimensions["github-pr"] + "\x00" +
			row.Dimensions["git-branch"] + "\x00" +
			row.Dimensions["git-tag"] + "\x00" +
			row.Dimensions["git-sha"] + "\x00" +
			row.Dimensions["workspace"]
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, row)
	}
	return out
}

func cloudCheckRows(orgName string, commits []cloudapi.CheckCommit) []cloudCheckRow {
	var rows []cloudCheckRow
	for _, commit := range commits {
		checks := visibleCloudChecks(commit.Checks)
		if len(checks) == 0 {
			continue
		}
		refs := cloudCheckRefDimensions(commit)
		for _, check := range checks {
			for _, refDims := range refs {
				dims := map[string]string{}
				for k, v := range refDims {
					dims[k] = v
				}
				repo := normalizeGitHubRepo(commit.Repo)
				dims["github-repo"] = repo
				dims["git-sha"] = firstNonEmpty(commit.CommitSHA, check.ModuleVersion)
				dims["workspace"] = firstNonEmpty(check.ModuleRef, repo)
				dims["check"] = check.Name
				traceURL := ""
				if check.TraceID != "" {
					traceURL = cloudTraceURL(orgName, check.TraceID)
				}
				rows = append(rows, cloudCheckRow{
					Dimensions: dims,
					Result:     cloudResultForStatus(check.Status),
					UpdatedAt:  checkUpdatedAt(commit, check),
					Duration:   check.DurationAsTime(),
					TraceID:    check.TraceID,
					TraceURL:   traceURL,
					Commit:     commit,
					Check:      check,
				})
			}
		}
	}
	return rows
}

func cloudCheckRefDimensions(commit cloudapi.CheckCommit) []map[string]string {
	refs := make([]map[string]string, 0, len(commit.Refs))
	for _, ref := range commit.Refs {
		dims := map[string]string{}
		switch ref.Typename {
		case "CheckCommitPullRequestRef":
			if ref.Number != 0 {
				dims["github-pr"] = strconv.Itoa(ref.Number)
			}
			dims["url"] = ref.URL
			dims["description"] = ref.Title
		case "CheckCommitBranchRef":
			dims["git-branch"] = ref.Name
		case "CheckCommitTagRef":
			dims["git-tag"] = ref.Name
		}
		if len(dims) > 0 {
			refs = append(refs, dims)
		}
	}
	if len(refs) == 0 {
		refs = append(refs, map[string]string{})
	}
	return refs
}

func visibleCloudChecks(checks []cloudapi.Check) []cloudapi.Check {
	var public, internal []cloudapi.Check
	for _, check := range checks {
		if check.Internal {
			internal = append(internal, check)
		} else {
			public = append(public, check)
		}
	}
	visible := public
	if len(visible) == 0 {
		visible = internal
	}
	return latestCloudChecksByName(visible)
}

func latestCloudChecksByName(checks []cloudapi.Check) []cloudapi.Check {
	byName := make(map[string]cloudapi.Check, len(checks))
	order := make([]string, 0, len(checks))
	for _, check := range checks {
		existing, ok := byName[check.Name]
		if !ok {
			order = append(order, check.Name)
			byName[check.Name] = check
			continue
		}
		if cloudCheckStart(check).After(cloudCheckStart(existing)) {
			byName[check.Name] = check
		}
	}
	out := make([]cloudapi.Check, 0, len(order))
	for _, name := range order {
		out = append(out, byName[name])
	}
	return out
}

func filterCloudCheckRows(rows []cloudCheckRow, selectors cloudCheckSelectorFlags) []cloudCheckRow {
	out := make([]cloudCheckRow, 0, len(rows))
	for _, row := range rows {
		if cloudCheckRowMatches(row, selectors) {
			out = append(out, row)
		}
	}
	return out
}

func cloudCheckRowMatches(row cloudCheckRow, selectors cloudCheckSelectorFlags) bool {
	for _, dim := range cloudCheckDimensions {
		values := selectors.values(dim)
		if len(values) == 0 {
			continue
		}
		if !cloudDimensionMatches(dim, row.Dimensions[dim], values) {
			return false
		}
	}
	return true
}

func cloudDimensionMatches(dim, got string, values []string) bool {
	for _, want := range values {
		switch dim {
		case "github-repo":
			if strings.EqualFold(normalizeGitHubRepo(got), normalizeGitHubRepo(want)) {
				return true
			}
		case "workspace":
			if strings.EqualFold(normalizeGitHubRepo(got), normalizeGitHubRepo(want)) {
				return true
			}
		case "git-sha":
			got = strings.ToLower(got)
			want = strings.ToLower(want)
			if got == want || strings.HasPrefix(got, want) || strings.HasPrefix(want, got) {
				return true
			}
		default:
			if strings.EqualFold(got, want) {
				return true
			}
		}
	}
	return false
}

func normalizeGitHubRepo(repo string) string {
	repo = strings.TrimSpace(repo)
	repo = strings.TrimPrefix(repo, "https://")
	repo = strings.TrimPrefix(repo, "http://")
	repo = strings.TrimPrefix(repo, "github.com/")
	repo = strings.TrimSuffix(repo, ".git")
	return repo
}

func cloudResultForStatus(status string) string {
	switch strings.ToLower(status) {
	case "success", "succeeded", "passed", "ok":
		return "green"
	case "failure", "failed", "error", "errored", "cancelled", "canceled":
		return "red"
	default:
		return "pending"
	}
}

func stricterCloudResult(a, b string) string {
	if cloudResultRank(b) > cloudResultRank(a) {
		return b
	}
	return a
}

func cloudResultRank(result string) int {
	switch result {
	case "red":
		return 3
	case "pending":
		return 2
	case "green":
		return 1
	default:
		return 0
	}
}

func cloudChecksSummary(rows []cloudCheckRow) string {
	if len(rows) == 0 {
		return "-"
	}
	byCheck := map[string]string{}
	for _, row := range rows {
		name := row.Dimensions["check"]
		if name == "" {
			name = row.Check.Name
		}
		if name == "" {
			continue
		}
		byCheck[name] = stricterCloudResult(byCheck[name], row.Result)
	}
	if len(byCheck) == 0 {
		return "-"
	}
	result := "green"
	passed := 0
	for _, checkResult := range byCheck {
		result = stricterCloudResult(result, checkResult)
		if checkResult == "green" {
			passed++
		}
	}
	return fmt.Sprintf("%s %d/%d", result, passed, len(byCheck))
}

func cloudChecksEmojiSummary(rows []cloudCheckRow) string {
	if len(rows) == 0 {
		return "-"
	}
	counts := map[string]int{}
	for _, checkResult := range cloudCheckResultsByName(rows) {
		counts[checkResult]++
	}
	if len(counts) == 0 {
		return "-"
	}
	type resultCount struct {
		result string
		count  int
	}
	resultCounts := []resultCount{
		{result: "red", count: counts["red"]},
		{result: "pending", count: counts["pending"]},
		{result: "green", count: counts["green"]},
	}
	sort.SliceStable(resultCounts, func(i, j int) bool {
		return resultCounts[i].count > resultCounts[j].count
	})

	parts := make([]string, 0, len(resultCounts))
	for _, resultCount := range resultCounts {
		if resultCount.count == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s%d", cloudResultEmoji(resultCount.result), resultCount.count))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func cloudCheckResultsByName(rows []cloudCheckRow) map[string]string {
	byCheck := map[string]string{}
	for _, row := range rows {
		name := row.Dimensions["check"]
		if name == "" {
			name = row.Check.Name
		}
		if name == "" {
			continue
		}
		byCheck[name] = stricterCloudResult(byCheck[name], row.Result)
	}
	return byCheck
}

func cloudResultEmoji(result string) string {
	switch result {
	case "red":
		return "🔴"
	case "pending":
		return "🟡"
	case "green":
		return "🟢"
	default:
		return "⚪"
	}
}

func cloudCheckWorkspaceAddress(row cloudCheckRow) (string, string) {
	base := cloudCheckWorkspaceBase(row)
	if base == "" {
		return "", ""
	}
	switch {
	case row.Dimensions["github-pr"] != "":
		return "pr", base + "@pull/" + row.Dimensions["github-pr"] + "/head"
	case row.Dimensions["git-branch"] != "":
		return "branch", base + "@" + row.Dimensions["git-branch"]
	case row.Dimensions["git-tag"] != "":
		return "tag", base + "@" + row.Dimensions["git-tag"]
	case row.Dimensions["git-sha"] != "":
		return "sha", base + "@" + shortCloudSHA(row.Dimensions["git-sha"])
	default:
		return "remote", base
	}
}

func cloudCheckWorkspaceBase(row cloudCheckRow) string {
	if workspace := row.Dimensions["workspace"]; workspace != "" {
		return workspace
	}
	repo := normalizeGitHubRepo(row.Dimensions["github-repo"])
	if repo == "" {
		return ""
	}
	if strings.Count(repo, "/") == 1 {
		return "github.com/" + repo
	}
	return repo
}

func checkUpdatedAt(commit cloudapi.CheckCommit, check cloudapi.Check) time.Time {
	switch {
	case check.EndTime != nil:
		return *check.EndTime
	case check.StartedAt != nil:
		return *check.StartedAt
	case !commit.Timestamp.IsZero():
		return commit.Timestamp
	default:
		return time.Time{}
	}
}

func latestCloudRowTime(rows []cloudCheckRow) time.Time {
	var latest time.Time
	for _, row := range rows {
		if row.UpdatedAt.After(latest) {
			latest = row.UpdatedAt
		}
	}
	return latest
}

func cloudCheckStart(check cloudapi.Check) time.Time {
	if check.StartedAt == nil {
		return time.Time{}
	}
	return *check.StartedAt
}

func cloudCommitKey(commit cloudapi.CheckCommit) string {
	return normalizeGitHubRepo(commit.Repo) + "@" + commit.CommitSHA
}

func cloudTraceURL(orgName, traceID string) string {
	if traceID == "" {
		return ""
	}
	return fmt.Sprintf("https://dagger.cloud/%s/traces/%s", orgName, traceID)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func shortCloudSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return t.Format("2006-01-02")
	}
}
