package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/slog"
	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/util/cleanups"
	telemetry "github.com/dagger/otel-go"
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

type cloudCheckQueryResult struct {
	OrgName string
	OrgID   string
	Client  *cloudapi.Client
	Rows    []cloudCheckRow
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

func (cli *CloudCLI) TryReplayCloudChecksForWorkspace(cmd *cobra.Command, address string, checks []string) (bool, error) {
	res, selectors, err := cli.loadCloudCheckRowsForWorkspace(cmd.Context(), address, checks, false)
	if err != nil {
		return false, err
	}
	if len(res.Rows) == 0 {
		return false, nil
	}
	if err := cli.replayCloudCheckResult(cmd, res, selectors); err != nil {
		return true, err
	}
	return true, nil
}

func (cli *CloudCLI) replayCloudCheckResult(cmd *cobra.Command, res *cloudCheckQueryResult, selectors cloudCheckSelectorFlags) error {
	if len(res.Rows) == 0 {
		return idtui.ExitError{OriginalCode: 1, Original: fmt.Errorf("no Cloud check result found")}
	}

	commit, rows, err := selectCloudCheckCommit(res.Rows, selectors)
	if err != nil {
		renderAmbiguousCloudChecks(cmd, res.Rows)
		return err
	}
	checks := checksFromRows(rows)
	if len(checks) == 0 {
		return idtui.ExitError{OriginalCode: 1, Original: fmt.Errorf("no Cloud check result found")}
	}

	result := aggregateCloudResult(rows)
	renderCloudCheckReplayBanner(cmd, commit, rows, result)

	if cloudChecksHaveTraces(checks) {
		if err := replayCloudChecks(cmd, res.Client, res.OrgID, commit, checks); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Cloud trace replay failed: %v\n\n", err)
			renderCloudCheckRows(cmd, res.OrgName, rows)
		}
	} else {
		renderCloudCheckRows(cmd, res.OrgName, rows)
	}

	if result != "green" {
		return idtui.ExitError{OriginalCode: 1, Original: fmt.Errorf("Cloud checks are %s", result)}
	}
	return nil
}

func (cli *CloudCLI) loadCloudCheckRowsNoLogin(ctx context.Context, selectors cloudCheckSelectorFlags) (*cloudCheckQueryResult, error) {
	return cli.loadCloudCheckRowsWithLogin(ctx, selectors, false)
}

func (cli *CloudCLI) loadCloudCheckRowsWithLogin(ctx context.Context, selectors cloudCheckSelectorFlags, login bool) (*cloudCheckQueryResult, error) {
	client, cloudAuth, err := cli.cloudClientWithLogin(ctx, login)
	if err != nil {
		return nil, err
	}
	org, err := cli.resolveCloudOrg(ctx, client, cloudAuth)
	if err != nil {
		return nil, err
	}
	commits, err := client.OrgChecks(ctx, org.Name, selectors.GitHubRepo, cloudCheckFetchLimit)
	if err != nil {
		return nil, fmt.Errorf("fetch Cloud checks: %w", err)
	}
	rows := cloudCheckRows(org.Name, commits)
	rows = filterCloudCheckRows(rows, selectors)
	return &cloudCheckQueryResult{
		OrgName: org.Name,
		OrgID:   org.ID,
		Client:  client,
		Rows:    rows,
	}, nil
}

func (cli *CloudCLI) loadCloudCheckRowsForWorkspace(ctx context.Context, address string, checks []string, login bool) (*cloudCheckQueryResult, cloudCheckSelectorFlags, error) {
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
	res, err := cli.loadCloudCheckRowsWithLogin(ctx, cloudCheckSelectorFlags{
		GitHubRepo: []string{remote.CloneRef},
	}, login)
	if err != nil {
		return nil, cloudCheckSelectorFlags{}, err
	}
	rows, selectors, err := cloudRowsForWorkspaceAddress(ctx, res.Rows, address, checks)
	if err != nil {
		return nil, cloudCheckSelectorFlags{}, err
	}
	res.Rows = rows
	if !selectors.hasCloudSelector() {
		selectors = baseSelectors
	}
	return res, selectors, nil
}

func cloudRowsForWorkspaceAddress(ctx context.Context, rows []cloudCheckRow, address string, checks []string) ([]cloudCheckRow, cloudCheckSelectorFlags, error) {
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

func firstNonEmptyCloudSelector(selectors []cloudCheckSelectorFlags) cloudCheckSelectorFlags {
	for _, selector := range selectors {
		if selector.hasCloudSelector() {
			return selector
		}
	}
	return cloudCheckSelectorFlags{}
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

func checksFromRows(rows []cloudCheckRow) []cloudapi.Check {
	byName := make(map[string]cloudapi.Check, len(rows))
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		name := row.Check.Name
		if _, ok := byName[name]; !ok {
			order = append(order, name)
		}
		byName[name] = row.Check
	}
	checks := make([]cloudapi.Check, 0, len(order))
	for _, name := range order {
		checks = append(checks, byName[name])
	}
	return checks
}

func renderAmbiguousCloudChecks(cmd *cobra.Command, rows []cloudCheckRow) {
	fmt.Fprintln(cmd.OutOrStdout(), "Selectors match multiple Cloud check subjects. Add more selectors.")
	renderCloudList(cmd, rows, []string{"github-repo", "github-pr", "git-branch", "git-tag", "git-sha"})
}

func renderCloudCheckReplayBanner(cmd *cobra.Command, commit cloudapi.CheckCommit, rows []cloudCheckRow, result string) {
	row := rows[0]
	ref := cloudCheckRef(row)
	fmt.Fprintf(cmd.OutOrStdout(), "Replaying Cloud Checks result from %s\n", relativeTime(latestCloudRowTime(rows)))
	fmt.Fprintf(cmd.OutOrStdout(), "Workspace: %s\n", row.Dimensions["workspace"])
	if ref != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Ref:       %s\n", ref)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "SHA:       %s\n", shortCloudSHA(firstNonEmpty(row.Dimensions["git-sha"], commit.CommitSHA)))
	fmt.Fprintf(cmd.OutOrStdout(), "Result:    %s\n\n", result)
}

func renderCloudCheckRows(cmd *cobra.Command, orgName string, rows []cloudCheckRow) {
	_ = orgName
	renderCloudList(cmd, rows, []string{"check"})
}

func renderCloudList(cmd *cobra.Command, rows []cloudCheckRow, columns []string) {
	grouped := groupCloudListRows(rows, columns)
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	headers := make([]string, 0, len(columns)+4)
	for _, col := range columns {
		headers = append(headers, strings.ToUpper(col))
	}
	headers = append(headers, "RESULT")
	if contains(columns, "check") {
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
		if contains(columns, "check") {
			fields = append(fields, formatCloudDuration(row.Duration), dash(row.TraceURL))
		}
		fields = append(fields, relativeTime(row.UpdatedAt))
		fmt.Fprintln(tw, strings.Join(fields, "\t"))
	}
	_ = tw.Flush()
}

type groupedCloudListRow struct {
	Values    map[string]string
	Result    string
	UpdatedAt time.Time
	Duration  time.Duration
	TraceURL  string
	count     int
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

func replayCloudChecks(cmd *cobra.Command, client *cloudapi.Client, orgID string, commit cloudapi.CheckCommit, checks []cloudapi.Check) error {
	return Frontend.Run(cmd.Context(), opts, func(ctx context.Context) (cleanups.CleanupF, error) {
		spanExp := Frontend.SpanExporter()
		defer spanExp.Shutdown(ctx)
		logExp := Frontend.LogExporter()
		defer logExp.Shutdown(ctx)

		traceID, err := randomHexID(16)
		if err != nil {
			return nil, err
		}
		rootID, err := randomHexID(8)
		if err != nil {
			return nil, err
		}

		var allSpans []cloudapi.SpanData
		var logBatches []struct {
			OriginalTraceID string
			RootSpanID      string
		}
		var rootStart, rootEnd time.Time

		bulkReplay := len(checks) > 3
		var mu sync.Mutex
		eg, ctx := errgroup.WithContext(ctx)
		eg.SetLimit(16)
		for _, check := range checks {
			check := check
			if check.TraceID == "" {
				continue
			}
			eg.Go(func() error {
				if bulkReplay {
					checkSpanID, err := randomHexID(8)
					if err != nil {
						return err
					}
					checkSpan, start, end := syntheticCloudCheckSpan(traceID, checkSpanID, check, commit.Timestamp)
					mu.Lock()
					rootStart, rootEnd = extendBounds(rootStart, rootEnd, start, end)
					allSpans = append(allSpans, checkSpan)
					mu.Unlock()
					return nil
				}

				var spans []cloudapi.SpanData
				traceCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
				defer cancel()
				if err := client.StreamSpans(traceCtx, orgID, check.TraceID, func(batch []cloudapi.SpanData) {
					spans = append(spans, batch...)
				}); err != nil {
					if len(spans) == 0 {
						slog.Warn("error streaming Cloud check trace", "check", check.Name, "trace", check.TraceID, "err", err)
						return nil
					}
					slog.Warn("using partial Cloud check trace", "check", check.Name, "trace", check.TraceID, "err", err)
				}
				if len(spans) == 0 {
					return nil
				}
				checkSpanID, err := randomHexID(8)
				if err != nil {
					return err
				}
				start, end := cloudSpanBounds(spans)
				statusCode := "STATUS_CODE_OK"
				if cloudResultForStatus(check.Status) == "red" {
					statusCode = "STATUS_CODE_ERROR"
				}
				checkSpan := cloudapi.SpanData{
					ID:        checkSpanID,
					TraceID:   traceID,
					Name:      check.Name,
					Timestamp: start,
					EndTime:   &end,
					Status: cloudapi.SpanStatus{
						Code:    statusCode,
						Message: check.Status,
					},
				}
				var checkLogBatches []struct {
					OriginalTraceID string
					RootSpanID      string
				}
				for i := range spans {
					if spans[i].ParentID == nil {
						originalRootID := spans[i].ID
						checkLogBatches = append(checkLogBatches, struct {
							OriginalTraceID string
							RootSpanID      string
						}{OriginalTraceID: check.TraceID, RootSpanID: originalRootID})
						parentID := checkSpanID
						spans[i].ParentID = &parentID
					}
					spans[i].TraceID = traceID
				}
				mu.Lock()
				rootStart, rootEnd = extendBounds(rootStart, rootEnd, start, end)
				allSpans = append(allSpans, checkSpan)
				allSpans = append(allSpans, spans...)
				logBatches = append(logBatches, checkLogBatches...)
				mu.Unlock()
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return nil, err
		}

		if len(allSpans) == 0 {
			return nil, fmt.Errorf("no replayable traces found")
		}
		if rootStart.IsZero() {
			rootStart = time.Now()
		}
		if rootEnd.IsZero() || rootEnd.Before(rootStart) {
			rootEnd = rootStart
		}
		rootSpan := cloudapi.SpanData{
			ID:        rootID,
			TraceID:   traceID,
			Name:      "dagger check",
			Timestamp: rootStart,
			EndTime:   &rootEnd,
			Attributes: map[string]any{
				"dagger.io/replay":        true,
				"dagger.io/replay.source": "cloud-checks",
				"git.sha":                 commit.CommitSHA,
			},
		}
		allSpans = append([]cloudapi.SpanData{rootSpan}, allSpans...)
		for i := range allSpans {
			if allSpans[i].ID != rootID && allSpans[i].ParentID == nil {
				parentID := rootID
				allSpans[i].ParentID = &parentID
			}
		}

		spans := telemetry.SpansFromPB(cloudapi.SpansToPB(allSpans))
		if err := spanExp.ExportSpans(ctx, spans); err != nil {
			return nil, err
		}
		Frontend.SetPrimary(dagui.SpanID{SpanID: spans[0].SpanContext().SpanID()})

		if len(checks) <= 3 {
			eg, ctx := errgroup.WithContext(ctx)
			for _, logs := range logBatches {
				logs := logs
				eg.Go(func() error {
					return client.StreamLogs(ctx, orgID, logs.OriginalTraceID, logs.RootSpanID, func(messages []cloudapi.LogMessage) {
						records := cloudapi.LogMessagesToRecords(traceID, messages)
						if len(records) == 0 {
							return
						}
						if err := logExp.Export(ctx, records); err != nil {
							slog.Warn("error exporting logs", "err", err)
						}
					})
				})
			}
			if err := eg.Wait(); err != nil {
				return nil, err
			}
		}

		return func() error { return nil }, nil
	})
}

func syntheticCloudCheckSpan(traceID, spanID string, check cloudapi.Check, fallback time.Time) (cloudapi.SpanData, time.Time, time.Time) {
	start := cloudCheckStart(check)
	if start.IsZero() {
		start = fallback
	}
	if start.IsZero() {
		start = time.Now()
	}
	end := start
	if check.EndTime != nil {
		end = *check.EndTime
	} else if d := check.DurationAsTime(); d > 0 {
		end = start.Add(d)
	}
	if end.Before(start) {
		end = start
	}
	statusCode := "STATUS_CODE_OK"
	if cloudResultForStatus(check.Status) == "red" {
		statusCode = "STATUS_CODE_ERROR"
	}
	return cloudapi.SpanData{
		ID:        spanID,
		TraceID:   traceID,
		Name:      check.Name,
		Timestamp: start,
		EndTime:   &end,
		Attributes: map[string]any{
			"dagger.io/replay.summary": true,
			"dagger.io/original.trace": check.TraceID,
		},
		Status: cloudapi.SpanStatus{
			Code:    statusCode,
			Message: check.Status,
		},
	}, start, end
}

func cloudSpanBounds(spans []cloudapi.SpanData) (time.Time, time.Time) {
	var start, end time.Time
	for _, span := range spans {
		if start.IsZero() || span.Timestamp.Before(start) {
			start = span.Timestamp
		}
		spanEnd := span.Timestamp
		if span.EndTime != nil {
			spanEnd = *span.EndTime
		}
		if end.IsZero() || spanEnd.After(end) {
			end = spanEnd
		}
	}
	return start, end
}

func extendBounds(rootStart, rootEnd, start, end time.Time) (time.Time, time.Time) {
	if rootStart.IsZero() || start.Before(rootStart) {
		rootStart = start
	}
	if rootEnd.IsZero() || end.After(rootEnd) {
		rootEnd = end
	}
	return rootStart, rootEnd
}

func randomHexID(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func cloudChecksHaveTraces(checks []cloudapi.Check) bool {
	for _, check := range checks {
		if check.TraceID != "" {
			return true
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

func aggregateCloudResult(rows []cloudCheckRow) string {
	result := "green"
	for _, row := range rows {
		result = stricterCloudResult(result, row.Result)
	}
	return result
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

func cloudCheckRef(row cloudCheckRow) string {
	switch {
	case row.Dimensions["github-pr"] != "":
		return "PR #" + row.Dimensions["github-pr"]
	case row.Dimensions["git-branch"] != "":
		return "branch " + row.Dimensions["git-branch"]
	case row.Dimensions["git-tag"] != "":
		return "tag " + row.Dimensions["git-tag"]
	default:
		return ""
	}
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

func dash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
