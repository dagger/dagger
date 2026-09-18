package cloud

import (
	"context"
)

// MonthlyComputeUsage is the Cloud Engine compute usage total for a UTC calendar
// month. The API also exposes a per-day breakdown via the daily field, but this
// command reports only the total, so it is intentionally not requested: on the
// server the daily field is resolved separately and skipping it avoids the
// expensive per-day expansion for large orgs.
type MonthlyComputeUsage struct {
	CoreMinutes        float64 `json:"coreMinutes"`
	CostUSD            float64 `json:"costUSD"`
	PricePerCoreMinute float64 `json:"pricePerCoreMinute"`
}

// MonthlyUsage is the telemetry usage (spans and log lines ingested) for a UTC
// calendar month, along with the plan cap.
type MonthlyUsage struct {
	Usage int `json:"usage"`
	Cap   int `json:"cap"`
}

const monthComputeUsageOperation = `
query GetMonthlyComputeUsage($org: ID!, $month: String!) {
	monthComputeUsage(org: $org, month: $month) {
		coreMinutes
		costUSD
		pricePerCoreMinute
	}
}
`

// MonthComputeUsage returns the Cloud Engine compute usage total (core minutes
// and cost) for the given org and UTC month (YYYY-MM). The daily breakdown is
// intentionally not requested; see MonthlyComputeUsage.
func (c *Client) MonthComputeUsage(ctx context.Context, orgID, month string) (*MonthlyComputeUsage, error) {
	vars := map[string]any{
		"org":   orgID,
		"month": month,
	}
	var data struct {
		MonthComputeUsage MonthlyComputeUsage `json:"monthComputeUsage"`
	}
	if err := c.doGraphQL(ctx, "GetMonthlyComputeUsage", monthComputeUsageOperation, vars, &data); err != nil {
		return nil, err
	}
	return &data.MonthComputeUsage, nil
}

const monthUsageOperation = `
query GetMonthlyUsage($org: ID!, $month: String!) {
	monthUsage(org: $org, month: $month) {
		usage
		cap
	}
}
`

// MonthUsage returns the telemetry usage (spans and log lines) for the given
// org and month. The month argument is the first day of the UTC month
// (YYYY-MM-01).
func (c *Client) MonthUsage(ctx context.Context, orgID, month string) (*MonthlyUsage, error) {
	vars := map[string]any{
		"org":   orgID,
		"month": month,
	}
	var data struct {
		MonthUsage MonthlyUsage `json:"monthUsage"`
	}
	if err := c.doGraphQL(ctx, "GetMonthlyUsage", monthUsageOperation, vars, &data); err != nil {
		return nil, err
	}
	return &data.MonthUsage, nil
}
