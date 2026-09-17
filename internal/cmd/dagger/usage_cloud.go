package daggercmd

import (
	"fmt"
	"time"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

var usageMonthFlag string

var cloudUsageCmd = newUsageCmd()

func init() {
	cloudCmd.AddCommand(cloudUsageCmd)
}

func newUsageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "usage [options] [org]",
		Short: "Show Dagger Cloud usage",
		Long: `Show Dagger Cloud usage for a UTC calendar month.

Reports Cloud Engine compute usage (core minutes and cost) and telemetry usage
(spans and log lines ingested) for the current month by default, or the month
given with --month.`,
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

	// The compute and telemetry usage queries are independent and hit different
	// backing stores (Postgres vs ClickHouse). Run them concurrently so the total
	// latency is the slower of the two rather than their sum. This matters for
	// large orgs where the compute query alone can take a while.
	var (
		compute   *cloudapi.MonthlyComputeUsage
		telemetry *cloudapi.MonthlyUsage
	)
	eg, egctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		var err error
		compute, err = client.MonthComputeUsage(egctx, org.ID, month.Format("2006-01"))
		return err
	})
	eg.Go(func() error {
		var err error
		telemetry, err = client.MonthUsage(egctx, org.ID, month.Format("2006-01-02"))
		return err
	})
	if err := eg.Wait(); err != nil {
		return err
	}

	if cloudJSON {
		return writeCloudJSON(cmd, map[string]any{
			"org":       org,
			"month":     month.Format("2006-01"),
			"compute":   compute,
			"telemetry": telemetry,
		})
	}

	printUsage(cmd, org, month, compute, telemetry)
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

func printUsage(
	cmd *cobra.Command,
	org *cloudapi.OrgResponse,
	month time.Time,
	compute *cloudapi.MonthlyComputeUsage,
	telemetry *cloudapi.MonthlyUsage,
) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Dagger Cloud usage for %s (%s UTC)\n", org.Name, month.Format("January 2006"))

	fmt.Fprintln(out, "\nCloud Engines")
	fmt.Fprintf(out, "  Core minutes used:  %.2f\n", compute.CoreMinutes)
	if compute.PricePerCoreMinute > 0 {
		fmt.Fprintf(out, "  Compute cost (USD): $%.2f ($%.5f per core minute)\n", compute.CostUSD, compute.PricePerCoreMinute)
	} else {
		fmt.Fprintf(out, "  Compute cost (USD): $%.2f\n", compute.CostUSD)
	}

	fmt.Fprintln(out, "\nTelemetry")
	fmt.Fprintf(out, "  Spans and log lines: %s used", formatUsageInt(telemetry.Usage))
	if telemetry.Cap > 0 {
		left := telemetry.Cap - telemetry.Usage
		fmt.Fprintf(out, " out of %s", formatUsageInt(telemetry.Cap))
		if left > 0 {
			fmt.Fprintf(out, " (%s left)", formatUsageInt(left))
		} else {
			fmt.Fprint(out, " (usage limit reached)")
		}
	}
	fmt.Fprintln(out)

	fmt.Fprintf(out, "\nFor detailed usage information, visit https://dagger.cloud/%s/settings/usage\n", org.Name)
}

// formatUsageInt renders an integer with thousands separators.
func formatUsageInt(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	digits := fmt.Sprintf("%d", n)
	var out []byte
	for i, c := range []byte(digits) {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}
