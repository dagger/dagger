package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
	"github.com/spf13/cobra"
)

var orgCmd = &cobra.Command{
	Use:     "org",
	Short:   "Manage Dagger Cloud organizations",
	Args:    cobra.NoArgs,
	GroupID: cloudGroup.ID,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var orgListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Dagger Cloud organizations",
	Args:  cobra.NoArgs,
	RunE:  cloudCLI.OrgList,
}

var orgInfoCmd = &cobra.Command{
	Use:   "info [org]",
	Short: "Show Dagger Cloud organization status",
	Args:  cobra.MaximumNArgs(1),
	RunE:  cloudCLI.OrgInfo,
}

var orgUseCmd = &cobra.Command{
	Use:   "use <org>",
	Short: "Select the current Dagger Cloud organization",
	Args:  cobra.ExactArgs(1),
	RunE:  cloudCLI.OrgUse,
}

func init() {
	orgCmd.PersistentFlags().BoolVar(&cloudJSON, "json", false, "Print JSON output")
	orgCmd.AddCommand(orgListCmd, orgInfoCmd, orgUseCmd)
	rootCmd.AddCommand(orgCmd)
}

func (cli *CloudCLI) OrgList(cmd *cobra.Command, args []string) error {
	client, _, err := cli.cloudClient(cmd.Context())
	if err != nil {
		return err
	}
	user, err := client.User(cmd.Context())
	if err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, user.Orgs)
	}
	printOrgList(cmd, user.Orgs)
	return nil
}

func (cli *CloudCLI) OrgInfo(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, cloudAuth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	var org *cloudapi.OrgResponse
	if len(args) > 0 {
		org, err = client.OrgByName(ctx, args[0])
	} else {
		org, err = cli.resolveCloudOrg(ctx, client, cloudAuth)
	}
	if err != nil {
		return err
	}
	details, err := client.OrgDetails(ctx, org.Name)
	if err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, details)
	}
	printOrgInfo(cmd, details)
	return nil
}

func (cli *CloudCLI) OrgUse(cmd *cobra.Command, args []string) error {
	client, _, err := cli.cloudClient(cmd.Context())
	if err != nil {
		return err
	}
	org, err := client.OrgByName(cmd.Context(), args[0])
	if err != nil {
		return err
	}
	if err := cloudauth.SetCurrentOrg(&cloudauth.Org{ID: org.ID, Name: org.Name}); err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, org)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Current org: %s\n", org.Name)
	return nil
}

func printOrgList(cmd *cobra.Command, orgs []cloudauth.Org) {
	if len(orgs) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No Dagger Cloud organizations found.")
		return
	}
	current, _ := cloudauth.CurrentOrgName()
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tID\tCURRENT")
	for _, org := range orgs {
		fmt.Fprintf(w, "%s\t%s\t%t\n", org.Name, org.ID, strings.EqualFold(org.Name, current))
	}
	_ = w.Flush()
}

func printOrgInfo(cmd *cobra.Command, org *cloudapi.OrgDetails) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Org:          %s\n", org.Name)
	fmt.Fprintf(out, "ID:           %s\n", org.ID)
	fmt.Fprintf(out, "Created:      %s\n", org.CreatedAt)
	fmt.Fprintf(out, "Plan:         %s\n", org.Subscription.PlanID)
	fmt.Fprintf(out, "Subscription: %s\n", org.Subscription.Status)
	fmt.Fprintf(out, "Caching:      %s\n", onOff(org.Subscription.HasCaching))
	if len(org.Features) == 0 {
		return
	}
	fmt.Fprintln(out, "\nFeatures:")
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATUS\tTRIAL END")
	for _, feature := range org.Features {
		fmt.Fprintf(w, "%s\t%s\t%s\n", feature.Name, feature.Status, stringValue(feature.TrialEnd))
	}
	_ = w.Flush()
}
