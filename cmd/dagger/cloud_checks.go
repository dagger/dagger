package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
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

var checkCloudSelectors cloudCheckSelectorFlags
var listCloudSelectors cloudCheckSelectorFlags

var cloudListCmd = &cobra.Command{
	Use:     "list [dimension]",
	Short:   "List Dagger Cloud check coordinate values",
	Args:    cobra.MaximumNArgs(1),
	GroupID: cloudGroup.ID,
	RunE:    cloudCLI.ListCloudChecks,
}

func init() {
	checkCloudSelectors.addFlags(checksCmd)
	cloudListCmd.Flags().BoolVar(&cloudJSON, "json", false, "Print JSON output")
	listCloudSelectors.addFlags(cloudListCmd)
	rootCmd.AddCommand(cloudListCmd)
}

type cloudCheckSelectorFlags struct {
	GitHubAccount []string
	GitHubRepo    []string
	GitHubPR      []string
	GitBranch     []string
	GitTag        []string
	GitSHA        []string
	Workspace     []string
	Check         []string
}

func (f *cloudCheckSelectorFlags) addFlags(cmd *cobra.Command) {
	cmd.Flags().StringArrayVar(&f.GitHubAccount, "github-account", nil, "GitHub account name")
	cmd.Flags().StringArrayVar(&f.GitHubRepo, "github-repo", nil, "GitHub repository, e.g. acme/hello")
	cmd.Flags().StringArrayVar(&f.GitHubPR, "github-pr", nil, "GitHub pull request number")
	cmd.Flags().StringArrayVar(&f.GitBranch, "git-branch", nil, "Git branch name")
	cmd.Flags().StringArrayVar(&f.GitTag, "git-tag", nil, "Git tag name")
	cmd.Flags().StringArrayVar(&f.GitSHA, "git-sha", nil, "Git commit SHA")
	cmd.Flags().StringArrayVar(&f.Workspace, "workspace", nil, "Dagger workspace or module ref")
	cmd.Flags().StringArrayVar(&f.Check, "check", nil, "Check name")
}

func (f cloudCheckSelectorFlags) hasCloudSelector() bool {
	return len(f.GitHubAccount) > 0 ||
		len(f.GitHubRepo) > 0 ||
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
	case "github-account":
		return f.GitHubAccount
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

func (f cloudCheckSelectorFlags) withChecks(checks []string) cloudCheckSelectorFlags {
	f.Check = append(append([]string{}, f.Check...), checks...)
	return f
}

var cloudCheckDimensions = []string{
	"github-account",
	"github-repo",
	"github-pr",
	"git-branch",
	"git-tag",
	"git-sha",
	"workspace",
	"check",
}

var cloudCheckDimensionAliases = map[string]string{
	"account":        "github-account",
	"github-account": "github-account",
	"repo":           "github-repo",
	"github-repo":    "github-repo",
	"pr":             "github-pr",
	"github-pr":      "github-pr",
	"branch":         "git-branch",
	"git-branch":     "git-branch",
	"tag":            "git-tag",
	"git-tag":        "git-tag",
	"sha":            "git-sha",
	"git-sha":        "git-sha",
	"workspace":      "workspace",
	"check":          "check",
	"checks":         "check",
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

func (cli *CloudCLI) ListCloudChecks(cmd *cobra.Command, args []string) error {
	var dimension string
	if len(args) > 0 {
		var ok bool
		dimension, ok = canonicalCloudCheckDimension(args[0])
		if !ok {
			return fmt.Errorf("unknown dimension %q; available dimensions: %s", args[0], strings.Join(cloudCheckDimensions, ", "))
		}
	}

	res, err := cli.loadCloudCheckRows(cmd.Context(), listCloudSelectors)
	if err != nil {
		return err
	}

	if cloudJSON {
		return writeCloudJSON(cmd, res.Rows)
	}
	if len(res.Rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No Cloud check results found for those selectors.")
		return nil
	}
	cols := cloudListProjectionColumns(dimension, listCloudSelectors, res.Rows)
	renderCloudList(cmd, res.Rows, cols)
	return nil
}

func (cli *CloudCLI) CheckCloud(cmd *cobra.Command, args []string) error {
	selectors := checkCloudSelectors.withChecks(args)
	res, err := cli.loadCloudCheckRows(cmd.Context(), selectors)
	if err != nil {
		return err
	}
	if len(res.Rows) == 0 {
		renderCloudCheckMiss(cmd, selectors)
		return idtui.ExitError{OriginalCode: 1, Original: fmt.Errorf("no Cloud check result found")}
	}

	commit, rows, err := selectCloudCheckCommit(res.Rows, selectors)
	if err != nil {
		renderAmbiguousCloudChecks(cmd, res.Rows)
		return err
	}
	checks := checksFromRows(rows)
	if len(checks) == 0 {
		renderCloudCheckMiss(cmd, selectors)
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

func (cli *CloudCLI) loadCloudCheckRows(ctx context.Context, selectors cloudCheckSelectorFlags) (*cloudCheckQueryResult, error) {
	client, cloudAuth, err := cli.cloudClient(ctx)
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
				dims["github-account"] = githubAccount(repo)
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

func renderCloudCheckMiss(cmd *cobra.Command, selectors cloudCheckSelectorFlags) {
	fmt.Fprintf(cmd.OutOrStdout(), "No Cloud check result found for %s.\n\n", selectorSummary(selectors))
	fmt.Fprintln(cmd.OutOrStdout(), "Checks are normally ingested from GitHub webhooks. To manually repair ingestion:")
	fmt.Fprintln(cmd.OutOrStdout(), "  dagger integration sync github --repo=OWNER/REPO --pr=NUMBER")
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

func cloudListProjectionColumns(dimension string, selectors cloudCheckSelectorFlags, rows []cloudCheckRow) []string {
	if dimension == "" {
		var cols []string
		for _, dim := range cloudCheckDimensions {
			if !selectors.selected(dim) && cloudRowsHaveDimension(rows, dim) {
				cols = append(cols, dim)
			}
		}
		return cols
	}
	var cols []string
	if dimension == "check" {
		for _, dim := range []string{"github-repo", "github-pr", "git-branch", "git-tag", "git-sha", "workspace"} {
			if selectors.selected(dim) || !cloudRowsHaveDimension(rows, dim) {
				continue
			}
			if isCloudRefDimension(dim) || cloudRowsHaveMultipleValues(rows, dim) {
				cols = append(cols, dim)
			}
		}
	}
	if !contains(cols, dimension) {
		cols = append(cols, dimension)
	}
	if shouldIncludeGitSHAContext(dimension) && !selectors.selected("git-sha") && cloudRowsHaveDimension(rows, "git-sha") && !contains(cols, "git-sha") {
		cols = append(cols, "git-sha")
	}
	return cols
}

func isCloudRefDimension(dim string) bool {
	return dim == "github-pr" || dim == "git-branch" || dim == "git-tag"
}

func shouldIncludeGitSHAContext(dimension string) bool {
	switch dimension {
	case "github-pr", "git-branch", "git-tag", "workspace":
		return true
	default:
		return false
	}
}

func cloudRowsHaveDimension(rows []cloudCheckRow, dim string) bool {
	for _, row := range rows {
		if row.Dimensions[dim] != "" {
			return true
		}
	}
	return false
}

func cloudRowsHaveMultipleValues(rows []cloudCheckRow, dim string) bool {
	values := map[string]struct{}{}
	for _, row := range rows {
		if row.Dimensions[dim] != "" {
			values[row.Dimensions[dim]] = struct{}{}
		}
	}
	return len(values) > 1
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

		for _, check := range checks {
			if check.TraceID == "" {
				continue
			}
			var spans []cloudapi.SpanData
			if err := client.StreamSpans(ctx, orgID, check.TraceID, func(batch []cloudapi.SpanData) {
				spans = append(spans, batch...)
			}); err != nil {
				return nil, err
			}
			if len(spans) == 0 {
				continue
			}
			checkSpanID, err := randomHexID(8)
			if err != nil {
				return nil, err
			}
			start, end := cloudSpanBounds(spans)
			rootStart, rootEnd = extendBounds(rootStart, rootEnd, start, end)
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
			for i := range spans {
				if spans[i].ParentID == nil {
					originalRootID := spans[i].ID
					logBatches = append(logBatches, struct {
						OriginalTraceID string
						RootSpanID      string
					}{OriginalTraceID: check.TraceID, RootSpanID: originalRootID})
					parentID := checkSpanID
					spans[i].ParentID = &parentID
				}
				spans[i].TraceID = traceID
			}
			allSpans = append(allSpans, checkSpan)
			allSpans = append(allSpans, spans...)
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

		return func() error { return nil }, nil
	})
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

func canonicalCloudCheckDimension(dim string) (string, bool) {
	canonical, ok := cloudCheckDimensionAliases[strings.ToLower(dim)]
	return canonical, ok
}

func normalizeGitHubRepo(repo string) string {
	repo = strings.TrimSpace(repo)
	repo = strings.TrimPrefix(repo, "https://")
	repo = strings.TrimPrefix(repo, "http://")
	repo = strings.TrimPrefix(repo, "github.com/")
	repo = strings.TrimSuffix(repo, ".git")
	return repo
}

func githubAccount(repo string) string {
	parts := strings.Split(normalizeGitHubRepo(repo), "/")
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
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

func selectorSummary(selectors cloudCheckSelectorFlags) string {
	var parts []string
	for _, dim := range cloudCheckDimensions {
		for _, value := range selectors.values(dim) {
			parts = append(parts, dim+"="+value)
		}
	}
	if len(parts) == 0 {
		return "the supplied selectors"
	}
	return strings.Join(parts, " ")
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
