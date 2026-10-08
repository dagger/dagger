package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/pkg/browser"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/core/gitref"
)

// cloudCheckWatchInterval is the default polling interval used by
// `dagger cloud checks list --watch`.
const cloudCheckWatchInterval = 5 * time.Second

var cloudCheckCmd = &cobra.Command{
	Use:     "checks",
	Aliases: []string{"check"},
	Short:   "Manage Cloud-side automated checks for this workspace",
	Args:    cobra.NoArgs,
	Annotations: map[string]string{
		hiddenAliasesAnnotation: "check",
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var cloudCheckOnCmd = &cobra.Command{
	Use:   "on [name]",
	Short: "Enable a Cloud-side check (by name; defaults to the workspace remote's default check)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runCloudCheckSet(true),
}

var cloudCheckOffCmd = &cobra.Command{
	Use:   "off [name]",
	Short: "Disable a Cloud-side check (by name; defaults to the workspace remote's default check)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runCloudCheckSet(false),
}

var (
	cloudCheckListFailed   bool
	cloudCheckListWatch    bool
	cloudCheckListFailFast bool
	cloudCheckListInterval time.Duration
)

var cloudCheckListCmd = &cobra.Command{
	Use:   "list [version]",
	Short: "List Cloud-side checks for this workspace",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runCloudCheckList,
}

var cloudCheckStatusCmd = &cobra.Command{
	Use:   "status [name]",
	Short: "Show the status of a Cloud-side check (by name; defaults to the workspace remote's default check)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runCloudCheckStatus,
}

func init() {
	cloudCheckListCmd.Flags().BoolVar(&cloudCheckListFailed, "failed", false, "Only list failed checks")
	cloudCheckListCmd.Flags().BoolVar(&cloudCheckListWatch, "watch", false, "Wait until all checks have finished; exit non-zero if any check did not succeed")
	cloudCheckListCmd.Flags().BoolVar(&cloudCheckListFailFast, "fail-fast", false, "When watching, stop as soon as a check is not successful")
	cloudCheckListCmd.Flags().DurationVar(&cloudCheckListInterval, "interval", cloudCheckWatchInterval, "Polling interval used while watching")
	cloudCheckCmd.AddCommand(cloudCheckOnCmd, cloudCheckOffCmd, cloudCheckListCmd, cloudCheckStatusCmd)
	cloudCmd.AddCommand(cloudCheckCmd)
}

// runCloudCheckSet returns a RunE that sets the workspace autocheck flag for
// the selected remote. The optional name arg is accepted but not used —
// today's underlying API only models a single autocheck per remote.
func runCloudCheckSet(enabled bool) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		var remote workspaceRemoteAddress
		if len(args) > 0 {
			remote.CloneRef = args[0]
		} else {
			var err error
			remote, _, err = selectedRemoteWorkspaceAddress(cmd.Context(), "cloud checks")
			if err != nil {
				return err
			}
		}
		if enabled {
			if err := prepareCloudChecksAccount(cmd, args); err != nil {
				return err
			}
		}
		state, err := setWorkspaceAutocheckState(cmd.Context(), remote, enabled)
		if enabled && errors.Is(err, errCloudSourceNotConfigured) {
			if setupErr := prepareCloudChecksIntegration(cmd, args); setupErr != nil {
				return setupErr
			}
			// Re-read Cloud state after the user completes the browser step.
			state, err = setWorkspaceAutocheckState(cmd.Context(), remote, enabled)
			if errors.Is(err, errCloudSourceNotConfigured) {
				return cloudChecksPrerequisiteError(cmd, args, "GitHub access is not configured for this repository", "dagger cloud integration create github")
			}
		}
		if errors.Is(err, errCloudNotAuthenticated) {
			return fmt.Errorf("not authenticated; run 'dagger cloud login' to update Cloud checks")
		}
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), workspaceAutocheckStateString(state))
		return err
	}
}

func canPromptForCloudChecks() bool {
	return canOpenShellOnError(progress, stdinIsTTY) && !cloudJSON && !autoApply
}

func prepareCloudChecksAccount(cmd *cobra.Command, args []string) error {
	client, cloudAuth, err := cloudCLI.cloudClientWithLogin(cmd.Context(), false)
	if err == nil {
		// Engine and OIDC tokens already identify their Cloud account. The
		// User query is an OAuth-only account/organization check.
		if tokenType := cloudAuth.Token.Type(); tokenType == "Basic" || tokenType == "OIDC" {
			return nil
		}
		user, userErr := client.User(cmd.Context())
		if userErr != nil {
			return userErr
		}
		if len(user.Orgs) > 0 {
			return nil
		}
	} else if !errors.Is(err, errCloudNotAuthenticated) {
		return err
	}
	required := cloudChecksPrerequisiteError(cmd, args, "Cloud checks need a Dagger Cloud account and organization", "dagger cloud signup")
	if !canPromptForCloudChecks() || !confirmSetupCommand(cmd, "Cloud checks need a Dagger Cloud account. Sign up or log in?", cloudSetupCommand("signup")) {
		return required
	}
	signup := newSignupCmd()
	signup.SetContext(cmd.Context())
	signup.SetIn(cmd.InOrStdin())
	signup.SetOut(cmd.OutOrStdout())
	signup.SetErr(cmd.ErrOrStderr())
	if err := cloudCLI.Signup(signup, nil); err != nil {
		return fmt.Errorf("%w\n\n%w", err, required)
	}
	return nil
}

func prepareCloudChecksIntegration(cmd *cobra.Command, args []string) error {
	required := cloudChecksPrerequisiteError(cmd, args, "Cloud checks need GitHub access to this repository", "dagger cloud integration create github")
	if !canPromptForCloudChecks() || !confirmSetupCommand(cmd, "Connect this repository to Dagger Cloud in the browser?", cloudSetupCommand("integration create github --open")) {
		return required
	}
	client, _, err := cloudCLI.cloudClientWithLogin(cmd.Context(), false)
	if err != nil {
		return err
	}
	setup, err := cloudCLI.githubConnectHandoff(cmd.Context(), client)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Manual step: grant Dagger access to this repository, then return here:\n%s\n", setup.URL)
	if err := browser.OpenURL(setup.URL); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "Could not open a browser. Open the URL above.")
	}
	if !confirm(cmd, "Have you completed the manual GitHub step?") {
		return required
	}
	return nil
}

func cloudSetupCommand(args string) string {
	command := "dagger cloud " + args
	if cloudOrgFlag != "" {
		command += " --org " + shellQuote(cloudOrgFlag)
	}
	return command
}

// These prompts run outside the TUI, so the command stays in terminal output
// after the response. Browser-only work is described as a manual step.
func confirmSetupCommand(cmd *cobra.Command, title, command string) bool {
	fmt.Fprint(cmd.OutOrStdout(), setupCommandPromptText(title, command))
	return confirm(cmd, "Run this command?")
}

func setupCommandPromptText(title, command string) string {
	return title + "\n\n" + command + "\n\n"
}

func cloudChecksPrerequisiteError(cmd *cobra.Command, args []string, reason, prerequisite string) error {
	prefix := "dagger"
	selected := workspaceRef
	if selected != "" && !isObviouslyRemoteWorkspaceRef(selected) {
		if abs, err := filepath.Abs(selected); err == nil {
			selected = abs
		}
	}
	if selected == "" && (cmd.Flags().Changed("workdir") || cmd.InheritedFlags().Changed("workdir")) {
		selected, _ = os.Getwd()
	}
	if selected != "" {
		prefix += " -W " + shellQuote(selected)
	}
	org := ""
	if cloudOrgFlag != "" {
		org = " --org " + shellQuote(cloudOrgFlag)
	}
	retry := prefix + " cloud checks on"
	for _, arg := range args {
		retry += " " + shellQuote(arg)
	}
	return fmt.Errorf("%s.\nComplete the prerequisite, then retry:\n\n%s%s\n%s%s", reason, prerequisite, org, retry, org)
}

func runCloudCheckStatus(cmd *cobra.Command, _ []string) error {
	remote, _, err := selectedRemoteWorkspaceAddress(cmd.Context(), "cloud checks status")
	if err != nil {
		return err
	}
	state, ok, err := loadWorkspaceAutocheckState(cmd.Context(), remote)
	if errors.Is(err, errCloudNotAuthenticated) {
		return fmt.Errorf("not authenticated; run 'dagger cloud login' to view Cloud checks")
	}
	if err != nil {
		return err
	}
	if !ok {
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "unconfigured\n")
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), workspaceAutocheckStateString(state))
	return err
}

func runCloudCheckList(cmd *cobra.Command, args []string) error {
	if cloudCheckListFailFast && !cloudCheckListWatch {
		return fmt.Errorf("--fail-fast requires --watch")
	}
	remote, address, err := selectedRemoteWorkspaceAddress(cmd.Context(), "cloud checks list")
	if err != nil {
		return err
	}
	if len(args) > 0 {
		remote.Version = args[0]
		address = gitref.RefString(remote.CloneRef, remote.Path, remote.Version)
	}
	if cloudCheckListWatch {
		return watchCloudCheckList(cmd, remote, address)
	}
	grouped, err := loadGroupedCloudCheckRows(cmd.Context(), remote)
	if err != nil {
		return err
	}
	return outputCloudCheckList(cmd, grouped, address)
}

// watchCloudCheckList polls the Cloud checks for the workspace until every
// check has finished (none pending), then renders the result. It exits with a
// non-zero status (a returned error) if any check did not succeed. With
// --fail-fast it stops and fails as soon as a non-successful check is seen,
// without waiting for the remaining checks to finish.
func watchCloudCheckList(cmd *cobra.Command, remote workspaceRemoteAddress, address string) error {
	return watchCloudCheckListLoop(cmd, address, cloudCheckListInterval, func(ctx context.Context) ([]groupedCloudListRow, error) {
		return loadGroupedCloudCheckRows(ctx, remote)
	})
}

// watchCloudCheckListLoop is the fetch-agnostic core of watchCloudCheckList,
// split out so the polling and exit behavior can be tested without a Cloud
// client.
func watchCloudCheckListLoop(cmd *cobra.Command, address string, interval time.Duration, fetch func(context.Context) ([]groupedCloudListRow, error)) error {
	ctx := cmd.Context()
	if interval <= 0 {
		interval = cloudCheckWatchInterval
	}
	var prevState map[string]string
	first := true
	for {
		grouped, err := fetch(ctx)
		if err != nil {
			return err
		}

		// Visual feedback on progress, written to stderr to keep the final
		// result table on stdout clean: the full initial state on the first
		// poll, then only state changes on subsequent polls.
		prevState = reportCloudCheckWatchProgress(cmd.ErrOrStderr(), prevState, first, grouped)
		first = false

		pending, failed := summarizeCloudCheckResults(grouped)
		if (cloudCheckListFailFast && failed) || !pending {
			if outErr := outputCloudCheckList(cmd, grouped, address); outErr != nil {
				return outErr
			}
			if failed {
				return cloudChecksFailedError(grouped)
			}
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// reportCloudCheckWatchProgress writes progress feedback and returns the new
// per-check state map. On the first poll it prints the full initial state of
// every check; on later polls it prints only the checks whose state changed
// (including checks that appear for the first time). It stays quiet when
// nothing changed.
func reportCloudCheckWatchProgress(w io.Writer, prev map[string]string, first bool, rows []groupedCloudListRow) map[string]string {
	cur := make(map[string]string, len(rows))
	for _, row := range rows {
		cur[dash(row.Values["check"])] = cloudCheckStateLabel(row)
	}

	if first {
		if len(rows) > 0 {
			noun := "check"
			if len(rows) != 1 {
				noun = "checks"
			}
			fmt.Fprintf(w, "Watching %d Cloud %s:\n", len(rows), noun)
			for _, row := range rows {
				fmt.Fprintf(w, "  %s %s: %s\n", cloudCheckStateEmoji(row), dash(row.Values["check"]), cloudCheckStateLabel(row))
			}
		}
		return cur
	}

	for _, row := range rows {
		name := dash(row.Values["check"])
		now := cur[name]
		before, existed := prev[name]
		switch {
		case !existed:
			fmt.Fprintf(w, "%s %s: %s\n", cloudCheckStateEmoji(row), name, now)
		case before != now:
			fmt.Fprintf(w, "%s %s: %s → %s\n", cloudCheckStateEmoji(row), name, before, now)
		}
	}
	return cur
}

// cloudCheckStateLabel is a human-readable state for a check, preferring the
// raw Cloud status (e.g. "queued", "running", "errored") and falling back to
// the collapsed result.
func cloudCheckStateLabel(row groupedCloudListRow) string {
	if row.Status != "" {
		return strings.ToLower(row.Status)
	}
	switch row.Result {
	case "green":
		return "success"
	case "red":
		return "errored"
	default:
		return "pending"
	}
}

// cloudCheckStateEmoji picks a status emoji for a check, distinguishing queued
// from other pending states.
func cloudCheckStateEmoji(row groupedCloudListRow) string {
	if row.Result == "pending" && strings.EqualFold(row.Status, "queued") {
		return "⏳"
	}
	return cloudResultEmoji(row.Result)
}

// summarizeCloudCheckResults reports whether any check is still pending and
// whether any check finished in a non-successful (non-green) state.
func summarizeCloudCheckResults(rows []groupedCloudListRow) (pending, failed bool) {
	for _, row := range rows {
		switch row.Result {
		case "green":
		case "pending":
			pending = true
		default:
			failed = true
		}
	}
	return pending, failed
}

// cloudChecksFailedError builds the error returned (and thus the non-zero exit)
// when one or more Cloud checks did not succeed.
func cloudChecksFailedError(rows []groupedCloudListRow) error {
	var names []string
	for _, row := range rows {
		if row.Result != "green" && row.Result != "pending" {
			names = append(names, dash(row.Values["check"]))
		}
	}
	if len(names) == 0 {
		return errors.New("one or more Cloud checks did not succeed")
	}
	return fmt.Errorf("cloud checks did not succeed: %s", strings.Join(names, ", "))
}

// loadGroupedCloudCheckRows fetches and groups (one row per check) the Cloud
// checks for the workspace.
func loadGroupedCloudCheckRows(ctx context.Context, remote workspaceRemoteAddress) ([]groupedCloudListRow, error) {
	rows, err := loadWorkspaceModuleCheckRows(ctx, remote)
	if errors.Is(err, errCloudNotAuthenticated) {
		return nil, fmt.Errorf("not authenticated; run 'dagger cloud login' to view Cloud checks")
	}
	if err != nil {
		return nil, err
	}
	return groupCloudListRows(rows, []string{"check"}), nil
}

// outputCloudCheckList renders the grouped check rows, applying the --failed
// filter and printing a friendly message when nothing matches.
func outputCloudCheckList(cmd *cobra.Command, grouped []groupedCloudListRow, address string) error {
	if cloudCheckListFailed {
		failed := grouped[:0]
		for _, row := range grouped {
			if row.Result == "red" {
				failed = append(failed, row)
			}
		}
		grouped = failed
	}
	if len(grouped) == 0 {
		what := "Cloud checks"
		if cloudCheckListFailed {
			what = "failed Cloud checks"
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "No %s found for %s.\n", what, address)
		return err
	}
	renderCloudCheckList(cmd, grouped)
	return nil
}

// renderCloudCheckList is renderCloudList specialized for the check list:
// one row per check, with the result shown as an emoji.
func renderCloudCheckList(cmd *cobra.Command, rows []groupedCloudListRow) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CHECK\tRESULT\tUPDATED")
	for _, row := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\n", dash(row.Values["check"]), cloudResultEmoji(row.Result), relativeTime(row.UpdatedAt))
	}
	_ = w.Flush()
}

// loadWorkspaceModuleCheckRows fetches the Cloud checks recorded for the
// workspace's module ref at the selected version, searching the user's orgs
// (orgs matching the repo owner first) and returning the first org's matches.
func loadWorkspaceModuleCheckRows(ctx context.Context, remote workspaceRemoteAddress) ([]cloudCheckRow, error) {
	client, _, err := cloudCLI.cloudClientWithLogin(ctx, false)
	if err != nil {
		return nil, err
	}
	// When --org is set, query just that org. Otherwise we'd probe every org
	// the user belongs to with a serial request each, which is pathologically
	// slow for accounts in thousands of orgs.
	if cloudOrgFlag != "" {
		commits, err := client.ModuleChecks(ctx, cloudOrgFlag, remote.BaseAddress, remote.Version)
		if err != nil {
			return nil, err
		}
		return cloudCheckRows(cloudOrgFlag, commits), nil
	}
	user, err := client.User(ctx)
	if err != nil {
		return nil, err
	}
	orgs, _ := orderCloudOrgsForRepos(user.Orgs, []string{remote.CloneRef})
	for _, org := range orgs {
		commits, err := client.ModuleChecks(ctx, org.Name, remote.BaseAddress, remote.Version)
		if err != nil {
			return nil, err
		}
		if rows := cloudCheckRows(org.Name, commits); len(rows) > 0 {
			return rows, nil
		}
	}
	return nil, nil
}
