package daggercmd

import (
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
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

var cloudCheckListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Cloud-side checks for this workspace",
	Args:  cobra.NoArgs,
	RunE:  runCloudCheckList,
}

var cloudCheckStatusCmd = &cobra.Command{
	Use:   "status [name]",
	Short: "Show the status of a Cloud-side check (by name; defaults to the workspace remote's default check)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runCloudCheckStatus,
}

func init() {
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

func runCloudCheckList(cmd *cobra.Command, _ []string) error {
	remote, _, err := selectedRemoteWorkspaceAddress(cmd.Context(), "cloud check list")
	if err != nil {
		return err
	}

	state := ""
	if s, ok, err := loadWorkspaceAutocheckState(cmd.Context(), remote); err != nil {
		if !errors.Is(err, errCloudNotAuthenticated) {
			return err
		}
		state = "(login required)"
	} else if ok {
		state = workspaceAutocheckStateString(s)
	} else {
		state = "unconfigured"
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tREMOTE\tSTATE"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "%s\t%s\t%s\n", "autocheck", remote.CloneRef, state); err != nil {
		return err
	}
	return w.Flush()
}
