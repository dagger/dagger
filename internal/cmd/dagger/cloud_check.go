package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/dagger/dagger/core/gitref"
)

var cloudCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Manage Cloud-side automated checks for this workspace",
	Args:  cobra.NoArgs,
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
			remote, _, err = selectedRemoteWorkspaceAddress(cmd.Context(), "cloud check")
			if err != nil {
				return err
			}
		}
		state, err := setWorkspaceAutocheckState(cmd.Context(), remote, enabled)
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

func runCloudCheckStatus(cmd *cobra.Command, _ []string) error {
	remote, _, err := selectedRemoteWorkspaceAddress(cmd.Context(), "cloud check status")
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
	remote, address, err := selectedRemoteWorkspaceAddress(cmd.Context(), "cloud check list")
	if err != nil {
		return err
	}
	if len(args) > 0 {
		remote.Version = args[0]
		address = gitref.RefString(remote.CloneRef, remote.Path, remote.Version)
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
