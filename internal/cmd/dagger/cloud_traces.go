package daggercmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
)

var cloudTracesCmd = &cobra.Command{
	Use:     "traces",
	Aliases: []string{"trace"},
	Short:   "View Dagger Cloud traces",
	Args:    cobra.NoArgs,
	Annotations: map[string]string{
		hiddenAliasesAnnotation: "trace",
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var cloudTracesStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show if this installation sends traces to Dagger Cloud",
	Long: `Show if the dagger CLI on this machine sends traces to Dagger Cloud, with
the current environment.

This command shows the state of this installation, not the state of Dagger
Cloud. It examines the local credential only. It does not ask Dagger Cloud if
the credential is valid.

The exit code is 0 when the CLI sends traces, and 1 when it does not.`,
	Args: cobra.NoArgs,
	RunE: runCloudTracesStatus,
}

var cloudTracesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Dagger Cloud traces",
	Long: `List the traces of a Dagger Cloud org, local and CI, newest first.

Use the flags to filter the traces. All filters must match.`,
	Example: `dagger cloud traces list --status failed --branch main --since 7d
dagger cloud traces list --mine --local --limit 5
dagger cloud traces list --pr 123 --json id,status,duration`,
	Args: cobra.NoArgs,
	RunE: runCloudTracesList,
}

var cloudTracesList struct {
	repos       []string
	branch      string
	commit      string
	tag         string
	pr          string
	status      string
	local       bool
	ci          bool
	mine        bool
	user        string
	token       string
	author      string
	command     string
	provider    string
	since       string
	until       string
	minDuration time.Duration
	maxDuration time.Duration
	limit       int
	sort        string
	json        []string
}

func init() {
	flags := cloudTracesListCmd.Flags()
	l := &cloudTracesList
	flags.StringSliceVar(&l.repos, "repo", nil, "Only traces of this repository, e.g. github.com/org/repo (repeatable)")
	flags.StringVar(&l.branch, "branch", "", "Only traces of this branch")
	flags.StringVar(&l.commit, "commit", "", "Only traces of this commit (full or short SHA)")
	flags.StringVar(&l.tag, "tag", "", "Only traces of this git tag")
	flags.StringVar(&l.pr, "pr", "", "Only traces of this pull request number")
	flags.StringVar(&l.status, "status", "", "Only traces with this status: passed, failed or running")
	flags.BoolVar(&l.local, "local", false, "Only traces of local runs")
	flags.BoolVar(&l.ci, "ci", false, "Only traces of CI runs")
	flags.BoolVar(&l.mine, "mine", false, "Only traces that you sent")
	flags.StringVar(&l.user, "user", "", "Only traces that this user sent (user ID or email)")
	flags.StringVar(&l.token, "token", "", "Only traces that this token sent (token ID or name)")
	flags.StringVar(&l.author, "author", "", "Only traces whose commit author or committer matches this text")
	flags.StringVar(&l.command, "command", "", "Only traces whose command matches this glob, e.g. 'dagger check*'")
	flags.StringVar(&l.provider, "provider", "", "Only traces of this CI provider, e.g. github")
	flags.StringVar(&l.since, "since", "", "Only traces that started after this time: a duration (30m, 2d, 1w) or a date (2006-01-02, RFC 3339)")
	flags.StringVar(&l.until, "until", "", "Only traces that started before this time: a duration or a date, as for --since")
	flags.DurationVar(&l.minDuration, "min-duration", 0, "Only traces that ran for at least this time, e.g. 5m")
	flags.DurationVar(&l.maxDuration, "max-duration", 0, "Only traces that ran for at most this time")
	flags.IntVarP(&l.limit, "limit", "L", 20, "Maximum number of traces to list (1-1000)")
	flags.StringVar(&l.sort, "sort", "start", "Sort order: start (newest first) or duration (longest first)")
	flags.StringSliceVar(&l.json, "json", nil, "Print JSON with these fields: "+strings.Join(traceJSONFields, ","))
	cloudTracesListCmd.MarkFlagsMutuallyExclusive("local", "ci")
	cloudTracesListCmd.MarkFlagsMutuallyExclusive("mine", "user", "token")

	cloudTracesCmd.AddCommand(cloudTracesStatusCmd, cloudTracesListCmd, cloudTracesViewCmd)
	cloudCmd.AddCommand(cloudTracesCmd)
}

func runCloudTracesStatus(cmd *cobra.Command, _ []string) error {
	status := enginetel.CloudEmitStatusFor(cmd.Context())
	if err := writeCloudTracesStatus(cmd.OutOrStdout(), status); err != nil {
		return err
	}
	if !status.Emitting {
		return idtui.Fail
	}
	return nil
}

func writeCloudTracesStatus(w io.Writer, status enginetel.CloudEmitStatus) error {
	if status.Emitting {
		credential := status.Credential
		if status.Org != "" {
			credential += " (org: " + status.Org + ")"
		}
		_, err := fmt.Fprintf(w, "emitting\n  credential: %s\n", credential)
		return err
	}
	const loginFix = "dagger login, or set DAGGER_CLOUD_TOKEN"
	var reason, fix string
	switch {
	case status.Err != nil:
		reason = "cannot read the credential: " + status.Err.Error()
		fix = loginFix
	case status.Credential == "":
		reason = "no credential"
		fix = loginFix
	default:
		reason = "logged in, but no org is selected"
		fix = "dagger cloud org use <org>"
	}
	_, err := fmt.Fprintf(w, "not emitting\n  reason: %s\n  fix:    %s\n", reason, fix)
	return err
}

func runCloudTracesList(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	l := &cloudTracesList

	filter, err := cloudTracesListFilter(time.Now())
	if err != nil {
		return err
	}
	if l.limit < 1 || l.limit > 1000 {
		return fmt.Errorf("--limit must be between 1 and 1000")
	}
	var sort string
	switch l.sort {
	case "start":
		sort = cloudapi.TraceListSortStart
	case "duration":
		sort = cloudapi.TraceListSortDuration
	default:
		return fmt.Errorf("--sort must be start or duration, not %q", l.sort)
	}
	for _, field := range l.json {
		if !slices.Contains(traceJSONFields, field) {
			return fmt.Errorf("unknown --json field %q; available fields: %s", field, strings.Join(traceJSONFields, ", "))
		}
	}

	client, cloudAuth, err := cloudCLI.cloudClient(ctx)
	if err != nil {
		return err
	}
	org, err := cloudCLI.resolveCloudOrg(ctx, client, cloudAuth)
	if err != nil {
		return err
	}
	traces, err := client.TraceList(ctx, org.Name, filter, sort, l.limit)
	if err != nil {
		return err
	}

	now := time.Now()
	if len(l.json) > 0 {
		return writeTracesJSON(cmd.OutOrStdout(), org.Name, traces, l.json, now)
	}
	if len(traces) == 0 {
		_, err := fmt.Fprintln(cmd.ErrOrStderr(), "No traces found.")
		return err
	}
	return writeTracesTable(cmd.OutOrStdout(), traces, now)
}

// cloudTracesListFilter turns the list flags into a server-side filter.
func cloudTracesListFilter(now time.Time) (cloudapi.TraceListFilter, error) {
	l := &cloudTracesList
	f := cloudapi.TraceListFilter{
		Repos:    l.repos,
		Branch:   l.branch,
		Commit:   l.commit,
		Tag:      l.tag,
		Change:   strings.TrimPrefix(l.pr, "#"),
		Mine:     l.mine,
		User:     l.user,
		Token:    l.token,
		Author:   l.author,
		Name:     l.command,
		Provider: l.provider,
	}
	switch l.status {
	case "":
	case cloudapi.TraceStatePassed, cloudapi.TraceStateFailed, cloudapi.TraceStateRunning:
		f.Status = strings.ToUpper(l.status)
	default:
		return f, fmt.Errorf("--status must be passed, failed or running, not %q", l.status)
	}
	if l.local || l.ci {
		local := l.local
		f.Local = &local
	}
	if l.commit != "" && len(l.commit) < 4 {
		return f, fmt.Errorf("--commit needs at least 4 characters")
	}
	var err error
	if f.Since, err = parseTimeFlag("since", l.since, now); err != nil {
		return f, err
	}
	if f.Until, err = parseTimeFlag("until", l.until, now); err != nil {
		return f, err
	}
	if l.minDuration < 0 || l.maxDuration < 0 {
		return f, fmt.Errorf("--min-duration and --max-duration must not be negative")
	}
	f.MinDuration = l.minDuration.Seconds()
	f.MaxDuration = l.maxDuration.Seconds()
	return f, nil
}

// parseTimeFlag reads a time as a duration before now (30m, 2d, 1w), a date
// (2006-01-02) or an RFC 3339 time. An empty value gives nil.
func parseTimeFlag(name, value string, now time.Time) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	if d, err := parseAgo(value); err == nil {
		t := now.Add(-d)
		return &t, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("--%s: cannot read %q; use a duration (30m, 2d, 1w) or a date (2006-01-02, RFC 3339)", name, value)
}

// parseAgo is time.ParseDuration with days (d) and weeks (w) added.
func parseAgo(value string) (time.Duration, error) {
	for suffix, unit := range map[string]time.Duration{"d": 24 * time.Hour, "w": 7 * 24 * time.Hour} {
		if n, ok := strings.CutSuffix(value, suffix); ok {
			v, err := strconv.ParseFloat(n, 64)
			if err != nil || v < 0 {
				return 0, errors.New("invalid duration")
			}
			return time.Duration(v * float64(unit)), nil
		}
	}
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 {
		return 0, errors.New("invalid duration")
	}
	return d, nil
}

func writeTracesTable(w io.Writer, traces []cloudapi.TraceSummary, now time.Time) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tDURATION\tCOMMAND\tBRANCH/PR\tSENDER\tSTARTED")
	for i := range traces {
		t := &traces[i]
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			t.ID,
			t.State(),
			dagui.FormatDuration(t.Duration(now)),
			truncateCell(t.Name, 50),
			dash(traceBranchOrPR(t)),
			dash(traceSender(t)),
			relativeTime(t.Timestamp),
		)
	}
	return tw.Flush()
}

func truncateCell(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func traceBranchOrPR(t *cloudapi.TraceSummary) string {
	if pr := tracePR(t); pr != "" {
		return "#" + pr
	}
	if b := traceBranch(t); b != "" {
		return b
	}
	return shortCloudSHA(traceCommit(t))
}

func tracePR(t *cloudapi.TraceSummary) string {
	if t.CI != nil && t.CI.Change != nil && t.CI.Change.ID != "" {
		return t.CI.Change.ID
	}
	return ""
}

func traceBranch(t *cloudapi.TraceSummary) string {
	if t.CI != nil && t.CI.Change != nil && t.CI.Change.Branch != "" {
		return t.CI.Change.Branch
	}
	if t.Git != nil && t.Git.Branch != nil {
		return *t.Git.Branch
	}
	return ""
}

func traceCommit(t *cloudapi.TraceSummary) string {
	if t.CI != nil && t.CI.Change != nil && t.CI.Change.HeadSHA != "" {
		return t.CI.Change.HeadSHA
	}
	if t.Git != nil {
		return t.Git.Ref
	}
	return ""
}

func traceSender(t *cloudapi.TraceSummary) string {
	if t.Sender != nil {
		return t.Sender.Name
	}
	return ""
}

// traceJSONFields are the fields 'dagger cloud traces list --json' can print.
var traceJSONFields = []string{
	"id", "url", "command", "status", "message", "startedAt", "endedAt", "duration",
	"local", "sender", "repo", "branch", "commit", "tag", "pr", "title", "author", "provider",
}

func writeTracesJSON(w io.Writer, orgName string, traces []cloudapi.TraceSummary, fields []string, now time.Time) error {
	rows := make([]map[string]any, 0, len(traces))
	for i := range traces {
		all := traceJSON(orgName, &traces[i], now)
		row := make(map[string]any, len(fields))
		for _, f := range fields {
			row[f] = all[f]
		}
		rows = append(rows, row)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}

func traceJSON(orgName string, t *cloudapi.TraceSummary, now time.Time) map[string]any {
	var message, repo, tag, title, author, provider string
	if t.Status != nil {
		message = t.Status.Message
	}
	if t.Git != nil {
		repo = t.Git.Remote
		title = t.Git.Title
		if t.Git.Tag != nil {
			tag = *t.Git.Tag
		}
		if t.Git.Author != nil {
			author = t.Git.Author.Name
			if t.Git.Author.Email != "" {
				author += " <" + t.Git.Author.Email + ">"
			}
		}
	}
	if t.CI != nil {
		if t.CI.Provider != nil {
			provider = *t.CI.Provider
		}
		if repo == "" && t.CI.Repository != nil {
			repo = *t.CI.Repository
		}
		if t.CI.Change != nil && t.CI.Change.Title != "" {
			title = t.CI.Change.Title
		}
	}
	var endedAt any
	if t.EndTime != nil && !t.EndTime.IsZero() {
		endedAt = t.EndTime
	}
	return map[string]any{
		"id":        t.ID,
		"url":       cloudTraceURL(orgName, t.ID),
		"command":   t.Name,
		"status":    t.State(),
		"message":   message,
		"startedAt": t.Timestamp,
		"endedAt":   endedAt,
		"duration":  t.Duration(now).Seconds(),
		"local":     t.Local,
		"sender":    traceSender(t),
		"repo":      repo,
		"branch":    traceBranch(t),
		"commit":    traceCommit(t),
		"tag":       tag,
		"pr":        tracePR(t),
		"title":     title,
		"author":    author,
		"provider":  provider,
	}
}

// cloudSpanURL is the web UI address of a span in a trace, or of the trace
// when spanID is empty.
func cloudSpanURL(orgName, traceID, spanID string) string {
	u := cloudTraceURL(orgName, traceID)
	if u != "" && spanID != "" {
		u += "?span=" + url.QueryEscape(spanID)
	}
	return u
}

// parseTraceRef reads a trace argument: a trace ID, or a Dagger Cloud trace
// URL (https://dagger.cloud/<org>/traces/<id>). It returns the trace ID, the
// org name when the URL gives one, and the span ID when the URL gives one.
func parseTraceRef(arg string) (traceID, orgName, spanID string, err error) {
	arg = strings.TrimSpace(arg)
	if u, perr := url.Parse(arg); perr == nil && u.Scheme != "" && u.Host != "" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		for i := range parts {
			if parts[i] == "traces" && i+1 < len(parts) {
				traceID = parts[i+1]
				if i > 0 {
					orgName = parts[i-1]
				}
				break
			}
		}
		if traceID == "" {
			return "", "", "", fmt.Errorf("invalid trace URL %q: use https://dagger.cloud/<org>/traces/<id>", arg)
		}
		spanID = u.Query().Get("span")
		arg = traceID
	}
	id, perr := trace.TraceIDFromHex(strings.ToLower(arg))
	if perr != nil {
		return "", "", "", fmt.Errorf("invalid trace %q: use a 32-character hex trace ID or a https://dagger.cloud/<org>/traces/<id> URL", arg)
	}
	return id.String(), orgName, spanID, nil
}

// traceWebOrg picks the org for a web link: --org, else the org that the
// trace URL or --last gave, else the credential's org.
func traceWebOrg(orgName string) (string, error) {
	if cloudOrgFlag != "" {
		return cloudOrgFlag, nil
	}
	if orgName != "" {
		return orgName, nil
	}
	if name, err := auth.CurrentOrgName(); err == nil && name != "" {
		return name, nil
	}
	return "", fmt.Errorf("no org specified; use --org or run 'dagger login <org>'")
}
