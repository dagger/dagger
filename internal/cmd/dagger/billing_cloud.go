package daggercmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"text/tabwriter"

	"github.com/muesli/termenv"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/dagql/idtui"
	cloudapi "github.com/dagger/dagger/internal/cloud"
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
	cmd.Flags().BoolVar(&billingPaymentOpen, "open", false, "Open the payment page in a browser (default: in an interactive terminal)")
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
	cmd.Flags().BoolVar(&billingOpen, "open", false, "Open the billing portal in a browser (default: in an interactive terminal)")
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
	return cli.billingURLCommand(cmd, args, billingPaymentOpen, billingPage{
		name:   "payment page",
		action: "add or update the payment method",
	},
		func(ctx context.Context, client *cloudapi.Client, orgID string) (string, error) {
			return client.CreatePaymentCheckout(ctx, orgID)
		})
}

func (cli *CloudCLI) BillingManage(cmd *cobra.Command, args []string) error {
	return cli.billingURLCommand(cmd, args, billingOpen, billingPage{
		name:   "billing portal",
		action: "manage the subscription, invoices, and payment method",
	},
		func(ctx context.Context, client *cloudapi.Client, orgID string) (string, error) {
			return client.CreatePortalSession(ctx, orgID)
		})
}

// billingURLCommand resolves the target org (positional arg, --org, or the
// current org), asks Cloud for a hosted billing URL, and reports it: opened in
// a browser when open is set, as JSON with --json, otherwise printed.
// billingPage describes a hosted billing page for the command output.
type billingPage struct {
	// name is what the page is, for example "payment page".
	name string
	// action is what the user does there, completing "... to <action> for <org>".
	action string
}

func (cli *CloudCLI) billingURLCommand(
	cmd *cobra.Command,
	args []string,
	open bool,
	page billingPage,
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

	// Open the page like dagger login does: automatically in an interactive
	// terminal, unless --open says otherwise.
	if !cmd.Flags().Changed("open") {
		open = canOpenBrowser()
	}
	browserOpened := open && openBrowser(url)

	if cloudJSON {
		return writeCloudJSON(cmd, map[string]any{"org": org, "url": url, "opened": browserOpened})
	}
	printBillingPage(cmd.OutOrStdout(), page, org.Name, url, browserOpened, stdoutIsTTY)
	return nil
}

// printBillingPage reports a hosted billing page. Piped output is the URL
// alone, as before, so scripts keep working.
func printBillingPage(w io.Writer, page billingPage, org, url string, opened, interactive bool) {
	if !interactive {
		fmt.Fprintln(w, url)
		return
	}
	out := idtui.NewOutput(w)
	if opened {
		fmt.Fprintf(w, "%s Opened the %s for %s in your browser.\n",
			out.String("✓").Foreground(termenv.ANSIGreen), page.name, out.String(org).Bold())
		fmt.Fprintf(w, "Use it to %s. If it didn't open, visit:\n", page.action)
	} else {
		fmt.Fprintf(w, "Open the %s to %s for %s:\n", page.name, page.action, out.String(org).Bold())
	}
	fmt.Fprintf(w, "  %s\n", out.String(url).Underline())
}

// canOpenBrowser reports whether opening a browser can reach the user: an
// interactive terminal on the machine with a graphical session.
func canOpenBrowser() bool {
	if !canOpenShellOnError(progress, stdinIsTTY) || cloudJSON {
		return false
	}
	// Over SSH the browser would open on the remote machine.
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return false
	}
	// Without a display, the fallback can be a text browser that takes over
	// the terminal.
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return false
	}
	return true
}

// openBrowser opens url and reports whether it did. The launcher's own output
// is dropped, as dagger login does: the command prints the URL either way.
func openBrowser(url string) bool {
	stdout, stderr := browser.Stdout, browser.Stderr
	defer func() { browser.Stdout, browser.Stderr = stdout, stderr }()
	browser.Stdout, browser.Stderr = io.Discard, io.Discard
	return browser.OpenURL(url) == nil
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
