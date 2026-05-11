package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
)

var (
	moduleChecksOrgFlag   string
	moduleChecksJSONFlag  bool
	moduleChecksWatchFlag bool
)

const moduleChecksWatchInterval = 5 * time.Second

var moduleChecksCmd = &cobra.Command{
	Use:    "module-checks <module-ref>@<version>",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	Annotations: map[string]string{
		"experimental": "true",
	},
	Short:   "View the checks running on Dagger Cloud for a module ref + version.",
	GroupID: cloudGroup.ID,
	Example: `dagger module-checks github.com/dagger/dagger@c49835decf45479a5e83e3e1372442b2ddceeef2`,
	RunE:    ModuleChecks,
}

func init() {
	moduleChecksCmd.Flags().StringVar(&moduleChecksOrgFlag, "org", "",
		"Dagger Cloud org name (defaults to current org)")
	moduleChecksCmd.Flags().BoolVar(&moduleChecksJSONFlag, "json", false,
		"Emit the matched commit and its checks as JSON (agent-friendly)")
	moduleChecksCmd.Flags().BoolVar(&moduleChecksWatchFlag, "watch", false,
		"After the initial fetch, refetch and redraw every 5 seconds until interrupted")
}

func ModuleChecks(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	cloudAuth, err := auth.GetCloudAuth(ctx)
	if err != nil {
		return fmt.Errorf("cloud auth: %w", err)
	}
	if cloudAuth == nil || cloudAuth.Token == nil {
		return fmt.Errorf("not authenticated; run 'dagger login' or set DAGGER_CLOUD_TOKEN")
	}

	orgName, err := resolveOrgName(cloudAuth)
	if err != nil {
		return err
	}

	client, err := cloud.NewClient(ctx, cloudAuth)
	if err != nil {
		return fmt.Errorf("cloud client: %w", err)
	}

	moduleRef, commitSHA, err := splitModuleRefVersion(args[0])
	if err != nil {
		return err
	}

	var matched *cloud.CheckCommit
	if moduleChecksWatchFlag {
		matched, err = pollForMatch(ctx, client, cmd.ErrOrStderr(), orgName, moduleRef, commitSHA)
	} else {
		matched, err = findMatch(ctx, client, orgName, moduleRef, commitSHA)
		if err == nil && matched == nil {
			err = fmt.Errorf("no checks found in %s for commit %s", moduleRef, shortSHA(commitSHA))
		}
	}
	if err != nil {
		return err
	}

	if moduleChecksJSONFlag {
		if err := renderModuleChecksJSON(cmd.OutOrStdout(), orgName, moduleRef, matched.CommitSHA, matched); err != nil {
			return err
		}
	} else {
		renderModuleChecks(cmd.OutOrStdout(), orgName, moduleRef, matched.CommitSHA, []cloud.CheckCommit{*matched})
	}

	if !moduleChecksWatchFlag {
		return nil
	}
	if allChecksTerminal(matched.Checks) {
		return nil
	}

	// The cloud stores its own (moduleRef, moduleVersion) on each check — for
	// PRs that pair points at the integration commit, not the user-supplied
	// SHA. Pull it off the matched commit so the watch loop's targeted
	// ModuleChecks call is keyed correctly.
	checksRef, checksVer := moduleRef, commitSHA
	for _, c := range matched.Checks {
		if c.ModuleRef != "" && c.ModuleVersion != "" {
			checksRef, checksVer = c.ModuleRef, c.ModuleVersion
			break
		}
	}
	return watchModuleChecks(ctx, client, cmd.OutOrStdout(), cmd.ErrOrStderr(), orgName, moduleRef, checksRef, checksVer, matched.CommitSHA)
}

// findMatch makes a single OrgChecks call and returns the CheckCommit whose
// CommitSHA matches the user-supplied SHA, or (nil, nil) if no match.
func findMatch(ctx context.Context, client *cloud.Client, orgName, moduleRef, commitSHA string) (*cloud.CheckCommit, error) {
	candidates, err := client.OrgChecks(ctx, orgName, []string{moduleRef}, 50)
	if err != nil {
		return nil, fmt.Errorf("fetch recent checks for %s: %w", moduleRef, err)
	}
	return matchCommitBySHA(candidates, commitSHA), nil
}

// pollForMatch repeatedly calls findMatch every moduleChecksWatchInterval
// until a match appears or ctx is cancelled (Ctrl+C). Transient fetch errors
// are logged to errW and don't terminate the loop — so the command can be
// invoked before the cloud has recorded the checks.
func pollForMatch(ctx context.Context, client *cloud.Client, errW io.Writer, orgName, moduleRef, commitSHA string) (*cloud.CheckCommit, error) {
	announced := false
	for {
		matched, err := findMatch(ctx, client, orgName, moduleRef, commitSHA)
		if err == nil && matched != nil {
			return matched, nil
		}
		if err != nil && ctx.Err() == nil {
			fmt.Fprintf(errW, "resolve: %v\n", err)
		}
		if !announced {
			fmt.Fprintf(errW, "no checks yet for %s@%s; waiting...\n", moduleRef, shortSHA(commitSHA))
			announced = true
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(moduleChecksWatchInterval):
		}
	}
}

// watchModuleChecks refetches and redraws the matched commit every
// moduleChecksWatchInterval, until ctx is cancelled (e.g. Ctrl+C). Uses the
// targeted ModuleChecks endpoint (keyed on the resolved checksRef/checksVer)
// so we don't repeatedly scan the org-wide recent-checks window. displayRef
// is the user-facing ref passed to the renderer; commitSHA is used to
// disambiguate if ModuleChecks happens to return multiple commits.
// Transient fetch errors are logged to errW and don't terminate the loop.
func watchModuleChecks(ctx context.Context, client *cloud.Client, outW, errW io.Writer, orgName, displayRef, checksRef, checksVer, commitSHA string) error {
	ticker := time.NewTicker(moduleChecksWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		commits, err := client.ModuleChecks(ctx, orgName, checksRef, checksVer)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			fmt.Fprintf(errW, "watch: refetch failed: %v\n", err)
			continue
		}
		matched := matchCommitBySHA(commits, commitSHA)
		if matched == nil {
			if len(commits) == 0 {
				fmt.Fprintf(errW, "watch: no checks returned for %s@%s\n", checksRef, shortSHA(checksVer))
				continue
			}
			// moduleChecks should only return commits for the requested
			// (ref, version) pair; if it returns something but the SHA
			// doesn't line up, fall back to the first entry.
			matched = &commits[0]
		}
		if moduleChecksJSONFlag {
			_ = renderModuleChecksJSON(outW, orgName, displayRef, matched.CommitSHA, matched)
		} else {
			fmt.Fprint(outW, "\x1b[H\x1b[2J")
			renderModuleChecks(outW, orgName, displayRef, matched.CommitSHA, []cloud.CheckCommit{*matched})
		}
		if allChecksTerminal(matched.Checks) {
			return nil
		}
	}
}

// allChecksTerminal reports whether every (deduped) visible check has reached
// a terminal state. Mirrors the renderer's internal-check policy: public
// checks count when any exist, otherwise the internal ones do. Used by
// --watch to know when there's nothing left to poll for.
func allChecksTerminal(checks []cloud.Check) bool {
	var public, internal []cloud.Check
	for _, c := range checks {
		if c.Internal {
			internal = append(internal, c)
		} else {
			public = append(public, c)
		}
	}
	visible := public
	if len(visible) == 0 {
		visible = internal
	}
	visible = latestChecksByName(visible)
	if len(visible) == 0 {
		return false
	}
	for _, c := range visible {
		if statusBucketFor(c.Status).isPending() {
			return false
		}
	}
	return true
}

// splitModuleRefVersion splits "<ref>@<version>" on the last '@', so that
// refs containing '@' (e.g. `git@github.com:org/repo`) are handled correctly.
func splitModuleRefVersion(arg string) (string, string, error) {
	idx := strings.LastIndex(arg, "@")
	if idx <= 0 || idx == len(arg)-1 {
		return "", "", fmt.Errorf("expected <module-ref>@<version>, got %q", arg)
	}
	return arg[:idx], arg[idx+1:], nil
}

// matchCommitBySHA finds the commit in commits whose CommitSHA matches sha.
// Allows prefix matches in either direction, so a short SHA on the input or
// the server side still resolves. Returns nil on no match.
func matchCommitBySHA(commits []cloud.CheckCommit, sha string) *cloud.CheckCommit {
	sha = strings.ToLower(strings.TrimSpace(sha))
	if sha == "" {
		return nil
	}
	for i := range commits {
		got := strings.ToLower(commits[i].CommitSHA)
		if got == sha || strings.HasPrefix(got, sha) || strings.HasPrefix(sha, got) {
			return &commits[i]
		}
	}
	return nil
}

// JSON output types. These describe a stable contract for agent consumers
// independent of the cloud GraphQL schema.

type checksJSON struct {
	Org           string      `json:"org"`
	ModuleRef     string      `json:"moduleRef"`
	ModuleVersion string      `json:"moduleVersion"`
	ChecksURL     string      `json:"checksUrl"`
	Commit        jsonCommit  `json:"commit"`
	Refs          []jsonRef   `json:"refs"`
	Summary       jsonSummary `json:"summary"`
	Checks        []jsonCheck `json:"checks"`
}

type jsonCommit struct {
	SHA         string     `json:"sha"`
	Message     string     `json:"message"`
	AuthorName  string     `json:"authorName"`
	AuthorEmail string     `json:"authorEmail"`
	Repo        string     `json:"repo"`
	Timestamp   *time.Time `json:"timestamp,omitempty"`
}

type jsonRef struct {
	Type              string `json:"type"`
	Name              string `json:"name,omitempty"`
	URL               string `json:"url,omitempty"`
	Number            int    `json:"number,omitempty"`
	Title             string `json:"title,omitempty"`
	State             string `json:"state,omitempty"`
	IntegrationCommit string `json:"integrationCommit,omitempty"`
}

type jsonSummary struct {
	Total      int `json:"total"`
	Failure    int `json:"failure"`
	Cancelled  int `json:"cancelled"`
	InProgress int `json:"inProgress"`
	Success    int `json:"success"`
}

type jsonCheck struct {
	Name            string     `json:"name"`
	Status          string     `json:"status"`
	StatusBucket    string     `json:"statusBucket"`
	DurationSeconds int        `json:"durationSeconds"`
	TraceID         string     `json:"traceId"`
	SpanID          string     `json:"spanId"`
	TraceURL        string     `json:"traceUrl"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
	EndTime         *time.Time `json:"endTime,omitempty"`
	Internal        bool       `json:"internal,omitempty"`
}

// renderModuleChecksJSON emits the matched commit as a single JSON object.
// Mirrors the human renderer's internal-check policy: public checks are
// preferred; internal checks are only included when no public checks exist
// (each carrying internal: true so consumers can tell).
func renderModuleChecksJSON(w io.Writer, orgName, moduleRef, moduleVersion string, commit *cloud.CheckCommit) error {
	out := checksJSON{
		Org:           orgName,
		ModuleRef:     moduleRef,
		ModuleVersion: moduleVersion,
		ChecksURL:     checksURL(orgName, moduleRef, mergedCommitSHA(*commit)),
		Commit: jsonCommit{
			SHA:         commit.CommitSHA,
			Message:     commit.CommitMessage,
			AuthorName:  commit.AuthorName,
			AuthorEmail: commit.AuthorEmail,
			Repo:        commit.Repo,
			Timestamp:   timePtr(commit.Timestamp),
		},
		Refs:   make([]jsonRef, 0, len(commit.Refs)),
		Checks: make([]jsonCheck, 0, len(commit.Checks)),
	}

	for _, r := range commit.Refs {
		ref := jsonRef{Name: r.Name, URL: r.URL}
		switch r.Typename {
		case "CheckCommitBranchRef":
			ref.Type = "branch"
		case "CheckCommitTagRef":
			ref.Type = "tag"
		case "CheckCommitPullRequestRef":
			ref.Type = "pr"
			ref.Number = r.Number
			ref.Title = r.Title
			ref.State = r.State
			ref.IntegrationCommit = r.IntegrationCommit
			// PR refs don't have a `name` field; clear the zero value.
			ref.Name = ""
		default:
			ref.Type = r.Typename
		}
		out.Refs = append(out.Refs, ref)
	}

	// Public checks are preferred. Fall back to internal only if no public
	// check exists for this commit. Either way, dedupe by name.
	var public, internal []cloud.Check
	for _, c := range commit.Checks {
		if c.Internal {
			internal = append(internal, c)
		} else {
			public = append(public, c)
		}
	}
	raw := public
	if len(raw) == 0 {
		raw = internal
	}
	for _, c := range latestChecksByName(raw) {
		b := statusBucketFor(c.Status)
		duration := 0
		if c.Duration != nil {
			duration = *c.Duration
		} else if d := c.DurationAsTime(); d > 0 {
			duration = int(d / time.Second)
		}
		out.Checks = append(out.Checks, jsonCheck{
			Name:            c.Name,
			Status:          c.Status,
			StatusBucket:    b.jsonName(),
			DurationSeconds: duration,
			TraceID:         c.TraceID,
			SpanID:          c.SpanID,
			TraceURL:        traceURL(orgName, c.TraceID),
			StartedAt:       c.StartedAt,
			EndTime:         c.EndTime,
			Internal:        c.Internal,
		})
		switch {
		case b == bucketFailure:
			out.Summary.Failure++
		case b == bucketCancelled:
			out.Summary.Cancelled++
		case b.isPending(): // queued + running both count toward inProgress
			out.Summary.InProgress++
		case b == bucketSuccess:
			out.Summary.Success++
		}
	}
	out.Summary.Total = len(out.Checks)

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// latestChecksByName keeps a single entry per Check.Name — the one with the
// most recent StartedAt — so re-runs (e.g. auto-retried failures) don't show
// up as duplicate rows. Display order matches the first occurrence of each
// name in the input.
func latestChecksByName(checks []cloud.Check) []cloud.Check {
	byName := make(map[string]cloud.Check, len(checks))
	order := make([]string, 0, len(checks))
	for _, c := range checks {
		existing, ok := byName[c.Name]
		if !ok {
			order = append(order, c.Name)
			byName[c.Name] = c
			continue
		}
		if checkStartTime(c).After(checkStartTime(existing)) {
			byName[c.Name] = c
		}
	}
	out := make([]cloud.Check, 0, len(order))
	for _, name := range order {
		out = append(out, byName[name])
	}
	return out
}

func checkStartTime(c cloud.Check) time.Time {
	if c.StartedAt == nil {
		return time.Time{}
	}
	return *c.StartedAt
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func resolveOrgName(cloudAuth *auth.Cloud) (string, error) {
	if moduleChecksOrgFlag != "" {
		return moduleChecksOrgFlag, nil
	}
	if cloudAuth.Org != nil && cloudAuth.Org.Name != "" {
		return cloudAuth.Org.Name, nil
	}
	// DAGGER_CLOUD_TOKEN doesn't populate cloudAuth.Org; the org name is
	// embedded in the token itself.
	if name, err := auth.CurrentOrgName(); err == nil && name != "" {
		return name, nil
	}
	return "", fmt.Errorf("no org specified; use --org or run 'dagger login' to set a default org")
}

// statusBucket categorises a Check.Status string into one of four display
// buckets. Falls back to the in-progress bucket for unknown values, since a
// missing status is most likely a not-yet-complete run.
type statusBucket int

const (
	bucketFailure statusBucket = iota
	bucketCancelled
	bucketQueued
	bucketRunning
	bucketSuccess
)

// isPending reports whether the bucket represents a non-terminal state
// (queued or running). Used by --watch and the bar/summary, which collapse
// the two into a single "in progress" segment.
func (b statusBucket) isPending() bool {
	return b == bucketQueued || b == bucketRunning
}

func statusBucketFor(status string) statusBucket {
	switch strings.ToLower(status) {
	case "success", "succeeded", "passed", "ok":
		return bucketSuccess
	case "failure", "failed", "error", "errored":
		return bucketFailure
	case "cancelled", "canceled":
		return bucketCancelled
	case "queued", "pending":
		return bucketQueued
	default:
		// Unknown / "running" / anything still active.
		return bucketRunning
	}
}

func (b statusBucket) color() termenv.Color {
	switch b {
	case bucketFailure:
		return termenv.ANSIRed
	case bucketCancelled:
		// idtui's color profile is capped at 16-color ANSI; gray reads as
		// "inactive/stopped" and stays distinct from the in-progress yellow.
		return termenv.ANSIBrightBlack
	case bucketQueued:
		// Light grey — clearly distinct from both the dark grey of
		// cancelled and the yellow of running.
		return termenv.ANSIWhite
	case bucketRunning:
		return termenv.ANSIYellow
	case bucketSuccess:
		return termenv.ANSIGreen
	}
	return termenv.ANSIWhite
}

// label is the human-readable display name. The camelCase form used in JSON
// output is jsonName().
func (b statusBucket) label() string {
	switch b {
	case bucketFailure:
		return "failure"
	case bucketCancelled:
		return "cancelled"
	case bucketQueued:
		return "queued"
	case bucketRunning:
		return "running"
	case bucketSuccess:
		return "success"
	}
	return "unknown"
}

// jsonName is the camelCase identifier used in the JSON output schema.
// Queued and running both collapse to "inProgress" — that's the umbrella
// "still active" bucket the JSON contract exposes; the raw `status` field
// preserves the fine-grained distinction for consumers that need it.
func (b statusBucket) jsonName() string {
	if b.isPending() {
		return "inProgress"
	}
	return b.label()
}

func (b statusBucket) icon() string {
	switch b {
	case bucketFailure:
		return "✗"
	case bucketCancelled:
		return "⊘"
	case bucketQueued:
		return "○"
	case bucketRunning:
		return "●"
	case bucketSuccess:
		return "✓"
	}
	return "·"
}

func renderModuleChecks(w io.Writer, orgName, moduleRef, moduleVersion string, commits []cloud.CheckCommit) {
	out := idtui.NewOutput(w)

	fmt.Fprintf(w, "%s %s@%s\n",
		out.String("Module:").Bold(),
		moduleRef, moduleVersion,
	)

	// Split internal vs public checks. Public is preferred; internal is shown
	// only as a last resort (i.e. when no public checks are recorded yet).
	var public, internal []cloud.Check
	for _, commit := range commits {
		for _, c := range commit.Checks {
			if c.Internal {
				internal = append(internal, c)
			} else {
				public = append(public, c)
			}
		}
	}

	raw := public
	showingInternal := false
	if len(raw) == 0 && len(internal) > 0 {
		raw = internal
		showingInternal = true
	}

	if len(raw) == 0 {
		fmt.Fprintf(w, "\n%s\n",
			out.String(fmt.Sprintf("No checks found for %s@%s", moduleRef, moduleVersion)).Faint())
		return
	}

	// Dedupe by name (latest StartedAt wins) for everything except the
	// duration bar; the bar wants to show every iteration.
	latest, iterations := groupChecksByName(raw)

	// Header: commit metadata for each commit returned (usually one).
	for _, commit := range commits {
		fmt.Fprintln(w)
		renderCommitHeader(w, out, orgName, moduleRef, commit)
	}

	if showingInternal {
		fmt.Fprintf(w, "\n%s\n",
			out.String(fmt.Sprintf("No public checks found; showing %d internal check(s).", len(latest))).Faint())
	}

	// Progress bar and per-bucket counts.
	fmt.Fprintln(w)
	renderProgressBar(w, out, latest)

	// Per-check table.
	fmt.Fprintln(w)
	renderCheckTableGrouped(w, out, orgName, latest, iterations)
}

// groupChecksByName collapses a check list by name. Returns:
//   - latest: one entry per name (the run with the most recent StartedAt),
//     preserving first-occurrence order from the input;
//   - iterations: every run per name, sorted by StartedAt ascending — used
//     by the multi-block duration bar to visualise re-runs.
func groupChecksByName(checks []cloud.Check) (latest []cloud.Check, iterations map[string][]cloud.Check) {
	iterations = make(map[string][]cloud.Check)
	pos := make(map[string]int)
	for _, c := range checks {
		iterations[c.Name] = append(iterations[c.Name], c)
		if i, ok := pos[c.Name]; ok {
			if checkStartTime(c).After(checkStartTime(latest[i])) {
				latest[i] = c
			}
			continue
		}
		pos[c.Name] = len(latest)
		latest = append(latest, c)
	}
	for name, runs := range iterations {
		sort.Slice(runs, func(i, j int) bool {
			return checkStartTime(runs[i]).Before(checkStartTime(runs[j]))
		})
		iterations[name] = runs
	}
	return
}

func renderCommitHeader(w io.Writer, out *termenv.Output, orgName, moduleRef string, commit cloud.CheckCommit) {
	sha := shortSHA(commit.CommitSHA)
	firstLine := commit.CommitMessage
	if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
		firstLine = firstLine[:idx]
	}

	author := commit.AuthorName
	if author != "" && commit.AuthorEmail != "" {
		author = fmt.Sprintf("%s <%s>", commit.AuthorName, commit.AuthorEmail)
	} else if commit.AuthorEmail != "" {
		author = commit.AuthorEmail
	}

	fmt.Fprintf(w, "%s  %s", out.String("Commit:").Bold(), out.String(sha).Foreground(termenv.ANSICyan))
	if firstLine != "" {
		fmt.Fprintf(w, " %s %s", out.String("—").Faint(), firstLine)
	}
	fmt.Fprintln(w)
	if author != "" {
		fmt.Fprintf(w, "%s  %s\n", out.String("Author:").Bold(), author)
	}
	if commit.Repo != "" {
		fmt.Fprintf(w, "%s    %s\n", out.String("Repo:").Bold(), commit.Repo)
	}
	fmt.Fprintf(w, "%s  %s\n", out.String("Checks:").Bold(), checksURL(orgName, moduleRef, mergedCommitSHA(commit)))
}

func checksURL(orgName, moduleRef, commitSHA string) string {
	return fmt.Sprintf("https://dagger.cloud/%s/checks/%s@%s", orgName, moduleRef, commitSHA)
}

// mergedCommitSHA returns the SHA the cloud uses to key this commit's checks
// — i.e. the integration/merge commit for PRs, or the pushed commit SHA for
// plain pushes. Read off any check's moduleVersion; falls back to CommitSHA
// if no checks are recorded yet.
func mergedCommitSHA(commit cloud.CheckCommit) string {
	for _, c := range commit.Checks {
		if c.ModuleVersion != "" {
			return c.ModuleVersion
		}
	}
	return commit.CommitSHA
}

// bucketOrder is the left-to-right display order for the per-bucket check
// list. Queued is listed separately from running so the user can see what's
// waiting vs. actively executing.
var bucketOrder = []statusBucket{bucketFailure, bucketCancelled, bucketQueued, bucketRunning, bucketSuccess}

// bucketBarOrder is the simpler order used for the top progress bar and
// summary line, which collapse queued + running into a single "in progress"
// segment (counted under bucketRunning).
var bucketBarOrder = []statusBucket{bucketFailure, bucketCancelled, bucketRunning, bucketSuccess}

func renderProgressBar(w io.Writer, out *termenv.Output, checks []cloud.Check) {
	counts := make(map[statusBucket]int, len(bucketBarOrder))
	for _, c := range checks {
		b := statusBucketFor(c.Status)
		if b == bucketQueued {
			b = bucketRunning // bar treats queued and running as one segment
		}
		counts[b]++
	}

	var bar strings.Builder
	var summary []string
	for _, b := range bucketBarOrder {
		n := counts[b]
		if n == 0 {
			continue
		}
		bar.WriteString(out.String(strings.Repeat("█", n)).Foreground(b.color()).String())
		summary = append(summary, fmt.Sprintf("%s %d",
			out.String(b.icon()).Foreground(b.color()).String(),
			n,
		))
	}
	fmt.Fprintln(w, bar.String())
	fmt.Fprintln(w, strings.Join(summary, "  "))
}

// renderCheckTableGrouped renders the checks grouped by status bucket, with a
// colored heading per group. Optimized for humans reading the output.
// durationBarWidth caps the duration bar at this many "█" cells; the slowest
// check across the whole table gets the full width, everything else scales
// linearly.
const durationBarWidth = 10

func renderCheckTableGrouped(w io.Writer, out *termenv.Output, orgName string, checks []cloud.Check, iterations map[string][]cloud.Check) {
	grouped := make(map[statusBucket][]cloud.Check, 4)
	var maxDuration time.Duration
	for _, c := range checks {
		grouped[statusBucketFor(c.Status)] = append(grouped[statusBucketFor(c.Status)], c)
		// Scale the bar against the slowest single iteration across the
		// table, so retried checks render as a series of comparable blocks.
		for _, iter := range iterations[c.Name] {
			if d, _ := effectiveDuration(iter); d > maxDuration {
				maxDuration = d
			}
		}
	}

	// One tabwriter spans the whole table so columns align across groups.
	// Group headers go through it too — as a single-column row with empty
	// trailing cells — which the tabwriter pads invisibly.
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	first := true
	for _, b := range bucketOrder {
		group := grouped[b]
		if len(group) == 0 {
			continue
		}
		if !first {
			// Empty row with the same cell shape as other rows; a bare
			// "\n" would have fewer cells and terminate the alignment
			// block, defeating cross-group column alignment.
			fmt.Fprint(tw, "\t\t\t\n")
		}
		first = false
		fmt.Fprintf(tw, "%s %s (%d)\t\t\t\n",
			out.String(b.icon()).Foreground(b.color()).String(),
			out.String(strings.ToUpper(b.label())).Foreground(b.color()).Bold().String(),
			len(group),
		)

		sort.Slice(group, func(i, j int) bool {
			return group[i].Name < group[j].Name
		})

		for _, c := range group {
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n",
				c.Name,
				formatCheckTime(c),
				durationBars(out, iterations[c.Name], maxDuration, durationBarWidth),
				traceURL(orgName, c.TraceID),
			)
		}
	}
	_ = tw.Flush()
}

// effectiveDuration returns the check's elapsed time and whether the check
// is still running (in which case the value is "time since started" rather
// than total duration).
func effectiveDuration(c cloud.Check) (time.Duration, bool) {
	if d := c.DurationAsTime(); d > 0 {
		return d, false
	}
	if c.StartedAt != nil {
		return time.Since(*c.StartedAt), true
	}
	return 0, false
}

// formatCheckTime renders the duration column for a check. Finished checks
// show their total elapsed time; still-running checks show "<elapsed> ago"
// computed from StartedAt so the user can see how long they've been going.
func formatCheckTime(c cloud.Check) string {
	d, running := effectiveDuration(c)
	if d == 0 {
		return "—"
	}
	if running {
		return formatDuration(d) + " ago"
	}
	return formatDuration(d)
}

// partialBlocks maps an "eighths" value 0..7 to the corresponding left-aligned
// partial-block glyph from the Unicode Block Elements range (U+258F..U+2589),
// used to render the last cell of a duration bar at sub-cell precision.
// Index 0 means "no partial cell" (the bar ends on a full-block boundary).
var partialBlocks = [8]string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}

// durationBars renders one block per iteration of a check, separated by
// spaces. Each block is sized linearly against max and colored by the
// iteration's own status bucket. The last cell of each block uses a partial
// "Block Elements" glyph for sub-cell precision (eighths), so a 6.5-cell
// duration draws as "██████▌" rather than "██████" or "███████".
func durationBars(out *termenv.Output, iterations []cloud.Check, max time.Duration, width int) string {
	if max <= 0 || len(iterations) == 0 {
		return ""
	}
	parts := make([]string, 0, len(iterations))
	for _, iter := range iterations {
		d, _ := effectiveDuration(iter)
		if d <= 0 {
			continue
		}
		// Eighths of a cell — gives 8× finer resolution than whole cells.
		eighths := int(float64(d) / float64(max) * float64(width) * 8)
		if eighths < 1 {
			eighths = 1
		}
		if eighths > width*8 {
			eighths = width * 8
		}
		full := eighths / 8
		bar := strings.Repeat("█", full) + partialBlocks[eighths%8]
		b := statusBucketFor(iter.Status)
		parts = append(parts, out.String(bar).Foreground(b.color()).String())
	}
	return strings.Join(parts, " ")
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		m := int(d / time.Minute)
		s := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		h := int(d / time.Hour)
		m := int((d % time.Hour) / time.Minute)
		return fmt.Sprintf("%dh %dm", h, m)
	}
}

func traceURL(orgName, traceID string) string {
	if traceID == "" {
		return "—"
	}
	return fmt.Sprintf("https://dagger.cloud/%s/traces/%s", orgName, traceID)
}
