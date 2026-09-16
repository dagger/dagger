package daggercmd

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"
)

var billingOpen bool

var cloudBillingCmd = newBillingCmd(false)
var billingCmd = newBillingCmd(true)

func init() {
	cloudCmd.AddCommand(cloudBillingCmd)
	rootCmd.AddCommand(billingCmd)
}

func newBillingCmd(hidden bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "billing",
		Short:  "Manage Dagger Cloud billing",
		Args:   cobra.NoArgs,
		Hidden: hidden,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.PersistentFlags().BoolVar(&cloudJSON, "json", false, "Print JSON output")
	cmd.AddCommand(
		&cobra.Command{
			Use:   "plans",
			Short: "List Dagger Cloud plans available at signup",
			Args:  cobra.NoArgs,
			RunE:  cloudCLI.BillingPlans,
		},
		newBillingManageCmd(),
		newBillingPaymentCmd(),
	)
	return cmd
}

var billingPaymentOpen bool

func newBillingPaymentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "payment [org]",
		Short: "Enter or update the payment method for a Dagger Cloud org",
		Long: `Enter or update the payment method for a Dagger Cloud org.

Creates a secure hosted checkout page for the org's existing subscription
where you can add or change the card on file, and prints its URL.`,
		Args: cobra.MaximumNArgs(1),
		RunE: cloudCLI.BillingPayment,
	}
	cmd.Flags().BoolVar(&billingPaymentOpen, "open", false, "Open the payment page in a browser")
	return cmd
}

func newBillingManageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "manage [org]",
		Aliases: []string{"portal"},
		Short:   "Open the billing portal for a Dagger Cloud org",
		Args:    cobra.MaximumNArgs(1),
		RunE:    cloudCLI.BillingManage,
	}
	cmd.Flags().BoolVar(&billingOpen, "open", false, "Open the billing portal in a browser")
	return cmd
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

func (cli *CloudCLI) BillingPayment(cmd *cobra.Command, args []string) error {
	return cli.billingURLCommand(cmd, args, billingPaymentOpen,
		func(ctx context.Context, client *cloudapi.Client, orgID string) (string, error) {
			return client.CreatePaymentCheckout(ctx, orgID)
		})
}

func (cli *CloudCLI) BillingManage(cmd *cobra.Command, args []string) error {
	return cli.billingURLCommand(cmd, args, billingOpen,
		func(ctx context.Context, client *cloudapi.Client, orgID string) (string, error) {
			return client.CreatePortalSession(ctx, orgID)
		})
}

// billingURLCommand resolves the target org (positional arg, --org, or the
// current org), asks Cloud for a hosted billing URL, and reports it: opened in
// a browser when open is set, as JSON with --json, otherwise printed.
func (cli *CloudCLI) billingURLCommand(
	cmd *cobra.Command,
	args []string,
	open bool,
	hostedURL func(ctx context.Context, client *cloudapi.Client, orgID string) (string, error),
) error {
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
	url, err := hostedURL(ctx, client, org.ID)
	if err != nil {
		return err
	}
	if open {
		if err := browser.OpenURL(url); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Failed to open browser: %s\n", err)
		}
	}
	if cloudJSON {
		return writeCloudJSON(cmd, map[string]any{"org": org, "url": url})
	}
	fmt.Fprintln(cmd.OutOrStdout(), url)
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
