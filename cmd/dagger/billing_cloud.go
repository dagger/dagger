package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"
)

var billingOpen bool

var billingCmd = &cobra.Command{
	Use:     "billing",
	Short:   "Manage Dagger Cloud billing",
	Args:    cobra.NoArgs,
	GroupID: cloudGroup.ID,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var billingPlansCmd = &cobra.Command{
	Use:   "plans",
	Short: "List Dagger Cloud plans available at signup",
	Args:  cobra.NoArgs,
	RunE:  cloudCLI.BillingPlans,
}

var billingManageCmd = &cobra.Command{
	Use:     "manage [org]",
	Aliases: []string{"portal"},
	Short:   "Open the billing portal for a Dagger Cloud org",
	Args:    cobra.MaximumNArgs(1),
	RunE:    cloudCLI.BillingManage,
}

func init() {
	billingCmd.PersistentFlags().BoolVar(&cloudJSON, "json", false, "Print JSON output")
	billingManageCmd.Flags().BoolVar(&billingOpen, "open", false, "Open the billing portal in a browser")
	billingCmd.AddCommand(billingPlansCmd, billingManageCmd)
	rootCmd.AddCommand(billingCmd)
}

func (cli *CloudCLI) BillingPlans(cmd *cobra.Command, args []string) error {
	client, err := cloudapi.NewClient(cmd.Context(), nil)
	if err != nil {
		return err
	}
	plans, err := client.Plans(cmd.Context())
	if err != nil {
		return err
	}
	if cloudJSON {
		return writeCloudJSON(cmd, plans)
	}
	printBillingPlans(cmd, plans.Plans)
	return nil
}

func (cli *CloudCLI) BillingManage(cmd *cobra.Command, args []string) error {
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
	portalURL, err := client.CreatePortalSession(ctx, org.ID)
	if err != nil {
		return err
	}
	if billingOpen {
		if err := browser.OpenURL(portalURL); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Failed to open browser: %s\n", err)
		}
	}
	if cloudJSON {
		return writeCloudJSON(cmd, map[string]any{"org": org, "url": portalURL})
	}
	fmt.Fprintln(cmd.OutOrStdout(), portalURL)
	return nil
}

func printBillingPlans(cmd *cobra.Command, plans []cloudapi.Plan) {
	if len(plans) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No Dagger Cloud plans found.")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PLAN\tPRICE\tPERIOD\tPRICE ID")
	for _, plan := range plans {
		if len(plan.Price) == 0 {
			fmt.Fprintf(w, "%s\t\t\t\n", planName(plan))
			continue
		}
		for _, price := range plan.Price {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				planName(plan),
				formatPlanPrice(price),
				price.PeriodUnit,
				price.ID,
			)
		}
	}
	_ = w.Flush()
}

func planName(plan cloudapi.Plan) string {
	if strings.TrimSpace(plan.Item.ExternalName) != "" {
		return plan.Item.ExternalName
	}
	return plan.Item.ID
}

func formatPlanPrice(price cloudapi.PlanPrice) string {
	if price.Price == 0 {
		return "free"
	}
	return fmt.Sprintf("$%.2f", float64(price.Price)/100)
}
