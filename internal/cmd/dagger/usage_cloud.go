package daggercmd

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	cloudapi "github.com/dagger/dagger/internal/cloud"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
)

var usageMonth string

var cloudUsageCmd = newUsageCmd()

func init() {
	cloudCmd.AddCommand(cloudUsageCmd)
}

func newUsageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "usage [org]",
		Short: "Show Dagger Cloud usage",
		Long:  "Show Dagger Cloud usage for the current billing period, including telemetry ingestion (spans and log lines) and Cloud compute minutes.",
		Args:  cobra.MaximumNArgs(1),
		RunE:  cloudCLI.Usage,
	}
	cmd.Flags().BoolVar(&cloudJSON, "json", false, "Print JSON output")
	cmd.Flags().StringVar(&usageMonth, "month", "", "Billing month to report in YYYY-MM form (default current month)")
	return cmd
}

// usageReport is the resolved usage for an org, used for both text and JSON
// rendering.
type usageReport struct {
	Org          string                 `json:"org"`
	Month        string                 `json:"month"`
	Telemetry    *cloudapi.MonthlyUsage `json:"telemetry"`
	CloudMinutes int                    `json:"cloudMinutes"`
}

func (cli *CloudCLI) Usage(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	month, err := resolveUsageMonth(usageMonth, time.Now())
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

	telemetry, err := client.MonthUsage(ctx, org.ID, month+"-01")
	if err != nil {
		return err
	}
	minutes, err := client.OrgUsedMinutes(ctx, org.ID)
	if err != nil {
		return err
	}

	report := usageReport{
		Org:          org.Name,
		Month:        month,
		Telemetry:    telemetry,
		CloudMinutes: minutes,
	}

	if cloudJSON {
		return writeCloudJSON(cmd, report)
	}
	printUsage(cmd.OutOrStdout(), report)
	return nil
}

// resolveUsageMonth validates the optional --month flag (YYYY-MM) and falls back
// to the given now's month, returning the month in YYYY-MM form.
func resolveUsageMonth(flag string, now time.Time) (string, error) {
	if flag == "" {
		return now.UTC().Format("2006-01"), nil
	}
	t, err := time.Parse("2006-01", flag)
	if err != nil {
		return "", fmt.Errorf("invalid --month %q: expected YYYY-MM", flag)
	}
	return t.Format("2006-01"), nil
}

func printUsage(out io.Writer, r usageReport) {
	fmt.Fprintf(out, "Org:    %s\n", r.Org)
	fmt.Fprintf(out, "Period: %s\n\n", r.Month)

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "Telemetry (spans + logs):\t%s\n", formatTelemetryUsage(r.Telemetry))
	fmt.Fprintf(w, "Cloud minutes:\t%s\n", formatCloudMinutes(r.CloudMinutes))
	_ = w.Flush()
}

// formatTelemetryUsage renders ingestion usage as "used / cap (pct, left)",
// omitting the cap details when the plan is unlimited (cap <= 0).
func formatTelemetryUsage(u *cloudapi.MonthlyUsage) string {
	if u == nil {
		return "n/a"
	}
	if u.Cap <= 0 {
		return fmt.Sprintf("%s used (unlimited)", humanize.Comma(int64(u.Usage)))
	}
	pct := float64(u.Usage) / float64(u.Cap) * 100
	left := u.Cap - u.Usage
	if left < 0 {
		left = 0
	}
	return fmt.Sprintf("%s / %s (%.1f%% used, %s left)",
		humanize.Comma(int64(u.Usage)),
		humanize.Comma(int64(u.Cap)),
		pct,
		humanize.Comma(int64(left)),
	)
}

func formatCloudMinutes(minutes int) string {
	unit := "minutes"
	if minutes == 1 {
		unit = "minute"
	}
	return fmt.Sprintf("%s %s used", humanize.Comma(int64(minutes)), unit)
}
