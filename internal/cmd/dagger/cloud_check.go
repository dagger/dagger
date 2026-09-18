package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/charmbracelet/huh"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/core/gitref"
	"github.com/dagger/dagger/dagql/idtui"
)

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

var cloudCheckListFailed bool

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
	requireCloudFeatures(cloudCheckOnCmd, cloudChecksRequiredFeatures...)
	cloudCheckOnCmd.Flags().Bool(startTrialFlag, false, "Start a free trial when a required Cloud feature is not enabled yet")
	cloudCheckCmd.AddCommand(cloudCheckOnCmd, cloudCheckOffCmd, cloudCheckListCmd, cloudCheckStatusCmd)
	cloudCmd.AddCommand(cloudCheckCmd)
}

// cloudChecksRequiredFeatures are the org features enabling a Cloud check
// needs: Cloud Checks itself, Cloud Modules (org-scoped source lookups are
// gated on it), and Cloud Engines. Missing ones are offered together as one
// trial at enforcement time. Other commands can declare their own
// requirements the same way via requireCloudFeatures.
var cloudChecksRequiredFeatures = []cloudFeature{featureCloudChecks, featureCloudModules, featureCloudEngines}

// runCloudCheckSet returns a RunE that sets the workspace autocheck flag for
// the selected remote. The optional name arg is accepted but not used —
// today's underlying API only models a single autocheck per remote.
func runCloudCheckSet(enabled bool) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		// The feature requirements (and their trial gate) are declared as
		// annotations on the checks-on command itself. This flow can also run
		// under a different command — e.g. the `dagger init` next-steps offer
		// invokes it with init's cobra command — so carry the requirements
		// over, otherwise the gate silently no-ops and the trial prompt never
		// appears.
		if enabled && len(commandCloudFeatures(cmd)) == 0 {
			requireCloudFeatures(cmd, cloudChecksRequiredFeatures...)
		}
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
		state, err := setWorkspaceAutocheckState(cmd, remote, enabled)
		if enabled && errors.Is(err, errCloudSourceNotConfigured) {
			if setupErr := prepareCloudChecksIntegration(cmd, args); setupErr != nil {
				return setupErr
			}
			// Re-read Cloud state after the user completes the browser step.
			state, err = setWorkspaceAutocheckState(cmd, remote, enabled)
			if errors.Is(err, errCloudSourceNotConfigured) {
				// Already actionable: names the owner and the app install page.
				return err
			}
		}
		var accessErr *repoAccessError
		if enabled && errors.As(err, &accessErr) {
			// The GitHub App installation exists but does not grant access to
			// this repository: offer to open the installation settings page,
			// then retry once the user has granted access.
			if promptErr := prepareCloudChecksRepoAccess(cmd, accessErr); promptErr != nil {
				return promptErr
			}
			state, err = setWorkspaceAutocheckState(cmd, remote, enabled)
		}
		if errors.Is(err, errCloudNotAuthenticated) {
			return fmt.Errorf("not authenticated; run 'dagger cloud login' to update Cloud checks")
		}
		if err != nil {
			return err
		}
		action := "enabled"
		if !enabled {
			action = "disabled"
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "checks for repo %s %s\n", state.Repo, action)
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
	client, _, err := cloudCLI.cloudClientWithLogin(cmd.Context(), false)
	if err != nil {
		return err
	}
	// When the GitHub identity is already connected, the missing piece is the
	// Dagger Cloud GitHub App installation on the repository's owner —
	// `dagger cloud integration create github` would just report "already
	// connected", so point at the app install page instead.
	if connected, _ := cloudCLI.githubConnected(cmd.Context(), client); connected {
		return prepareCloudChecksAppInstall(cmd, args)
	}
	required := cloudChecksPrerequisiteError(cmd, args, "Cloud checks need GitHub access to this repository", "dagger cloud integration create github")
	if !canPromptForCloudChecks() || !confirmSetupCommand(cmd, "Connect this repository to Dagger Cloud in the browser?", cloudSetupCommand("integration create github --open")) {
		return required
	}
	setup, err := cloudCLI.githubConnectHandoff(cmd.Context(), client)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Manual step: grant Dagger access to this repository, then return here:\n%s\n", setup.URL)
	if err := browser.OpenURL(setup.URL); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "Could not open a browser. Open the URL above.")
	}
	if !confirmManualStep(cmd, "Have you completed the manual GitHub step?") {
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
// confirmSetupCommand asks whether to run a follow-up command, rendered with
// the same explicit-choice prompt (Run / Skip) that `dagger init` uses for its
// next-step offers. `checks on` runs outside a frontend, so the form is run
// standalone with the TUI's theme.
func confirmSetupCommand(cmd *cobra.Command, title, command string) bool {
	selected := true
	form := huh.NewForm(huh.NewGroup(setupCommandChoice(title, command, &selected)))
	if err := idtui.RunStandaloneForm(cmd.Context(), Frontend, form); err != nil {
		return false
	}
	return selected
}

// confirmManualStep asks whether a browser-side manual step is complete,
// rendered with the same explicit-choice style as the other setup prompts.
func confirmManualStep(cmd *cobra.Command, title string) bool {
	confirmed := true
	form := huh.NewForm(huh.NewGroup(
		idtui.NewExplicitConfirm("Done", "Cancel", &confirmed).Title(title),
	))
	if err := idtui.RunStandaloneForm(cmd.Context(), Frontend, form); err != nil {
		return false
	}
	return confirmed
}

// prepareCloudChecksAppInstall handles a connected GitHub identity whose
// Dagger Cloud GitHub App is not installed on the repository's owner: offer
// the app install page (browser), or return an actionable error pointing at
// it when prompting is not possible.
func prepareCloudChecksAppInstall(cmd *cobra.Command, args []string) error {
	installURL := gitHubAppInstallURL()
	required := cloudChecksPrerequisiteError(cmd, args,
		fmt.Sprintf("Cloud checks need the Dagger Cloud GitHub App installed for this repository's owner (install it here: %s)", installURL),
		"")
	if !canPromptForCloudChecks() || !confirmSetupCommand(cmd, "Install the Dagger Cloud GitHub App in the browser?", installURL) {
		return required
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Manual step: install the app for this repository's owner, then return here:\n%s\n", installURL)
	if err := browser.OpenURL(installURL); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "Could not open a browser. Open the URL above.")
	}
	if !confirmManualStep(cmd, "Have you completed the manual GitHub step?") {
		return required
	}
	return nil
}

// prepareCloudChecksRepoAccess offers to open the GitHub installation's
// settings page so the user can grant the app access to the repository, then
// waits for confirmation. Without a prompt (non-interactive), it returns the
// actionable error as-is.
func prepareCloudChecksRepoAccess(cmd *cobra.Command, accessErr *repoAccessError) error {
	if !canPromptForCloudChecks() || !confirmSetupCommand(cmd, "Repository permissions not found for the GitHub App. Open browser to configure?", "open "+accessErr.settingsURL) {
		return accessErr
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Manual step: grant access to this repository, then return here:\n%s\n", accessErr.settingsURL)
	if err := browser.OpenURL(accessErr.settingsURL); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "Could not open a browser. Open the URL above.")
	}
	if !confirmManualStep(cmd, "Have you completed the manual GitHub step?") {
		return accessErr
	}
	return nil
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
	script := retry + org
	if prerequisite != "" {
		script = prerequisite + org + "\n" + script
	}
	return fmt.Errorf("%s.\nComplete the prerequisite, then retry:\n\n%s", reason, script)
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
	remote, address, err := selectedRemoteWorkspaceAddress(cmd.Context(), "cloud checks list")
	if err != nil {
		return err
	}
	if len(args) > 0 {
		remote.Version = args[0]
		address = gitref.RefString(remote.CloneRef, remote.Path, remote.Version)
	} else {
		// Checks are recorded against the commit the engine resolved, and the
		// lookup matches module_version exactly. The inferred workspace
		// version is a symbolic ref (branch name) whenever HEAD is not
		// detached, which never matches; resolve it to the commit for the
		// query. Best-effort: a remote workspace (-W) has no local HEAD, and
		// the branch name stays in the printed address either way.
		if sha, err := localHeadCommitSHA(cmd.Context(), workspaceRef); err == nil && sha != "" {
			remote.Version = sha
		}
	}
	rows, err := loadWorkspaceModuleCheckRows(cmd.Context(), remote)
	if errors.Is(err, errCloudNotAuthenticated) {
		return fmt.Errorf("not authenticated; run 'dagger cloud login' to view Cloud checks")
	}
	if err != nil {
		return err
	}
	grouped := groupCloudListRows(rows, []string{"check"})
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
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "No %s found for %s.\n", what, address)
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
