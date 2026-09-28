package daggercmd

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	cloudapi "github.com/dagger/dagger/internal/cloud"
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
Cloud. It does not ask Dagger Cloud if the credential is valid, but it can
refresh an expired login token.

The exit code is 0 when the CLI sends traces, and 1 when it does not.`,
	Args: cobra.NoArgs,
	RunE: runCloudTracesStatus,
}

func init() {
	cloudTracesCmd.AddCommand(cloudTracesStatusCmd, newCloudTracesListCmd(), cloudTracesViewCmd)
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
	case errors.Is(status.Err, enginetel.ErrInvalidCloudURL):
		reason = status.Err.Error()
		fix = "unset DAGGER_CLOUD_URL, or set it to a valid URL"
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

// cloudTracesListOptions are the flags of 'dagger cloud traces list'. The
// server checks the values; the CLI only converts them.
type cloudTracesListOptions struct {
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
}

func newCloudTracesListCmd() *cobra.Command {
	o := &cloudTracesListOptions{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List Dagger Cloud traces",
		Long: `List the traces of a Dagger Cloud org, local and CI, newest first.

Use the flags to filter the traces. All filters must match.`,
		Example: `dagger cloud traces list --status failed --branch main --since 7d
dagger cloud traces list --mine --local --limit 5
dagger cloud traces list --pr 123 --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCloudTracesList(cmd, o)
		},
	}
	flags := cmd.Flags()
	flags.StringSliceVar(&o.repos, "repo", nil, "Only traces of this repository, e.g. github.com/org/repo (repeatable)")
	flags.StringVar(&o.branch, "branch", "", "Only traces of this branch")
	flags.StringVar(&o.commit, "commit", "", "Only traces of this commit (full or short SHA)")
	flags.StringVar(&o.tag, "tag", "", "Only traces of this git tag")
	flags.StringVar(&o.pr, "pr", "", "Only traces of this pull request number")
	flags.StringVar(&o.status, "status", "", "Only traces with this status: passed, failed or running")
	flags.BoolVar(&o.local, "local", false, "Only traces of local runs")
	flags.BoolVar(&o.ci, "ci", false, "Only traces of CI runs")
	flags.BoolVar(&o.mine, "mine", false, "Only traces that you sent")
	flags.StringVar(&o.user, "user", "", "Only traces that this user sent (user ID or email)")
	flags.StringVar(&o.token, "token", "", "Only traces that this token sent (token ID or name)")
	flags.StringVar(&o.author, "author", "", "Only traces whose commit author or committer matches this text")
	flags.StringVar(&o.command, "command", "", "Only traces whose command matches this glob, e.g. 'dagger check*'")
	flags.StringVar(&o.provider, "provider", "", "Only traces of this CI provider, e.g. github")
	flags.StringVar(&o.since, "since", "", "Only traces that started after this time: a duration (30m, 2d, 1w) or a date (2006-01-02, RFC 3339)")
	flags.StringVar(&o.until, "until", "", "Only traces that started before this time: a duration or a date, as for --since")
	flags.DurationVar(&o.minDuration, "min-duration", 0, "Only traces that ran for at least this time, e.g. 5m")
	flags.DurationVar(&o.maxDuration, "max-duration", 0, "Only traces that ran for at most this time")
	flags.IntVarP(&o.limit, "limit", "L", 20, "Maximum number of traces to list (at most 1000)")
	flags.StringVar(&o.sort, "sort", "start", "Sort order: start (newest first) or duration (longest first)")
	flags.BoolVar(&cloudJSON, "json", false, "Print JSON output")
	cmd.MarkFlagsMutuallyExclusive("local", "ci")
	cmd.MarkFlagsMutuallyExclusive("mine", "user", "token")
	return cmd
}

func runCloudTracesList(cmd *cobra.Command, o *cloudTracesListOptions) error {
	ctx := cmd.Context()
	now := time.Now()
	filter, err := o.filter(now)
	if err != nil {
		return err
	}
	sort, ok := map[string]string{
		"start":    cloudapi.TraceListSortStart,
		"duration": cloudapi.TraceListSortDuration,
	}[o.sort]
	if !ok {
		return fmt.Errorf("--sort must be start or duration, not %q", o.sort)
	}

	client, cloudAuth, err := cloudCLI.cloudClient(ctx)
	if err != nil {
		return err
	}
	org, err := cloudCLI.resolveCloudOrg(ctx, client, cloudAuth)
	if err != nil {
		return err
	}
	traces, err := client.TraceList(ctx, org.Name, filter, sort, o.limit)
	if err != nil {
		return err
	}

	rows := make([]traceRow, len(traces))
	for i := range traces {
		rows[i] = newTraceRow(org.Name, &traces[i], now)
	}
	if cloudJSON {
		return writeCloudJSON(cmd, rows)
	}
	if len(rows) == 0 {
		_, err := fmt.Fprintln(cmd.ErrOrStderr(), "No traces found.")
		return err
	}
	return writeTracesTable(cmd.OutOrStdout(), rows)
}

// filter turns the list flags into a server-side filter.
func (o *cloudTracesListOptions) filter(now time.Time) (cloudapi.TraceListFilter, error) {
	f := cloudapi.TraceListFilter{
		Repos:       o.repos,
		Branch:      o.branch,
		Commit:      o.commit,
		Tag:         o.tag,
		Change:      o.pr,
		Mine:        o.mine,
		User:        o.user,
		Token:       o.token,
		Author:      o.author,
		Name:        o.command,
		Provider:    o.provider,
		MinDuration: o.minDuration.Seconds(),
		MaxDuration: o.maxDuration.Seconds(),
	}
	switch o.status {
	case "":
	case cloudapi.TraceStatePassed, cloudapi.TraceStateFailed, cloudapi.TraceStateRunning:
		f.Status = strings.ToUpper(o.status)
	default:
		return f, fmt.Errorf("--status must be passed, failed or running, not %q", o.status)
	}
	if o.local || o.ci {
		f.Local = &o.local
	}
	var err error
	if f.Since, err = parseTimeFlag("since", o.since, now); err != nil {
		return f, err
	}
	if f.Until, err = parseTimeFlag("until", o.until, now); err != nil {
		return f, err
	}
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

// traceRow is one trace as 'dagger cloud traces list' prints it.
type traceRow struct {
	ID        string     `json:"id"`
	URL       string     `json:"url"`
	Command   string     `json:"command"`
	Status    string     `json:"status"`
	Message   string     `json:"message"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt"`
	Duration  float64    `json:"duration"` // seconds
	Local     bool       `json:"local"`
	Sender    string     `json:"sender"`
	Repo      string     `json:"repo"`
	Branch    string     `json:"branch"`
	Commit    string     `json:"commit"`
	Tag       string     `json:"tag"`
	PR        string     `json:"pr"`
	Title     string     `json:"title"`
	Author    string     `json:"author"`
	Provider  string     `json:"provider"`
}

// newTraceRow flattens a trace. The CI change, when there is one, gives the
// branch, commit and title, as it does for the server's filters.
func newTraceRow(orgName string, t *cloudapi.TraceSummary, now time.Time) traceRow {
	r := traceRow{
		ID:        t.ID,
		URL:       cloudTraceURL(orgName, t.ID),
		Command:   t.Name,
		Status:    t.State(),
		StartedAt: t.Timestamp,
		EndedAt:   t.EndTime,
		Duration:  t.Duration(now).Seconds(),
		Local:     t.Local,
	}
	if t.Status != nil {
		r.Message = t.Status.Message
	}
	if t.Sender != nil {
		r.Sender = t.Sender.Name
	}
	if g := t.Git; g != nil {
		r.Repo, r.Commit, r.Title = g.Remote, g.Ref, g.Title
		r.Branch, r.Tag = stringValue(g.Branch), stringValue(g.Tag)
		if a := g.Author; a != nil {
			r.Author = a.Name
			if a.Email != "" {
				r.Author += " <" + a.Email + ">"
			}
		}
	}
	if ci := t.CI; ci != nil {
		r.Provider = stringValue(ci.Provider)
		r.Repo = cmp.Or(r.Repo, stringValue(ci.Repository))
		if c := ci.Change; c != nil {
			r.PR = c.ID
			r.Branch = cmp.Or(c.Branch, r.Branch)
			r.Commit = cmp.Or(c.HeadSHA, r.Commit)
			r.Title = cmp.Or(c.Title, r.Title)
		}
	}
	return r
}

func writeTracesTable(w io.Writer, rows []traceRow) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tDURATION\tCOMMAND\tBRANCH/PR\tSENDER\tSTARTED")
	for _, r := range rows {
		ref := shortCloudSHA(r.Commit)
		switch {
		case r.PR != "":
			ref = "#" + r.PR
		case r.Branch != "":
			ref = r.Branch
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.ID,
			r.Status,
			dagui.FormatDuration(time.Duration(r.Duration*float64(time.Second))),
			truncateCell(r.Command, 50),
			dash(ref),
			dash(r.Sender),
			relativeTime(r.StartedAt),
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
