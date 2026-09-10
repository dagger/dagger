package cloud

import "context"

// MonthlyUsage is the telemetry ingestion usage for a billing period. Usage and
// Cap count spans and log lines ingested (Cap is the plan allowance, 0 when the
// plan is unlimited).
type MonthlyUsage struct {
	Usage int `json:"usage"`
	Cap   int `json:"cap"`
}

const monthUsageOperation = `
query MonthUsage($org: ID!, $month: String!) {
	monthUsage(org: $org, month: $month) {
		usage
		cap
	}
}
`

// MonthUsage returns the telemetry ingestion usage (spans + log lines) for the
// given org and billing month. month is a date string in the "2006-01-02" form
// identifying the period (e.g. "2026-09-01" for September 2026).
func (c *Client) MonthUsage(ctx context.Context, orgID, month string) (*MonthlyUsage, error) {
	var data struct {
		MonthUsage MonthlyUsage `json:"monthUsage"`
	}
	if err := c.doGraphQL(ctx, "MonthUsage", monthUsageOperation, map[string]any{
		"org":   orgID,
		"month": month,
	}, &data); err != nil {
		return nil, err
	}
	return &data.MonthUsage, nil
}

const orgComputeMinutesOperation = `
query OrgComputeMinutes($org: ID!) {
	orgComputeMinutes(org: $org)
}
`

// OrgComputeMinutes returns the number of Cloud compute (engine) minutes the org
// has consumed in the current billing period.
func (c *Client) OrgComputeMinutes(ctx context.Context, orgID string) (int, error) {
	var data struct {
		OrgComputeMinutes int `json:"orgComputeMinutes"`
	}
	if err := c.doGraphQL(ctx, "OrgComputeMinutes", orgComputeMinutesOperation, map[string]any{
		"org": orgID,
	}, &data); err != nil {
		return 0, err
	}
	return data.OrgComputeMinutes, nil
}
