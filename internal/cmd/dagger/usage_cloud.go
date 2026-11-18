package daggercmd

import (
	"fmt"
	"io"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"

	cloudapi "github.com/dagger/dagger/internal/cloud"
)

var usageMonthFlag string

var cloudUsageCmd = newUsageCmd()

func init() {
	cloudCmd.AddCommand(cloudUsageCmd)
}

func newUsageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "usage [options] [org]",
		Short: "Show Dagger Cloud usage and bill",
		Long: `Show Dagger Cloud usage and bill for a UTC calendar month.

Reports the plan minimum, the usage, and the bill, followed by the monthly
statement: the charges of each resource for new and repeat work. Defaults to
the current month, or the month given with --month.

The numbers are the last saved calculation, as shown on the Billing & usage
page of Dagger Cloud.`,
		Args: cobra.MaximumNArgs(1),
		RunE: cloudCLI.Usage,
	}
	cmd.Flags().StringVar(&usageMonthFlag, "month", "", "UTC month to report, as YYYY-MM (defaults to the current month)")
	cmd.Flags().BoolVar(&cloudJSON, "json", false, "Print JSON output")
	return cmd
}

func (cli *CloudCLI) Usage(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	month, err := resolveUsageMonth(usageMonthFlag)
	if err != nil {
		return err
	}

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

	usage, err := client.BillingUsage(ctx, org.ID, month.Format("2006-01"))
	if err != nil {
		return err
	}

	if cloudJSON {
		return writeCloudJSON(cmd, map[string]any{
			"org":   org,
			"month": month.Format("2006-01"),
			"usage": usage,
		})
	}

	printBillingUsage(cmd.OutOrStdout(), org, month, usage, time.Now())
	return nil
}

// resolveUsageMonth parses the --month flag (YYYY-MM) into the first day of that
// UTC month. An empty value defaults to the current UTC month.
func resolveUsageMonth(value string) (time.Time, error) {
	now := time.Now().UTC()
	if value == "" {
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC), nil
	}
	month, err := time.ParseInLocation("2006-01", value, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid --month %q: expected format YYYY-MM", value)
	}
	return month, nil
}

// billingResources are the resources of the pricing page, in its order, with
// the unit that the statement shows.
var billingResources = []struct{ Resource, Name, Unit string }{
	{"CPU", "Pipeline CPU", "CPU-min"},
	{"Memory", "Pipeline memory", "GB-min"},
	{"Inputs", "Pipeline inputs", "GB"},
	{"Outputs", "Pipeline outputs", "GB"},
}

// billingUnitFactor converts a quantity in the API unit to the statement unit.
func billingUnitFactor(unit string) float64 {
	switch unit {
	case "CPU-hours", "GB-hours":
		return 60
	default:
		return 1
	}
}

func billingMoney(usd float64) string {
	return "$" + humanize.FormatFloat("#,###.##", usd)
}

// billingQuantity keeps up to two decimals for small values and drops
// decimals that do not matter for large ones.
func billingQuantity(value float64, unit string) string {
	switch {
	case value > 0 && value < .01:
		return "<0.01 " + unit
	case value < 10:
		return strconv.FormatFloat(math.Round(value*100)/100, 'f', -1, 64) + " " + unit
	case value < 100:
		return strconv.FormatFloat(math.Round(value*10)/10, 'f', -1, 64) + " " + unit
	default:
		return humanize.Comma(int64(math.Round(value))) + " " + unit
	}
}

// billingCents formats a USD price in cents, as the pricing page does.
func billingCents(usd float64) string {
	return strconv.FormatFloat(math.Round(usd*100*1e4)/1e4, 'f', -1, 64) + "¢"
}

func billingBusy(status string) bool { return status == "queued" || status == "running" }

// billingBillLabel names the bill: "October bill", or "October bill so far"
// while the month is open.
func billingBillLabel(month time.Time, usage *cloudapi.BillingUsage, now time.Time) string {
	label := month.Format("January") + " bill"
	now = now.UTC()
	if usage.FinalizedAt == nil && month.Year() == now.Year() && month.Month() == now.Month() {
		label += " so far"
	}
	return label
}

func billingUsageURL(org string, month time.Time) string {
	return fmt.Sprintf("https://dagger.cloud/%s/settings/billing?month=%s", url.PathEscape(org), month.Format("2006-01"))
}

func printBillingUsage(out io.Writer, org *cloudapi.OrgResponse, month time.Time, usage *cloudapi.BillingUsage, now time.Time) {
	fmt.Fprintf(out, "Dagger Cloud billing & usage for %s (%s UTC)\n\n", org.Name, month.Format("January 2006"))

	if usage.Revision == 0 && usage.Bill == nil {
		fmt.Fprintln(out, "Not calculated yet.")
		if usage.Error != "" && usage.RefreshMonth == usage.Month {
			fmt.Fprintln(out, usage.Error)
		}
		fmt.Fprintf(out, "\nTo calculate it, visit %s\n", billingUsageURL(org.Name, month))
		return
	}

	billLabel := billingBillLabel(month, usage, now)

	// Summary, as the cards at the top of the page.
	summary := [][]string{
		{"Plan minimum", "—", ""},
		{"Usage", "—", ""},
		{billLabel, "—", ""},
	}
	if p := usage.Pricing; p != nil {
		summary[0][1] = billingMoney(p.MinimumUSD)
		summary[0][2] = p.Name + " plan"
	}
	if b := usage.Bill; b != nil {
		summary[1][1] = billingMoney(b.UsageUSD)
		summary[2][1] = billingMoney(b.TotalUSD)
		if p := usage.Pricing; p != nil && p.MinimumUSD > 0 {
			summary[1][2] = strconv.Itoa(int(math.Round(b.UsageUSD/p.MinimumUSD*100))) + "% of the plan minimum"
		}
	}
	writeTable(out, []bool{false, true, false}, summary)

	fmt.Fprintln(out)
	fmt.Fprintln(out, "You pay the plan price or your usage, whichever is higher.")
	switch {
	case usage.FinalizedAt != nil:
		if usage.InvoiceURL != nil && *usage.InvoiceURL != "" {
			fmt.Fprintf(out, "Final. Invoice: %s\n", *usage.InvoiceURL)
		} else {
			fmt.Fprintln(out, "Final.")
		}
	case billingBusy(usage.RefreshStatus):
		fmt.Fprintln(out, "Updating…")
	case usage.CalculatedAt != nil:
		fmt.Fprintf(out, "Updated %s.\n", humanize.RelTime(*usage.CalculatedAt, now, "ago", "from now"))
	}
	if usage.FinalizedAt == nil && usage.ProcessedThrough != nil && usage.AsOf != nil && usage.ProcessedThrough.Before(*usage.AsOf) {
		fmt.Fprintf(out, "Sessions that ended after %s are not included yet.\n", usage.ProcessedThrough.UTC().Format("January 2, 15:04 UTC"))
	}
	for _, warning := range usage.Warnings {
		fmt.Fprintln(out, warning)
	}
	if usage.Error != "" && usage.RefreshMonth == usage.Month {
		fmt.Fprintf(out, "Error: %s\n", usage.Error)
	}

	if usage.Bill != nil && usage.Pricing != nil {
		fmt.Fprintln(out)
		printBillingStatement(out, usage.Pricing, usage.Bill, billLabel)
	}

	fmt.Fprintf(out, "\nPrices and definitions: https://dagger.io/pricing\n")
	fmt.Fprintf(out, "Daily usage: %s\n", billingUsageURL(org.Name, month))
}

// printBillingStatement shows the month as an invoice: the plan minimum, one
// line per resource and kind of work, the usage that the minimum covers, and
// the bill.
func printBillingStatement(out io.Writer, p *cloudapi.BillingUsagePricing, bill *cloudapi.BillingUsageBill, billLabel string) {
	fmt.Fprintln(out, "Monthly statement")
	fmt.Fprintln(out, "Charges in USD, before tax.")
	fmt.Fprintln(out)

	rows := [][]string{
		{"DESCRIPTION", "QUANTITY", "UNIT PRICE", "AMOUNT"},
		{p.Name + " plan minimum", "1 month", billingMoney(p.MinimumUSD) + " / month", billingMoney(p.MinimumUSD)},
	}
	for _, r := range billingResources {
		var line *cloudapi.BillingUsageResource
		for i := range bill.Resources {
			if v := &bill.Resources[i]; v.Resource == r.Resource && !v.Estimated {
				line = v
			}
		}
		if line == nil {
			// Nothing measured this resource for the month.
			rows = append(rows,
				[]string{r.Name + " — New work", "—", "—", "—"},
				[]string{r.Name + " — Repeat work", "—", "—", "—"})
			continue
		}
		factor := billingUnitFactor(line.Unit)
		rows = append(rows,
			[]string{
				r.Name + " — New work",
				billingQuantity(line.NewQuantity*factor, r.Unit),
				billingCents(line.NewPrice/factor) + " / " + r.Unit,
				billingMoney(line.NewChargeUSD),
			},
			[]string{
				r.Name + " — Repeat work",
				billingQuantity((line.RepeatRan+line.RepeatCached)*factor, r.Unit),
				billingCents(line.RepeatPrice/factor) + " / " + r.Unit,
				billingMoney(line.RepeatChargeUSD),
			})
	}
	covered := min(p.MinimumUSD, bill.UsageUSD)
	coveredText := billingMoney(covered)
	if covered > 0 {
		coveredText = "−" + coveredText
	}
	rows = append(rows,
		[]string{"Covered by the plan minimum", "", "", coveredText},
		nil, // rule
		[]string{billLabel, "", "", billingMoney(bill.TotalUSD)},
	)
	writeTable(out, []bool{false, true, true, true}, rows)
}

// writeTable writes rows as columns separated by two spaces. Columns marked in
// rightAlign are right-aligned. A nil row is written as a horizontal rule.
func writeTable(out io.Writer, rightAlign []bool, rows [][]string) {
	widths := make([]int, len(rightAlign))
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], utf8.RuneCountInString(cell))
		}
	}
	total := 0
	for _, w := range widths {
		total += w
	}
	total += 2 * (len(widths) - 1)
	for _, row := range rows {
		if row == nil {
			fmt.Fprintln(out, strings.Repeat("─", total))
			continue
		}
		var b strings.Builder
		for i, cell := range row {
			if i > 0 {
				b.WriteString("  ")
			}
			pad := strings.Repeat(" ", widths[i]-utf8.RuneCountInString(cell))
			if rightAlign[i] {
				b.WriteString(pad + cell)
			} else {
				b.WriteString(cell + pad)
			}
		}
		fmt.Fprintln(out, strings.TrimRight(b.String(), " "))
	}
}
